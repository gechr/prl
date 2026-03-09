package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	cliansi "github.com/gechr/clib/ansi"
	"github.com/gechr/clib/table"
	"github.com/stretchr/testify/require"
)

var testPRL = New()

// sgr8spaces replicates the grid's unexported spaces() for test assertions.
func sgr8spaces(n int) string {
	return "\x1b[8m" + strings.Repeat(" ", n) + "\x1b[28m"
}

func TestMain(m *testing.M) {
	lipgloss.Writer.Profile = colorprofile.ANSI256
	m.Run()
}

// testPRs returns 3 PRs ordered oldest to newest by CreatedAt.
func testPRs() []PullRequest {
	now := time.Now().UTC()
	return []PullRequest{
		{
			Number:     1,
			Title:      "oldest PR",
			URL:        "https://github.com/org/alpha/pull/1",
			State:      "open",
			Repository: Repository{Name: "alpha", NameWithOwner: "org/alpha"},
			Author:     Author{Login: "alice"},
			CreatedAt:  now.Add(-3 * time.Hour),
			UpdatedAt:  now.Add(-1 * time.Hour),
		},
		{
			Number:     2,
			Title:      "middle PR",
			URL:        "https://github.com/org/bravo/pull/2",
			State:      "merged",
			Repository: Repository{Name: "bravo", NameWithOwner: "org/bravo"},
			Author:     Author{Login: "bob"},
			CreatedAt:  now.Add(-2 * time.Hour),
			UpdatedAt:  now.Add(-2 * time.Hour),
		},
		{
			Number:     3,
			Title:      "newest PR",
			URL:        "https://github.com/org/charlie/pull/3",
			State:      "closed",
			Repository: Repository{Name: "charlie", NameWithOwner: "org/charlie"},
			Author:     Author{Login: "carol"},
			CreatedAt:  now.Add(-1 * time.Hour),
			UpdatedAt:  now.Add(-3 * time.Hour),
		},
	}
}

// newTestRenderer creates a table.Renderer with tty=true and prl's default reverse
// (newest at top). Extra options override the defaults.
func newTestRenderer(columns []Column, opts ...table.Option) *table.Renderer[PullRequest] {
	return newTestRendererWithTTY(columns, true, opts...)
}

func newTestRendererWithTTY(
	columns []Column, tty bool, opts ...table.Option,
) *table.Renderer[PullRequest] {
	ansiOpts := []cliansi.Option{cliansi.WithTerminal(tty)}
	if !tty {
		ansiOpts = append(ansiOpts, cliansi.WithHyperlinkFallback(cliansi.HyperlinkFallbackURL))
	}
	ctx := table.NewRenderContext(testPRL.theme, cliansi.New(ansiOpts...))
	// prl default: newest at top → clib WithReverse(true).
	allOpts := []table.Option{table.WithReverse(true)}
	allOpts = append(allOpts, opts...)
	return table.NewRenderer[PullRequest](columns, ctx, allOpts...)
}

// simpleColumns returns minimal columns (repo, number) for testing ordering/indexing.
func simpleColumns() []Column {
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	return []Column{defs["repo"], defs["number"]}
}

// extractVisibleColumn extracts the visible text of a given column index from aligned output.
// Strips ANSI for field splitting only. Skips header (line 0). Returns values in display order.
func extractVisibleColumn(output string, col int) []string {
	lines := strings.Split(output, "\n")
	var vals []string
	for i, line := range lines {
		if i == 0 { // skip header
			continue
		}
		fields := strings.Fields(ansi.Strip(line))
		if col < len(fields) {
			vals = append(vals, fields[col])
		}
	}
	return vals
}

func TestRender_Empty(t *testing.T) {
	r := newTestRenderer(simpleColumns())
	out, rows := r.Render(nil)
	require.Empty(t, out)
	require.Nil(t, rows)
}

func TestRender_NoColumns(t *testing.T) {
	r := newTestRenderer(nil)
	out, rows := r.Render(testPRs())
	require.Empty(t, out)
	require.Nil(t, rows)
}

func TestRender_DefaultOrder_NewestAtTop(t *testing.T) {
	// Default (prl reverse=true → newest at top).
	prs := testPRs()
	r := newTestRenderer(simpleColumns())
	_, rows := r.Render(prs)

	require.Len(t, rows, 3)
	require.Equal(t, "newest PR", rows[0].Item.Title)
	require.Equal(t, "oldest PR", rows[2].Item.Title)
}

func TestRender_Reverse_OldestAtTop(t *testing.T) {
	// --reverse: oldest first → clib reverse=false.
	prs := testPRs()
	r := newTestRenderer(simpleColumns(), table.WithReverse(false))
	_, rows := r.Render(prs)

	require.Len(t, rows, 3)
	require.Equal(t, "oldest PR", rows[0].Item.Title)
	require.Equal(t, "newest PR", rows[2].Item.Title)
}

func TestRender_Index_DefaultOrder(t *testing.T) {
	// Default order: #1 at top (newest), highest number at bottom (oldest).
	prs := testPRs()
	r := newTestRenderer(simpleColumns(), table.WithShowIndex(true))
	out, rows := r.Render(prs)

	require.Len(t, rows, 3)

	indices := extractVisibleColumn(out, 0)
	require.Len(t, indices, 3)
	// Top row = newest = #1
	require.Equal(t, "1", indices[0], "top index should be 1 (newest)")
	// Bottom row = oldest = highest number
	require.Equal(t, "3", indices[2], "bottom index should be 3 (oldest)")
}

func TestRender_Index_Reverse(t *testing.T) {
	// Reverse order: #1 at bottom (newest), highest number at top (oldest).
	prs := testPRs()
	r := newTestRenderer(simpleColumns(),
		table.WithReverse(false),
		table.WithShowIndex(true),
	)
	out, rows := r.Render(prs)

	require.Len(t, rows, 3)

	indices := extractVisibleColumn(out, 0)
	require.Len(t, indices, 3)
	// Top row = oldest = highest number
	require.Equal(t, "3", indices[0], "top index should be 3 (oldest)")
	// Bottom row = newest = #1
	require.Equal(t, "1", indices[2], "bottom index should be 1 (newest)")
}

func TestRender_IndexLeftPadding(t *testing.T) {
	// With 10+ items, single-digit indices should be left-padded with spaces.
	prs := make([]PullRequest, 12)
	now := time.Now().UTC()
	for i := range prs {
		prs[i] = PullRequest{
			Number:     i + 1,
			Title:      "title",
			URL:        "https://github.com/org/repo/pull/1",
			State:      "open",
			Repository: Repository{Name: "repo", NameWithOwner: "org/repo"},
			Author:     Author{Login: "user"},
			CreatedAt:  now.Add(-time.Duration(12-i) * time.Hour),
			UpdatedAt:  now,
		}
	}

	r := newTestRenderer(simpleColumns(), table.WithShowIndex(true))
	out, _ := r.Render(prs)

	lines := strings.Split(out, "\n")
	// First data line = newest = #1. The dim style wraps " 1", so the visible
	// text should be " 1" (left-padded to 2-digit width).
	firstDataLine := ansi.Strip(lines[1]) // skip header
	require.True(t, strings.HasPrefix(firstDataLine, " "),
		"single-digit index not left-padded: %q", firstDataLine)

	// Last data line = oldest = #12, should start with "12" (no padding needed).
	lastLine := ansi.Strip(lines[len(lines)-1])
	require.True(t, strings.HasPrefix(strings.TrimLeft(lastLine, " "), "12"),
		"last data line should start with 12, got: %q", lastLine)
}

func TestRender_IndexContainsDimStyle(t *testing.T) {
	// Index numbers should be wrapped in faint/dim ANSI styling.
	prs := testPRs()[:2]
	r := newTestRenderer(simpleColumns(), table.WithShowIndex(true))
	_, rows := r.Render(prs)

	// Default order: newest first. Row 0 = newest = index 1.
	// Display line starts with dim-styled index, followed by SGR8-wrapped gap.
	parts := strings.SplitN(rows[0].Display, sgr8spaces(2), 2)
	require.Equal(t, testPRL.theme.Dim.Render("1"), parts[0])
}

func TestRender_HeaderContainsBoldStyle(t *testing.T) {
	prs := testPRs()[:1]
	r := newTestRenderer(simpleColumns())
	out, _ := r.Render(prs)

	lines := strings.Split(out, "\n")
	headerFields := strings.Fields(ansi.Strip(lines[0]))
	require.Equal(t, []string{"REPO", "NUMBER"}, headerFields)

	// Verify bold styling by checking the raw header contains bold-rendered values
	// Col widths: max(vw("REPO")=4, vw("alpha")=5)=5, max(vw("NUMBER")=6, vw("#1")=2)=6
	// Header: bold("REPO") + pad(1) + gap(2) + bold("NUMBER")
	expectedHeader := testPRL.theme.Bold.Render("REPO") +
		sgr8spaces(1) + sgr8spaces(2) +
		testPRL.theme.Bold.Render("NUMBER")
	require.Equal(t, expectedHeader, lines[0])
}

func TestRender_RefContainsStateColor(t *testing.T) {
	// Open PR ref with unknown merge status should be styled with dim.
	prs := testPRs()[:1] // state = "open"
	defs := testPRL.allColumnDefs("org", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["ref"]})
	_, rows := r.Render(prs)

	expected := ansi.SetHyperlink("https://github.com/org/alpha/pull/1") +
		testPRL.theme.Dim.Render("alpha#1") + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RefMergedColor(t *testing.T) {
	prs := testPRs()[1:2] // state = "merged"
	defs := testPRL.allColumnDefs("org", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["ref"]})
	_, rows := r.Render(prs)

	expected := ansi.SetHyperlink("https://github.com/org/bravo/pull/2") +
		testPRL.theme.Magenta.Render("bravo#2") + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RefClosedColor(t *testing.T) {
	prs := testPRs()[2:3] // state = "closed"
	defs := testPRL.allColumnDefs("org", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["ref"]})
	_, rows := r.Render(prs)

	expected := ansi.SetHyperlink("https://github.com/org/charlie/pull/3") +
		testPRL.theme.Red.Render("charlie#3") + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RefContainsHyperlink(t *testing.T) {
	// With tty=true, ref column should contain OSC 8 hyperlinks.
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("org", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["ref"]})
	_, rows := r.Render(prs)

	expected := ansi.SetHyperlink("https://github.com/org/alpha/pull/1") +
		testPRL.theme.Dim.Render("alpha#1") + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RepoContainsHyperlink(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["repo"]})
	_, rows := r.Render(prs)

	expected := ansi.SetHyperlink("https://github.com/org/alpha") + "alpha" + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_NoHyperlinkWhenNoTTY(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("org", nil, tableLayout{})
	r := newTestRendererWithTTY([]Column{defs["ref"]}, false)
	_, rows := r.Render(prs)

	// tty=false: falls back to plain URL
	expected := "https://github.com/org/alpha/pull/1"
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RefIncludesOrg_WhenNoOrgFilter(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["ref"]})
	_, rows := r.Render(prs)

	// Without org filter, ref includes org/repo#N
	expected := ansi.SetHyperlink("https://github.com/org/alpha/pull/1") +
		testPRL.theme.Dim.Render("org/alpha#1") + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RefIncludesOrg_WhenOrgFilterAll(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs(valueAll, nil, tableLayout{})
	r := newTestRenderer([]Column{defs["ref"]})
	_, rows := r.Render(prs)

	// With "all" org filter, ref includes org/repo#N (same as no filter)
	expected := ansi.SetHyperlink("https://github.com/org/alpha/pull/1") +
		testPRL.theme.Dim.Render("org/alpha#1") + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RefExcludesOrg_WhenOrgFilter(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("org", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["ref"]})
	_, rows := r.Render(prs)

	// With org filter, ref uses just repo#N (no org prefix)
	expected := ansi.SetHyperlink("https://github.com/org/alpha/pull/1") +
		testPRL.theme.Dim.Render("alpha#1") + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestRender_RowDisplayLinesSet(t *testing.T) {
	prs := testPRs()[:2]
	r := newTestRenderer(simpleColumns())
	_, rows := r.Render(prs)

	for i, row := range rows {
		require.NotEmpty(t, row.Display, "row[%d].Display should not be empty", i)
	}
}

func TestRender_HeaderPresent(t *testing.T) {
	prs := testPRs()[:1]
	r := newTestRenderer(simpleColumns())
	out, _ := r.Render(prs)

	lines := strings.Split(out, "\n")
	require.Len(t, lines, 2, "expected header + 1 data line")

	// Col widths: max(vw("REPO")=4, vw("alpha")=5)=5, max(vw("NUMBER")=6, vw("#1")=2)=6
	// Header: bold("REPO") + pad(1) + gap(2) + bold("NUMBER")
	expectedHeader := testPRL.theme.Bold.Render("REPO") +
		sgr8spaces(1) + sgr8spaces(2) +
		testPRL.theme.Bold.Render("NUMBER")
	require.Equal(t, expectedHeader, lines[0])
}

func TestRender_SingleRow(t *testing.T) {
	prs := testPRs()[:1]
	r := newTestRenderer(simpleColumns(), table.WithShowIndex(true))
	out, _ := r.Render(prs)

	// Single row should still show index.
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 2, "expected header + 1 data line")

	// Data line should have 3 visible fields (index, repo, number)
	dataFields := strings.Fields(ansi.Strip(lines[1]))
	require.Len(t, dataFields, 3)
	require.Equal(t, "1", dataFields[0])
	require.Equal(t, "alpha", dataFields[1])
	require.Equal(t, "#1", dataFields[2])
}

func TestRender_AuthorColumn(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["author"]})
	_, rows := r.Render(prs)

	require.Equal(t, "alice", rows[0].Cells[0])
}

func TestRender_LabelsColumn(t *testing.T) {
	prs := []PullRequest{{
		Number:     1,
		Title:      "test",
		URL:        "https://github.com/org/repo/pull/1",
		State:      "open",
		Repository: Repository{Name: "repo", NameWithOwner: "org/repo"},
		Author:     Author{Login: "user"},
		Labels:     []Label{{Name: "bug"}, {Name: "urgent"}},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}}
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["labels"]})
	_, rows := r.Render(prs)

	require.Equal(t, "bug, urgent", rows[0].Cells[0])
}

func TestRender_StateColumn(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["state"]})
	_, rows := r.Render(prs)

	require.Equal(t, "open", rows[0].Cells[0])
}

func TestRender_URLColumn(t *testing.T) {
	prs := testPRs()[:1]
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["url"]})
	_, rows := r.Render(prs)

	require.Equal(t, "https://github.com/org/alpha/pull/1", rows[0].Cells[0])
}

func TestRender_TitleTruncation(t *testing.T) {
	longTitle := strings.Repeat("x", maxTitleLen+20)
	prs := []PullRequest{{
		Number:     1,
		Title:      longTitle,
		URL:        "https://github.com/org/repo/pull/1",
		State:      "open",
		Repository: Repository{Name: "repo", NameWithOwner: "org/repo"},
		Author:     Author{Login: "user"},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}}
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["title"]})
	_, rows := r.Render(prs)

	// The cell value itself should be truncated to maxTitleLen runes (with ellipsis).
	visible := ansi.Strip(rows[0].Cells[0])
	require.Len(t, []rune(visible), maxTitleLen)
	require.True(t, strings.HasSuffix(visible, "…"))
}

func TestRender_ColumnAlignment(t *testing.T) {
	// Verify that columns are aligned: all data lines should have same visible width
	// for each column position as the header.
	prs := testPRs()
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["state"], defs["author"]})
	out, _ := r.Render(prs)

	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 2, "expected header + data lines")

	// All lines should have consistent column alignment (same number of visual columns).
	headerCols := len(strings.Fields(ansi.Strip(lines[0])))
	for i := 1; i < len(lines); i++ {
		dataCols := len(strings.Fields(ansi.Strip(lines[i])))
		require.Equal(t, headerCols, dataCols,
			"line %d has %d visible columns, header has %d", i, dataCols, headerCols)
	}
}

func TestRender_IndexHeaderPadding(t *testing.T) {
	// When showIndex is on, the header's index column should be spaces (not a label).
	prs := testPRs()
	r := newTestRenderer(simpleColumns(), table.WithShowIndex(true))
	out, _ := r.Render(prs)

	lines := strings.Split(out, "\n")
	header := ansi.Strip(lines[0])
	// Header should start with space(s) then the first real column header.
	fields := strings.Fields(header)
	require.NotEmpty(t, fields)
	require.Equal(t, "REPO", fields[0],
		"index header should be blank (spaces), first visible field should be REPO")
}

func TestRender_NumberColumnStateColor(t *testing.T) {
	// Number column should also use state-based colors.
	prs := testPRs()[:1] // open
	defs := testPRL.allColumnDefs("", nil, tableLayout{})
	r := newTestRenderer([]Column{defs["number"]})
	_, rows := r.Render(prs)

	expected := ansi.SetHyperlink("https://github.com/org/alpha/pull/1") +
		testPRL.theme.Dim.Render(fmt.Sprintf("#%d", prs[0].Number)) + ansi.ResetHyperlink()
	require.Equal(t, expected, rows[0].Cells[0])
}

func TestNormalizeColumns(t *testing.T) {
	tests := []struct {
		input []string
		want  []string
	}{
		{nil, nil},
		{[]string{"ref", "title"}, []string{"ref", "title"}},
		{[]string{" Ref ", " Title ", " Author "}, []string{"ref", "title", "author"}},
		{[]string{"index", "ref"}, []string{"index", "ref"}},
	}
	for _, tt := range tests {
		got := normalizeColumns(tt.input)
		require.Equal(t, tt.want, got, "normalizeColumns(%v)", tt.input)
	}
}

func TestNewTableRenderer_Columns(t *testing.T) {
	cli := &CLI{Columns: CSVFlag{Values: []string{"ref", "title", "author"}}}
	r := testPRL.newTableRenderer(cli, true, nil, 0)

	// Verify by rendering and checking the header columns.
	prs := testPRs()[:1]
	out, _ := r.Render(prs)
	lines := strings.Split(out, "\n")
	headerFields := strings.Fields(ansi.Strip(lines[0]))
	require.Equal(t, []string{"PR", "TITLE", "AUTHOR"}, headerFields)
}

func TestNewTableRenderer_IndexColumn(t *testing.T) {
	cli := &CLI{Columns: CSVFlag{Values: []string{"index", "ref"}}}
	r := testPRL.newTableRenderer(cli, true, nil, 0)

	// Index should show for >1 result.
	prs := testPRs()
	out, _ := r.Render(prs)
	lines := strings.Split(out, "\n")
	header := ansi.Strip(lines[0])
	fields := strings.Fields(header)
	// Index header is blank spaces, first visible field should be "PR"
	require.Equal(t, "PR", fields[0])
}

func TestNewTableRenderer_IndexDisabledInInteractive(t *testing.T) {
	cli := &CLI{Columns: CSVFlag{Values: []string{"index", "ref"}}, Approve: true}
	r := testPRL.newTableRenderer(cli, true, nil, 0)

	// In interactive mode, index should not be shown.
	prs := testPRs()
	out, _ := r.Render(prs)
	lines := strings.Split(out, "\n")
	headerFields := strings.Fields(ansi.Strip(lines[0]))
	// Should only have "PR" (no index padding).
	require.Equal(t, []string{"PR"}, headerFields)
}

func TestNewTableRenderer_OrgFilter(t *testing.T) {
	cli := &CLI{
		Organization: CSVFlag{Values: []string{"myorg"}},
		Columns:      CSVFlag{Values: []string{"ref"}},
	}
	r := testPRL.newTableRenderer(cli, true, nil, 0)

	// With single org, ref should exclude org prefix. But org is "myorg" and PR has "org/alpha".
	// singleOrg returns "myorg", orgFilter = "myorg" (not empty, not "all").
	// So ref should use repo#N (without org prefix).
	prs := testPRs()[:1]
	_, rows := r.Render(prs)
	visible := ansi.Strip(rows[0].Cells[0])
	require.Equal(t, "alpha#1", visible)
}

func TestNewTableRenderer_OrgFilter_Multiple(t *testing.T) {
	cli := &CLI{
		Organization: CSVFlag{Values: []string{"org1", "org2"}},
		Columns:      CSVFlag{Values: []string{"ref"}},
	}
	r := testPRL.newTableRenderer(cli, true, nil, 0)

	// Multiple orgs → no org filter → ref includes full org/repo
	prs := testPRs()[:1]
	_, rows := r.Render(prs)
	visible := ansi.Strip(rows[0].Cells[0])
	require.Equal(t, "org/alpha#1", visible)
}

func TestNewTableRenderer_Reverse(t *testing.T) {
	cli := &CLI{Reverse: true, Columns: CSVFlag{Values: []string{"ref"}}}
	r := testPRL.newTableRenderer(cli, true, nil, 0)

	// --reverse → oldest at top (clib reverse=false).
	prs := testPRs()
	_, rows := r.Render(prs)
	require.Equal(t, "oldest PR", rows[0].Item.Title)
	require.Equal(t, "newest PR", rows[2].Item.Title)
}

// --- Layout computation tests ---

func TestComputeLayout_ZeroWidth(t *testing.T) {
	// Zero width (non-TTY / unknown) → no compact, no hiding.
	layout := computeLayout(0, defaultColumns())
	require.False(t, layout.compact)
	require.Empty(t, layout.hidden)
}

func TestComputeLayout_WideTerminal(t *testing.T) {
	// Wide terminal: no compact, no hiding.
	layout := computeLayout(200, defaultColumns())
	require.False(t, layout.compact)
	require.Empty(t, layout.hidden)
}

func TestComputeLayout_NarrowTerminal_CompactTime(t *testing.T) {
	// Narrow terminal with time columns: should trigger compact time.
	layout := computeLayout(80, defaultColumns())
	require.True(t, layout.compact)
}

func TestComputeLayout_MediumTerminal(t *testing.T) {
	// At or above compact threshold: no compact.
	layout := computeLayout(compactTimeThreshold, defaultColumns())
	require.False(t, layout.compact)
}

func TestComputeLayout_CustomColumns_NoTime(t *testing.T) {
	// Without time columns, compact is not needed.
	cols := []string{"index", colTitle, "ref"}
	layout := computeLayout(80, cols)
	require.False(t, layout.compact)
}

func TestEstimatedWidth_CompactShorter(t *testing.T) {
	cols := defaultColumns()
	long := estimatedWidth(cols, false)
	compact := estimatedWidth(cols, true)
	require.Less(t, compact, long, "compact layout should be narrower")
}

func testCLI() *CLI {
	cli := &CLI{}
	cli.Normalize(&Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueOpen,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
		},
	})
	return cli
}

func TestRender_NarrowTerminal_CompactTime(t *testing.T) {
	// Render with a narrow terminal width: time columns should use compact format.
	prs := testPRs()[:1]
	r := testPRL.newTableRenderer(testCLI(), true, nil, 80)
	out, _ := r.Render(prs)

	stripped := ansi.Strip(out)
	require.NotContains(t, stripped, "minutes")
	require.NotContains(t, stripped, "hours")
}

func TestRender_WideTerminal_LongTime(t *testing.T) {
	// Render with a wide terminal: time columns should use long format.
	prs := testPRs()[:1]
	r := testPRL.newTableRenderer(testCLI(), true, nil, 200)
	out, _ := r.Render(prs)

	stripped := ansi.Strip(out)
	require.Contains(t, stripped, "hour")
}

func TestRender_FlexTruncation(t *testing.T) {
	// The flex column (title) should be truncated to fit within terminal width.
	longTitle := strings.Repeat("x", maxTitleLen)
	prs := []PullRequest{{
		Number:     1,
		Title:      longTitle,
		URL:        "https://github.com/org/repo/pull/1",
		State:      "open",
		Repository: Repository{Name: "repo", NameWithOwner: "org/repo"},
		Author:     Author{Login: "user"},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}}

	r := testPRL.newTableRenderer(testCLI(), true, nil, 80)
	out, _ := r.Render(prs)

	// Every line should fit within 80 columns.
	for i, line := range strings.Split(out, "\n") {
		w := lipgloss.Width(line)
		require.LessOrEqual(t, w, 80,
			"line %d has visible width %d, exceeds terminal width 80", i, w)
	}
}

func TestRender_FlexNoTruncationWhenWide(t *testing.T) {
	// On a wide terminal, title should not be truncated beyond maxTitleLen.
	longTitle := strings.Repeat("x", maxTitleLen+20)
	prs := []PullRequest{{
		Number:     1,
		Title:      longTitle,
		URL:        "https://github.com/org/repo/pull/1",
		State:      "open",
		Repository: Repository{Name: "repo", NameWithOwner: "org/repo"},
		Author:     Author{Login: "user"},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}}

	r := testPRL.newTableRenderer(testCLI(), true, nil, 300)
	_, rows := r.Render(prs)

	// Title cell should be truncated to maxTitleLen (the pre-render cap), not beyond.
	visible := ansi.Strip(rows[0].Cells[0])
	require.Len(t, []rune(visible), maxTitleLen)
	require.True(t, strings.HasSuffix(visible, "…"))
}

// --- Column hiding tests ---

func TestComputeLayout_NeverHidesIndex(t *testing.T) {
	// Index should never be hidden, even at very narrow widths.
	layout := computeLayout(30, defaultColumns())
	require.False(t, layout.hidden["index"], "index should never be hidden")
	require.False(t, layout.hidden["idx"], "idx should never be hidden")
	require.False(t, layout.hidden["i"], "i should never be hidden")
}

func TestComputeLayout_HidesCreatedBeforeUpdated(t *testing.T) {
	// Progressively narrowing: created should be hidden before updated.
	// Find a width where created is hidden but updated is not.
	cols := defaultColumns()
	var foundWidth int
	for w := 80; w > 30; w-- {
		layout := computeLayout(w, cols)
		if layout.hidden[valueCreated] && !layout.hidden[valueUpdated] {
			foundWidth = w
			break
		}
	}
	require.NotZero(t, foundWidth,
		"should find a width where created is hidden but updated is not")
}

func TestComputeLayout_HidesUpdatedBeforeTitle(t *testing.T) {
	// Find a width where updated is hidden but title is not.
	cols := defaultColumns()
	var foundWidth int
	for w := 80; w > 20; w-- {
		layout := computeLayout(w, cols)
		if layout.hidden[valueUpdated] && !layout.hidden[colTitle] {
			foundWidth = w
			break
		}
	}
	require.NotZero(t, foundWidth,
		"should find a width where updated is hidden but title is not")
}

func TestComputeLayout_NeverHidesRef(t *testing.T) {
	// Even at extremely narrow widths, ref should never be hidden.
	layout := computeLayout(10, defaultColumns())
	require.False(t, layout.hidden["ref"], "ref should never be hidden")
}

func TestComputeLayout_HideOrder(t *testing.T) {
	// Verify the progressive hiding order: created → updated → title.
	cols := defaultColumns()
	var (
		createdHiddenAt int
		updatedHiddenAt int
		titleHiddenAt   int
	)

	for w := 200; w > 10; w-- {
		layout := computeLayout(w, cols)
		if layout.hidden[valueCreated] && createdHiddenAt == 0 {
			createdHiddenAt = w
		}
		if layout.hidden[valueUpdated] && updatedHiddenAt == 0 {
			updatedHiddenAt = w
		}
		if layout.hidden[colTitle] && titleHiddenAt == 0 {
			titleHiddenAt = w
		}
		// Index should never be hidden.
		require.False(t, layout.hidden["index"],
			"index should never be hidden (width=%d)", w)
	}

	if createdHiddenAt > 0 && updatedHiddenAt > 0 {
		require.GreaterOrEqual(t, createdHiddenAt, updatedHiddenAt,
			"created should be hidden before updated")
	}
	if updatedHiddenAt > 0 && titleHiddenAt > 0 {
		require.GreaterOrEqual(t, updatedHiddenAt, titleHiddenAt,
			"updated should be hidden before title")
	}
}

func TestComputeLayout_WideDoesNotHide(t *testing.T) {
	// At a wide terminal, nothing should be hidden.
	layout := computeLayout(200, defaultColumns())
	require.Empty(t, layout.hidden, "nothing should be hidden on a wide terminal")
}

func TestRender_VeryNarrow_HidesColumns(t *testing.T) {
	// At a very narrow width, the rendered table should have fewer header columns.
	prs := testPRs()[:1]
	cli := &CLI{}
	cli.Normalize(&Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueOpen,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
		},
	})

	// Wide: should have TITLE, PR, CREATED, UPDATED in header.
	rWide := testPRL.newTableRenderer(cli, true, nil, 200)
	outWide, _ := rWide.Render(prs)
	wideHeader := strings.Fields(ansi.Strip(strings.Split(outWide, "\n")[0]))

	// Very narrow: should have fewer columns.
	rNarrow := testPRL.newTableRenderer(cli, true, nil, 40)
	outNarrow, _ := rNarrow.Render(prs)
	narrowHeader := strings.Fields(ansi.Strip(strings.Split(outNarrow, "\n")[0]))

	require.Less(t, len(narrowHeader), len(wideHeader),
		"narrow terminal should show fewer columns than wide")
	// PR column should always be present.
	require.Contains(t, narrowHeader, "PR", "PR column should always be visible")
}
