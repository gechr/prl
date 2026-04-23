package main

import (
	"encoding/json"
	"net/http"
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
		require.NotContains(t, body, `"PR_1"`)
		require.Contains(t, body, `"PR_2"`)

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
			`query ListMetadata($timelineIDs: [ID!]!, $automergeIDs: [ID!]!, $mergeIDs: [ID!]!){timelineNodes:nodes(ids:$timelineIDs){... on PullRequest{id closed:timelineItems(itemTypes:[CLOSED_EVENT],last:1){nodes{... on ClosedEvent{actor{login}}}} merged:timelineItems(itemTypes:[MERGED_EVENT],last:1){nodes{... on MergedEvent{actor{login}}}}}} automergeNodes:nodes(ids:$automergeIDs){... on PullRequest{id autoMergeRequest{enabledAt}}} mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{id reviewDecision commits(last:1){nodes{commit{statusCheckRollup{state}}}} autoMergeRequest{enabledAt}}}}`,
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
					{"id":"PR_1","reviewDecision":"APPROVED","autoMergeRequest":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}
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
			`query ListMetadata($mergeIDs: [ID!]!){mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{id reviewDecision commits(last:1){nodes{commit{statusCheckRollup{state}}}}}}}`,
			got.Query,
		)
		require.Equal(t, map[string][]string{"mergeIDs": {"PR_1"}}, got.Variables)

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
