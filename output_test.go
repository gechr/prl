package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestFilterBots(t *testing.T) {
	prs := []PullRequest{
		{Author: Author{Login: "user1"}},
		{Author: Author{Login: "dependabot[bot]"}},
		{Author: Author{Login: "user2"}},
		{Author: Author{Login: "renovate[bot]"}},
	}
	got := filterBots(prs)
	require.Len(t, got, 2)
	require.Equal(t, "user1", got[0].Author.Login)
	require.Equal(t, "user2", got[1].Author.Login)
}

func TestFilterByDrift(t *testing.T) {
	now := time.Now().UTC()
	prs := []PullRequest{
		{
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-47 * time.Hour),
			// Drift: 1 hour = 3600 seconds
		},
		{
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-24 * time.Hour),
			// Drift: 24 hours = 86400 seconds
		},
		{
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
			// Drift: 0 seconds
		},
	}

	// <= 1 day: should include drift 0 and 3600
	got := filterByDrift(prs, "<=", 86400)
	require.Len(t, got, 3, "filterByDrift(<=86400)")

	// < 1 hour: should only include drift 0
	got = filterByDrift(prs, "<", 3600)
	require.Len(t, got, 1, "filterByDrift(<3600)")

	// > 1 hour: should include drift 86400
	got = filterByDrift(prs, ">", 3600)
	require.Len(t, got, 1, "filterByDrift(>3600)")

	// = 0: should include drift 0
	got = filterByDrift(prs, "=", 0)
	require.Len(t, got, 1, "filterByDrift(=0)")
}

func TestAllAutomergeLoaded(t *testing.T) {
	prs := []PullRequest{
		{automergeLoaded: true},
		{automergeLoaded: true},
	}
	require.True(t, allAutomergeLoaded(prs))

	prs[1].automergeLoaded = false
	require.False(t, allAutomergeLoaded(prs))
}

func TestFilterByAutomergeState(t *testing.T) {
	prs := []PullRequest{
		{URL: "https://example.com/1", Automerge: true},
		{URL: "https://example.com/2", Automerge: false},
		{URL: "https://example.com/3", Automerge: true},
	}

	enabled := filterByAutomergeState(prs, true)
	require.Len(t, enabled, 2)
	require.Equal(t, "https://example.com/1", enabled[0].URL)
	require.Equal(t, "https://example.com/3", enabled[1].URL)

	disabled := filterByAutomergeState(prs, false)
	require.Len(t, disabled, 1)
	require.Equal(t, "https://example.com/2", disabled[0].URL)
}

func TestFetchAutomergeStatusOnlyQueriesMissingIDs(t *testing.T) {
	t.Helper()

	var calls int
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		calls++

		body := readBody(t, req.Body)
		var got struct {
			Variables map[string][]string `json:"variables"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &got))
		require.Equal(t, map[string][]string{"ids": {"PR_2"}}, got.Variables)

		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{"nodes":[{"id":"PR_2","autoMergeRequest":{"enabledAt":"2026-04-10T00:00:00Z"}}]}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := []PullRequest{
		{NodeID: "PR_1", Automerge: false, automergeLoaded: true},
		{NodeID: "PR_2"},
	}

	enabled, err := fetchAutomergeStatus(gql, prs)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, map[string]bool{"PR_2": true}, enabled)

	applyAutomergeStatus(prs, enabled)
	require.False(t, prs[0].Automerge)
	require.True(t, prs[0].automergeLoaded)
	require.True(t, prs[1].Automerge)
	require.True(t, prs[1].automergeLoaded)
}

func TestAllReviewDecisionsLoaded(t *testing.T) {
	prs := []PullRequest{
		{reviewDecisionLoaded: true},
		{reviewDecisionLoaded: true},
	}
	require.True(t, allReviewDecisionsLoaded(prs))

	prs[0].reviewDecisionLoaded = false
	require.False(t, allReviewDecisionsLoaded(prs))
}

func TestApplyReviewDecisions(t *testing.T) {
	prs := []PullRequest{
		{NodeID: "pr-1"},
		{NodeID: "pr-2"},
	}

	applyReviewDecisions(prs, map[string]string{
		"pr-1": valueReviewApproved,
		"pr-2": valueReviewChanges,
	})

	require.Equal(t, valueReviewApproved, prs[0].ReviewDecision)
	require.True(t, prs[0].reviewDecisionLoaded)
	require.Equal(t, valueReviewChanges, prs[1].ReviewDecision)
	require.True(t, prs[1].reviewDecisionLoaded)
}

func viewerReviewNode(id, login, state string) listViewerReviewNode {
	node := listViewerReviewNode{ID: id}
	node.LatestOpinionatedReviews.Nodes = append(node.LatestOpinionatedReviews.Nodes, struct {
		Author *struct {
			Login string `json:"login"`
		} `json:"author"`
		State string `json:"state"`
	}{
		Author: &struct {
			Login string `json:"login"`
		}{Login: login},
		State: state,
	})
	return node
}

func TestApplyListViewerReviewNodes(t *testing.T) {
	prs := []PullRequest{
		{NodeID: "pr-1"},
		{NodeID: "pr-2"},
		{NodeID: "pr-3"},
	}

	applyListViewerReviewNodes(prs, "alice", []listViewerReviewNode{
		viewerReviewNode("pr-1", "alice", valueReviewApproved),
		viewerReviewNode("pr-2", "alice", valueReviewChanges),
	})

	require.True(t, prs[0].viewerApprovalLoaded)
	require.True(t, prs[0].viewerApproved)
	require.True(t, prs[1].viewerApprovalLoaded)
	require.False(t, prs[1].viewerApproved)
	require.False(t, prs[2].viewerApprovalLoaded)
}

func TestFilterByViewerApproval(t *testing.T) {
	prs := []PullRequest{
		{NodeID: "pr-1", viewerApprovalLoaded: true, viewerApproved: true},
		{NodeID: "pr-2", viewerApprovalLoaded: true, viewerApproved: false},
		{NodeID: "pr-3"},
		{NodeID: "pr-4", viewerApprovalLoaded: true, viewerIsAuthor: true},
	}

	got := filterByViewerApproval(prs)

	require.Equal(t, []PullRequest{prs[1], prs[2]}, got)
}

func TestFilterByTimelineActorsLoaded(t *testing.T) {
	prs := []PullRequest{
		{
			NodeID: "pr-1",
			URL:    "https://example.com/1",
		},
		{
			NodeID: "pr-2",
			URL:    "https://example.com/2",
		},
		{
			NodeID: "pr-3",
			URL:    "https://example.com/3",
		},
	}

	actors := timelineActors{
		closed: map[string]string{
			"pr-1": "alice",
			"pr-2": "bob",
			"pr-3": "alice",
		},
		merged: map[string]string{
			"pr-1": "carol",
			"pr-2": "carol",
			"pr-3": "dave",
		},
	}

	filtered := filterByTimelineActorsLoaded(
		prs,
		map[string]bool{"alice": true},
		map[string]bool{"carol": true},
		actors,
	)
	require.Len(t, filtered, 1)
	require.Equal(t, "pr-1", filtered[0].NodeID)
}

func TestHydrateListMetadataBatchesGraphQLRequests(t *testing.T) {
	t.Helper()

	var calls int
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		calls++

		body := readBody(t, req.Body)
		var got struct {
			Query     string              `json:"query"`
			Variables map[string][]string `json:"variables"`
		}
		err := json.Unmarshal([]byte(body), &got)
		require.NoError(t, err)
		require.Equal(
			t,
			`query ListMetadata($timelineIDs: [ID!]!, $automergeIDs: [ID!]!, $mergeIDs: [ID!]!){timelineNodes:nodes(ids:$timelineIDs){... on PullRequest{id closed:timelineItems(itemTypes:[CLOSED_EVENT],last:1){nodes{... on ClosedEvent{actor{login}}}} merged:timelineItems(itemTypes:[MERGED_EVENT],last:1){nodes{... on MergedEvent{actor{login}}}}}} automergeNodes:nodes(ids:$automergeIDs){... on PullRequest{id autoMergeRequest{enabledAt}}} mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{id headRefOid mergeStateStatus reviewDecision commits(last:1){nodes{commit{statusCheckRollup{state} checkSuites(first:50){totalCount nodes{conclusion checkRuns(first:1){totalCount}}}}}} autoMergeRequest{enabledAt}}}}`,
			got.Query,
		)
		require.Equal(
			t,
			map[string][]string{
				"timelineIDs":  {"PR_1", "PR_2"},
				"automergeIDs": {"PR_2"},
				"mergeIDs":     {"PR_1"},
			},
			got.Variables,
		)

		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{
				"timelineNodes":[
					{"id":"PR_1","closed":{"nodes":[{"actor":{"login":"alice"}}]},"merged":{"nodes":[]}},
					{"id":"PR_2","closed":{"nodes":[]},"merged":{"nodes":[{"actor":{"login":"bob"}}]}}
				],
				"automergeNodes":[
					{"id":"PR_2","autoMergeRequest":{"enabledAt":"2026-04-10T00:00:00Z"}}
				],
				"mergeNodes":[
					{"id":"PR_1","headRefOid":"sha-1","reviewDecision":"APPROVED","autoMergeRequest":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}
				]
			}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := []PullRequest{
		{NodeID: "PR_1", State: valueOpen},
		{NodeID: "PR_2", State: valueClosed},
	}

	actors, err := hydrateListMetadata(gql, prs, listMetadataRequest{
		automerge:      true,
		mergeStatus:    true,
		timelineClosed: true,
		timelineMerged: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)

	require.Equal(t, MergeStatusReady, prs[0].MergeStatus)
	require.True(t, prs[0].automergeLoaded)
	require.Equal(t, valueReviewApproved, prs[0].ReviewDecision)
	require.True(t, prs[0].reviewDecisionLoaded)

	require.True(t, prs[1].Automerge)
	require.True(t, prs[1].automergeLoaded)

	require.Equal(t, "alice", actors.closed["PR_1"])
	require.Equal(t, "bob", actors.merged["PR_2"])
}

func TestHydrateListMetadataKeepsReviewDecisionOnConflicts(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		readBody(t, req.Body)
		return jsonResponse(
			req,
			http.StatusOK,
			fmt.Sprintf(`{"data":{"mergeNodes":[
				{"id":"PR_1","headRefOid":"sha-1","mergeStateStatus":%q,"reviewDecision":%q,"commits":{"nodes":[]}}
			]}}`, valueMergeStateDirty, valueReviewChanges),
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := []PullRequest{{NodeID: "PR_1", State: valueOpen}}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{mergeStatus: true})
	require.NoError(t, err)

	// A conflicted PR still has a review decision; reporting it as loaded but
	// empty renders the review column as "none".
	require.Equal(t, MergeStatusConflict, prs[0].MergeStatus)
	require.True(t, prs[0].reviewDecisionLoaded)
	require.Equal(t, valueReviewChanges, prs[0].ReviewDecision)
}

func TestHydrateListMetadataKeepsPartialResponseData(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		readBody(t, req.Body)
		// GitHub's shape when a token cannot read one PR's check suites: usable
		// data for every node, plus field-level errors for the parts it withheld.
		return jsonResponse(
			req,
			http.StatusOK,
			fmt.Sprintf(`{
				"data":{"mergeNodes":[
					{"id":"PR_1","headRefOid":"sha-1","mergeStateStatus":%q,"reviewDecision":%q,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"},"checkSuites":{"totalCount":1,"nodes":[null]}}}]}}
				]},
				"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by personal access token","path":["mergeNodes",0,"commits","nodes",0,"commit","checkSuites","nodes",0]}]
			}`, valueMergeStateClean, valueReviewApproved),
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := []PullRequest{{NodeID: "PR_1", State: valueOpen}}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{mergeStatus: true})
	require.NoError(t, err)
	require.True(t, prs[0].reviewDecisionLoaded)
	require.Equal(t, valueReviewApproved, prs[0].ReviewDecision)
	require.Equal(t, MergeStatusReady, prs[0].MergeStatus)
}

func TestHydrateListMetadataFailsWhenResponseHasNoData(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		readBody(t, req.Body)
		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{},"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by personal access token"}]}`,
		), nil
	})
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := []PullRequest{{NodeID: "PR_1", State: valueOpen}}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{mergeStatus: true})
	require.Error(t, err)
	require.False(t, prs[0].reviewDecisionLoaded)
}

func TestHydrateListMetadataHalvesChunkAfterGatewayTimeout(t *testing.T) {
	var mu sync.Mutex
	var sizes []int

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		ids, _ := decodeGraphQLBody(t, readBody(t, req.Body)).Variables["mergeIDs"].([]any)

		mu.Lock()
		sizes = append(sizes, len(ids))
		mu.Unlock()

		// GitHub gives up on the whole batch; only smaller ones come back.
		if len(ids) > 2 {
			return jsonResponse(req, http.StatusGatewayTimeout, `{"message":"Gateway Timeout"}`), nil
		}
		nodes := make([]string, 0, len(ids))
		for _, id := range ids {
			nodes = append(nodes, fmt.Sprintf(
				`{"id":%q,"headRefOid":"sha","mergeStateStatus":%q,"reviewDecision":%q,"commits":{"nodes":[]}}`,
				id, valueMergeStateClean, valueReviewApproved,
			))
		}
		return jsonResponse(
			req,
			http.StatusOK,
			fmt.Sprintf(`{"data":{"mergeNodes":[%s]}}`, strings.Join(nodes, ",")),
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := make([]PullRequest, 8)
	for i := range prs {
		prs[i] = PullRequest{NodeID: fmt.Sprintf("PR_%d", i), State: valueOpen}
	}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{mergeStatus: true})
	require.NoError(t, err)

	require.Greater(t, len(sizes), 1, "expected the failed batch to be retried in halves")
	for i := range prs {
		require.True(t, prs[i].reviewDecisionLoaded, "PR %d never enriched", i)
		require.Equal(t, MergeStatusReady, prs[i].MergeStatus)
	}
}

func TestHydrateListMetadataDoesNotRetryNonTimeoutFailures(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		readBody(t, req.Body)
		mu.Lock()
		calls++
		mu.Unlock()
		return jsonResponse(req, http.StatusForbidden, `{"message":"Forbidden"}`), nil
	})
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := make([]PullRequest, 8)
	for i := range prs {
		prs[i] = PullRequest{NodeID: fmt.Sprintf("PR_%d", i), State: valueOpen}
	}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{mergeStatus: true})
	require.Error(t, err)
	require.Equal(t, 1, calls, "a refused request must not be retried in halves")
}

func TestHydrateListMetadataChunksLargeResultSets(t *testing.T) {
	// Fixed, not derived from hydrateChunkSize: the point is that a result set
	// larger than one chunk gets split.
	const total = 53

	var mu sync.Mutex
	var batchSizes []int

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)

		gqlReq := decodeGraphQLBody(t, readBody(t, req.Body))
		ids, ok := gqlReq.Variables["mergeIDs"].([]any)
		require.True(t, ok)

		mu.Lock()
		batchSizes = append(batchSizes, len(ids))
		mu.Unlock()

		nodes := make([]string, 0, len(ids))
		for _, id := range ids {
			nodes = append(nodes, fmt.Sprintf(
				`{"id":%q,"headRefOid":"sha","mergeStateStatus":%q,"reviewDecision":%q,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}`,
				id, valueMergeStateClean, valueReviewApproved,
			))
		}
		return jsonResponse(
			req,
			http.StatusOK,
			fmt.Sprintf(`{"data":{"mergeNodes":[%s]}}`, strings.Join(nodes, ",")),
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := make([]PullRequest, total)
	for i := range prs {
		prs[i] = PullRequest{NodeID: fmt.Sprintf("PR_%d", i), State: valueOpen}
	}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{mergeStatus: true})
	require.NoError(t, err)

	// Split into several queries, none larger than the chunk size, together
	// covering every PR.
	require.Greater(t, len(batchSizes), 1)
	covered := 0
	for _, size := range batchSizes {
		require.LessOrEqual(t, size, hydrateChunkSize)
		covered += size
	}
	require.Equal(t, total, covered)

	// Every PR is enriched - no silent truncation of the oldest results.
	for i := range prs {
		require.True(t, prs[i].reviewDecisionLoaded, "PR %d missing review decision", i)
		require.Equal(t, MergeStatusReady, prs[i].MergeStatus, "PR %d missing merge status", i)
	}
}

func TestHydrateListMetadataSkipsAutomergeFieldWhenNotRequested(t *testing.T) {
	t.Helper()

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)

		body := readBody(t, req.Body)
		var got struct {
			Query     string              `json:"query"`
			Variables map[string][]string `json:"variables"`
		}
		err := json.Unmarshal([]byte(body), &got)
		require.NoError(t, err)
		require.Equal(
			t,
			`query ListMetadata($mergeIDs: [ID!]!){mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{id headRefOid mergeStateStatus reviewDecision commits(last:1){nodes{commit{statusCheckRollup{state} checkSuites(first:50){totalCount nodes{conclusion checkRuns(first:1){totalCount}}}}}}}}}`,
			got.Query,
		)
		require.Equal(t, map[string][]string{"mergeIDs": {"PR_1"}}, got.Variables)

		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{
				"mergeNodes":[
					{"id":"PR_1","headRefOid":"sha-1","reviewDecision":"APPROVED","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}
				]
			}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := []PullRequest{
		{NodeID: "PR_1", State: valueOpen},
	}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{
		mergeStatus: true,
	})
	require.NoError(t, err)
	require.Equal(t, MergeStatusReady, prs[0].MergeStatus)
	require.False(t, prs[0].Automerge)
	require.False(t, prs[0].automergeLoaded)
	require.Equal(t, valueReviewApproved, prs[0].ReviewDecision)
	require.True(t, prs[0].reviewDecisionLoaded)
}

func TestHydrateListMetadataLoadsViewerApproval(t *testing.T) {
	t.Helper()

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)

		body := readBody(t, req.Body)
		var got struct {
			Query     string              `json:"query"`
			Variables map[string][]string `json:"variables"`
		}
		err := json.Unmarshal([]byte(body), &got)
		require.NoError(t, err)
		require.Equal(
			t,
			`query ListMetadata($viewerReviewIDs: [ID!]!){viewer{login} viewerReviewNodes:nodes(ids:$viewerReviewIDs){... on PullRequest{id latestOpinionatedReviews(last:100){nodes{author{login} state}}}}}`,
			got.Query,
		)
		require.Equal(t, map[string][]string{"viewerReviewIDs": {"PR_1", "PR_2"}}, got.Variables)

		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{
				"viewer":{"login":"alice"},
				"viewerReviewNodes":[
					{"id":"PR_1","latestOpinionatedReviews":{"nodes":[{"author":{"login":"alice"},"state":"APPROVED"}]}},
					{"id":"PR_2","latestOpinionatedReviews":{"nodes":[{"author":{"login":"alice"},"state":"CHANGES_REQUESTED"}]}}
				]
			}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs := []PullRequest{
		{NodeID: "PR_1", State: valueOpen},
		{NodeID: "PR_2", State: valueOpen},
	}

	_, err = hydrateListMetadata(gql, prs, listMetadataRequest{
		viewerApproval: true,
	})
	require.NoError(t, err)
	require.True(t, prs[0].viewerApprovalLoaded)
	require.True(t, prs[0].viewerApproved)
	require.True(t, prs[1].viewerApprovalLoaded)
	require.False(t, prs[1].viewerApproved)
}

func TestHydrateListMetadataCachedReusesUnchangedPRMetadata(t *testing.T) {
	t.Helper()

	var calls int
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		calls++
		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{
				"mergeNodes":[
					{"id":"PR_1","reviewDecision":"APPROVED","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}
				]
			}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	updatedAt := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	req := listMetadataRequest{mergeStatus: true}
	cache := newListMetadataCache()

	prs := []PullRequest{{NodeID: "PR_1", State: valueOpen, UpdatedAt: updatedAt}}
	_, err = hydrateListMetadataCached(gql, prs, req, cache)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, MergeStatusReady, prs[0].MergeStatus)

	prs = []PullRequest{{NodeID: "PR_1", State: valueOpen, UpdatedAt: updatedAt}}
	_, err = hydrateListMetadataCached(gql, prs, req, cache)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, MergeStatusReady, prs[0].MergeStatus)
	require.Equal(t, valueReviewApproved, prs[0].ReviewDecision)
	require.True(t, prs[0].reviewDecisionLoaded)
}

func TestHydrateListMetadataCachedRefetchesWhenPRUpdatedAtChanges(t *testing.T) {
	t.Helper()

	var calls int
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		calls++

		state := "SUCCESS"
		if calls > 1 {
			state = "PENDING"
		}

		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{
				"mergeNodes":[
					{"id":"PR_1","reviewDecision":"APPROVED","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"`+state+`"}}}]}}
				]
			}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	req := listMetadataRequest{mergeStatus: true}
	cache := newListMetadataCache()

	prs := []PullRequest{
		{
			NodeID:    "PR_1",
			State:     valueOpen,
			UpdatedAt: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		},
	}
	_, err = hydrateListMetadataCached(gql, prs, req, cache)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, MergeStatusReady, prs[0].MergeStatus)

	prs = []PullRequest{
		{
			NodeID:    "PR_1",
			State:     valueOpen,
			UpdatedAt: time.Date(2026, 4, 23, 12, 1, 0, 0, time.UTC),
		},
	}
	_, err = hydrateListMetadataCached(gql, prs, req, cache)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, MergeStatusCIPending, prs[0].MergeStatus)
}

func TestValidateCachedHeadsInvalidatesChangedEntries(t *testing.T) {
	t.Helper()

	var calls int
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		calls++

		body := readBody(t, req.Body)
		var got struct {
			Query     string              `json:"query"`
			Variables map[string][]string `json:"variables"`
		}
		err := json.Unmarshal([]byte(body), &got)
		require.NoError(t, err)
		require.Equal(
			t,
			`query HeadRefOIDs($ids:[ID!]!){nodes(ids:$ids){... on PullRequest{id headRefOid}}}`,
			got.Query,
		)
		require.Equal(t, map[string][]string{"ids": {"PR_1"}}, got.Variables)

		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{"nodes":[{"id":"PR_1","headRefOid":"sha-2"}]}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	cache := newListMetadataCache()
	pr := PullRequest{
		NodeID:         "PR_1",
		State:          valueOpen,
		UpdatedAt:      time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		HeadSHA:        "sha-1",
		MergeStatus:    MergeStatusReady,
		ReviewDecision: valueReviewApproved,
	}
	cache.store(pr, listMetadataRequest{mergeStatus: true}, newTimelineActors())

	changed, err := validateCachedHeads(gql, []PullRequest{{
		NodeID:    "PR_1",
		State:     valueOpen,
		UpdatedAt: pr.UpdatedAt,
	}}, cache)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, calls)
	require.Empty(t, cache.pendingHeadChecks([]PullRequest{{
		NodeID:    "PR_1",
		State:     valueOpen,
		UpdatedAt: pr.UpdatedAt,
	}}))
}

func TestSortPRs(t *testing.T) {
	now := time.Now().UTC()
	prs := []PullRequest{
		{
			Repository: Repository{Name: "charlie"},
			CreatedAt:  now.Add(-3 * time.Hour),
			UpdatedAt:  now.Add(-1 * time.Hour),
		},
		{
			Repository: Repository{Name: "alpha"},
			CreatedAt:  now.Add(-1 * time.Hour),
			UpdatedAt:  now.Add(-3 * time.Hour),
		},
		{
			Repository: Repository{Name: "bravo"},
			CreatedAt:  now.Add(-2 * time.Hour),
			UpdatedAt:  now.Add(-2 * time.Hour),
		},
	}

	// Sort by name
	sortPRs(prs, SortName)
	require.Equal(t, "alpha", prs[0].Repository.Name)
	require.Equal(t, "bravo", prs[1].Repository.Name)
	require.Equal(t, "charlie", prs[2].Repository.Name)

	// Sort by created
	sortPRs(prs, SortCreated)
	require.Equal(
		t,
		"charlie",
		prs[0].Repository.Name,
		"SortCreated: expected charlie first (oldest)",
	)

	// Sort by updated
	sortPRs(prs, SortUpdated)
	require.Equal(
		t,
		"alpha",
		prs[0].Repository.Name,
		"SortUpdated: expected alpha first (oldest update)",
	)
}

func TestRenderRepos(t *testing.T) {
	prs := []PullRequest{
		{Repository: Repository{Name: "zulu"}},
		{Repository: Repository{Name: "alpha"}},
		{Repository: Repository{Name: "zulu"}},
		{Repository: Repository{Name: "bravo"}},
		{Repository: Repository{Name: "alpha"}},
	}
	got := renderRepos(prs)
	require.Equal(t, `alpha
bravo
zulu`, got)
}

func TestRenderURLs(t *testing.T) {
	prs := []PullRequest{
		{URL: "https://github.com/owner/repo1/pull/1"},
		{URL: "https://github.com/owner/repo2/pull/2"},
	}
	got := renderURLs(prs)
	want := `https://github.com/owner/repo1/pull/1
https://github.com/owner/repo2/pull/2`
	require.Equal(t, want, got)
}

func TestRenderBullets(t *testing.T) {
	prs := []PullRequest{
		{URL: "https://github.com/owner/repo1/pull/1"},
	}
	got := renderBullets(prs)
	want := "* https://github.com/owner/repo1/pull/1"
	require.Equal(t, want, got)
}

func TestResolveMergeStatus(t *testing.T) {
	approved := valueReviewApproved
	changes := valueReviewChanges

	tests := []struct {
		name             string
		ciState          string
		reviewDecision   *string
		mergeStateStatus string
		want             MergeStatus
	}{
		{"ci failed", valueCIFailure, &approved, "", MergeStatusCIFailed},
		{"ci pending", valueCIPending, &approved, "", MergeStatusCIPending},
		{"ci success + approved", valueCISuccess, &approved, "", MergeStatusReady},
		// CODEOWNERS-only repos: required_reviewers=0 leaves reviewDecision empty but mergeStateStatus=CLEAN
		{
			"clean + ci success + empty reviewDecision",
			valueCISuccess,
			nil,
			valueMergeStateClean,
			MergeStatusReady,
		},
		// repos with no CI checks: ciState="" but mergeStateStatus=CLEAN
		{"clean + no ci + empty reviewDecision", "", nil, valueMergeStateClean, MergeStatusReady},
		{"ci success + no approval + not clean", valueCISuccess, nil, "", MergeStatusBlocked},
		// Repos without a required-review rule report CLEAN even when a reviewer
		// asked for changes.
		{
			"clean + changes requested",
			valueCISuccess,
			&changes,
			valueMergeStateClean,
			MergeStatusBlocked,
		},
		{
			"ci success + changes requested + not clean",
			valueCISuccess,
			&changes,
			"",
			MergeStatusBlocked,
		},
		{
			"ci success + no approval + dirty",
			valueCISuccess,
			nil,
			valueMergeStateDirty,
			MergeStatusBlocked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMergeStatus(tt.ciState, tt.reviewDecision, tt.mergeStateStatus)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCheckSuitesCIStateIgnoresPhantomSuites(t *testing.T) {
	suites := listCheckSuites{
		TotalCount: 2,
		Nodes: []listCheckSuite{
			testCheckSuite(new(valueCIFailure), 0),
			testCheckSuite(nil, 0),
		},
	}

	got, ok := checkSuitesCIState(suites)
	require.True(t, ok)
	require.Empty(t, got)
}

func TestCheckSuitesCIStateCountsStartupFailuresWithoutRuns(t *testing.T) {
	suites := listCheckSuites{
		TotalCount: 1,
		Nodes:      []listCheckSuite{testCheckSuite(new(valueCIStartupFailed), 0)},
	}

	got, ok := checkSuitesCIState(suites)
	require.True(t, ok)
	require.Equal(t, valueCIFailure, got)
}

func TestCheckSuitesCIStateDegradesTruncatedPassToPending(t *testing.T) {
	suites := listCheckSuites{
		TotalCount: 2,
		Nodes:      []listCheckSuite{testCheckSuite(new(valueCISuccess), 1)},
	}

	got, ok := checkSuitesCIState(suites)
	require.True(t, ok)
	require.Equal(t, valueCIPending, got)
}

func TestCheckSuitesCIStatePassesWhenAllVisibleSuitesPass(t *testing.T) {
	suites := listCheckSuites{
		TotalCount: 1,
		Nodes:      []listCheckSuite{testCheckSuite(new(valueCISuccess), 1)},
	}

	got, ok := checkSuitesCIState(suites)
	require.True(t, ok)
	require.Equal(t, valueCISuccess, got)
}

func testCheckSuite(conclusion *string, runs int) listCheckSuite {
	return listCheckSuite{
		CheckRuns: new(struct {
			TotalCount int `json:"totalCount"`
		}{TotalCount: runs}),
		Conclusion: conclusion,
	}
}
