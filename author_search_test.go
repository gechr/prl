package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestAuthorSearchFallback(t *testing.T) {
	const reason = `GitHub rejected the author filter for "sample-user" (the profile may be private) - try again with `
	const dateHint = "a time window such as `--created=7d`"
	const scopeHint = "an owner (-O) or repository (-R)"

	const invalid = `{"message":"Validation Failed","errors":[{"resource":"Search","field":"q","code":"invalid","message":"The listed users cannot be searched"}]}`
	for _, tc := range []struct {
		name, date, errorText          string
		resolves, searchable, unscoped bool
		wantProbes, wantPages          int
	}{
		{name: "unknown login", errorText: "422"},
		{name: "bad owner", resolves: true, searchable: true, wantProbes: 1, errorText: "422"},
		{name: "missing date", resolves: true, wantProbes: 1, errorText: reason + dateHint},
		{name: "upper bound only", date: "<2025-07-01", resolves: true, wantProbes: 1, errorText: reason + dateHint},
		{name: "missing scope", date: "7d", resolves: true, unscoped: true, wantProbes: 1, errorText: reason + scopeHint},
		{name: "missing scope and date", resolves: true, unscoped: true, wantProbes: 1, errorText: reason + dateHint + " and " + scopeHint},
		{name: "bounded", date: "7d", resolves: true, wantProbes: 1, wantPages: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			author := CSVFlag{Values: []string{"sample-user"}}
			owner := CSVFlag{Values: []string{"sample-org"}}
			if tc.unscoped {
				owner = CSVFlag{}
			}
			params, err := buildSearchQuery(
				&CLI{
					Owner:   owner,
					Author:  &author,
					Created: tc.date,
					Limit:   new(1),
				},
				&Config{},
			)
			require.NoError(t, err)
			probes, pages := 0, 0
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/users/sample-user":
					if !tc.resolves {
						return jsonResponse(req, 404, `{}`), nil
					}
					return jsonResponse(req, 200, `{"login":"sample-user"}`), nil
				case "/search/issues":
					values := req.URL.Query()
					switch q := values.Get("q"); q {
					case params.Query:
						return jsonResponse(req, 422, invalid), nil
					case "is:pr author:sample-user":
						probes++
						if tc.searchable {
							return jsonResponse(req, 200, `{}`), nil
						}
						return jsonResponse(req, 422, invalid), nil
					default:
						require.Equal(
							t,
							strings.Replace(params.Query, " author:sample-user", "", 1),
							q,
						)
						require.Equal(t, "100", values.Get("per_page"))
						pages++
						if values.Get("page") == "1" {
							body, marshalErr := json.Marshal(
								searchResponse{Items: make([]searchItem, 100), TotalCount: 101},
							)
							require.NoError(t, marshalErr)
							return jsonResponse(req, 200, string(body)), nil
						}
						require.Equal(t, "2", values.Get("page"))
						return jsonResponse(
							req,
							200,
							`{"total_count":101,"items":[{"number":17,"user":{"login":"Sample-User"}}]}`,
						), nil
					}
				default:
					t.Fatalf("unexpected endpoint %s", req.URL.Path)
					return nil, errors.New("unexpected endpoint")
				}
			})
			rest, err := api.NewRESTClient(
				api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: transport},
			)
			require.NoError(t, err)
			rows, err := executeListSearch(rest, nil, params, false)
			if tc.errorText != "" {
				if tc.errorText == "422" {
					var httpErr *api.HTTPError
					require.ErrorAs(t, err, &httpErr)
					require.Equal(t, http.StatusUnprocessableEntity, httpErr.StatusCode)
				} else {
					require.EqualError(t, err, tc.errorText)
				}
			} else {
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Equal(t, 17, rows[0].Number)
			}
			require.Equal(t, tc.wantProbes, probes)
			require.Equal(t, tc.wantPages, pages)
		})
	}
}

func TestAuthorSearchRejectsIncompleteCounts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response searchResponse
		message  string
	}{
		{"incomplete", searchResponse{Incomplete: true}, "GitHub returned incomplete search results. Narrow --created or specify a repository"},
		{"too many candidates", searchResponse{Items: make([]searchItem, 100), TotalCount: 1001}, "more than 1,000 candidates share one timestamp. Specify a repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.response)
			require.NoError(t, err)
			rest, err := api.NewRESTClient(
				api.ClientOptions{
					Host:      "github.com",
					AuthToken: "test",
					Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						require.Equal(t, "1", req.URL.Query().Get("page"))
						return jsonResponse(req, 200, string(body)), nil
					}),
				},
			)
			require.NoError(t, err)
			_, err = scanAuthorSearch(
				rest,
				"is:pr",
				"sample-user",
				valueCreated,
				valueDesc,
				0,
				searchWindow{time.Unix(1, 0), time.Unix(1, 0)},
			)
			require.EqualError(t, err, tc.message)
		})
	}
}

func TestAuthorSearchPrefetchKeepsOrderAndCancels(t *testing.T) {
	thirdReady := make(chan struct{})
	fourthStarted := make(chan struct{})
	fourthDone := make(chan struct{})
	rest, err := api.NewRESTClient(api.ClientOptions{
		Host: "github.com", AuthToken: "test",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Query().Get("page") {
			case "1":
				body, marshalErr := json.Marshal(
					searchResponse{TotalCount: 400, Items: make([]searchItem, 100)},
				)
				if marshalErr != nil {
					return nil, marshalErr
				}
				return jsonResponse(req, 200, string(body)), nil
			case "2":
				for _, ready := range []chan struct{}{thirdReady, fourthStarted} {
					select {
					case <-ready:
					case <-time.After(3 * time.Second):
						return nil, errors.New("timed out waiting for concurrent page")
					}
				}
				return jsonResponse(
					req,
					200,
					`{"total_count":400,"items":[{"number":12,"user":{"login":"sample-user"}}]}`,
				), nil
			case "3":
				close(thirdReady)
				return jsonResponse(
					req,
					200,
					`{"total_count":400,"items":[{"number":13,"user":{"login":"sample-user"}}]}`,
				), nil
			case "4":
				close(fourthStarted)
				<-req.Context().Done()
				close(fourthDone)
				return nil, req.Context().Err()
			default:
				return nil, errors.New("unexpected page")
			}
		}),
	})
	require.NoError(t, err)
	rows, err := scanAuthorSearch(
		rest,
		"is:pr",
		"sample-user",
		valueCreated,
		valueDesc,
		1,
		searchWindow{time.Unix(1, 0), time.Now()},
	)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 12, rows[0].Number)
	select {
	case <-fourthDone:
	case <-time.After(3 * time.Second):
		t.Fatal("unused page was not canceled")
	}
}

func TestAuthorSearchSplitsBusyWindows(t *testing.T) {
	const query = "is:pr created:2025-04-03..2025-04-10"
	const prefix = "(" + query + ") AND updated:2025-04-11T00:00:"
	for _, tc := range []struct {
		name  string
		limit int
		want  []int
	}{
		{"early stop", 2, []int{4, 3}},
		{"exact count", 0, []int{4, 3, 2, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			rest, err := api.NewRESTClient(
				api.ClientOptions{
					Host:      "github.com",
					AuthToken: "test",
					Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						q := req.URL.Query().Get("q")
						seen = append(seen, q)
						var resp searchResponse
						switch q {
						case query, prefix + "04Z..2025-04-11T00:00:07Z":
							resp.TotalCount = 1200
							// A match from the parent page must not be counted twice.
							resp.Items = []searchItem{
								{Number: 4, User: searchUser{Login: "sample-user"}},
							}
						case prefix + "06Z..2025-04-11T00:00:07Z":
							resp.Items = []searchItem{
								{Number: 4, User: searchUser{Login: "sample-user"}},
							}
						case prefix + "04Z..2025-04-11T00:00:05Z":
							resp.Items = []searchItem{
								{Number: 3, User: searchUser{Login: "sample-user"}},
							}
						case prefix + "00Z..2025-04-11T00:00:03Z":
							resp.Items = []searchItem{
								{Number: 2, User: searchUser{Login: "sample-user"}},
								{Number: 1, User: searchUser{Login: "sample-user"}},
							}
						default:
							return nil, errors.New("unexpected window: " + q)
						}
						body, marshalErr := json.Marshal(resp)
						if marshalErr != nil {
							return nil, marshalErr
						}
						return jsonResponse(req, http.StatusOK, string(body)), nil
					}),
				},
			)
			require.NoError(t, err)
			start := time.Date(2025, 4, 11, 0, 0, 0, 0, time.UTC)
			rows, err := scanAuthorSearch(
				rest,
				query,
				"sample-user",
				valueUpdated,
				valueDesc,
				tc.limit,
				searchWindow{start, start.Add(7 * time.Second)},
			)
			require.NoError(t, err)
			var got []int
			for _, row := range rows {
				got = append(got, row.Number)
			}
			require.Equal(t, tc.want, got)
			if tc.limit > 0 {
				require.Len(t, seen, 4)
			} else {
				require.Len(t, seen, 5)
			}
		})
	}
}

func TestAuthorWindowBounds(t *testing.T) {
	now := time.Date(2025, 6, 20, 12, 30, 0, 0, time.UTC)
	day := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	created, ok := authorSearchWindow("2025-05-01", valueCreated, now)
	require.True(t, ok)
	require.Equal(t, searchWindow{day, day.Add(24*time.Hour - time.Second)}, created)
	updated, ok := authorSearchWindow("2025-05-01", valueUpdated, now)
	require.True(t, ok)
	require.Equal(t, searchWindow{day, now}, updated)
}

func TestAuthorSearchRetriesThrottledPage(t *testing.T) {
	calls := 0
	rest, err := api.NewRESTClient(
		api.ClientOptions{
			Host:      "github.com",
			AuthToken: "test",
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "1", req.URL.Query().Get("page"))
				if calls == 1 {
					resp := jsonResponse(
						req,
						http.StatusTooManyRequests,
						`{"message":"rate limit"}`,
					)
					resp.Header.Set("Retry-After", "0")
					return resp, nil
				}
				return jsonResponse(
					req,
					http.StatusOK,
					`{"total_count":1,"items":[{"user":{"login":"sample-user"}}]}`,
				), nil
			}),
		},
	)
	require.NoError(t, err)
	rows, split, err := scanAuthorWindow(rest, "is:pr", "sample-user", valueCreated, valueDesc, 1)
	require.NoError(t, err)
	require.False(t, split)
	require.Len(t, rows, 1)
	require.Equal(t, 2, calls)
}
