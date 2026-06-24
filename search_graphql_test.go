package main

import (
	"testing"
	"time"

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
		Query: "type:pr archived:false",
		Sort:  valueUpdated,
		Order: valueDesc,
	}

	require.Equal(t, "type:pr archived:false sort:updated-desc", graphQLSearchQuery(params))
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
