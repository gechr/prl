package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestBuildCloneTargetsUsesRepoNumberGraphQLFallbackForMissingNodeID(t *testing.T) {
	t.Helper()

	var graphQLCalls int
	gqlTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/graphql", req.URL.Path)
		graphQLCalls++

		body := readBody(t, req.Body)
		var payload struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &payload))
		require.Equal(
			t,
			`query {_repo_0: repository(owner: "owner", name: "repo") {_pr_0_0: pullRequest(number: 1315) { headRefName }}}`,
			payload.Query,
		)

		return jsonResponse(
			req,
			http.StatusOK,
			`{"data":{"_repo_0":{"_pr_0_0":{"headRefName":"chore/fetch-head-via-graphql"}}}}`,
		), nil
	})

	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: gqlTransport,
	})
	require.NoError(t, err)

	restTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected REST request: %s", req.URL.Path)
		return nil, nil //nolint:nilnil // test transport always fails the test above
	})

	rest, err := api.NewRESTClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: restTransport,
	})
	require.NoError(t, err)

	targets := buildCloneTargets(rest, gql, []PullRequest{
		{
			Number: 1315,
			Repository: Repository{
				Name:          "repo",
				NameWithOwner: "owner/repo",
			},
		},
	})

	require.Equal(t, 1, graphQLCalls)
	require.Equal(t, []cloneTarget{
		{
			NameWithOwner: "owner/repo",
			Branch:        "chore/fetch-head-via-graphql",
			Number:        1315,
		},
	}, targets)
}
