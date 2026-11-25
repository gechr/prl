package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"time"

	cliapi "github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/pkg/cmd/event/client"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/prl/internal/eventstream"
)

func newEventClient() (client.EventClient, error) {
	// Event streams are long-lived. The ordinary REST transport buffers GETs for conditional reads.
	httpClient, err := api.NewHTTPClient(api.ClientOptions{Host: "github.com", LogIgnoreEnv: true})
	if err != nil {
		return nil, err
	}
	return client.NewEventClient(httpClient, "github.com"), nil
}

func (s *eventStreams) listenPREvents(
	ctx context.Context,
	resource string,
	notify func(prEventScope),
) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	id, err := subscribePREvents(ctx, c, resource)
	if err != nil {
		return err
	}
	var renewed bool
	backoff := eventBackoff(time.Second)
	for ctx.Err() == nil {
		delivered, streamErr := followSubscription(ctx, c, id, notify)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if delivered {
			backoff = eventBackoff(time.Second)
		}
		missing := errors.Is(streamErr, client.ErrSubscriptionNotFound)
		if missing && !renewed || errors.Is(streamErr, errRenewSubscription) {
			id, err = subscribePREvents(ctx, c, resource)
			if err != nil {
				return err
			}
			renewed = true
			if missing {
				continue // Try the fresh subscription once before treating a 404 as final.
			}
			streamErr = nil
		} else if streamErr == nil {
			renewed = false
		}
		if err := backoff.wait(ctx, streamErr); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func subscribePREvents(ctx context.Context, c client.EventClient, resource string) (string, error) {
	backoff := eventBackoff(time.Second)
	for ctx.Err() == nil {
		sub, _, err := c.CreateSubscription(ctx, resource, pullRequestEvents)
		if err == nil {
			if sub.ID == "" {
				return "", errors.New("GitHub returned an empty event subscription ID")
			}
			return sub.ID, nil
		}
		if err = backoff.wait(ctx, err); err != nil {
			return "", err
		}
	}
	return "", ctx.Err()
}

type eventBackoff time.Duration

func (b *eventBackoff) wait(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	wait := time.Duration(*b)
	if httpErr, ok := errors.AsType[cliapi.HTTPError](err); ok {
		if httpErr.HTTPError == nil ||
			httpErr.StatusCode < http.StatusInternalServerError &&
				httpErr.StatusCode != http.StatusTooManyRequests {
			return err
		}
		retryAfter, _ := parseRetryAfter(httpErr.Headers.Get(headerRetryAfter))
		wait = max(wait, retryAfter)
	} else if _, networkError := errors.AsType[*url.Error](err); err != nil && !networkError {
		return err
	}
	const factor = 2
	const maxDelay = 30 * time.Second
	jitter := rand.N(wait / factor) //nolint:gosec // Reconnect jitter.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait + jitter):
		*b = min(*b*factor, eventBackoff(maxDelay))
		return nil
	}
}

var errRenewSubscription = errors.New("event subscription needs renewal")

func followSubscription(
	ctx context.Context,
	c client.EventClient,
	id string,
	notify func(prEventScope),
) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	body, err := c.OpenStream(ctx, id)
	if err != nil {
		return false, err
	}
	defer body.Close()
	notify(eventStatusAndReviews) // Events missed before each attachment are never replayed.

	const idleTimeout = 45 * time.Second
	idle := time.AfterFunc(idleTimeout, cancel)
	defer idle.Stop()
	delivered := false
	reader := eventstream.NewFrameReader(body)
	reader.OnComment = func(string) {
		delivered = true
		idle.Reset(idleTimeout)
	}
	for {
		frame, err := reader.Next()
		if err != nil {
			// EOF, read failures, and missed heartbeats all require a fresh connection.
			return delivered, nil
		}
		idle.Reset(idleTimeout)
		if frame.Event == "github.stream.end" {
			return delivered, streamEndError(frame.Data)
		}
		delivered = true
		switch frame.Event {
		case eventCheckSuite, eventConflict:
			notify(eventStatus)
		case eventHead:
			notify(eventStatusAndReviews | eventNewHead)
		case eventReview, eventState:
			notify(eventStatusAndReviews)
		}
	}
}

func streamEndError(data string) error {
	var end struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal([]byte(data), &end) != nil || end.Outcome == "reconnect" {
		return nil //nolint:nilerr // Reconnect after a malformed end frame.
	}
	if end.Outcome != "subscription_ended" {
		return errors.New("GitHub returned an unknown event stream outcome")
	}
	switch end.Reason {
	case "deleted", "access_revoked":
		return fmt.Errorf("GitHub ended the event subscription (%s)", end.Reason)
	default:
		// The reason is optional. Create a subscription to continue following the resource.
		return errRenewSubscription
	}
}
