package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	xansi "github.com/gechr/x/ansi"
	"github.com/stretchr/testify/require"
)

func groupTestSearchParams(terms ...searchQueryTerm) *SearchParams {
	queries := make([]string, len(terms))
	for i, term := range terms {
		queries[i] = term.query
	}
	return &SearchParams{
		Query:      strings.Join(queries, " "),
		queryTerms: terms,
	}
}

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

func TestShouldStripGroupRepoOwner(t *testing.T) {
	prs := groupTestPRs()
	require.True(t, shouldStripGroupRepoOwner(prs, nil))
	require.True(t, shouldStripGroupRepoOwner(prs, []string{"acme"}))
	require.False(t, shouldStripGroupRepoOwner(prs, []string{"acme", "other"}))
	require.True(t, shouldStripGroupRepoOwner(prs, []string{"acme", "!other"}))
}

func TestGroupValues_MissingAuthorAndOwner(t *testing.T) {
	pr := PullRequest{}
	require.Equal(t, []string{groupNoneValue}, groupValues(pr, groupAuthor, false))
	require.Equal(t, []string{groupNoneValue}, groupValues(pr, groupOwner, false))

	owned := PullRequest{Repository: Repository{Name: "api", NameWithOwner: "acme/api"}}
	require.Equal(t, []string{"acme"}, groupValues(owned, groupOwner, false))
	require.Equal(t, []string{"api"}, groupValues(owned, groupRepo, true))
}

func TestGroupSearchQualifier(t *testing.T) {
	repoPR := PullRequest{
		Repository: Repository{Name: "api", NameWithOwner: "acme/api"},
	}
	tests := []struct {
		name  string
		key   groupKey
		value string
		prs   []PullRequest
		want  string
	}{
		{"author", groupAuthor, "alice", nil, "author:alice"},
		{"missing author", groupAuthor, groupNoneValue, nil, ""},
		{"short repo", groupRepo, "api", []PullRequest{repoPR}, "repo:acme/api"},
		{"owner", groupOwner, "acme", nil, "user:acme"},
		{"merged", groupState, valueMerged, nil, "is:merged"},
		{"open", groupState, valueOpen, nil, "state:open"},
		{"closed", groupState, valueClosed, nil, "state:closed is:unmerged"},
		{"draft", groupDraft, valueDraft, nil, "draft:true"},
		{"ready", groupDraft, valueReady, nil, "draft:false"},
		{"label", groupLabel, "help wanted", nil, `label:"help wanted"`},
		{"missing label", groupLabel, groupNoneValue, nil, "no:label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, groupSearchQualifier(tt.key, tt.value, tt.prs))
		})
	}
}

func TestGroupSearchQuery_ReplacesBroadAuthorScope(t *testing.T) {
	params := groupTestSearchParams(
		searchQueryTerm{query: "is:pr"},
		searchQueryTerm{query: "archived:false"},
		searchQueryTerm{query: "user:acme", key: groupOwner, grouped: true},
		searchQueryTerm{query: "created:>=2026-01-01"},
		searchQueryTerm{
			query:   "(author:alice OR author:bob OR author:carol)",
			key:     groupAuthor,
			grouped: true,
		},
	)

	repoPath := []groupSearchFilter{{
		key:   groupRepo,
		query: "repo:acme/api",
	}}
	require.Equal(
		t,
		"is:pr archived:false user:acme created:>=2026-01-01 "+
			"repo:acme/api (author:alice OR author:bob OR author:carol)",
		params.groupSearchQuery(repoPath),
	)

	authorPath := slices.Clone(repoPath)
	authorPath = append(authorPath, groupSearchFilter{
		key:   groupAuthor,
		query: "author:alice",
	})
	require.Equal(
		t,
		"is:pr archived:false user:acme created:>=2026-01-01 "+
			"repo:acme/api author:alice",
		params.groupSearchQuery(authorPath),
	)
}

func TestBuildGroupNodesWithLinks_AccumulatesAncestorFilters(t *testing.T) {
	params := groupTestSearchParams(
		searchQueryTerm{query: "is:pr"},
		searchQueryTerm{query: "archived:false"},
	)
	baseQuery := params.Query
	prs := groupTestPRs()
	nodes := buildGroupNodesWithLinks(
		prs, []groupKey{groupRepo, groupAuthor, groupState}, true, params,
	)

	api := nodes[0]
	require.Equal(t, "api", api.Value)
	require.Equal(t, githubRepoPullsURL("acme/api", baseQuery), api.url)

	alice := api.Children[0]
	require.Equal(t, "alice", alice.Value)
	require.Equal(
		t,
		githubRepoPullsURL("acme/api", baseQuery+" author:alice"),
		alice.url,
	)
	require.Equal(
		t,
		githubRepoPullsURL(
			"acme/api",
			baseQuery+" author:alice state:open",
		),
		alice.Children[0].url,
	)

	bob := api.Children[1]
	require.Equal(t, "bob", bob.Value)
	require.Equal(
		t,
		githubRepoPullsURL(
			"acme/api",
			baseQuery+" author:bob is:merged",
		),
		bob.Children[0].url,
	)

	nodes = buildGroupNodesWithLinks(
		prs, []groupKey{groupRepo, groupState, groupAuthor}, true, params,
	)
	api = nodes[0]
	merged := api.Children[0]
	require.Equal(t, valueMerged, merged.Value)
	require.Equal(
		t,
		githubRepoPullsURL("acme/api", baseQuery+" is:merged"),
		merged.url,
	)
	require.Equal(t, "bob", merged.Children[0].Value)
	require.Equal(
		t,
		githubRepoPullsURL(
			"acme/api",
			baseQuery+" is:merged author:bob",
		),
		merged.Children[0].url,
	)
}

func TestGroupNodeLabel_HyperlinksNestedSearch(t *testing.T) {
	params := groupTestSearchParams(
		searchQueryTerm{query: "is:pr"},
		searchQueryTerm{query: "archived:false"},
	)
	nodes := buildGroupNodesWithLinks(
		groupTestPRs(),
		[]groupKey{groupRepo, groupAuthor, groupState},
		true,
		params,
	)
	merged := nodes[0].Children[1].Children[0]
	wantURL := githubRepoPullsURL(
		"acme/api",
		params.Query+" author:bob is:merged",
	)
	require.Equal(t, wantURL, merged.url)

	want := xansi.Force().Hyperlink(
		wantURL,
		styleGroupName(valueMerged, colorMerged),
	) + " " + styleDim.Render("(1)")
	require.Equal(
		t,
		want,
		groupNodeLabel(
			merged,
			2,
			[]groupKey{groupRepo, groupAuthor, groupState},
			true,
			nil,
		),
	)
	require.Equal(
		t,
		"merged (1)",
		groupNodeLabel(
			merged,
			2,
			[]groupKey{groupRepo, groupAuthor, groupState},
			false,
			nil,
		),
	)
}

func TestRenderGroup_Text(t *testing.T) {
	out, err := renderGroup(
		groupTestPRs(), []groupKey{groupAuthor}, false, false, nil, 0, 0, nil, nil, true,
	)
	require.NoError(t, err)

	want := "alice (2)\n" +
		"bob (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_ResolvesAuthorNames(t *testing.T) {
	resolver := &AuthorResolver{
		names: map[string]string{
			"alice": "Alice Example",
			"bob":   "Bob Person",
		},
	}
	out, err := renderGroup(
		groupTestPRs(),
		[]groupKey{groupRepo, groupAuthor},
		false,
		false,
		nil,
		0,
		0,
		resolver,
		nil,
		true,
	)
	require.NoError(t, err)

	want := "api (2)\n" +
		"├─ Alice Example (1)\n" +
		"└─ Bob Person (1)\n" +
		"\n" +
		"web (1)\n" +
		"└─ Alice Example (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_JSONResolvesAuthorNames(t *testing.T) {
	resolver := &AuthorResolver{
		names: map[string]string{"alice": "Alice Example"},
	}
	out, err := renderGroup(
		groupTestPRs(),
		[]groupKey{groupAuthor},
		true,
		false,
		nil,
		0,
		0,
		resolver,
		nil,
		true,
	)
	require.NoError(t, err)

	want := `[
  {
    "value": "Alice Example",
    "count": 2
  },
  {
    "value": "bob",
    "count": 1
  }
]`
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

func TestGroupGrid_FlowsLeftToRightWhenShorter(t *testing.T) {
	mk := func(prefix string, n int) []string {
		lines := make([]string, 0, n)
		for i := range n {
			lines = append(lines, fmt.Sprintf("%s-%d", prefix, i))
		}
		return lines
	}
	blocks := [][]string{
		mk("item-a", 10),
		mk("item-b", 8),
		mk("item-c", 7),
		mk("item-d", 3),
		mk("item-e", 2),
	}

	lines := groupGrid(blocks, true, 28, 10)

	require.Len(t, lines, 11)
	require.Equal(t, "item-a-0  item-b-0  item-c-0", lines[0])
	require.Equal(t, "item-a-7  item-b-7", lines[7])
	require.Equal(t, "item-a-8            item-d-0", lines[8])
	require.Equal(t, "item-a-9  item-e-0  item-d-1", lines[9])
	require.Equal(t, "          item-e-1  item-d-2", lines[10])
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
	out, err := renderGroup(
		prs, []groupKey{groupAuthor}, false, false, nil, 80, 0, nil, nil, true,
	)
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
	out, err := renderGroup(
		prs, []groupKey{groupAuthor}, false, false, nil, 80, 40, nil, nil, true,
	)
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
	out, err := renderGroup(
		prs, []groupKey{groupAuthor}, false, false, nil, 0, 0, nil, nil, true,
	)
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
		nil,
		nil,
		true,
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
		nil,
		nil,
		true,
	)
	require.NoError(t, err)

	want := "api (2)       web (1)\n" +
		"├─ alice (1)  └─ alice (1)\n" +
		"└─ bob (1)"
	require.Equal(t, want, out)
}

func TestRenderGroup_JSON(t *testing.T) {
	out, err := renderGroup(
		groupTestPRs(), []groupKey{groupState}, true, false, nil, 0, 0, nil, nil, true,
	)
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
	out, err := renderGroup(
		nil, []groupKey{groupAuthor}, false, false, nil, 0, 0, nil, nil, true,
	)
	require.NoError(t, err)

	require.Empty(t, out)
}
