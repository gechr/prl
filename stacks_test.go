package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/primer/table"
	"github.com/gechr/x/ansi"
	"github.com/stretchr/testify/require"
)

func stackTestPRs() []PullRequest {
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	repo := Repository{Name: "repo", NameWithOwner: "owner/repo"}
	stack := &PRStack{ID: "example-stack", BaseRefName: "main"}
	prs := make([]PullRequest, 4)
	for i := range prs {
		prs[i] = PullRequest{
			Number: i + 1, Title: "middle", State: valueOpen,
			Repository: repo, HeadRepoNWO: repo.NameWithOwner,
			HeadRefName: fmt.Sprintf("feature/layer-%d", i+1), BaseRefName: "main",
			CreatedAt: now, UpdatedAt: now, Author: Author{Login: "middle"},
			URL: fmt.Sprintf("https://github.com/owner/repo/pull/%d", i+1),
		}
		if i < 3 {
			prs[i].Stack = stack
			prs[i].StackPosition = i + 1
		}
	}
	// Both children have extreme sort values, straddling the representative.
	prs[0].Title, prs[0].Number, prs[0].Author.Login = "aardvark", 1, "aardvark"
	prs[0].CreatedAt, prs[0].UpdatedAt = now.Add(-time.Hour), now.Add(time.Hour)
	prs[1].Title, prs[1].Number, prs[1].Author.Login = "zulu", 99, "zulu"
	prs[1].CreatedAt, prs[1].UpdatedAt = now.Add(time.Hour), now.Add(-time.Hour)
	prs[2].Number = 2
	prs[3].Title, prs[3].Number, prs[3].Author.Login = "zulu", 10, "zulu"
	prs[3].CreatedAt, prs[3].UpdatedAt = now.Add(time.Minute), now.Add(time.Minute)
	return prs
}

func stackRows(prs []PullRequest) []TableRow {
	models := buildPRRowModels(prs, "", nil)
	rows := make([]TableRow, len(models))
	for i, model := range models {
		rows[i].Item = model
	}
	return rows
}

func stackRowNumbers(rows []TableRow) []int {
	result := make([]int, len(rows))
	for i, row := range rows {
		result[i] = row.Item.Number
	}
	return result
}

func newStackTestModel(t *testing.T) tuiModel {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := tuiModel{
		items:       buildPRRowModels(stackTestPRs(), "", nil),
		p:           testPRL,
		cli:         testCLI(),
		cfg:         &Config{TUI: TUIConfig{Stacks: TUIStacksConfig{Enabled: true}}},
		width:       160,
		height:      16,
		tty:         true,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		selected:    make(prKeys),
		removed:     make(prKeys),
		sortColumn:  colNumber,
		sortAsc:     true,
	}
	m.cli.Columns.Values = []string{colTitle, colRef, colNumber, valueCreated, valueUpdated}
	m.header, m.rows, m.colWidths = m.rerender()
	return m
}

func TestStackSortUsesOnlyTopPR(t *testing.T) {
	for _, column := range []string{colTitle, colNumber, colAuthor, valueCreated, valueUpdated} {
		for _, asc := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/ascending=%t", column, asc), func(t *testing.T) {
				defs := testPRL.allColumnDefs(tableLayout{})
				columns := []Column{defs[column]}
				renderer := newTestRenderer(columns, table.WithReverse(false))
				rows := renderer.Render(buildPRRowModels(stackTestPRs(), "", nil)).Rows
				grouped := groupStackRows(rows, columns, column, asc)
				want := []int{2, 99, 1, 10}
				if !asc {
					want = []int{10, 2, 99, 1}
				}
				require.Equal(t, want, stackRowNumbers(grouped))
			})
		}
	}
}

func TestStackDefaultOrderUsesTopPRPosition(t *testing.T) {
	prs := stackTestPRs()
	rows := stackRows([]PullRequest{prs[0], prs[3], prs[2], prs[1]})
	grouped := groupStackRows(rows, nil, "", false)
	require.Equal(t, []int{10, 2, 99, 1}, stackRowNumbers(grouped))
	require.Equal(t, "repo · feature/layer… · 3 PRs onto main", grouped[1].Item.StackHeader)
}

func TestStackNativeIdentityHandlesMissingIntermediatePR(t *testing.T) {
	prs := stackTestPRs()
	grouped := groupStackRows(stackRows([]PullRequest{prs[0], prs[2]}), nil, "", false)
	require.Equal(t, []int{2, 1}, stackRowNumbers(grouped))
	require.Equal(t, grouped[0].Item.StackKey, grouped[1].Item.StackKey)
	require.Equal(t, "repo · feature/layer… · 2 PRs onto main", grouped[0].Item.StackHeader)
}

func TestStackLegacyBranchChain(t *testing.T) {
	prs := stackTestPRs()[:3]
	for i := range prs {
		prs[i].Stack = nil
		if i > 0 {
			prs[i].BaseRefName = prs[i-1].HeadRefName
		}
	}
	grouped := groupStackRows(stackRows(prs), nil, "", false)
	require.Equal(t, []int{2, 99, 1}, stackRowNumbers(grouped))
	require.NotEmpty(t, grouped[0].Item.StackKey)
}

func TestStackLegacyRejectsAmbiguousOrUnrelatedBranches(t *testing.T) {
	for _, scenario := range []string{"fork", "different repo", "branching", "duplicate head", "cycle"} {
		t.Run(scenario, func(t *testing.T) {
			prs := stackTestPRs()[:3]
			for i := range prs {
				prs[i].Stack = nil
			}
			prs[1].BaseRefName = prs[0].HeadRefName
			switch scenario {
			case "fork":
				prs[0].HeadRepoNWO = "fork/repo"
			case "different repo":
				prs[1].Repository = Repository{Name: "other", NameWithOwner: "owner/other"}
			case "branching":
				prs[2].BaseRefName = prs[0].HeadRefName
			case "duplicate head":
				prs[2].HeadRefName = prs[0].HeadRefName
			case "cycle":
				prs[0].BaseRefName = prs[1].HeadRefName
			}
			groups := stackGroups(stackRows(prs))
			require.Len(t, groups, 3)
			for _, group := range groups {
				require.Len(t, group, 1)
			}
		})
	}
}

func TestStackListIDsAndHitTesting(t *testing.T) {
	m := newStackTestModel(t)
	lines := m.listLines()
	require.Len(t, lines, 5)
	labels := make([]string, len(lines))
	for i, line := range lines {
		labels[i] = line.label
	}
	require.Equal(t, []string{"1", "1a", "1b", "1c", "2"}, labels)
	require.Equal(t, "1 ", ansi.Strip(m.renderTuiIndexLabel("1", false)))
	require.Equal(t, "1a", ansi.Strip(m.renderTuiIndexLabel("1a", false)))
	require.Regexp(t, `(?m)^❯ 1a ├─ `, ansi.Strip(m.viewList().Content))
	_, ok := m.rowIndexAt(1)
	require.False(t, ok, "expanded headers are not action targets")
	idx, ok := m.rowIndexAt(2)
	require.True(t, ok)
	require.Equal(t, 2, m.rows[idx].Item.Number)
	require.Equal(t, []int{0, 3}, m.jumpIndices())
	for y := 1; y <= 4; y++ {
		require.Equal(
			t,
			m.rows[0].Item.StackKey,
			m.stackGlyphAt(m.listPrefixWidth()-stackTreeWidth, y),
		)
	}
	require.Empty(t, m.stackGlyphAt(m.listPrefixWidth()+5, 2), "PR title clicks do not fold")
}

func TestStackConnectorClickFoldsAndExpands(t *testing.T) {
	for _, y := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("line %d", y), func(t *testing.T) {
			m := newStackTestModel(t)
			m.cursor = 2
			click := tea.MouseClickMsg{
				X:      m.listPrefixWidth() - stackTreeWidth,
				Y:      y,
				Button: tea.MouseLeft,
			}
			model, _ := m.Update(click)
			folded, ok := model.(tuiModel)
			require.True(t, ok)
			require.Len(t, folded.listLines(), 2)
			require.Equal(t, "▸  ", folded.listLines()[0].tree)
			require.Nil(t, folded.currentPR())
			require.False(t, folded.toggleCurrentSelection())
			require.Empty(t, folded.targetPRs())
			model, _ = folded.Update(tea.MouseClickMsg{X: click.X, Y: 1, Button: tea.MouseLeft})
			expanded, ok := model.(tuiModel)
			require.True(t, ok)
			require.Len(t, expanded.listLines(), 5)
			require.Equal(t, 2, expanded.currentPR().Number)
		})
	}
}

func TestStackFoldPersistsAndSurvivesNewTopPR(t *testing.T) {
	m := newStackTestModel(t)
	key := m.rows[0].Item.StackKey
	m.toggleStack(key)
	state, err := loadTUIState()
	require.NoError(t, err)
	require.Equal(t, []string{string(key)}, state.FoldedStacks)
	fresh := &Config{TUI: TUIConfig{Stacks: TUIStacksConfig{Enabled: true}}}
	applyTUIState(fresh, state)
	m.cfg = fresh
	m.collapsedStacks = foldedStackKeys(fresh.TUI.FoldedStacks)
	newTop := m.items[2]
	newTop.PR.Number, newTop.Number, newTop.PR.StackPosition = 100, 100, 4
	m.items = append(m.items, newTop)
	m.header, m.rows, m.colWidths = m.rerender()
	require.Len(t, m.listLines(), 2)
	require.Equal(t, key, m.listLines()[1].stackKey)
	m.toggleStack(key)
	state, err = loadTUIState()
	require.NoError(t, err)
	require.Empty(t, state.FoldedStacks)
}

func TestStackTogglePersistsWithoutFetching(t *testing.T) {
	m := newStackTestModel(t)
	m.cursor = 1
	wantKey := m.rowKeyAt(m.cursor)
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Values[filterRowStacks] = filterChoiceIndex(filterRowStacks, filterChoiceFalse)
	model, cmd := m.applyFilterOptions()
	require.Nil(t, cmd)
	flat, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, flat.cfg.TUI.Stacks.Enabled)
	require.Equal(t, wantKey, flat.rowKeyAt(flat.cursor))
	require.Len(t, flat.listLines(), 4)
	state, err := loadTUIState()
	require.NoError(t, err)
	require.False(t, state.Stacks.Enabled)
	flat.optionsPicker = flat.newFilterPicker()
	flat.optionsPicker.Values[filterRowStacks] = filterChoiceIndex(
		filterRowStacks,
		filterChoiceTrue,
	)
	model, cmd = flat.applyFilterOptions()
	require.Nil(t, cmd)
	stacked, ok := model.(tuiModel)
	require.True(t, ok)
	require.Len(t, stacked.listLines(), 5)
	require.Equal(t, wantKey, stacked.rowKeyAt(stacked.cursor))
}

func TestStackViewportAccountsForHeader(t *testing.T) {
	m := newStackTestModel(t)
	m.height = 6
	m.cursor = 3
	m.offset = m.scrolledOffset()
	viewport := m.listViewport()
	require.Positive(t, m.offset)
	require.Less(t, m.cursorLinePosition(), m.offset+viewport)
	idx, ok := m.rowIndexAt(m.cursorLinePosition() - m.offset + 1)
	require.True(t, ok)
	require.Equal(t, m.cursor, idx)
}

func TestStackMetadataGraphQLShapeAndCache(t *testing.T) {
	var node graphQLSearchPRNode
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"example-pr","number":3,"headRefName":"feature/top","baseRefName":"feature/base",
		"repository":{"name":"repo","nameWithOwner":"owner/repo"},
		"headRepository":{"name":"repo","nameWithOwner":"owner/repo"},
		"stack":{"id":"example-stack","baseRefName":"main"},"stackEntry":{"position":3}
	}`), &node))
	pr := toPullRequestGraphQL(node)
	require.Equal(t, "example-stack", pr.Stack.ID)
	require.Equal(t, 3, pr.StackPosition)
	require.Equal(t, "owner/repo", pr.HeadRepoNWO)
	pr.Labels = nil // the cache normalizes empty labels to nil
	require.Equal(t, pr, cachedPR(pr).pullRequest())
}

func TestHydrateStackMetadataRESTFallback(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		var payload struct {
			Variables struct {
				IDs []string `json:"ids"`
			} `json:"variables"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, []string{"example-pr"}, payload.Variables.IDs)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body: io.NopCloser(strings.NewReader(`{
			"data":{"nodes":[{"id":"example-pr","headRefName":"feature/top","baseRefName":"feature/base",
			"headRepository":{"nameWithOwner":"owner/repo"},"stack":{"id":"example-stack","baseRefName":"main"},
			"stackEntry":{"position":3}}]}}`)),
		}, nil
	})
	gql, err := api.NewGraphQLClient(
		api.ClientOptions{AuthToken: "test", Host: "github.com", Transport: transport},
	)
	require.NoError(t, err)
	prs := []PullRequest{{NodeID: "example-pr"}}
	require.NoError(t, hydrateStackMetadata(gql, prs))
	require.Equal(t, "feature/top", prs[0].HeadRefName)
	require.Equal(t, "main", prs[0].Stack.BaseRefName)
	require.Equal(t, 3, prs[0].StackPosition)
	require.NoError(t, hydrateStackMetadata(gql, prs))
	require.Equal(t, 1, requests, "GraphQL search metadata needs no extra hydration")
}

func TestStackKeyboardControlsAndNumericJumps(t *testing.T) {
	m := newStackTestModel(t)
	model, _ := m.updateListView(tea.KeyPressMsg{Code: tea.KeyLeft})
	m, ok := model.(tuiModel)
	require.True(t, ok)
	require.Len(t, m.listLines(), 2)
	model, _ = m.updateListView(tea.KeyPressMsg{Code: '2', Text: "2"})
	m, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 10, m.currentPR().Number)
	m.cursor = 0
	model, _ = m.updateListView(tea.KeyPressMsg{Code: tea.KeyRight})
	m, ok = model.(tuiModel)
	require.True(t, ok)
	require.Len(t, m.listLines(), 5)
}

func TestStackRefreshRetainsFoldAndVisibleCursor(t *testing.T) {
	m := newStackTestModel(t)
	m.toggleStack(m.rows[0].Item.StackKey)
	prs := stackTestPRs()
	newTop := prs[2]
	newTop.Number, newTop.StackPosition = 100, 4
	prs = append(prs, newTop)
	items := buildPRRowModels(prs, "", nil)
	model, _ := m.Update(refreshResultMsg{items: items})
	fresh, ok := model.(tuiModel)
	require.True(t, ok)
	require.Len(t, fresh.listLines(), 2)
	require.Contains(t, fresh.visibleIndices(), fresh.cursor)
	require.Nil(t, fresh.currentPR())
	require.Equal(t, "▸  ", fresh.listLines()[fresh.cursorLinePosition()].tree)
}

func TestStackFilteringKeepsHeadersOutOfActions(t *testing.T) {
	m := newStackTestModel(t)
	m.filterInput.SetValue("aardvark")
	m.resyncCursorAndOffset()
	lines := m.listLines()
	require.Len(t, lines, 2)
	require.Equal(t, "1a", lines[1].label)
	require.Equal(t, "╰─ ", lines[1].tree)
	require.Equal(t, 1, m.currentPR().Number)
	m.toggleStack(lines[0].stackKey)
	require.Len(t, m.listLines(), 1)
	require.Nil(t, m.currentPR())
}

func TestStackHeaderStyleUsesTerminalColours(t *testing.T) {
	t.Cleanup(func() { initPalette(false) })
	for _, light := range []bool{false, true} {
		initPalette(light)
		require.True(t, styleStackHeader.GetFaint())
		require.True(t, styleStackHeader.GetItalic())
		require.False(t, styleStackHeader.GetBold())
		require.Equal(t, lg.NoColor{}, styleStackHeader.GetForeground())
	}
}

func TestStackLetterBeyond26Entries(t *testing.T) {
	require.Equal(t, "a", stackLetter(0))
	require.Equal(t, "z", stackLetter(25))
	require.Equal(t, "aa", stackLetter(26))
	require.Equal(t, "az", stackLetter(51))
	require.Equal(t, "ba", stackLetter(52))
}

func TestStackGlyphsAdaptToThemeAndIconMode(t *testing.T) {
	original := activeIcons
	t.Cleanup(func() {
		activeIcons = original
		initPalette(false)
	})
	for _, tc := range []struct {
		icons     iconSet
		expanded  string
		collapsed string
		sortUp    string
		sortDown  string
	}{
		{iconsUnicode, "▾  ", "▸  ", "▲", "▼"},
		{iconsNerd, "\uf47c  ", "\uf460  ", "\ueab7", "\ueab4"},
	} {
		useIcons(tc.icons)
		for _, light := range []bool{false, true} {
			initPalette(light)
			m := newStackTestModel(t)
			m.cursor = 3 // keep stack lines free of the cursor's background escapes
			lines := m.listLines()
			require.Equal(
				t,
				[]string{"├─ ", "├─ ", "╰─ "},
				[]string{lines[1].tree, lines[2].tree, lines[3].tree},
			)
			for _, asc := range []bool{true, false} {
				sorted := m
				sorted.sortAsc = asc
				header, _, _ := sorted.rerender()
				glyph := tc.sortDown
				if asc {
					glyph = tc.sortUp
				}
				expected := m.p.RenderDim(" " + glyph)
				if tc.icons == iconsNerd {
					expected = styleStackArrow.Render(" " + glyph)
				}
				require.Regexp(t, regexp.QuoteMeta(expected), header)
			}
			output := m.viewList().Content
			require.Regexp(t, regexp.QuoteMeta(styleStackArrow.Render(tc.expanded)), output)
			for _, glyph := range []string{"├─ ", "╰─ "} {
				require.Regexp(t, regexp.QuoteMeta(m.p.theme.MarkdownText.Render(glyph)), output)
			}
			m.toggleStack(m.rows[0].Item.StackKey)
			require.Regexp(
				t,
				regexp.QuoteMeta(styleHeading.Render(tc.collapsed)),
				m.viewList().Content,
			)
		}
	}
}

func TestStackScrollingBackUpRestoresFirstHeader(t *testing.T) {
	m := newStackTestModel(t)
	m.height = 6
	lines := m.listLines()
	m.cursor = lines[len(lines)-1].row
	m.offset = m.scrolledOffset()
	require.Positive(t, m.offset)
	m.cursor = lines[1].row
	m.offset = m.scrolledOffset()
	require.Zero(t, m.offset)
	// One-line viewports prioritize the PR itself, since both lines cannot fit.
	m.height = 4
	m.offset = m.scrolledOffset()
	require.Equal(t, 1, m.offset)
}

func TestStackJumpToTopShowsHeader(t *testing.T) {
	m := newStackTestModel(t)
	m.height = 6
	m.cursor = 3
	m.offset = m.scrolledOffset()
	model, _ := m.updateListView(tea.KeyPressMsg{Code: 'g', Text: "g"})
	m, ok := model.(tuiModel)
	require.True(t, ok)
	require.Zero(t, m.offset)
	require.Equal(t, 2, m.currentPR().Number)
}

func TestStackShiftMoveSkipsFoldedHeaderSelection(t *testing.T) {
	m := newStackTestModel(t)
	m.toggleStack(m.rows[0].Item.StackKey)
	m.extendSelectionAndMove(1)
	require.Equal(t, 10, m.currentPR().Number)
	require.Len(t, m.selected, 1)
	m.extendSelectionAndMove(-1)
	require.Nil(t, m.currentPR())
	require.Len(t, m.selected, 1)
	require.Len(t, m.targetPRs(), 1)
	require.Equal(t, 10, m.targetPRs()[0].pr.Number)
}

func TestStackLabelPreservesUnicode(t *testing.T) {
	rows := []TableRow{
		{Item: PRRowModel{PR: PullRequest{HeadRefName: "feature/éclair"}}},
		{Item: PRRowModel{PR: PullRequest{HeadRefName: "feature/être"}}},
	}
	require.Equal(t, "feature/être", stackLabel(rows))
}

func TestStackLabelMarksShortenedPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		branches []string
		want     string
	}{
		{"partial word", []string{"topic/refactor-cache", "topic/remove-cache"}, "topic/re…"},
		{"layer suffix", []string{"feature/layer-3", "feature/layer-1"}, "feature/layer…"},
		{"single-character suffix", []string{"topic/cacheA", "topic/cacheB"}, "topic/cache…"},
		{"unicode prefix", []string{"topic/éclair", "topic/école"}, "topic/éc…"},
		{"no prefix", []string{"add-metrics", "enable-cache"}, "enable-cache"},
		{"namespace only", []string{"dev/add-cache", "dev/remove-cache"}, "dev/remove-cache"},
		{"complete branch", []string{"feature/cache", "feature/cache"}, "feature/cache"},
		{"one branch extends the other", []string{"feature/cache-more", "feature/cache"}, "feature/cache…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]TableRow, len(tc.branches))
			for i, branch := range tc.branches {
				rows[i].Item.PR.HeadRefName = branch
			}
			require.Equal(t, tc.want, stackLabel(rows))
		})
	}
}
