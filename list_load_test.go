package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestListLoadFiltersAfterEnrichment(t *testing.T) {
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		Host: "github.com", AuthToken: "test",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			query := decodeGraphQLBody(t, readBody(t, req.Body)).Query
			if strings.Contains(query, "search(") {
				return jsonResponse(
					req,
					http.StatusOK,
					`{"data":{"search":{"nodes":[{"id":"P1","number":1,"state":"OPEN"},{"id":"P2","number":2,"state":"OPEN"}]}}}`,
				), nil
			}
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"mergeNodes":[{"id":"P1","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED"},{"id":"P2","mergeStateStatus":"DIRTY"}],"timelineNodes":[{"id":"P1","closed":{"nodes":[]}},{"id":"P2","closed":{"nodes":[{"actor":{"login":"reviewer"}}]}}]}}`,
			), nil
		}),
	})
	require.NoError(t, err)
	for _, watch := range []bool{false, true} {
		loader := listLoader{graphQL: func() (*api.GraphQLClient, error) { return gql, nil }}
		if watch {
			loader.cache = newListMetadataCache()
		}
		for _, tc := range []struct {
			state, ci, closedBy string
			number              int
		}{
			{"ready", "", "", 1}, {"open", "success", "", 1}, {"all", "", "reviewer", 2},
		} {
			cli := testCLI()
			cli.State, cli.CI = tc.state, tc.ci
			if tc.closedBy != "" {
				cli.ClosedBy.Values = []string{tc.closedBy}
			}
			loaded, err := loader.load(
				cli,
				&SearchParams{Query: "is:pr", PerPage: 10, TotalLimit: 10},
			)
			require.NoError(t, err)
			require.Equal(t, 2, loaded.rawFetched)
			require.Len(t, loaded.prs, 1)
			require.Equal(t, tc.number, loaded.prs[0].Number)
		}
	}
}
