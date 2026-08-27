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

	prs, err := executeListSearch(
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
	require.Len(t, prs, 1)
	require.Equal(t, 7, prs[0].Number)
	require.Equal(t, "owner/repo", prs[0].Repository.NameWithOwner)
	require.Equal(t, "Restore fallback", prs[0].Title)
}

func TestToPullRequestGraphQLLeavesEnrichmentUnloaded(t *testing.T) {
	node := graphQLSearchPRNode{
		Author: &struct {
			Login string `json:"login"`
		}{Login: "alice"},
		AutoMergeRequest: &struct {
			EnabledAt string `json:"enabledAt"`
		}{EnabledAt: "2026-06-24T12:01:00Z"},
		CreatedAt:  mustTime(t, "2026-06-24T11:00:00Z"),
		HeadRefOID: "abc123",
		ID:         "PR_node",
		Number:     42,
		Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"},
		State:      "OPEN",
		Title:      "  Ship GraphQL  ",
		UpdatedAt:  mustTime(t, "2026-06-24T12:00:00Z"),
		URL:        "https://github.com/owner/repo/pull/42",
	}
	node.Labels.Nodes = []struct {
		Name string `json:"name"`
	}{{Name: "enhancement"}}

	pr := toPullRequestGraphQL(node)

	require.Equal(t, 42, pr.Number)
	require.Equal(t, "Ship GraphQL", pr.Title)
	require.Equal(t, "  Ship GraphQL  ", pr.TitleRaw)
	require.Equal(t, valueOpen, pr.State)
	require.Equal(t, "abc123", pr.HeadSHA)
	require.Equal(t, []Label{{Name: "enhancement"}}, pr.Labels)

	// Merge status and review decision are hydrated separately so the search
	// query stays cheap enough to survive a full page of results. Auto-merge is
	// a scalar and stays in the search response.
	require.Equal(t, MergeStatusUnknown, pr.MergeStatus)
	require.False(t, pr.reviewDecisionLoaded)
	require.True(t, pr.automergeLoaded)
	require.True(t, pr.Automerge)
}

// searchPRsGoldenQuery is the exact document executeSearchGraphQL sends. It is
// pinned deliberately: adding per-node mergeability or check-suite fields here
// makes a full page of search results expensive enough for GitHub to time the
// query out, which is what hydrateListMetadata's chunked node queries exist to
// avoid.
const searchPRsGoldenQuery = `query SearchPullRequests($query: String!, $first: Int!, $after: String) {
				search(type: ISSUE, query: $query, first: $first, after: $after) {
					nodes {
						... on PullRequest {
							id
							number
							title
							url
							state
							isDraft
							createdAt
							updatedAt
							mergedAt
							headRefOid
							autoMergeRequest { enabledAt }
							author { login }
							repository { name nameWithOwner }
							labels(first: 100) { nodes { name } }
						}
					}
					pageInfo { hasNextPage endCursor }
				}
			}`

func TestExecuteSearchGraphQLSendsPinnedQuery(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		require.Equal(t, searchPRsGoldenQuery, decodeGraphQLBody(t, readBody(t, req.Body)).Query)
		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}`,
		), nil
	})
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	_, err = executeSearchGraphQL(gql, &SearchParams{
		Query:      "is:pr archived:false",
		PerPage:    100,
		TotalLimit: 100,
	})
	require.NoError(t, err)
}
