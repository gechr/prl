package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/prl/internal/table"
	"github.com/stretchr/testify/require"
)

func TestCurrentClaudeReviewLauncher(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	require.Equal(t, claudeLauncherGhostty, currentClaudeReviewLauncher())

	t.Setenv("TERM_PROGRAM", "iTerm.app")
	require.Equal(t, claudeLauncherITerm2, currentClaudeReviewLauncher())

	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	require.Equal(t, claudeLauncherNone, currentClaudeReviewLauncher())
}

func TestBuildClaudeReviewAppleScriptGhosttyUsesNewTab(t *testing.T) {
	script, err := buildClaudeReviewAppleScript(claudeLauncherGhostty, "echo 'review'\n")

	require.NoError(t, err)
	require.Contains(t, script, `tell application "Ghostty"`)
	require.Contains(t, script, "set cfg to new surface configuration")
	require.Contains(t, script, `set initial input of cfg to "echo 'review'\n"`)
	require.Contains(t, script, "new tab in front window with configuration cfg")
	require.NotContains(t, script, "split focused terminal")
	require.NotContains(t, script, "display dialog")
}

func TestBuildClaudeReviewAppleScriptITerm2UsesNewTab(t *testing.T) {
	script, err := buildClaudeReviewAppleScript(claudeLauncherITerm2, "echo review")

	require.NoError(t, err)
	require.Contains(t, script, `tell application "iTerm2"`)
	require.Contains(t, script, `tell current window`)
	require.Contains(t, script, `set newTab to (create tab with default profile)`)
	require.Contains(t, script, `write text " " & "echo review"`)
	require.NotContains(t, script, "split horizontally")
	require.NotContains(t, script, "split vertically")
	require.NotContains(t, script, "display dialog")
}

func TestBuildClaudeReviewAppleScriptUnsupported(t *testing.T) {
	_, err := buildClaudeReviewAppleScript(claudeLauncherNone, "echo review")

	require.ErrorContains(t, err, "unsupported terminal")
}

func TestPrepareClaudeReviewConfirmUsesYesNo(t *testing.T) {
	var m tuiModel
	pr := testReviewPullRequest()

	m = m.prepareClaudeReviewConfirm(pr, 0)

	require.Equal(t, "review", m.confirmAction)
	require.NotNil(t, m.confirmCmd)
	require.True(t, m.confirmYes)
	require.Contains(t, m.confirmPrompt, "This will clone the repo and open a new terminal tab.")
}

func TestUpdateListViewAltRBypassesConfirm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	pr := testReviewPullRequest()
	m := tuiModel{
		items:    []PRRowModel{{PR: pr}},
		rows:     []TableRow{{Item: PRRowModel{PR: pr}}},
		removed:  make(prKeys),
		selected: make(prKeys),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, "review", bm.confirmAction)

	model, cmd = m.updateListView(tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	require.NotNil(t, cmd)
	altModel, ok := model.(tuiModel)
	require.True(t, ok)

	require.Empty(t, altModel.confirmAction)
	require.Empty(t, altModel.confirmPrompt)
	require.Nil(t, altModel.confirmCmd)
}

func TestRenderHelpOverlayIncludesAltRReviewShortcut(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := tuiModel{styles: newTuiStyles()}

	overlay := m.renderHelpOverlay()

	require.Contains(t, overlay, "Launch Claude review")
	require.Contains(t, overlay, "alt+r")
	require.Contains(t, overlay, "Launch Claude review (no confirm)")
	require.Contains(t, overlay, "shift+↑/↓")
}

func TestRenderHelpOverlayAlignsExtendedSelectionKey(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}

	overlay := ansi.Strip(m.renderHelpOverlay())
	lines := strings.Split(overlay, "\n")

	spaceLine := ""
	shiftLine := ""
	for _, line := range lines {
		if strings.Contains(line, "Toggle selection") {
			spaceLine = line
		}
		if strings.Contains(line, "Extend selection") {
			shiftLine = line
		}
	}

	require.NotEmpty(t, spaceLine)
	require.NotEmpty(t, shiftLine)
	require.Equal(
		t,
		lg.Width(strings.SplitN(spaceLine, "Toggle selection", 2)[0]),
		lg.Width(strings.SplitN(shiftLine, "Extend selection", 2)[0]),
	)
}

func TestTuiTableRendererSuppressesIndexColumn(t *testing.T) {
	models := testModels("org")
	m := tuiModel{
		p:     testPRL,
		cli:   &CLI{Author: &CSVFlag{}},
		width: 120,
	}

	rt := m.tableRendererFor(len(models)).Render(models)

	require.NotEmpty(t, rt.Rows)
	require.True(t, strings.HasPrefix(ansi.Strip(rt.Rows[0].Display), "newest PR"))
}

func TestRerenderShowsEstimatedHeaderWithoutRows(t *testing.T) {
	m := tuiModel{
		p:     testPRL,
		cli:   testCLI(),
		width: 120,
	}

	header, rows, colWidths := m.rerender()
	renderer := m.tableRendererFor(len(m.items))

	titleIdx := -1
	for i, col := range renderer.Columns() {
		if col.Name == colTitle {
			titleIdx = i
			break
		}
	}

	require.Empty(t, rows)
	require.NotEmpty(t, colWidths)
	require.NotEqual(t, -1, titleIdx)
	require.Contains(t, ansi.Strip(header), "TITLE")
	require.GreaterOrEqual(t, colWidths[titleIdx], columnWidthEstimate[colTitle])
}

func TestRefreshResultClearsRowsButKeepsHeader(t *testing.T) {
	items := testModels("org")[:1]
	renderer := testPRL.newTableRenderer(testCLI(), true, 120, table.WithShowIndex(false))
	rt := renderer.Render(items)

	m := tuiModel{
		items:       items,
		rows:        rt.Rows,
		header:      rt.Header,
		colWidths:   rt.ColWidths,
		width:       120,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed:     make(prKeys),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
	}

	model, cmd := m.Update(refreshResultMsg{})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Empty(t, bm.items)
	require.Empty(t, bm.rows)
	require.Contains(t, ansi.Strip(bm.header), "TITLE")
	require.NotEmpty(t, bm.colWidths)
}

func TestViewListShowsRefreshingHeaderWithoutRows(t *testing.T) {
	m := tuiModel{
		width:       120,
		height:      12,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed:     make(prKeys),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
		spinner:     spinner{frames: []string{"*"}},
		refreshing:  true,
	}
	m.header, m.rows, m.colWidths = m.rerender()

	out := ansi.Strip(m.viewList().Content)
	lines := strings.Split(out, "\n")

	require.NotEmpty(t, lines)
	require.Contains(t, lines[0], "*")
	require.Contains(t, lines[0], "TITLE")
}

func TestViewListNumbersVisibleRows(t *testing.T) {
	fi := textinput.New()
	fi.SetValue("eta")
	m := tuiModel{
		rows: []TableRow{
			{Cells: []table.Cell{{Plain: "alpha"}}, Display: "alpha"},
			{Cells: []table.Cell{{Plain: "beta"}}, Display: "beta"},
			{Cells: []table.Cell{{Plain: "gamma"}}, Display: "gamma"},
		},
		cursor:      -1,
		height:      20,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: fi,
		removed:     make(prKeys),
		selected:    make(prKeys),
		p:           testPRL,
	}

	out := ansi.Strip(m.viewList().Content)

	require.Contains(t, out, "  1  beta")
	require.NotContains(t, out, "alpha")
	require.NotContains(t, out, "gamma")
}

func TestViewListFilterIndicatorIsLeftClamped(t *testing.T) {
	m := tuiModel{
		height:      12,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed:     make(prKeys),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         &CLI{State: valueClosed},
	}

	out := ansi.Strip(m.viewList().Content)
	lines := strings.Split(out, "\n")

	found := false
	for _, line := range lines {
		if strings.HasSuffix(line, " state:closed ──") {
			found = true
			require.True(t, strings.HasPrefix(line, "──"))
			break
		}
	}
	require.True(t, found)
	require.NotContains(t, out, " · ")
}

func TestUpdateListViewShiftDownSelectsAndMovesNext(t *testing.T) {
	m := tuiModel{
		rows: []TableRow{
			{Item: PRRowModel{PR: testReviewPullRequest()}},
			{Item: PRRowModel{PR: PullRequest{Number: 43}}},
			{Item: PRRowModel{PR: PullRequest{Number: 44}}},
		},
		cursor:      0,
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: textinput.New(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(0)])
	require.True(t, bm.selected[bm.rowKeyAt(1)])
	require.Equal(t, 1, bm.cursor)
}

func TestUpdateListViewShiftUpSelectsAndMovesPrevious(t *testing.T) {
	m := tuiModel{
		rows: []TableRow{
			{Item: PRRowModel{PR: testReviewPullRequest()}},
			{Item: PRRowModel{PR: PullRequest{Number: 43}}},
			{Item: PRRowModel{PR: PullRequest{Number: 44}}},
		},
		cursor:      2,
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: textinput.New(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(1)])
	require.True(t, bm.selected[bm.rowKeyAt(2)])
	require.Equal(t, 1, bm.cursor)
}

func TestUpdateListViewShiftUpAtTopIsNoOp(t *testing.T) {
	m := tuiModel{
		rows: []TableRow{
			{Item: PRRowModel{PR: testReviewPullRequest()}},
			{Item: PRRowModel{PR: PullRequest{Number: 43}}},
		},
		cursor:      0,
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: textinput.New(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(0)])
	require.Equal(t, 0, bm.cursor)
}

func TestUpdateListViewShiftDownAtBottomIsNoOp(t *testing.T) {
	m := tuiModel{
		rows: []TableRow{
			{Item: PRRowModel{PR: testReviewPullRequest()}},
			{Item: PRRowModel{PR: PullRequest{Number: 43}}},
		},
		cursor:      1,
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: textinput.New(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(1)])
	require.Equal(t, 1, bm.cursor)
}

func TestUpdateListViewShiftDownAtBottomAfterRangeDoesNotFlicker(t *testing.T) {
	m := tuiModel{
		rows: []TableRow{
			{Item: PRRowModel{PR: testReviewPullRequest()}},
			{Item: PRRowModel{PR: PullRequest{Number: 43}}},
		},
		cursor:      0,
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: textinput.New(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(0)])
	require.True(t, bm.selected[bm.rowKeyAt(1)])
	require.Equal(t, 1, bm.cursor)

	model, cmd = bm.updateListView(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(0)])
	require.True(t, bm.selected[bm.rowKeyAt(1)])
	require.Equal(t, 1, bm.cursor)
}

func TestUpdateListViewShiftDirectionChangeDoesNotDeselect(t *testing.T) {
	m := tuiModel{
		rows: []TableRow{
			{Item: PRRowModel{PR: testReviewPullRequest()}},
			{Item: PRRowModel{PR: PullRequest{Number: 43}}},
			{Item: PRRowModel{PR: PullRequest{Number: 44}}},
		},
		cursor:      0,
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: textinput.New(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)

	model, cmd = bm.updateListView(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(0)])
	require.True(t, bm.selected[bm.rowKeyAt(1)])
	require.Equal(t, 0, bm.cursor)
}

func TestUpdateListViewDigitJumpImmediateWhenUnambiguous(t *testing.T) {
	m := testDigitJumpModel(15)

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: '2', Text: "2"})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 1, bm.cursor)
	require.Zero(t, bm.offset)
	require.Zero(t, bm.jumpDigit)
}

func TestUpdateListViewDigitJumpWaitsWhenTwoDigitRowsExist(t *testing.T) {
	m := testDigitJumpModel(15)

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: '1', Text: "1"})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.cursor)
	require.Equal(t, 1, bm.jumpDigit)

	model, cmd = bm.updateListView(tea.KeyPressMsg{Code: '5', Text: "5"})

	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 14, bm.cursor)
	require.Zero(t, bm.jumpDigit)
}

func TestUpdateListViewSpaceOnlySelectsCurrent(t *testing.T) {
	m := tuiModel{
		rows: []TableRow{
			{Item: PRRowModel{PR: testReviewPullRequest()}},
			{Item: PRRowModel{PR: PullRequest{Number: 43}}},
		},
		cursor:      0,
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: textinput.New(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: tea.KeySpace})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.selected[bm.rowKeyAt(0)])
	require.Equal(t, 0, bm.cursor)
}

func TestRefreshTickIgnoresStaleAndInFlight(t *testing.T) {
	m := tuiModel{
		autoRefresh: true,
		refreshID:   1,
		view:        tuiViewDetail,
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 2, bm.refreshID)

	model, cmd = bm.Update(refreshTickMsg{id: 1})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.refreshing)

	bm.refreshing = true
	model, cmd = bm.Update(refreshTickMsg{id: bm.refreshID})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.refreshing)
}

func TestViewDiffHandlesTinyViewport(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diffKey:   makePRKey(pr),
		diffLines: []string{"@@ -1 +1 @@", "+small"},
		height:    2,
		width:     20,
		styles:    newTuiStyles(),
	}

	require.NotPanics(t, func() {
		_ = m.viewDiff()
	})
}

func TestUpdateDiffViewBottomUsesContentViewport(t *testing.T) {
	pr := testReviewPullRequest()
	diffLines := make([]string, 20)
	for i := range diffLines {
		diffLines[i] = fmt.Sprintf("line %d", i)
	}
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diffKey:   makePRKey(pr),
		diffLines: diffLines,
		view:      tuiViewDiff,
		height:    12,
		width:     180,
		styles:    newTuiStyles(),
	}

	vp := m.diffContentViewport()
	topPct := scrollPercent(0, len(diffLines), vp)
	topStatus := fmt.Sprintf("1-%d/%d (%d%%)", vp, len(diffLines), topPct)
	require.Equal(t, 1, strings.Count(ansi.Strip(m.viewDiff().Content), topStatus))

	model, cmd := m.updateDiffView(tea.KeyPressMsg{Code: 'G', Text: "G"})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, len(diffLines)-vp, bm.diffScroll)
	bottomStatus := fmt.Sprintf("%d-%d/%d (100%%)", bm.diffScroll+1, len(diffLines), len(diffLines))
	require.Equal(t, 1, strings.Count(ansi.Strip(bm.viewDiff().Content), bottomStatus))
}

func TestWrapDiffLinesCreatesStandaloneANSIWrappedRows(t *testing.T) {
	line := lg.NewStyle().Foreground(lg.Color("196")).Render("+abcdef")

	rows := wrapDiffLines(line, 4)

	require.Len(t, rows, 2)
	require.Equal(t, []string{"+abc", "def"}, []string{
		ansi.Strip(rows[0]),
		ansi.Strip(rows[1]),
	})
	require.LessOrEqual(t, lg.Width(rows[0]), 4)
	require.LessOrEqual(t, lg.Width(rows[1]), 4)
	require.True(t, strings.HasPrefix(rows[1], "\x1b["))
}

func TestWindowSizeMsgRewrapsDiffAndClampsScroll(t *testing.T) {
	pr := testReviewPullRequest()
	diff := lg.NewStyle().Foreground(lg.Color("196")).Render("+abcdef")
	m := tuiModel{
		rows:       []TableRow{{Item: PRRowModel{PR: pr}}},
		diff:       diff,
		diffKey:    makePRKey(pr),
		diffLines:  wrapDiffLines(diff, 4),
		diffScroll: 1,
		view:       tuiViewDiff,
		height:     8,
		width:      4,
		styles:     newTuiStyles(),
		p:          testPRL,
		cli:        testCLI(),
	}

	model, cmd := m.Update(tea.WindowSizeMsg{Width: 8, Height: 8})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, []string{"+abcdef"}, []string{ansi.Strip(bm.diffLines[0])})
	require.Len(t, bm.diffLines, 1)
	require.Zero(t, bm.diffScroll)
}

func TestViewDiffShowsWrappedContinuationRows(t *testing.T) {
	pr := testReviewPullRequest()
	diff := lg.NewStyle().Foreground(lg.Color("196")).Render("+" + strings.Repeat("a", 85))
	diffLines := wrapDiffLines(diff, 80)
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diff:      diff,
		diffKey:   makePRKey(pr),
		diffLines: diffLines,
		height:    8,
		width:     80,
		styles:    newTuiStyles(),
	}

	out := ansi.Strip(m.viewDiff().Content)

	require.Contains(t, out, strings.Join([]string{
		ansi.Strip(diffLines[0]),
		ansi.Strip(diffLines[1]),
	}, "\n"))
}

func TestTruncateDisplayLinePreservesUTF8(t *testing.T) {
	line := lg.NewStyle().Foreground(lg.Color("196")).Render("ééé")

	truncated := truncateDisplayLine(line, 1)

	require.True(t, utf8.ValidString(ansi.Strip(truncated)))
	require.Equal(t, 1, lg.Width(ansi.Strip(truncated)))
}

func TestActionMsgRemovalRecomputesOffset(t *testing.T) {
	prs := []PullRequest{
		testReviewPullRequest(),
		{Number: 43, Repository: Repository{Name: "prl", NameWithOwner: "gechr/prl"}},
		{Number: 44, Repository: Repository{Name: "prl", NameWithOwner: "gechr/prl"}},
		{Number: 45, Repository: Repository{Name: "prl", NameWithOwner: "gechr/prl"}},
		{Number: 46, Repository: Repository{Name: "prl", NameWithOwner: "gechr/prl"}},
	}
	rows := make([]TableRow, len(prs))
	for i, pr := range prs {
		rows[i] = TableRow{Item: PRRowModel{PR: pr}}
	}
	m := tuiModel{
		rows:        rows,
		cursor:      len(rows) - 1,
		offset:      len(rows) - 1,
		height:      6,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed:     make(prKeys),
		selected:    make(prKeys),
	}

	model, cmd := m.Update(actionMsg{
		index:  len(rows) - 1,
		key:    makePRKey(prs[len(prs)-1]),
		action: tuiActionClosed,
	})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Less(t, bm.offset, m.offset)
	require.Equal(t, bm.scrolledOffset(), bm.offset)
}

func TestBatchActionFailuresSurfaceDetails(t *testing.T) {
	pr := testReviewPullRequest()
	msg := batchActionMsg{
		action: tuiActionApproved,
		count:  1,
		failed: 1,
		failures: []batchResult{{
			key: makePRKey(pr),
			ref: pr.Ref(),
			url: pr.URL,
			err: errors.New("boom"),
		}},
	}
	m := tuiModel{
		styles:   newTuiStyles(),
		removed:  make(prKeys),
		selected: make(prKeys),
	}

	model, cmd := m.Update(msg)

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiActionInfo, bm.confirmAction)
	require.Contains(t, bm.confirmPrompt, pr.Ref())
	require.Contains(t, bm.confirmPrompt, "boom")
}

func TestMergeRefreshKeepsKeyedStateAcrossReorder(t *testing.T) {
	prA := testReviewPullRequest()
	prB := PullRequest{
		Number: 43,
		Repository: Repository{
			Name:          "prl",
			NameWithOwner: "gechr/prl",
		},
	}
	oldRows := []TableRow{
		{Item: PRRowModel{PR: prA}},
		{Item: PRRowModel{PR: prB}},
	}
	newRows := []TableRow{
		{Item: PRRowModel{PR: prB}},
		{Item: PRRowModel{PR: prA}},
	}
	m := tuiModel{
		rows:        oldRows,
		items:       []PRRowModel{{PR: prA}, {PR: prB}},
		cursor:      1,
		height:      8,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed: prKeys{
			makePRKey(prA): true,
		},
		selected: prKeys{
			makePRKey(prB): true,
		},
		diffQueue: []prKey{makePRKey(prB)},
	}

	bm := m.mergeRefresh(newRows, []PRRowModel{{PR: prB}, {PR: prA}})

	require.True(t, bm.removed[makePRKey(prA)])
	require.True(t, bm.selected[makePRKey(prB)])
	require.Equal(t, 0, bm.cursor)
	require.Equal(t, []int{0}, bm.visibleIndices())
	require.Equal(t, []prKey{makePRKey(prB)}, bm.diffQueue)
}

func testDigitJumpModel(total int) tuiModel {
	rows := make([]TableRow, total)
	for i := range total {
		rows[i] = TableRow{
			Item: PRRowModel{
				PR: PullRequest{
					Number: i + 1,
					Repository: Repository{
						Name:          "prl",
						NameWithOwner: "gechr/prl",
					},
				},
			},
		}
	}
	return tuiModel{
		rows:        rows,
		cursor:      0,
		height:      20,
		width:       120,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed:     make(prKeys),
		selected:    make(prKeys),
	}
}

func TestParseFilterTerm(t *testing.T) {
	tests := []struct {
		input string
		want  filterTerm
	}{
		{"foo", filterTerm{text: "foo"}},
		{"^foo", filterTerm{text: "foo", prefix: true}},
		{"foo$", filterTerm{text: "foo", suffix: true}},
		{"^foo$", filterTerm{text: "foo", prefix: true, suffix: true}},
		{"!foo", filterTerm{text: "foo", negate: true}},
		{"!^foo", filterTerm{text: "foo", negate: true, prefix: true}},
		{"!foo$", filterTerm{text: "foo", negate: true, suffix: true}},
		{"!^foo$", filterTerm{text: "foo", negate: true, prefix: true, suffix: true}},
		{"Foo", filterTerm{text: "Foo", caseSensitive: true}},
		// Bare modifiers: flags set but empty text matches everything.
		{"^", filterTerm{text: "", prefix: true}},
		{"$", filterTerm{text: "", suffix: true}},
		{"!", filterTerm{text: "", negate: true}},
		{"!^", filterTerm{text: "", negate: true, prefix: true}},
		{"!$", filterTerm{text: "", negate: true, suffix: true}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, parseFilterTerm(tt.input))
		})
	}
}

func TestMatchesTerm(t *testing.T) {
	tests := []struct {
		name string
		text string
		term filterTerm
		want bool
	}{
		{"contains", "hello world", filterTerm{text: "world"}, true},
		{"contains miss", "hello world", filterTerm{text: "xyz"}, false},
		{"case insensitive", "Hello World", filterTerm{text: "hello"}, true},
		{"case sensitive", "Hello World", filterTerm{text: "Hello", caseSensitive: true}, true},
		{
			"case sensitive miss",
			"hello world",
			filterTerm{text: "Hello", caseSensitive: true},
			false,
		},
		{"prefix", "hello world", filterTerm{text: "hello", prefix: true}, true},
		{"prefix miss", "hello world", filterTerm{text: "world", prefix: true}, false},
		{"suffix", "hello world", filterTerm{text: "world", suffix: true}, true},
		{"suffix miss", "hello world", filterTerm{text: "hello", suffix: true}, false},
		{"exact", "hello", filterTerm{text: "hello", prefix: true, suffix: true}, true},
		{"exact miss", "hello world", filterTerm{text: "hello", prefix: true, suffix: true}, false},
		{"negate", "hello world", filterTerm{text: "xyz", negate: true}, true},
		{"negate miss", "hello world", filterTerm{text: "hello", negate: true}, false},
		{
			"negate prefix",
			"hello world",
			filterTerm{text: "world", prefix: true, negate: true},
			true,
		},
		{
			"negate suffix",
			"hello world",
			filterTerm{text: "hello", suffix: true, negate: true},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, matchesTerm(tt.text, tt.term))
		})
	}
}

// --- Filter options overlay tests ---

func TestCurrentFilterValuesDefaultCLI(t *testing.T) {
	cli := testCLI()
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	// testCLI() Normalize sets NoBot=true (from Default.Bots=false),
	// so Bots is "hide" (index 1). Draft defaults to "show" (index 0), CI/Review default to "all",
	// and Archived defaults to "hide" (index 1).
	require.Equal(t, [6]int{0, 0, 1, 1, 3, 4}, vals)
}

func TestCurrentFilterValuesMapsStateCorrectly(t *testing.T) {
	cli := testCLI()
	cli.State = "merged"
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 2, vals[0]) // "merged" is index 2
}

func TestCurrentFilterValuesMapsCIFromAlias(t *testing.T) {
	cli := testCLI()
	cli.CI = "s" // alias for "success"
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 0, vals[4]) // "success" is index 0
}

func TestCurrentFilterValuesDraft(t *testing.T) {
	cli := testCLI()
	cli.Draft = new(false)
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 1, vals[1]) // "hide" is index 1
}

func TestCurrentFilterValuesDraftTrueMapsToShow(t *testing.T) {
	cli := testCLI()
	cli.Draft = new(true)
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 0, vals[1]) // "show" is index 0
}

func TestCurrentFilterValuesNoBots(t *testing.T) {
	cli := testCLI()
	cli.NoBot = true
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 1, vals[2]) // "hide" is index 1
}

func TestCurrentFilterValuesArchived(t *testing.T) {
	cli := testCLI()
	cli.Archived = true
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 0, vals[3]) // "show" is index 0
}

func TestUpdateOptionsOverlayNavigation(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
	}

	// Down from 0 → 1
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, filterRowDraft, bm.optionsCursor)

	// Up from 1 → 0
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, filterRowState, bm.optionsCursor)

	// Up from 0 → 0 (clamped)
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, filterRowState, bm.optionsCursor)
}

func TestUpdateOptionsOverlayChangeValue(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
	}

	// Right on state: 0→1 (open→closed)
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 1, bm.optionsValues[0])

	// Left back: 1→0 (closed→open)
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsValues[0])
}

func TestUpdateOptionsOverlaySpaceCyclesAndWraps(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
	}

	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 1, bm.optionsValues[0])

	bm.optionsValues[0] = len(filterOptionDefs[filterRowState].choices) - 1
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsValues[0])
}

func TestUpdateOptionsOverlayEscCancels(t *testing.T) {
	m := tuiModel{
		showOptions:   true,
		optionsValues: [6]int{2, 0, 0, 0, 0, 0},
		cli:           testCLI(),
	}

	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.showOptions)
	// CLI state should be unchanged (was "open" before overlay)
	require.Equal(t, valueOpen, bm.cli.State)
}

func TestUpdateOptionsOverlayAsteriskApplies(t *testing.T) {
	cfg := &Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueOpen,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
		},
	}
	cli := &CLI{}
	cli.Normalize(cfg)
	cli.stateExplicit = true
	cli.draftExplicit = true
	cli.noBotExplicit = true
	cli.archivedExplicit = true
	cli.ciExplicit = true
	cli.reviewExplicit = true

	m := tuiModel{
		showOptions: true,
		cli:         cli,
		cfg:         cfg,
	}

	model, cmd := m.updateOptionsOverlay(
		tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt, Text: "alt+o"},
	)
	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.showOptions)
	require.True(t, bm.refreshing)
}

func TestUpdateOptionsOverlayLockedRowsAreNoOps(t *testing.T) {
	cli := testCLI()
	cli.stateExplicit = true
	m := tuiModel{
		showOptions: true,
		cli:         cli,
	}

	// Right on locked row 0 → no change
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsValues[0])

	// Backspace on locked row → no change
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsValues[0])
}

func TestRenderOptionsOverlayLockedSelectionUsesSelectedStyle(t *testing.T) {
	cli := testCLI()
	cli.State = valueMerged
	cli.stateExplicit = true
	m := tuiModel{
		cli:           cli,
		styles:        newTuiStyles(),
		optionsCursor: filterRowDraft,
		optionsValues: [6]int{filterChoiceIndex(filterRowState, valueMerged)},
	}

	overlay := m.renderOptionsOverlay()

	require.Contains(
		t,
		overlay,
		lg.NewStyle().Bold(true).Foreground(lg.Color("218")).Render(valueMerged),
	)
	require.Contains(t, overlay, lg.NewStyle().Faint(true).Render("  (CLI)"))
}

func TestRenderOptionsOverlayHighlightsActiveRow(t *testing.T) {
	cli := testCLI()
	m := tuiModel{
		cli:           cli,
		styles:        newTuiStyles(),
		optionsCursor: filterRowDraft,
		optionsValues: tuiModel{cli: cli}.currentFilterValues(),
	}

	overlay := m.renderOptionsOverlay()

	require.Contains(t, overlay, cursorLineBG)
}

func TestRenderOptionsOverlayStylesDefaultChoices(t *testing.T) {
	cfg := &Config{
		Default: Defaults{
			State: valueOpen,
			Bots:  true,
		},
	}
	cli := &CLI{}
	cli.Normalize(cfg)

	m := tuiModel{
		cli:    cli,
		cfg:    cfg,
		styles: newTuiStyles(),
	}
	m.optionsCursor = filterRowDraft
	m.optionsValues = m.currentFilterValues()

	overlay := m.renderOptionsOverlay()

	require.Contains(
		t,
		overlay,
		lg.NewStyle().Bold(true).Foreground(lg.Color("218")).Render(valueOpen),
	)

	m.optionsReset[filterRowState] = true
	overlay = m.renderOptionsOverlay()

	require.Contains(
		t,
		overlay,
		lg.NewStyle().Bold(true).Foreground(lg.Color("218")).Render(valueOpen),
	)

	m.optionsValues[filterRowState] = filterChoiceIndex(filterRowState, valueClosed)
	m.optionsReset[filterRowState] = false
	overlay = m.renderOptionsOverlay()

	require.Contains(t, overlay, m.styles.defaultChoice.Render(valueOpen))
}

func TestUpdateOptionsOverlayResetSetsFirstChoice(t *testing.T) {
	m := tuiModel{
		showOptions:   true,
		optionsValues: [6]int{3, 0, 0, 0, 0, 0}, // state = "ready"
		cli:           testCLI(),
	}

	// Backspace resets to 0
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsValues[0])
}

func TestUpdateOptionsOverlayResetUsesConfigDefaults(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
		cfg: &Config{
			Default: Defaults{
				State: valueMerged,
				Bots:  false,
			},
		},
	}

	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsReset[filterRowState])
	require.Equal(
		t,
		filterChoiceIndex(filterRowState, valueMerged),
		bm.optionsValues[filterRowState],
	)

	bm.optionsCursor = filterRowBots
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsReset[filterRowBots])
	require.Equal(t, 1, bm.optionsValues[filterRowBots])

	bm.optionsCursor = filterRowDraft
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsReset[filterRowDraft])
	require.Equal(t, 0, bm.optionsValues[filterRowDraft])

	bm.optionsCursor = filterRowArchived
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsReset[filterRowArchived])
	require.Equal(t, 1, bm.optionsValues[filterRowArchived])
}

func TestActiveFilterTagsNoBotsFromTestCLI(t *testing.T) {
	// testCLI() Normalize sets NoBot=true from Default.Bots=false,
	// so "bots:hide" is always present.
	cli := testCLI()
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Equal(t, []string{"bots:hide"}, tags)
}

func TestActiveFilterTagsEmptyWhenAllDefaults(t *testing.T) {
	cli := &CLI{State: valueOpen}
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Empty(t, tags)
}

func TestActiveFilterTagsVariousFilters(t *testing.T) {
	cli := testCLI()
	cli.State = "merged"
	cli.Draft = new(false)
	cli.NoBot = true
	cli.CI = "success"
	cli.Review = "approved"
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Contains(t, tags, "state:merged")
	require.Contains(t, tags, "drafts:hide")
	require.Contains(t, tags, "bots:hide")
	require.Contains(t, tags, "ci:success")
	require.Contains(t, tags, "review:approved")
}

func TestActiveFilterTagsAbbreviatesCIFailure(t *testing.T) {
	cli := testCLI()
	cli.CI = "failure"
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Contains(t, tags, "ci:fail")
	require.NotContains(t, tags, "ci:failure")
}

func TestActiveFilterTagsNilCLI(t *testing.T) {
	m := tuiModel{}
	require.Nil(t, m.activeFilterTags())
}

func TestListViewportAccountsForFilterIndicator(t *testing.T) {
	// CLI with no active filter tags → no indicator line.
	cliClean := &CLI{State: valueOpen}
	m := tuiModel{
		height:      20,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		cli:         cliClean,
		p:           testPRL,
	}

	vpNoIndicator := m.listViewport()

	// Activate a filter → tags render inline on the separator, so viewport stays the same.
	cliClean.State = "merged"
	vpWithIndicator := m.listViewport()

	require.Equal(t, vpNoIndicator, vpWithIndicator)
}

func TestFilterGenStaleResultDiscarded(t *testing.T) {
	items := testModels("org")[:1]
	renderer := testPRL.newTableRenderer(testCLI(), true, 120, table.WithShowIndex(false))
	rt := renderer.Render(items)

	m := tuiModel{
		items:       items,
		rows:        rt.Rows,
		header:      rt.Header,
		colWidths:   rt.ColWidths,
		width:       120,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed:     make(prKeys),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
		filterGen:   2,
		autoRefresh: true, // needed for rescheduleRefresh to return non-nil
	}

	// Stale result with old filterGen should be discarded.
	model, cmd := m.Update(refreshResultMsg{
		items:     nil,
		rows:      nil,
		filterGen: 1,
	})

	require.NotNil(t, cmd) // rescheduleRefresh
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	// Items should not be cleared since the result was stale.
	require.Len(t, bm.items, len(items))
}

func TestStaleRefreshResultWithOldSpinnerIDKeepsRefreshing(t *testing.T) {
	m := tuiModel{
		refreshing: true,
		spinnerID:  2,
		filterGen:  2,
	}

	model, cmd := m.Update(refreshResultMsg{
		filterGen: 1,
		spinnerID: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.refreshing)
}

func TestRefreshResultCompletesSilently(t *testing.T) {
	items := testModels("org")[:1]
	renderer := testPRL.newTableRenderer(testCLI(), true, 120, table.WithShowIndex(false))
	rt := renderer.Render(items)

	m := tuiModel{
		width:       120,
		height:      12,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		removed:     make(prKeys),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
		refreshing:  true,
		spinnerID:   1,
		filterGen:   1,
	}

	model, cmd := m.Update(refreshResultMsg{
		items:     items,
		rows:      rt.Rows,
		filterGen: 1,
		spinnerID: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.refreshing)
	require.Empty(t, bm.statusMsg)
	require.Zero(t, bm.statusID)
}

func TestApplyTUIFilterDefaultsSetsNonExplicitFields(t *testing.T) {
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

	cfg := &Config{
		TUI: TUIConfig{
			Filters: TUIFiltersConfig{
				State: "merged",
				CI:    "success",
			},
		},
	}

	changed := applyTUIFilterDefaults(cli, cfg)

	require.True(t, changed)
	require.Equal(t, "merged", cli.State)
	require.Equal(t, "success", cli.CI)
}

func TestApplyTUIFilterDefaultsSkipsExplicitFields(t *testing.T) {
	cli := &CLI{
		State: "closed",
	}
	cli.stateExplicit = true
	cli.Normalize(&Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueOpen,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
		},
	})

	cfg := &Config{
		TUI: TUIConfig{
			Filters: TUIFiltersConfig{
				State: "merged",
			},
		},
	}

	changed := applyTUIFilterDefaults(cli, cfg)

	require.False(t, changed)
	require.Equal(t, "closed", cli.State) // not overridden
}

func TestApplyTUIFilterDefaultsIgnoresLegacyDraftTrue(t *testing.T) {
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

	cfg := &Config{
		TUI: TUIConfig{
			Filters: TUIFiltersConfig{
				Draft: new(true),
			},
		},
	}

	changed := applyTUIFilterDefaults(cli, cfg)

	require.False(t, changed)
	require.Nil(t, cli.Draft)
}

func TestApplyFilterOptionsResetClearsOverridesAndRestoresDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(t, os.WriteFile(cp, []byte(defaultConfigYAML), 0o600))

	cfg := &Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueMerged,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
			Bots:   false,
		},
	}

	cli := &CLI{}
	cli.Normalize(cfg)
	cli.State = valueClosed
	cli.NoBot = false
	cli.Archived = true

	m := tuiModel{
		cli:      cli,
		cfg:      cfg,
		styles:   newTuiStyles(),
		removed:  make(prKeys),
		selected: make(prKeys),
		p:        testPRL,
	}
	m.optionsReset[filterRowState] = true
	m.optionsReset[filterRowBots] = true
	m.optionsReset[filterRowArchived] = true

	model, cmd := m.applyFilterOptions()
	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, valueMerged, bm.cli.State)
	require.True(t, bm.cli.NoBot)
	require.False(t, bm.cli.Archived)

	loaded, err := loadConfig()
	require.NoError(t, err)
	require.Empty(t, loaded.TUI.Filters.State)
	require.Nil(t, loaded.TUI.Filters.Bots)
	require.Nil(t, loaded.TUI.Filters.Archived)
}

func testReviewPullRequest() PullRequest {
	return PullRequest{
		Number: 42,
		Repository: Repository{
			Name:          "prl",
			NameWithOwner: "gechr/prl",
		},
		URL: "https://github.com/gechr/prl/pull/42",
	}
}
