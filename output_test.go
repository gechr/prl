package main

import (
	"testing"
	"time"

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
	require.Equal(t, "alpha\nbravo\nzulu", got)
}

func TestRenderURLs(t *testing.T) {
	prs := []PullRequest{
		{URL: "https://github.com/org/repo1/pull/1"},
		{URL: "https://github.com/org/repo2/pull/2"},
	}
	got := renderURLs(prs)
	want := "https://github.com/org/repo1/pull/1\nhttps://github.com/org/repo2/pull/2"
	require.Equal(t, want, got)
}

func TestRenderBullets(t *testing.T) {
	prs := []PullRequest{
		{URL: "https://github.com/org/repo1/pull/1"},
	}
	got := renderBullets(prs)
	want := "* https://github.com/org/repo1/pull/1"
	require.Equal(t, want, got)
}
