package main

import (
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

	got, ok, err := loadListResultCache(testCacheCLI(), testCacheParams())
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
	got, ok, err := loadListResultCache(cli, &changed)
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
	got, ok, err := loadListResultCache(&changed, params)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, got)
}
