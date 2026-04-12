package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/filter"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/picker"
	"github.com/gechr/primer/scrollbar"
	"github.com/gechr/primer/scrollwheel"
	"github.com/gechr/primer/table"
	"github.com/stretchr/testify/require"
)

func TestRenderConfirmOptionsHeaderStyleOmitsCaret(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)

	rendered := m.confirmOptionsHeader()
	stripped := ansi.Strip(rendered)

	require.Equal(
		t,
		`Provider
claude  codex  gemini

Model
sonnet  opus

Effort
low  medium  high  max  auto

`,
		stripped,
	)
	require.Equal(t, 0, strings.Count(rendered, cursorLineBG))
	require.Contains(t, rendered, styleTitle.Bold(true).Render(claudeReviewModelSonnet))
	require.Contains(t, rendered, styleTitle.Bold(true).Render(claudeReviewEffortMedium))
}

func TestShellSingleQuoteEscapesSingleQuotes(t *testing.T) {
	require.Equal(t, `'it'"'"'s fine'`, shellescape.Quote("it's fine"))
}

func TestUpdateListViewAltRBypassesConfirm(t *testing.T) {
	if !isDarwin() {
		t.Skip("AI review requires macOS")
	}

	t.Setenv("TERM_PROGRAM", "ghostty")
	pr := testReviewPullRequest()
	m := tuiModel{
		items:        []PRRowModel{{PR: pr}},
		rows:         []TableRow{{Item: PRRowModel{PR: pr}}},
		removed:      make(prKeys),
		selected:     make(prKeys),
		confirmInput: newConfirmInput(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, "review", bm.confirmAction)
	require.True(t, bm.confirmState.OptFocus)
	require.False(t, bm.confirmInput.Focused())

	model, cmd = m.updateListView(tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	require.NotNil(t, cmd)
	altModel, ok := model.(tuiModel)
	require.True(t, ok)

	require.Empty(t, altModel.confirmAction)
	require.Empty(t, altModel.confirmPrompt)
	require.Nil(t, altModel.confirmCmd)
}

func TestUpdateListViewCtrlRSinglePRBypassesConfirm(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{
		items:        []PRRowModel{{PR: pr}},
		rows:         []TableRow{{Item: PRRowModel{PR: pr}}},
		removed:      make(prKeys),
		selected:     make(prKeys),
		confirmInput: newConfirmInput(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Empty(t, bm.confirmAction)
	require.Empty(t, bm.confirmPrompt)
	require.Nil(t, bm.confirmCmd)
}

func TestUpdateListViewCtrlRMultiplePRsRequiresConfirm(t *testing.T) {
	prA := testReviewPullRequest()
	prB := testReviewPullRequest()
	prB.Number = 43
	prB.URL = "https://github.com/owner/repo/pull/43"
	m := tuiModel{
		items: []PRRowModel{{PR: prA}, {PR: prB}},
		rows: []TableRow{
			{Item: PRRowModel{PR: prA}},
			{Item: PRRowModel{PR: prB}},
		},
		removed:  make(prKeys),
		selected: prKeys{makePRKey(prA): true, makePRKey(prB): true},
		styles:   newTuiStyles(),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.Nil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiActionCopilotReview, bm.confirmAction)
	require.Equal(
		t,
		"Request Copilot review for 2 PRs?",
		bm.confirmPrompt,
	)
	require.Equal(t, "2 PRs", bm.confirmSubject)
	require.NotNil(t, bm.confirmCmd)
	require.True(t, bm.confirmState.Yes)
}

func TestRenderHelpOverlayIncludesAltRReviewShortcut(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := tuiModel{styles: newTuiStyles()}

	overlay := m.renderHelpOverlay()

	if isDarwin() {
		require.Contains(t, overlay, "Launch AI review")
		require.Contains(t, overlay, "alt+r")
		require.Contains(t, overlay, "Launch AI review (no confirm)")
	} else {
		require.NotContains(t, overlay, "Launch AI review")
	}
	require.Contains(t, overlay, "shift+↑↓")
}

func TestInlineHelpKeyEmbedsModifiedSingleLetterShortcut(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}

	rendered, ok := key.Inline(
		"alt+c",
		"copy",
		m.styles.helpKey,
		m.styles.helpText,
	)

	require.True(t, ok)
	require.Equal(
		t,
		m.styles.helpKey.Render("alt+")+
			m.styles.helpKey.Render("c")+
			m.styles.helpText.Render("opy"),
		rendered,
	)
	require.Equal(t, "alt+copy", ansi.Strip(rendered))
}

func TestInlineHelpKeyEmbedsModifiedCtrlShiftChord(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}

	rendered, ok := key.Inline(
		"ctrl+shift+t",
		"toggle",
		m.styles.helpKey,
		m.styles.helpText,
	)

	require.True(t, ok)
	require.Equal(
		t,
		m.styles.helpKey.Render("ctrl+shift+")+
			m.styles.helpKey.Render("t")+
			m.styles.helpText.Render("oggle"),
		rendered,
	)
	require.Equal(t, "ctrl+shift+toggle", ansi.Strip(rendered))
}

func TestUpdateConfirmOverlaySwitchingProviderUpdatesModelChoices(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)

	bm := m

	model, cmd := bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Nil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, string(reviewProviderCodex), bm.selectedConfirmOptionValue(0))
	require.Equal(t, defaultReviewModel(nil, reviewProviderCodex), bm.selectedConfirmOptionValue(1))
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewModel54, value: codexReviewModel54},
			{label: codexReviewModel54Mini, value: codexReviewModel54Mini},
			{label: codexReviewModel53Codex, value: codexReviewModel53Codex},
		},
		bm.confirmOptions[1].choices,
	)
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
		},
		bm.confirmOptions[2].choices,
	)
}

func TestUpdateConfirmOverlaySwitchingToGeminiShowsEffortImmediately(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)

	bm := m
	model, cmd := bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)

	require.Equal(t, string(reviewProviderGemini), bm.selectedConfirmOptionValue(0))
	require.Equal(
		t,
		defaultReviewModel(nil, reviewProviderGemini),
		bm.selectedConfirmOptionValue(1),
	)
	require.Len(t, bm.confirmOptions, 3)
	require.Equal(t, reviewEffortOptionLabel, bm.confirmOptions[2].label)
	require.NotEmpty(t, bm.confirmOptions[2].choices)
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		bm.confirmOptions[2].choices,
	)
}

func TestRenderConfirmOptionsHighlightsActiveRowInGreen(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)
	m.confirmState.OptFocus = true
	m.confirmState.OptCursor = 1

	rendered := m.confirmOptionsHeader()

	require.NotContains(t, rendered, cursorLineBG)
	require.Contains(t, rendered, m.styles.helpKey.Render("Model"))
	require.Contains(t, rendered, styleHighlight.Bold(true).Render(claudeReviewModelSonnet))
	require.Contains(t, rendered, styleHighlight.Faint(true).Render(claudeReviewModelOpus))
}

func TestUpdateConfirmOverlayTabLoopsAcrossOptions(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)

	model, cmd := m.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Nil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 1, bm.confirmState.OptCursor)

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Nil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 2, bm.confirmState.OptCursor)

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyTab})
	require.NotNil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.confirmState.OptFocus)
	require.True(t, bm.confirmInput.Focused())

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Nil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.confirmState.OptFocus)
	require.Equal(t, 0, bm.confirmState.OptCursor)
}

func TestUpdateConfirmOverlayUpDownCanFocusPrompt(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)

	model, cmd := m.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyUp})
	require.NotNil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.confirmState.OptFocus)
	require.True(t, bm.confirmInput.Focused())

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.confirmState.OptFocus)
	require.True(t, bm.confirmInput.Focused())

	bm = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)
	bm.confirmState.OptFocus = true
	bm.confirmInput.Blur()
	bm.confirmState.OptCursor = len(bm.confirmOptions) - 1
	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyDown})
	require.NotNil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.confirmState.OptFocus)
	require.True(t, bm.confirmInput.Focused())
}

func TestUpdateConfirmOverlayPromptDoesNotExitOnArrowKeys(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)
	m, cmd := m.focusConfirmInput()
	require.NotNil(t, cmd)

	model, cmd := m.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyUp})
	require.NotNil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.confirmState.OptFocus)
	require.True(t, bm.confirmInput.Focused())

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyDown})
	require.NotNil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.confirmState.OptFocus)
	require.True(t, bm.confirmInput.Focused())
}

func TestRenderHelpOverlayAlignsExtendedSelectionKey(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}

	overlay := ansi.Strip(m.renderHelpOverlay())
	lines := strings.Split(overlay, nl)

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
	models := testModels("owner")
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
	items := testModels("owner")[:1]
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
	lines := strings.Split(out, nl)

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
	lines := strings.Split(out, nl)

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
		autoRefresh:     true,
		refreshID:       1,
		view:            tuiViewDetail,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now(),
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

func TestExitDetailViewReschedulesWhenRefreshNotDue(t *testing.T) {
	pr := testReviewPullRequest()
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detailLines:     []string{"line"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now(),
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.Equal(t, prKey(""), bm.detailKey)
	require.False(t, bm.refreshing)
	require.Equal(t, 1, bm.detailRefreshID)
	require.Equal(t, 1, bm.refreshID)
}

func TestExitDetailViewStartsImmediateRefreshWhenDue(t *testing.T) {
	pr := testReviewPullRequest()
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detailLines:     []string{"line"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now().Add(-2 * watchMaxInterval),
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.Equal(t, prKey(""), bm.detailKey)
	require.True(t, bm.refreshing)
	require.Equal(t, 1, bm.detailRefreshID)
	require.Equal(t, 1, bm.refreshID)
}

func TestStaleDetailRefreshCompletionIgnoredAfterExitAndReopen(t *testing.T) {
	pr := testReviewPullRequest()
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detail:          PRDetail{Checks: []PRCheck{{Name: "build", Status: ciStatusPending}}},
		detailLines:     []string{"old"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now(),
	}

	model, _ := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})
	bm, ok := model.(tuiModel)
	require.True(t, ok)

	bm.view = tuiViewDetail
	bm.detailKey = key
	bm.detail = PRDetail{Checks: []PRCheck{{Name: "build", Status: ciStatusPending}}}
	bm.detailLines = []string{"old"}

	model, cmd := bm.Update(detailChecksRefreshedMsg{
		id:  0,
		key: key,
		checks: []PRCheck{{
			Name:       "build",
			Status:     ciStatusCompleted,
			Conclusion: ciStatusSuccess,
		}},
	})

	require.Nil(t, cmd)
	after, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, ciStatusPending, after.detail.Checks[0].Status)
}

func TestDetailApproveUsesImmediateRefreshPathWhenDue(t *testing.T) {
	pr := testReviewPullRequest()
	pr.State = valueOpen
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detailLines:     []string{"line"},
		actions:         &ActionRunner{},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now().Add(-2 * watchMaxInterval),
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: 'a', Text: "a"})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.Equal(t, tuiActionApprove, bm.confirmAction)
	require.True(t, bm.refreshing)
	require.Equal(t, prKey(""), bm.detailKey)
}

func TestExitDiffViewStartsImmediateRefreshWhenDue(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDiff,
		diffKey:         makePRKey(pr),
		diffLines:       []string{"diff"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now().Add(-2 * watchMaxInterval),
	}

	model, cmd := m.updateDiffView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.True(t, bm.refreshing)
	require.Equal(t, prKey(""), bm.diffKey)
}

func TestRenderDetailContentShowsCopilotReviewIcon(t *testing.T) {
	pr := testReviewPullRequest()
	pr.Author.Login = "alice"
	resolver := NewAuthorResolver(&Config{})
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		detailKey: makePRKey(pr),
		detail: PRDetail{
			Reviews: []PRReview{{
				User:  copilotReviewer,
				State: "COMMENTED",
			}},
		},
		resolver: resolver,
		width:    80,
	}

	lines := m.renderDetailContent()
	rendered := strings.Join(lines, nl)

	stripped := ansi.Strip(rendered)
	require.Contains(t, stripped, "🤖 Copilot")
	require.NotContains(t, stripped, "💬 Copilot")
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
		diffView:  newScrollView(),
		view:      tuiViewDiff,
		height:    12,
		width:     250,
		styles:    newTuiStyles(),
	}
	m.syncDiffView()

	vpHeight := m.diffView.Height()
	topPct := int(math.Round(m.diffView.ScrollPercent() * 100))
	topStatus := fmt.Sprintf("1-%d/%d (%d%%)", vpHeight, len(diffLines), topPct)
	require.Equal(t, 1, strings.Count(ansi.Strip(m.viewDiff().Content), topStatus))

	model, cmd := m.updateDiffView(tea.KeyPressMsg{Code: 'G', Text: "G"})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, len(diffLines)-vpHeight, bm.diffView.YOffset())
	offset := bm.diffView.YOffset()
	bottomStatus := fmt.Sprintf("%d-%d/%d (100%%)", offset+1, len(diffLines), len(diffLines))
	require.Equal(t, 1, strings.Count(ansi.Strip(bm.viewDiff().Content), bottomStatus))
}

func TestWrapDiffLinesCreatesStandaloneANSIWrappedRows(t *testing.T) {
	line := styleDanger.Render("+abcdef")

	rows := layout.WrapLines(line, 4)

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
	diff := styleDanger.Render("+abcdef")
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diff:      diff,
		diffKey:   makePRKey(pr),
		diffLines: layout.WrapLines(diff, 4),
		diffView:  newScrollView(),
		view:      tuiViewDiff,
		height:    8,
		width:     4,
		styles:    newTuiStyles(),
		p:         testPRL,
		cli:       testCLI(),
	}
	m.syncDiffView()
	m.diffView.ScrollDown(1)

	model, cmd := m.Update(tea.WindowSizeMsg{Width: 8, Height: 8})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, []string{"+abcdef"}, []string{ansi.Strip(bm.diffLines[0])})
	require.Len(t, bm.diffLines, 1)
	require.Zero(t, bm.diffView.YOffset())
}

func TestViewDiffShowsWrappedContinuationRows(t *testing.T) {
	pr := testReviewPullRequest()
	diff := styleDanger.Render("+" + strings.Repeat("a", 85))
	width := 80
	diffLines := layout.WrapLines(diff, width-tuiScrollbarWidth)
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diff:      diff,
		diffKey:   makePRKey(pr),
		diffLines: diffLines,
		diffView:  newScrollView(),
		height:    8,
		width:     width,
		styles:    newTuiStyles(),
	}
	m.syncDiffView()

	out := ansi.Strip(m.viewDiff().Content)
	lines := strings.Split(out, nl)
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	out = strings.Join(lines, nl)

	require.Contains(t, out, strings.Join([]string{
		ansi.Strip(diffLines[0]),
		ansi.Strip(diffLines[1]),
	}, nl))
}

func TestViewDiffFillsTerminalRectangle(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diffKey:   makePRKey(pr),
		diffLines: []string{"+small"},
		diffView:  newScrollView(),
		height:    8,
		width:     120,
		styles:    newTuiStyles(),
	}
	m.syncDiffView()

	assertRenderedFullScreen(t, m.viewDiff().Content, m.width, m.height)
}

func TestViewDiffEnablesMouseTracking(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diffKey:   makePRKey(pr),
		diffLines: []string{"+small"},
		diffView:  newScrollView(),
		height:    8,
		width:     120,
		styles:    newTuiStyles(),
	}
	m.syncDiffView()

	require.Equal(t, tea.MouseModeCellMotion, m.viewDiff().MouseMode)
}

func TestViewDetailFillsTerminalRectangle(t *testing.T) {
	m := tuiModel{
		detailLines: []string{"title", "body"},
		detailView:  newScrollView(),
		height:      8,
		width:       120,
		styles:      newTuiStyles(),
	}
	m.syncDetailView()

	assertRenderedFullScreen(t, m.viewDetail().Content, m.width, m.height)
}

func TestViewDetailEnablesMouseTracking(t *testing.T) {
	m := tuiModel{
		detailLines: []string{"title", "body"},
		detailView:  newScrollView(),
		height:      8,
		width:       120,
		styles:      newTuiStyles(),
	}
	m.syncDetailView()

	require.Equal(t, tea.MouseModeCellMotion, m.viewDetail().MouseMode)
}

func TestAppendRightStatusDoesNotIncreaseFooterLineCount(t *testing.T) {
	m := tuiModel{width: 24}
	help := " ↑↓ scroll\nc comment"
	status := "Diffing owner/repo#42…"

	got := m.appendRightStatus(help, status)

	require.Equal(t, strings.Count(help, nl), strings.Count(got, nl))
	lines := strings.Split(ansi.Strip(got), nl)
	require.Len(t, lines, 2)
	require.LessOrEqual(t, lg.Width(lines[1]), m.width)
	require.Contains(t, lines[1], "Diffing")
}

func TestAppendRightStatusTruncatesLongStatusToSingleLine(t *testing.T) {
	m := tuiModel{width: 10}

	got := ansi.Strip(m.appendRightStatus("", "Diffing owner/repo#42…"))

	require.NotContains(t, got, nl)
	require.LessOrEqual(t, lg.Width(got), m.width)
	require.Contains(t, got, "…")
}

func TestAppendRightStatusUsesLeftPaddingForExactFit(t *testing.T) {
	m := tuiModel{width: 21}

	got := ansi.Strip(m.appendRightStatus("", "Diffing foo/bar#123…"))

	require.Equal(t, "Diffing foo/bar#123…", got)
}

func TestViewListDiffingStatusDoesNotAddFooterLines(t *testing.T) {
	for width := 20; width <= 120; width++ {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			base := tuiModel{
				header: "TITLE",
				rows: []TableRow{{
					Item:    PRRowModel{PR: testReviewPullRequest()},
					Display: "demo row",
				}},
				height:      8,
				width:       width,
				styles:      newTuiStyles(),
				filterInput: textinput.New(),
				removed:     make(prKeys),
				selected:    make(prKeys),
				p:           testPRL,
				cli:         testCLI(),
			}

			withStatus := base
			withStatus.flash.Msg = withStatus.styles.statusPending.Render(statusDiffing) +
				" " + styleRef.Render("foo/bar#123") + valueEllipsis

			baseLines := strings.Split(ansi.Strip(base.viewList().Content), nl)
			lines := strings.Split(ansi.Strip(withStatus.viewList().Content), nl)
			require.Len(t, lines, len(baseLines))
			for _, line := range lines {
				require.LessOrEqual(t, lg.Width(line), withStatus.width)
			}
		})
	}
}

func TestSyncDiffViewCachesNormalizedRenderLines(t *testing.T) {
	m := tuiModel{
		diffLines: []string{
			"left\tright",
			strings.Repeat("x", 40),
		},
		diffView: newScrollView(),
		height:   6,
		width:    20,
		styles:   newTuiStyles(),
	}

	m.syncDiffView()

	require.Len(t, m.diffRenderLines, 2)
	require.NotContains(t, ansi.Strip(m.diffRenderLines[0]), "\t")
	require.Contains(t, ansi.Strip(m.diffRenderLines[0]), "left    right")
	require.Equal(t, 19, ansi.WcWidth.StringWidth(m.diffRenderLines[0]))
	require.Equal(t, 19, ansi.WcWidth.StringWidth(m.diffRenderLines[1]))
	require.Equal(t, len(m.diffRenderLines), m.diffView.TotalLineCount())
}

func TestRenderViewportContentUsesCachedLinesWithScrollbar(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}
	lines := []string{
		layout.NormalizeLine("line 1", 10),
		layout.NormalizeLine("line 2", 10),
		layout.NormalizeLine("line 3", 10),
		layout.NormalizeLine("line 4", 10),
	}
	vp := newScrollView()
	vp.SetWidth(10)
	vp.SetHeight(3)
	vp.SetContentLines(lines)
	vp.SetYOffset(1)

	got := ansi.Strip(m.renderViewportContent(lines, vp, true))
	rows := strings.Split(got, nl)

	require.Len(t, rows, 3)
	require.Contains(t, rows[0], "line 2")
	require.Contains(t, rows[1], "line 3")
	require.Contains(t, rows[2], "line 4")
	for _, row := range rows {
		require.Equal(t, 11, ansi.WcWidth.StringWidth(row))
	}
}

func TestTUIWheelFilterCoalescesMouseWheelInput(t *testing.T) {
	ch := make(chan tea.Msg, 1)
	resolve := func(m tea.Model) (wheelTarget, bool) {
		tui, ok := m.(tuiModel)
		if !ok {
			return wheelTargetNone, false
		}
		return tui.wheelScrollTarget()
	}
	sw := scrollwheel.New(resolve, func(msg tea.Msg) { ch <- msg },
		scrollwheel.WithDelay(time.Millisecond))
	defer sw.Stop()

	model := tuiModel{view: tuiViewDiff}

	require.Nil(t, sw.Filter(model, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown})))
	require.Nil(t, sw.Filter(model, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown})))

	select {
	case msg := <-ch:
		wm, ok := msg.(scrollwheel.Msg[wheelTarget])
		require.True(t, ok)
		require.Equal(t, wheelTargetDiff, wm.Target)
		require.Equal(t, 2, wm.Delta)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for coalesced wheel message")
	}
}

func TestTUIWheelFilterPassesNonWheelMessages(t *testing.T) {
	resolve := func(m tea.Model) (wheelTarget, bool) {
		tui, ok := m.(tuiModel)
		if !ok {
			return wheelTargetNone, false
		}
		return tui.wheelScrollTarget()
	}
	sw := scrollwheel.New(resolve, nil, scrollwheel.WithDelay(time.Millisecond))
	defer sw.Stop()

	model := tuiModel{view: tuiViewDiff}
	key := tea.KeyPressMsg{}

	got := sw.Filter(model, key)

	require.Equal(t, key, got)
}

func TestScrollbarTrackClickJumpsDiffViewport(t *testing.T) {
	pr := testReviewPullRequest()
	diffLines := make([]string, 60)
	for i := range diffLines {
		diffLines[i] = fmt.Sprintf("line %d", i)
	}
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diffKey:   makePRKey(pr),
		diffLines: diffLines,
		diffView:  newScrollView(),
		view:      tuiViewDiff,
		height:    14,
		width:     80,
		styles:    newTuiStyles(),
	}
	m.syncDiffView()

	hitbox, ok := m.scrollbarHitbox(scrollbarTargetDiff)
	require.True(t, ok)

	require.True(t, m.handleScrollbarPress(tea.Mouse{
		X:      hitbox.X,
		Y:      hitbox.Y + hitbox.Height - 1,
		Button: tea.MouseLeft,
	}))

	require.True(t, m.scrollDrag.Active)
	require.Equal(t, scrollbarTargetDiff, m.scrollDrag.target)
	require.Positive(t, m.diffView.YOffset())
	require.GreaterOrEqual(t, m.diffView.ScrollPercent(), 0.9)
}

func TestScrollbarThumbDragMovesDiffViewport(t *testing.T) {
	pr := testReviewPullRequest()
	diffLines := make([]string, 60)
	for i := range diffLines {
		diffLines[i] = fmt.Sprintf("line %d", i)
	}
	m := tuiModel{
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diffKey:   makePRKey(pr),
		diffLines: diffLines,
		diffView:  newScrollView(),
		view:      tuiViewDiff,
		height:    14,
		width:     80,
		styles:    newTuiStyles(),
	}
	m.syncDiffView()
	m.diffView.SetYOffset(m.diffView.Height())

	hitbox, ok := m.scrollbarHitbox(scrollbarTargetDiff)
	require.True(t, ok)
	thumbPos, _ := scrollbar.ThumbMetrics(
		hitbox.Height,
		hitbox.TotalLines,
		m.diffView.ScrollPercent(),
	)
	pressY := hitbox.Y + thumbPos

	require.True(t, m.handleScrollbarPress(tea.Mouse{
		X:      hitbox.X,
		Y:      pressY,
		Button: tea.MouseLeft,
	}))

	initialOffset := m.diffView.YOffset()
	require.True(t, m.handleScrollbarMotion(tea.Mouse{
		X:      hitbox.X,
		Y:      hitbox.Y + hitbox.Height - 1,
		Button: tea.MouseLeft,
	}))

	require.Greater(t, m.diffView.YOffset(), initialOffset)
	require.GreaterOrEqual(t, m.diffView.ScrollPercent(), 0.9)

	model, cmd := m.Update(tea.MouseReleaseMsg(tea.Mouse{
		X:      hitbox.X,
		Y:      hitbox.Y + hitbox.Height - 1,
		Button: tea.MouseLeft,
	}))
	require.Nil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.scrollDrag.Active)
}

func TestScrollbarTrackClickJumpsConfirmViewport(t *testing.T) {
	m := tuiModel{
		confirmAction: tuiActionInfo,
		confirmPrompt: strings.TrimSuffix(strings.Repeat("line\n", 40), nl),
		confirmView:   newScrollViewSoftWrap(),
		confirmInput:  newConfirmInput(),
		width:         80,
		height:        18,
		styles:        newTuiStyles(),
	}
	m.syncConfirmView()

	hitbox, ok := m.scrollbarHitbox(scrollbarTargetConfirm)
	require.True(t, ok)

	require.True(t, m.handleScrollbarPress(tea.Mouse{
		X:      hitbox.X,
		Y:      hitbox.Y + hitbox.Height - 1,
		Button: tea.MouseLeft,
	}))

	require.Positive(t, m.confirmView.YOffset())
	require.GreaterOrEqual(t, m.confirmView.ScrollPercent(), 0.9)
}

func TestWrapDiffLinesExpandsTabs(t *testing.T) {
	rows := layout.WrapLines("a\tb", 80)

	require.Len(t, rows, 1)
	require.NotContains(t, rows[0], "\t")
	require.Equal(t, "a    b", ansi.Strip(rows[0]))
}

func TestSyncDetailViewExpandsTabs(t *testing.T) {
	m := tuiModel{
		detailLines: []string{"left\tright"},
		detailView:  newScrollView(),
		height:      5,
		width:       20,
		styles:      newTuiStyles(),
	}

	m.syncDetailView()

	out := ansi.Strip(m.detailView.View())
	require.NotContains(t, out, "\t")
	require.Contains(t, out, "left    right")
}

func TestFillViewToTerminalExpandsTabs(t *testing.T) {
	m := tuiModel{width: 12, height: 2}

	got := layout.Fill("left\tright", m.width, m.height)

	require.NotContains(t, got, "\t")
	lines := strings.Split(got, nl)
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "left    right")
	require.Equal(t, 12, lg.Width(lines[1]))
}

func assertRenderedFullScreen(t *testing.T, content string, width, height int) {
	t.Helper()

	lines := strings.Split(ansi.Strip(content), nl)
	require.Len(t, lines, height)
	for _, line := range lines {
		require.LessOrEqual(t, lg.Width(line), width)
	}
}

func TestActionMsgRemovalRecomputesOffset(t *testing.T) {
	prs := []PullRequest{
		testReviewPullRequest(),
		{Number: 43, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
		{Number: 44, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
		{Number: 45, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
		{Number: 46, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
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
			Name:          "repo",
			NameWithOwner: "owner/repo",
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
						Name:          "repo",
						NameWithOwner: "owner/repo",
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
		want  filter.Term
	}{
		{"foo", filter.Term{Text: "foo"}},
		{"^foo", filter.Term{Text: "foo", Prefix: true}},
		{"foo$", filter.Term{Text: "foo", Suffix: true}},
		{"^foo$", filter.Term{Text: "foo", Prefix: true, Suffix: true}},
		{"!foo", filter.Term{Text: "foo", Negate: true}},
		{"!^foo", filter.Term{Text: "foo", Negate: true, Prefix: true}},
		{"!foo$", filter.Term{Text: "foo", Negate: true, Suffix: true}},
		{"!^foo$", filter.Term{Text: "foo", Negate: true, Prefix: true, Suffix: true}},
		{"Foo", filter.Term{Text: "Foo", Case: filter.CaseSensitive}},
		// Bare modifiers: flags set but empty text matches everything.
		{"^", filter.Term{Text: "", Prefix: true}},
		{"$", filter.Term{Text: "", Suffix: true}},
		{"!", filter.Term{Text: "", Negate: true}},
		{"!^", filter.Term{Text: "", Negate: true, Prefix: true}},
		{"!$", filter.Term{Text: "", Negate: true, Suffix: true}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, filter.Parse(tt.input))
		})
	}
}

func TestMatchesTerm(t *testing.T) {
	tests := []struct {
		name string
		text string
		term filter.Term
		want bool
	}{
		{"contains", "hello world", filter.Term{Text: "world"}, true},
		{"contains miss", "hello world", filter.Term{Text: "xyz"}, false},
		{"case insensitive", "Hello World", filter.Term{Text: "hello"}, true},
		{
			"case sensitive",
			"Hello World",
			filter.Term{Text: "Hello", Case: filter.CaseSensitive},
			true,
		},
		{
			"case sensitive miss",
			"hello world",
			filter.Term{Text: "Hello", Case: filter.CaseSensitive},
			false,
		},
		{"prefix", "hello world", filter.Term{Text: "hello", Prefix: true}, true},
		{"prefix miss", "hello world", filter.Term{Text: "world", Prefix: true}, false},
		{"suffix", "hello world", filter.Term{Text: "world", Suffix: true}, true},
		{"suffix miss", "hello world", filter.Term{Text: "hello", Suffix: true}, false},
		{"exact", "hello", filter.Term{Text: "hello", Prefix: true, Suffix: true}, true},
		{
			"exact miss",
			"hello world",
			filter.Term{Text: "hello", Prefix: true, Suffix: true},
			false,
		},
		{"negate", "hello world", filter.Term{Text: "xyz", Negate: true}, true},
		{"negate miss", "hello world", filter.Term{Text: "hello", Negate: true}, false},
		{
			"negate prefix",
			"hello world",
			filter.Term{Text: "world", Prefix: true, Negate: true},
			true,
		},
		{
			"negate suffix",
			"hello world",
			filter.Term{Text: "hello", Suffix: true, Negate: true},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.term.Match(tt.text))
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
	require.Equal(t, []int{0, 0, 1, 1, 3, 4}, vals)
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
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()

	// Down from 0 → 1
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, int(filterRowDraft), bm.optionsPicker.Cursor)

	// Up from 1 → 0
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, int(filterRowState), bm.optionsPicker.Cursor)

	// Up from 0 → 0 (clamped)
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, int(filterRowState), bm.optionsPicker.Cursor)
}

func TestUpdateOptionsOverlayChangeValue(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()

	// Right on state: 0→1 (open→closed)
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 1, bm.optionsPicker.Values[0])

	// Left back: 1→0 (closed→open)
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsPicker.Values[0])
}

func TestUpdateOptionsOverlaySpaceCyclesAndWraps(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()

	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 1, bm.optionsPicker.Values[0])

	bm.optionsPicker.Values[0] = len(filterOptionDefs[filterRowState].choices) - 1
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsPicker.Values[0])
}

func TestUpdateOptionsOverlayEscCancels(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Values[0] = 2

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
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()

	model, cmd := m.updateOptionsOverlay(
		tea.KeyPressMsg{Code: 'O', Text: "O"},
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
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()

	// Right on locked row 0 → no change
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsPicker.Values[0])

	// Backspace on locked row → no change
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsPicker.Values[0])
}

func TestRenderOptionsOverlayLockedSelectionUsesSelectedStyle(t *testing.T) {
	cli := testCLI()
	cli.State = valueMerged
	cli.stateExplicit = true
	m := tuiModel{
		cli:    cli,
		styles: newTuiStyles(),
		optionsPicker: picker.Model{
			Cursor:  int(filterRowDraft),
			Values:  []int{filterChoiceIndex(filterRowState, valueMerged), 0, 0, 0, 0, 0},
			IsReset: make([]bool, 6),
		},
	}
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Cursor = int(filterRowDraft)
	m.optionsPicker.Values[0] = filterChoiceIndex(filterRowState, valueMerged)

	overlay := m.renderOptionsOverlay()

	require.Contains(
		t,
		overlay,
		styleTitle.Bold(true).Render(valueMerged),
	)
	require.Contains(t, overlay, lg.NewStyle().Faint(true).Render("  (CLI)"))
}

func TestRenderOptionsOverlayHighlightsActiveRow(t *testing.T) {
	cli := testCLI()
	m := tuiModel{
		cli:    cli,
		styles: newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Cursor = int(filterRowDraft)

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
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Cursor = int(filterRowDraft)

	overlay := m.renderOptionsOverlay()

	require.Contains(
		t,
		overlay,
		styleTitle.Bold(true).Render(valueOpen),
	)

	m.optionsPicker.IsReset[filterRowState] = true
	overlay = m.renderOptionsOverlay()

	require.Contains(
		t,
		overlay,
		styleTitle.Bold(true).Render(valueOpen),
	)

	m.optionsPicker.Values[filterRowState] = filterChoiceIndex(filterRowState, valueClosed)
	m.optionsPicker.IsReset[filterRowState] = false
	overlay = m.renderOptionsOverlay()

	require.Contains(t, overlay, m.styles.defaultChoice.Render(valueOpen))
}

func TestUpdateOptionsOverlayResetSetsFirstChoice(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Values[0] = 3

	// Backspace resets to 0
	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 0, bm.optionsPicker.Values[0])
}

func TestUpdateOptionsOverlayResetUsesConfigDefaults(t *testing.T) {
	m := tuiModel{
		showOptions: true,
		cli:         testCLI(),
		styles:      newTuiStyles(),
		cfg: &Config{
			Default: Defaults{
				State: valueMerged,
				Bots:  false,
			},
		},
	}
	m.optionsPicker = m.newFilterPicker()

	model, cmd := m.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsPicker.IsReset[filterRowState])
	require.Equal(
		t,
		filterChoiceIndex(filterRowState, valueMerged),
		bm.optionsPicker.Values[filterRowState],
	)

	bm.optionsPicker.Cursor = int(filterRowBots)
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsPicker.IsReset[filterRowBots])
	require.Equal(t, 1, bm.optionsPicker.Values[filterRowBots])

	bm.optionsPicker.Cursor = int(filterRowDraft)
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsPicker.IsReset[filterRowDraft])
	require.Equal(t, 0, bm.optionsPicker.Values[filterRowDraft])

	bm.optionsPicker.Cursor = int(filterRowArchived)
	model, cmd = bm.updateOptionsOverlay(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.optionsPicker.IsReset[filterRowArchived])
	require.Equal(t, 1, bm.optionsPicker.Values[filterRowArchived])
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

func TestStaleRefreshResultWithOldQueryGenDiscarded(t *testing.T) {
	items := testModels("owner")[:1]
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
		queryGen:    2,
	}

	// Stale result with old queryGen should be discarded.
	model, cmd := m.Update(refreshResultMsg{
		items:    nil,
		rows:     nil,
		queryGen: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	// Items should not be cleared since the result was stale.
	require.Len(t, bm.items, len(items))
}

func TestStaleRefreshResultWithOldQueryGenKeepsRefreshing(t *testing.T) {
	m := tuiModel{
		refreshing: true,
		queryGen:   2,
	}

	model, cmd := m.Update(refreshResultMsg{
		queryGen: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.refreshing)
}

func TestRefreshResultCompletesSilently(t *testing.T) {
	items := testModels("owner")[:1]
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
		queryGen:    1,
	}

	model, cmd := m.Update(refreshResultMsg{
		items:    items,
		rows:     rt.Rows,
		queryGen: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.refreshing)
	require.False(t, bm.flash.Active())
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
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.IsReset[filterRowState] = true
	m.optionsPicker.IsReset[filterRowBots] = true
	m.optionsPicker.IsReset[filterRowArchived] = true

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
			Name:          "repo",
			NameWithOwner: "owner/repo",
		},
		URL: "https://github.com/owner/repo/pull/42",
	}
}
