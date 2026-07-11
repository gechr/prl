package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}

func TestGraphQLSearchQueryCarriesSort(t *testing.T) {
	params := &SearchParams{
		Query: "is:pr archived:false",
		Sort:  valueUpdated,
		Order: valueDesc,
	}

	require.Equal(t, "is:pr archived:false sort:updated-desc", graphQLSearchQuery(params))
}

func TestExecuteListSearchFallsBackWhenGraphQLIsEmpty(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/graphql":
			gqlReq := decodeGraphQLBody(t, readBody(t, req.Body))
			require.Equal(t, "is:pr archived:false sort:updated-desc", gqlReq.Variables["query"])
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}`,
			), nil
		case "/search/issues":
			require.Equal(t, "true", req.URL.Query().Get("advanced_search"))
			require.Equal(t, "is:pr archived:false", req.URL.Query().Get("q"))
			return jsonResponse(
				req,
				http.StatusOK,
				`{"total_count":1,"items":[{
					"created_at":"2026-06-24T11:00:00Z",
					"draft":false,
					"html_url":"https://github.com/owner/repo/pull/7",
					"labels":[],
					"node_id":"PR_7",
					"number":7,
					"pull_request":{"merged_at":null},
					"repository_url":"https://api.github.com/repos/owner/repo",
					"state":"open",
					"title":"Restore fallback",
					"updated_at":"2026-06-24T12:00:00Z",
					"user":{"login":"alice"}
				}]}`,
			), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
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

	prs, hydrated, err := executeListSearch(
		rest,
		func() (*api.GraphQLClient, error) { return gql, nil },
		&SearchParams{
			Query:      "is:pr archived:false",
			Sort:       valueUpdated,
			Order:      valueDesc,
			PerPage:    30,
			TotalLimit: 30,
		},
		true,
	)

	require.NoError(t, err)
	require.False(t, hydrated)
	require.Len(t, prs, 1)
	require.Equal(t, 7, prs[0].Number)
	require.Equal(t, "owner/repo", prs[0].Repository.NameWithOwner)
	require.Equal(t, "Restore fallback", prs[0].Title)
}

func TestToPullRequestGraphQLHydratesMergeStatus(t *testing.T) {
	node := graphQLSearchPRNode{
		CreatedAt:        mustTime(t, "2026-06-24T11:00:00Z"),
		HeadRefOID:       "abc123",
		ID:               "PR_node",
		MergeStateStatus: valueMergeStateClean,
		Number:           42,
		Repository:       Repository{Name: "repo", NameWithOwner: "owner/repo"},
		ReviewDecision:   new(valueReviewApproved),
		State:            "OPEN",
		Title:            "  Ship GraphQL  ",
		UpdatedAt:        mustTime(t, "2026-06-24T12:00:00Z"),
		URL:              "https://github.com/owner/repo/pull/42",
	}
	node.Author = &struct {
		Login string `json:"login"`
	}{Login: "alice"}
	node.AutoMergeRequest = &struct {
		EnabledAt string `json:"enabledAt"`
	}{EnabledAt: "2026-06-24T12:01:00Z"}
	node.Labels.Nodes = []struct {
		Name string `json:"name"`
	}{{Name: "enhancement"}}
	node.Commits.Nodes = []struct {
		Commit struct {
			CheckSuites       listCheckSuites `json:"checkSuites"`
			StatusCheckRollup *struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}{{}}
	node.Commits.Nodes[0].Commit.StatusCheckRollup = &struct {
		State string `json:"state"`
	}{State: valueCISuccess}

	pr := toPullRequestGraphQL(node)

	require.Equal(t, 42, pr.Number)
	require.Equal(t, "Ship GraphQL", pr.Title)
	require.Equal(t, "  Ship GraphQL  ", pr.TitleRaw)
	require.Equal(t, valueOpen, pr.State)
	require.Equal(t, MergeStatusReady, pr.MergeStatus)
	require.True(t, pr.Automerge)
	require.True(t, pr.automergeLoaded)
	require.True(t, pr.reviewDecisionLoaded)
	require.Equal(t, []Label{{Name: "enhancement"}}, pr.Labels)
}
