package main

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var errUnexpectedConditionalCall = errors.New("unexpected conditional transport call")

func TestSecondaryRateLimitRetryPreservesBodyAndUsesRetryAfter(t *testing.T) {
	resp := jsonResponse(
		&http.Request{Method: http.MethodGet},
		http.StatusForbidden,
		`{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`,
	)
	resp.Header.Set(headerRetryAfter, "7")

	wait, ok := secondaryRateLimitRetry(resp)
	require.True(t, ok)
	require.Equal(t, 7*time.Second, wait)
	require.Contains(t, readBody(t, resp.Body), "secondary rate limit")
}

func TestSecondaryRateLimitRetryFallsBackWithoutHeader(t *testing.T) {
	resp := jsonResponse(
		&http.Request{Method: http.MethodGet},
		http.StatusForbidden,
		`{"message":"You have exceeded a secondary rate limit."}`,
	)

	wait, ok := secondaryRateLimitRetry(resp)
	require.True(t, ok)
	require.Equal(t, githubSecondaryRetryFallback, wait)
}

func TestSecondaryRateLimitRetryUsesRateLimitReset(t *testing.T) {
	resp := jsonResponse(
		&http.Request{Method: http.MethodGet},
		http.StatusForbidden,
		`{"message":"API rate limit exceeded"}`,
	)
	resp.Header.Set(headerRateLimitRemaining, "0")
	resp.Header.Set(
		headerRateLimitReset,
		strconv.FormatInt(time.Now().Add(4*time.Second).Unix(), 10),
	)

	wait, ok := secondaryRateLimitRetry(resp)
	require.True(t, ok)
	require.GreaterOrEqual(t, wait, 3*time.Second)
}

func TestConditionalGetReusesCachedBodyOnNotModified(t *testing.T) {
	limiter := newGitHubRateLimiter(0)
	var calls int
	transport := limiter.wrap(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			resp := jsonResponse(req, http.StatusOK, `{"items":[1]}`)
			resp.Header.Set("ETag", `"etag-1"`)
			return resp, nil
		case 2:
			require.Equal(t, `"etag-1"`, req.Header.Get("If-None-Match"))
			return &http.Response{
				StatusCode: http.StatusNotModified,
				Status:     "304 Not Modified",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected call %d", calls)
			return nil, errUnexpectedConditionalCall
		}
	}))

	req1, err := http.NewRequest(http.MethodGet, "https://api.github.com/search/issues?q=test", nil)
	require.NoError(t, err)
	resp1, err := transport.RoundTrip(req1)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":[1]}`, readBody(t, resp1.Body))

	req2, err := http.NewRequest(http.MethodGet, "https://api.github.com/search/issues?q=test", nil)
	require.NoError(t, err)
	resp2, err := transport.RoundTrip(req2)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	require.JSONEq(t, `{"items":[1]}`, readBody(t, resp2.Body))
}

func TestRateLimiterRespectsCooldownOverBaseInterval(t *testing.T) {
	previous := sharedGitHubRateLimiter
	sharedGitHubRateLimiter = newGitHubRateLimiter(0)
	t.Cleanup(func() { sharedGitHubRateLimiter = previous })

	sharedGitHubRateLimiter.noteRetryAfter(12 * time.Second)

	require.GreaterOrEqual(t, refreshCooldownDelay(5*time.Second), 11*time.Second)
	require.GreaterOrEqual(t, watchInterval(0), 11*time.Second)
	require.GreaterOrEqual(t, refreshDelay(0, 0), 11*time.Second)
}
