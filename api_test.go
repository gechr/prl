package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestMergeOrAutomergeBlockedFallsBackToAutoMergeWhenQueueUnavailable(t *testing.T) {
	t.Helper()

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/graphql":
			body := readBody(t, req.Body)
			switch {
			case strings.Contains(body, "enablePullRequestAutoMerge"):
				return jsonResponse(
					req,
					http.StatusOK,
					`{"data":{"enablePullRequestAutoMerge":{"clientMutationId":"ok"}}}`,
				), nil
			case strings.Contains(body, "enqueuePullRequest"):
				return jsonResponse(
					req,
					http.StatusOK,
					`{"errors":[{"message":"merge queue unavailable"}]}`,
				), nil
			default:
				t.Fatalf("unexpected GraphQL call: %s", body)
				return nil, errUnexpectedGraphQLCall
			}
		case "/repos/owner/repo/pulls/42/merge":
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

	result, err := actions.mergeOrAutomerge("owner", "repo", pr)
	require.NoError(t, err)
	require.Equal(t, resultAutomerged, result)
}

func TestAutomergeMutationsUseCurrentGitHubFieldNames(t *testing.T) {
	t.Helper()

	var seen []string
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/graphql" {
			return jsonResponse(req, http.StatusNotFound, `{"message":"not found"}`), nil
		}

		body := readBody(t, req.Body)
		switch {
		case strings.Contains(body, "enablePullRequestAutoMerge"):
			seen = append(seen, "enable")
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"enablePullRequestAutoMerge":{"clientMutationId":"ok"}}}`,
			), nil
		case strings.Contains(body, "disablePullRequestAutoMerge"):
			seen = append(seen, "disable")
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"disablePullRequestAutoMerge":{"clientMutationId":"ok"}}}`,
			), nil
		default:
			t.Fatalf("unexpected GraphQL call: %s", body)
			return nil, errUnexpectedGraphQLCall
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

	require.NoError(t, actions.enableAutomerge("PR_node"))
	require.NoError(t, actions.disableAutomerge("PR_node"))
	require.Equal(t, []string{"enable", "disable"}, seen)
}

func TestExecuteBulkUnsubscribeResolvesCurrentLoginOnce(t *testing.T) {
	t.Helper()

	var userCalls atomic.Int32
	var unsubCalls atomic.Int32

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/user":
			userCalls.Add(1)
			return jsonResponse(req, http.StatusOK, `{"login":"me"}`), nil
		case "/graphql":
			body := readBody(t, req.Body)
			require.Contains(t, body, "updateSubscription")
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"updateSubscription":{"clientMutationId":"ok"}}}`,
			), nil
		case "/repos/owner/repo/pulls/1/requested_reviewers",
			"/repos/owner/repo/pulls/2/requested_reviewers":
			unsubCalls.Add(1)
			require.Equal(t, http.MethodDelete, req.Method)
			return jsonResponse(req, http.StatusNoContent, `{}`), nil
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
	err = actions.Execute(&CLI{Unsubscribe: true}, []PullRequest{
		{
			NodeID:     "PR_1",
			Number:     1,
			URL:        "https://github.com/owner/repo/pull/1",
			Repository: Repository{NameWithOwner: "owner/repo", Name: "repo"},
		},
		{
			NodeID:     "PR_2",
			Number:     2,
			URL:        "https://github.com/owner/repo/pull/2",
			Repository: Repository{NameWithOwner: "owner/repo", Name: "repo"},
		},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, userCalls.Load())
	require.EqualValues(t, 2, unsubCalls.Load())
}

func TestExecuteForceMergeBatchesCheckPolling(t *testing.T) {
	t.Helper()

	checksStarted := make(chan string, 1)
	mergesStarted := make(chan string, 2)
	releaseChecks := make(chan struct{})
	var checkCalls atomic.Int32

	nodeIDFromBody := func(body string) string {
		switch {
		case strings.Contains(body, `"PR_1"`):
			return "PR_1"
		case strings.Contains(body, `"PR_2"`):
			return "PR_2"
		default:
			t.Fatalf("missing PR node id in GraphQL body: %s", body)
			return ""
		}
	}

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/graphql" {
			return jsonResponse(req, http.StatusNotFound, `{"message":"not found"}`), nil
		}

		body := readBody(t, req.Body)
		switch {
		case strings.Contains(body, "query CheckStates"):
			checkCalls.Add(1)
			checksStarted <- body
			<-releaseChecks
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"nodes":[
					{"id":"PR_1","state":"OPEN","mergeStateStatus":"CLEAN","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
					{"id":"PR_2","state":"OPEN","mergeStateStatus":"CLEAN","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}
				]}}`,
			), nil
		case strings.Contains(body, "mutation ForceMerge"):
			mergesStarted <- nodeIDFromBody(body)
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"mergePullRequest":{"clientMutationId":"ok"}}}`,
			), nil
		default:
			t.Fatalf("unexpected GraphQL request: %s", body)
			return nil, errUnexpectedGraphQLCall
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
	prs := []PullRequest{
		{
			NodeID:     "PR_1",
			Number:     1,
			URL:        "https://github.com/owner/repo/pull/1",
			Repository: Repository{NameWithOwner: "owner/repo", Name: "repo"},
		},
		{
			NodeID:     "PR_2",
			Number:     2,
			URL:        "https://github.com/owner/repo/pull/2",
			Repository: Repository{NameWithOwner: "owner/repo", Name: "repo"},
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- actions.Execute(&CLI{ForceMerge: true}, prs)
	}()

	select {
	case body := <-checksStarted:
		require.Contains(t, body, `"PR_1"`)
		require.Contains(t, body, `"PR_2"`)
	case <-time.After(2 * time.Second):
		t.Fatal("batched force-merge check poll did not start")
	}

	select {
	case nodeID := <-mergesStarted:
		close(releaseChecks)
		t.Fatalf("merge started too early before all waiting PRs began polling: %s", nodeID)
	default:
	}

	close(releaseChecks)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("force-merge execution did not finish")
	}

	require.EqualValues(t, 1, checkCalls.Load())
	require.ElementsMatch(t, []string{"PR_1", "PR_2"}, []string{<-mergesStarted, <-mergesStarted})
}

func TestFetchPRDetailUsesSingleGraphQLPageWhenItFits(t *testing.T) {
	t.Helper()

	var restCalls atomic.Int32
	var gqlCalls atomic.Int32

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/graphql":
			gqlCalls.Add(1)
			body := readBody(t, req.Body)
			require.Contains(t, body, "query PRDetailPage")
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"node":{
					"body":"hello world",
					"mergeStateStatus":"BEHIND",
					"reviews":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
						{"author":{"login":"alice"},"state":"COMMENTED"},
						{"author":{"login":"alice"},"state":"APPROVED"},
						{"author":{"login":"bob"},"state":"CHANGES_REQUESTED"}
					]},
					"files":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
						{"path":"gone.txt","changeType":"DELETED","additions":0,"deletions":4}
					]},
					"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
						{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS","startedAt":"2026-04-10T00:00:00Z","completedAt":"2026-04-10T00:01:00Z"},
						{"__typename":"StatusContext","context":"lint","state":"PENDING"}
					]}}}}]}
				}}}`,
			), nil
		default:
			restCalls.Add(1)
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
	detail, err := actions.fetchPRDetail("owner", "repo", 42, "PR_node")
	require.NoError(t, err)

	require.Zero(t, restCalls.Load())
	require.EqualValues(t, 1, gqlCalls.Load())
	require.Equal(t, "hello world", detail.Body)
	require.Equal(t, valueBehind, detail.MergeableState)
	require.Equal(t, []PRReview{
		{User: "alice", State: "APPROVED"},
		{User: "bob", State: "CHANGES_REQUESTED"},
	}, detail.Reviews)
	require.Equal(t, []PRFile{{
		Filename:  "gone.txt",
		Status:    "removed",
		Additions: 0,
		Deletions: 4,
	}}, detail.Files)
	require.Len(t, detail.Checks, 2)
	require.Equal(t, "lint", detail.Checks[0].Name)
	require.Equal(t, "build", detail.Checks[1].Name)
}

func TestFetchPRDetailPaginatesFilesWhenNeeded(t *testing.T) {
	t.Helper()

	var gqlCalls atomic.Int32

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/graphql" {
			return jsonResponse(req, http.StatusNotFound, `{"message":"not found"}`), nil
		}

		gqlCalls.Add(1)
		body := readBody(t, req.Body)
		switch {
		case strings.Contains(body, "query PRDetailPage"):
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"node":{
					"body":"body",
					"mergeStateStatus":"CLEAN",
					"reviews":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]},
					"files":{"pageInfo":{"hasNextPage":true,"endCursor":"files-2"},"nodes":[
						{"path":"a.txt","changeType":"ADDED","additions":1,"deletions":0}
					]},
					"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}]}
				}}}`,
			), nil
		case strings.Contains(body, "query PRDetailFiles"):
			require.Contains(t, body, "files-2")
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"node":{"files":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
					{"path":"b.txt","changeType":"RENAMED","additions":2,"deletions":1}
				]}}}}`,
			), nil
		default:
			t.Fatalf("unexpected GraphQL request: %s", body)
			return nil, errUnexpectedGraphQLCall
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
	detail, err := actions.fetchPRDetail("owner", "repo", 42, "PR_node")
	require.NoError(t, err)

	require.EqualValues(t, 2, gqlCalls.Load())
	require.Equal(t, []PRFile{
		{Filename: "a.txt", Status: "added", Additions: 1, Deletions: 0},
		{Filename: "b.txt", Status: "renamed", Additions: 2, Deletions: 1},
	}, detail.Files)
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
