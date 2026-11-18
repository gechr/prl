package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestCompactAuthorSearchOrdersMatchesAcrossWindows(t *testing.T) {
	const base = "is:pr repo:sample-org/project"
	const prefix = "(" + base + ") AND created:2025-03-01T12:00:"
	const middle = "Z..2025-03-01T12:00:"
	const suffix = "Z sort:created-desc"
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := decodeGraphQLBody(t, readBody(t, req.Body))
		switch body.Query {
		case compactAuthorPage:
			// The newest range needs two pages. The next range supplies the
			// final match regardless of the order responses arrive in.
			var nodes []map[string]any
			total := 1
			next := ""
			switch body.Variables["q"] {
			case prefix + "06" + middle + "07" + suffix:
				total = 2
				if body.Variables["after"] == nil {
					nodes = []map[string]any{
						{"id": "unrelated", "author": map[string]string{"login": "another-user"}},
					}
					next = "next"
				} else {
					nodes = []map[string]any{
						{"id": "newer", "author": map[string]string{"login": "Sample-User"}},
					}
				}
			case prefix + "04" + middle + "05" + suffix:
				nodes = []map[string]any{
					{"id": "older", "author": map[string]string{"login": "sample-user"}},
				}
			case prefix + "00" + middle + "01" + suffix, prefix + "02" + middle + "03" + suffix:
				total = 0
			default:
				return nil, errors.New("unexpected candidate range")
			}
			payload, err := json.Marshal(
				map[string]any{
					"data": map[string]any{
						"search": map[string]any{
							"issueCount": total,
							"nodes":      nodes,
							"pageInfo":   graphQLPageInfo{HasNextPage: next != "", EndCursor: next},
						},
					},
				},
			)
			if err != nil {
				return nil, err
			}
			return jsonResponse(req, 200, string(payload)), nil
		default:
			require.Equal(t, []any{"newer", "older"}, body.Variables["ids"])
			return jsonResponse(
				req,
				200,
				`{"data":{"nodes":[{"id":"newer","number":7},{"id":"older","number":5}]}}`,
			), nil
		}
	})
	gql, err := api.NewGraphQLClient(
		api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: transport},
	)
	require.NoError(t, err)
	start := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	rows, err := compactAuthorSearch(
		gql,
		base,
		"sample-user",
		valueCreated,
		valueDesc,
		2,
		searchWindow{start, start.Add(7 * time.Second)},
	)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, 7, rows[0].Number)
	require.Equal(t, 5, rows[1].Number)
}
