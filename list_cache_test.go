package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testCacheCLI() *CLI {
	output := valueTable
	draft := false
	return &CLI{
		ClosedBy: CSVFlag{Values: []string{"alice"}},
		CI:       ciStatusSuccess,
		Draft:    &draft,
		NoBot:    true,
		Output:   &output,
		Quick:    true,
		Review:   valueReviewFilterSelfRequired,
		State:    valueOpen,
	}
}

func testCacheParams() *SearchParams {
	return &SearchParams{
		Query:      "is:pr archived:false author:@me",
		Sort:       valueUpdated,
		Order:      valueDesc,
		PerPage:    30,
		TotalLimit: 30,
	}
}

func TestListResultCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(envKeyGitHubToken, "cache-test-token")

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	prs := []PullRequest{
		{
			Automerge:      true,
			Author:         Author{Login: "alice"},
			CreatedAt:      now.Add(-time.Hour),
			HeadSHA:        "abc123",
			IsDraft:        true,
			Labels:         []Label{{Name: "bug"}},
			MergeStatus:    MergeStatusCIFailed,
			NodeID:         "PR_node",
			Number:         42,
			Repository:     Repository{Name: "repo", NameWithOwner: "owner/repo"},
			ReviewDecision: valueReviewApproved,
			State:          valueOpen,
			Title:          "Fix cache",
			TitleRaw:       " Fix cache ",
			UpdatedAt:      now,
			URL:            "https://github.com/owner/repo/pull/1",

			automergeLoaded:      true,
			reviewDecisionLoaded: true,
			viewerApprovalLoaded: true,
			viewerApproved:       true,
			viewerIsAuthor:       true,
		},
	}

	require.NoError(t, saveListResultCache(testCacheCLI(), testCacheParams(), prs))

	got, ok, err := loadListResultCache(testCacheCLI(), testCacheParams(), 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, prs, got)
}

func TestListResultCacheIgnoresDifferentSearch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(envKeyGitHubToken, "cache-test-token")

	cli := testCacheCLI()
	params := testCacheParams()
	require.NoError(t, saveListResultCache(cli, params, []PullRequest{{NodeID: "one"}}))

	changed := *params
	changed.Query += " repo:other/repo"
	got, ok, err := loadListResultCache(cli, &changed, 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, got)
}

func TestListResultCacheKeyIncludesLocalFilters(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(envKeyGitHubToken, "cache-test-token")

	cli := testCacheCLI()
	params := testCacheParams()
	require.NoError(t, saveListResultCache(cli, params, []PullRequest{{NodeID: "one"}}))

	changed := *cli
	changed.CI = ciStatusFailure
	got, ok, err := loadListResultCache(&changed, params, 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, got)
}

func TestListResultCacheIgnoresStaleResult(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(envKeyGitHubToken, "cache-test-token")

	cli := testCacheCLI()
	params := testCacheParams()
	require.NoError(t, saveListResultCache(cli, params, []PullRequest{{NodeID: "one"}}))

	path, _, err := listResultCachePath(cli, params)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var cache listResultCacheFile
	require.NoError(t, json.Unmarshal(data, &cache))
	cache.SavedAt = time.Now().Add(-tuiListResultCacheMaxAge - time.Minute)
	data, err = json.Marshal(cache)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, listResultCacheFilePerm))

	got, ok, err := loadListResultCache(cli, params, tuiListResultCacheMaxAge)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, got)

	got, ok, err = loadListResultCache(cli, params, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, got, 1)
}

func TestListResultCacheAcceptsFreshResult(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(envKeyGitHubToken, "cache-test-token")

	cli := testCacheCLI()
	params := testCacheParams()
	prs := []PullRequest{{NodeID: "one"}}
	require.NoError(t, saveListResultCache(cli, params, prs))

	got, ok, err := loadListResultCache(cli, params, tuiListResultCacheMaxAge)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, prs, got)
}
