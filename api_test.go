package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

var errUnexpectedGraphQLCall = errors.New("unexpected GraphQL call")

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMergeOrAutomergeBlockedDoesNotFallBackToDirectMerge(t *testing.T) {
	t.Helper()

	var restCalls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/graphql":
			return jsonResponse(
				req,
				http.StatusOK,
				`{"errors":[{"message":"automerge unavailable"}]}`,
			), nil
		case "/repos/owner/repo/pulls/42/merge":
			restCalls.Add(1)
			return jsonResponse(
				req,
				http.StatusMethodNotAllowed,
				`{"message":"merge not allowed"}`,
			), nil
		default:
			return jsonResponse(req, http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})

	rest, err := api.NewRESTClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	actions := NewActionRunner(rest, gql)
	pr := PullRequest{
		MergeStatus: MergeStatusBlocked,
		NodeID:      "PR_node",
		Number:      42,
	}

	_, err = actions.mergeOrAutomerge("owner", "repo", pr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "enable automerge")
	require.Contains(t, err.Error(), "enqueue PR")
	require.Zero(t, restCalls.Load(), "blocked PRs should not try the direct merge endpoint")
}

func TestMergeOrAutomergeReadyStillPrefersDirectMerge(t *testing.T) {
	t.Helper()

	var mergeCalls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/owner/repo/pulls/42/merge":
			mergeCalls.Add(1)
			return jsonResponse(req, http.StatusOK, `{"merged":true}`), nil
		case "/graphql":
			t.Fatalf("unexpected GraphQL call for ready PR: %s", readBody(t, req.Body))
			return nil, errUnexpectedGraphQLCall
		default:
			return jsonResponse(req, http.StatusNotFound, `{"message":"not found"}`), nil
		}
	})

	rest, err := api.NewRESTClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	actions := NewActionRunner(rest, gql)
	pr := PullRequest{
		MergeStatus: MergeStatusReady,
		NodeID:      "PR_node",
		Number:      42,
	}

	result, err := actions.mergeOrAutomerge("owner", "repo", pr)
	require.NoError(t, err)
	require.Equal(t, resultMerged, result)
	require.EqualValues(t, 1, mergeCalls.Load())
}

func jsonResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func readBody(t *testing.T, body io.ReadCloser) string {
	t.Helper()

	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	return string(bytes.TrimSpace(data))
}
