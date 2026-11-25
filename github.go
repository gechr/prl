package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/cli/cli/v2/pkg/cmd/event/client"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	xslices "github.com/gechr/x/slices"
)

const envKeyGitHubToken = "PRL_GITHUB_TOKEN" //nolint:gosec // env var name, not a credential

var tokenEnvKeys = []string{envKeyGitHubToken, "GITHUB_TOKEN", "GH_TOKEN"}

func githubToken() string {
	for _, key := range tokenEnvKeys {
		if token := os.Getenv(key); token != "" {
			return token
		}
	}
	if token, _ := auth.TokenForHost("github.com"); token != "" {
		return token
	}
	return ""
}

func ensureGHAuth() error {
	token := githubToken()
	if token == "" {
		return fmt.Errorf(
			"not authenticated with GitHub (set %s or run 'gh auth login')",
			tokenEnvKeys[0],
		)
	}
	_ = os.Setenv("GH_TOKEN", token)
	return nil
}

// githubErrorDetail returns the error messages GitHub sent that go-gh dropped
// from err. api.HandleHTTPError swaps an item's own message for a generic
// "<resource>.<field> is invalid" summary whenever the item carries a code
// other than "custom", so a search refused because the token may not reach
// `user:<org>` reports only "Search.q is invalid" and loses GitHub's reason.
// The originals survive on HTTPError.Errors.
func githubErrorDetail(err error) []string {
	httpErr, ok := errors.AsType[*api.HTTPError](err)
	if !ok {
		return nil
	}
	detail := make([]string, 0, len(httpErr.Errors))
	for _, item := range httpErr.Errors {
		msg := strings.TrimSpace(item.Message)
		if msg == "" || strings.Contains(httpErr.Message, msg) {
			continue
		}
		detail = append(detail, msg)
	}
	return xslices.Unique(detail)
}

// githubDetailError appends GitHub's dropped messages to the error it wraps.
// Wrapping happens where errors are displayed, leaving the wrapped error itself
// intact so errors.Is and errors.As keep matching what the caller returned.
type githubDetailError struct {
	err    error
	detail []string
}

func (e *githubDetailError) Error() string {
	return strings.Join(append([]string{e.err.Error()}, e.detail...), "\n")
}

func (e *githubDetailError) Unwrap() error { return e.err }

// withGitHubErrorDetail returns err with GitHub's own messages appended when
// go-gh replaced them with its generic summary, and err untouched otherwise.
func withGitHubErrorDetail(err error) error {
	detail := githubErrorDetail(err)
	if len(detail) == 0 {
		return err
	}
	return &githubDetailError{err: err, detail: detail}
}

// getCurrentLogin returns the login of the authenticated GitHub user.
func getCurrentLogin(rest *api.RESTClient) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if err := rest.Get("user", &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// actionRunner creates an ActionRunner, initializing a GraphQL client
// only when the CLI flags require one.
func (clients *githubClients) actionRunner(cli *CLI) (*ActionRunner, error) {
	var gql *api.GraphQLClient
	if cli.Approve || cli.ForceMerge || cli.MarkDraft || cli.MarkReady || cli.Merge != nil ||
		cli.Unsubscribe {
		var err error
		gql, err = clients.graphQL()
		if err != nil {
			return nil, err
		}
	}
	return NewActionRunner(clients.rest, gql), nil
}

type githubClients struct {
	rest    *api.RESTClient
	graphQL func() (*api.GraphQLClient, error)
	events  func() (client.EventClient, error)
}

func newGitHubClients(debug bool) (*githubClients, error) {
	opts := api.ClientOptions{Transport: sharedGitHubRateLimiter.wrap(nil)}
	if debug {
		opts.Log = os.Stderr
	}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, fmt.Errorf("creating REST client: %w", err)
	}
	getGraphQL := sync.OnceValues(func() (*api.GraphQLClient, error) {
		gql, err := api.NewGraphQLClient(opts)
		if err != nil {
			return nil, fmt.Errorf("creating GraphQL client: %w", err)
		}
		return gql, nil
	})
	return &githubClients{
		rest:    rest,
		graphQL: getGraphQL,
		events:  sync.OnceValues(newEventClient),
	}, nil
}
