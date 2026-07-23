package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func groupTestPRs() []PullRequest {
	return []PullRequest{
		{
			Author:     Author{Login: "alice"},
			Repository: Repository{Name: "api", NameWithOwner: "acme/api"},
			State:      valueOpen,
			IsDraft:    true,
			Labels:     []Label{{Name: "bug"}, {Name: "urgent"}},
		},
		{
			Author:     Author{Login: "alice"},
			Repository: Repository{Name: "web", NameWithOwner: "acme/web"},
			State:      valueMerged,
			Labels:     []Label{{Name: "bug"}},
		},
		{
			Author:     Author{Login: "bob"},
			Repository: Repository{Name: "api", NameWithOwner: "acme/api"},
			State:      valueMerged,
		},
	}
}

func TestParseGroupKey(t *testing.T) {
	tests := []struct {
		input string
		want  groupKey
		ok    bool
	}{
		{"author", groupAuthor, true},
		{"a", groupAuthor, true},
		{"Repo", groupRepo, true},
		{"repository", groupRepo, true},
		{"owner", groupOwner, true},
		{"org", groupOwner, true},
		{"STATE", groupState, true},
		{"draft", groupDraft, true},
		{"labels", groupLabel, true},
		{"  author  ", groupAuthor, true},
		{"nonsense", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parseGroupKey(tt.input)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestBuildGroupNodes_CountsAndOrder(t *testing.T) {
	nodes := buildGroupNodes(groupTestPRs(), []groupKey{groupAuthor}, false)

	require.Equal(t, []groupNode{
		{Value: "alice", Count: 2},
		{Value: "bob", Count: 1},
	}, nodes)
}

func TestBuildGroupNodes_Nested(t *testing.T) {
	nodes := buildGroupNodes(groupTestPRs(), []groupKey{groupRepo, groupAuthor}, false)

	require.Equal(t, []groupNode{
		{
			Value: "acme/api",
			Count: 2,
			Children: []groupNode{
				{Value: "alice", Count: 1},
				{Value: "bob", Count: 1},
			},
		},
		{
			Value:    "acme/web",
			Count:    1,
			Children: []groupNode{{Value: "alice", Count: 1}},
		},
	}, nodes)
}

func TestBuildGroupNodes_StripsCommonOwner(t *testing.T) {
	nodes := buildGroupNodes(groupTestPRs(), []groupKey{groupRepo}, true)

	require.Equal(t, []groupNode{
		{Value: "api", Count: 2},
		{Value: "web", Count: 1},
	}, nodes)
}

func TestBuildGroupNodes_LabelFansOut(t *testing.T) {
	nodes := buildGroupNodes(groupTestPRs(), []groupKey{groupLabel}, false)

	require.Equal(t, []groupNode{
		{Value: "bug", Count: 2},
		{Value: groupNoneValue, Count: 1},
		{Value: "urgent", Count: 1},
	}, nodes)
}

func TestBuildGroupNodes_StateTieBreaksByLifecycle(t *testing.T) {
	prs := []PullRequest{
		{State: valueClosed},
		{State: valueOpen},
		{State: valueMerged},
	}
	nodes := buildGroupNodes(prs, []groupKey{groupState}, false)

	require.Equal(t, []groupNode{
		{Value: valueMerged, Count: 1},
		{Value: valueOpen, Count: 1},
		{Value: valueClosed, Count: 1},
	}, nodes)
}

func TestBuildGroupNodes_DraftAndTie(t *testing.T) {
	nodes := buildGroupNodes(groupTestPRs(), []groupKey{groupDraft}, false)

	require.Equal(t, []groupNode{
		{Value: valueReady, Count: 2},
		{Value: valueDraft, Count: 1},
	}, nodes)
}

func TestCommonOwner(t *testing.T) {
	require.Equal(t, "acme", commonOwner(groupTestPRs()))
	require.Empty(t, commonOwner(nil))

	mixed := []PullRequest{
		{Repository: Repository{NameWithOwner: "acme/api"}},
		{Repository: Repository{NameWithOwner: "other/web"}},
	}
	require.Empty(t, commonOwner(mixed))
}

func TestGroupValues_MissingAuthorAndOwner(t *testing.T) {
	pr := PullRequest{}
	require.Equal(t, []string{groupNoneValue}, groupValues(pr, groupAuthor, false))
	require.Equal(t, []string{groupNoneValue}, groupValues(pr, groupOwner, false))

	owned := PullRequest{Repository: Repository{Name: "api", NameWithOwner: "acme/api"}}
	require.Equal(t, []string{"acme"}, groupValues(owned, groupOwner, false))
	require.Equal(t, []string{"api"}, groupValues(owned, groupRepo, true))
}

func TestRenderGroup_Text(t *testing.T) {
	out, err := renderGroup(groupTestPRs(), []groupKey{groupAuthor}, false, false, nil, 0, 0)
	require.NoError(t, err)

	want := "alice (2)\n" +
		"bob (1)"
	require.Equal(t, want, out)
}

func TestGroupGrid_ColumnMajor(t *testing.T) {
	blocks := [][]string{
		{"alice (5)"}, {"bob (4)"}, {"carol (3)"}, {"dave (2)"}, {"erin (1)"},
	}
	// One row per block won't fit 30 columns, so the grid settles on
	// two-tall columns, read top-to-bottom then across.
	lines := groupGrid(blocks, false, 30, 0)

	require.Equal(t, []string{
		"alice (5)  carol (3)  erin (1)",
		"bob (4)    dave (2)",
	}, lines)
}

func TestGroupGrid_BalancesColumns(t *testing.T) {
	mk := func(prefix string, n int) []string {
		lines := make([]string, 0, n)
		for i := range n {
			lines = append(lines, fmt.Sprintf("%s%d", prefix, i))
		}
		return lines
	}
	blocks := [][]string{mk("a", 10), mk("b", 8), mk("c", 9), mk("d", 4), mk("e", 3)}
	// Greedy filling to the 13-line height limit would tuck block d under c
	// and give e its own column; balancing re-cuts at the smallest height
	// that still yields four columns, pairing d+e in the last one instead.
	lines := groupGrid(blocks, false, 80, 13)

	require.Len(t, lines, 10)
	require.Equal(t, "a0  b0  c0  d0", lines[0])
	require.Equal(t, "a4  b4  c4  e0", lines[4])
	require.Equal(t, "a9", lines[9])
}

func TestRenderGroup_GridFillsWidth(t *testing.T) {
	// All four buckets fit side by side in an 80-column terminal, so the
	// breakdown collapses to a single row.
	prs := make([]PullRequest, 0, 10)
	for _, a := range []struct {
		login string
		n     int
	}{{"alice", 4}, {"bob", 3}, {"carol", 2}, {"dave", 1}} {
		for range a.n {
			prs = append(prs, PullRequest{Author: Author{Login: a.login}})
		}
	}
	out, err := renderGroup(prs, []groupKey{groupAuthor}, false, false, nil, 80, 0)
	require.NoError(t, err)

	want := "alice (4)  bob (3)  carol (2)  dave (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_FitsHeightStacks(t *testing.T) {
	// With plenty of terminal height, the breakdown stays a single stacked
	// column even though the buckets would fit side by side.
	prs := make([]PullRequest, 0, 10)
	for _, a := range []struct {
		login string
		n     int
	}{{"alice", 4}, {"bob", 3}, {"carol", 2}, {"dave", 1}} {
		for range a.n {
			prs = append(prs, PullRequest{Author: Author{Login: a.login}})
		}
	}
	out, err := renderGroup(prs, []groupKey{groupAuthor}, false, false, nil, 80, 40)
	require.NoError(t, err)

	want := "alice (4)\n" +
		"bob (3)\n" +
		"carol (2)\n" +
		"dave (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_NoWidthStacks(t *testing.T) {
	// The same data with no known terminal width stays a single stacked column.
	prs := make([]PullRequest, 0, 10)
	for _, a := range []struct {
		login string
		n     int
	}{{"alice", 4}, {"bob", 3}, {"carol", 2}, {"dave", 1}} {
		for range a.n {
			prs = append(prs, PullRequest{Author: Author{Login: a.login}})
		}
	}
	out, err := renderGroup(prs, []groupKey{groupAuthor}, false, false, nil, 0, 0)
	require.NoError(t, err)

	want := "alice (4)\n" +
		"bob (3)\n" +
		"carol (2)\n" +
		"dave (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_TextNested(t *testing.T) {
	out, err := renderGroup(
		groupTestPRs(),
		[]groupKey{groupRepo, groupAuthor},
		false,
		false,
		nil,
		0,
		0,
	)
	require.NoError(t, err)

	want := "api (2)\n" +
		"├─ alice (1)\n" +
		"└─ bob (1)\n" +
		"\n" +
		"web (1)\n" +
		"└─ alice (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_NestedGrid(t *testing.T) {
	// With a known width, nested group blocks flow into side-by-side columns
	// instead of one long stack.
	out, err := renderGroup(
		groupTestPRs(),
		[]groupKey{groupRepo, groupAuthor},
		false,
		false,
		nil,
		30,
		0,
	)
	require.NoError(t, err)

	want := "api (2)       web (1)\n" +
		"├─ alice (1)  └─ alice (1)\n" +
		"└─ bob (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_JSON(t *testing.T) {
	out, err := renderGroup(groupTestPRs(), []groupKey{groupState}, true, false, nil, 0, 0)
	require.NoError(t, err)

	want := `[
  {
    "value": "merged",
    "count": 2
  },
  {
    "value": "open",
    "count": 1
  }
]`
	require.Equal(t, want, out)
}

func TestRenderGroup_Empty(t *testing.T) {
	out, err := renderGroup(nil, []groupKey{groupAuthor}, false, false, nil, 0, 0)
	require.NoError(t, err)

	require.Empty(t, out)
}
