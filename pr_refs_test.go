package main

import (
	"net/http"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func TestParsePRRefs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []prRef
		wantErr string
	}{
		{
			name: "no args",
		},
		{
			name: "free text",
			args: []string{"fix", "flaky test"},
		},
		{
			name: "separate URLs",
			args: []string{
				"https://github.com/org/repo/pull/1",
				"https://github.com/org/other/pull/2",
			},
			want: []prRef{{"org", "repo", 1}, {"org", "other", 2}},
		},
		{
			name: "pasted newline separated list",
			args: []string{
				"https://github.com/org/repo/pull/1\nhttps://github.com/org/repo/pull/2\n",
			},
			want: []prRef{{"org", "repo", 1}, {"org", "repo", 2}},
		},
		{
			name: "comma separated",
			args: []string{"org/repo#1,org/repo#2"},
			want: []prRef{{"org", "repo", 1}, {"org", "repo", 2}},
		},
		{
			name: "URL suffixes and shorthand",
			args: []string{
				"https://github.com/org/repo/pull/3/files",
				"github.com/org/repo/pull/4",
				"org/repo/pull/5",
			},
			want: []prRef{{"org", "repo", 3}, {"org", "repo", 4}, {"org", "repo", 5}},
		},
		{
			name: "duplicates removed",
			args: []string{"org/repo#1", "https://github.com/org/repo/pull/1"},
			want: []prRef{{"org", "repo", 1}},
		},
		{
			name:    "mixed with search terms",
			args:    []string{"https://github.com/org/repo/pull/1", "flaky"},
			wantErr: "cannot mix pull request URLs with search terms: flaky",
		},
		{
			name:    "non-GitHub host",
			args:    []string{"https://gitlab.com/org/repo/-/merge_requests/1"},
			wantErr: "pull request URL must refer to GitHub: https://gitlab.com/org/repo/-/merge_requests/1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePRRefs(tt.args)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBuildSearchQueryUsesPRRefs(t *testing.T) {
	cli := &CLI{Query: []string{"https://github.com/org/repo/pull/1", "org/repo#2"}}
	params, err := buildSearchQuery(cli, &Config{})
	require.NoError(t, err)
	require.Equal(t, []prRef{{"org", "repo", 1}, {"org", "repo", 2}}, params.PRRefs)
	require.Equal(t, "org/repo#1 org/repo#2", params.Query)
	require.Equal(t, 2, params.TotalLimit)
}

func TestFetchPRsByRef(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := decodeGraphQLBody(t, readBody(t, req.Body))
		require.Equal(t, "query PullRequestsByRef("+
			"$o0: String!, $r0: String!, $n0: Int!, $o1: String!, $r1: String!, $n1: Int!) {\n"+
			"pr0: repository(owner: $o0, name: $r0) { pullRequest(number: $n0) { ...prFields } }\n"+
			"pr1: repository(owner: $o1, name: $r1) { pullRequest(number: $n1) { ...prFields } }\n"+
			"}\n"+prFieldsFragment, body.Query)
		require.Equal(t, "org", body.Variables["o1"])
		require.Equal(t, "other", body.Variables["r1"])
		require.InDelta(t, 7, body.Variables["n1"], 0)
		return jsonResponse(req, http.StatusOK, `{"data":{
			"pr0":{"pullRequest":{"id":"A","number":1,"state":"OPEN","title":"one",
				"repository":{"name":"repo","nameWithOwner":"org/repo"}}},
			"pr1":{"pullRequest":{"id":"B","number":7,"state":"MERGED","title":"seven",
				"repository":{"name":"other","nameWithOwner":"org/other"}}}
		}}`), nil
	})
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: transport,
	})
	require.NoError(t, err)

	prs, err := fetchPRsByRef(gql, []prRef{{"org", "repo", 1}, {"org", "other", 7}})
	require.NoError(t, err)
	require.Len(t, prs, 2)
	require.Equal(t, "org/repo", prs[0].Repository.NameWithOwner)
	require.Equal(t, 1, prs[0].Number)
	require.Equal(t, valueMerged, prs[1].State)
	require.Equal(t, 7, prs[1].Number)
}
