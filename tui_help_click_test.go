package main

import (
	"strings"
	"testing"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep/reel"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/stretchr/testify/require"
)

// footerLines returns the plain help lines and the screen row of the first.
func footerLines(m tuiModel) ([]string, int) {
	top := m.listSeparatorY() + 1
	return strings.Split(ansi.Strip(m.View().Content), nl)[top:], top
}

// Clicking anywhere on a "key desc" hint acts like pressing its key.
func TestHelpHintClickPressesItsKey(t *testing.T) {
	for _, tc := range []struct{ label, overlay string }{
		{"Options", "Stacks"},
		{"? " + tuiHelpHelp, "Press any key to dismiss"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			m := numberedListTestModel(3)
			m.width, m.height = 120, 35
			h := newTUIHarness(t, m)
			for _, offset := range []int{0, ansi.StringWidth(tc.label) - 1} {
				h.Locate(reel.Contains(tc.label)).Offset(offset, 0).LeftClick().Wait(tc.overlay)
				h.KeyEsc().WaitNot(tc.overlay).Assert("❯ 1")
			}
		})
	}
}

func TestHelpHintsDroppedForStatusAreNotButtons(t *testing.T) {
	// 30 rows overflow the viewport, so the footer shows a scroll position
	// that can push trailing hints off the help line.
	m := numberedListTestModel(30)
	m.height = 12
	m.ensureScreen()
	dropped := 0
	// 47, 72 and 140 drop a hint for the status, 80 and 120 do not.
	for _, width := range []int{47, 72, 80, 120, 140} {
		m.width = width
		lines, top := footerLines(m)
		footer := strings.Join(lines, nl)
		labels := make(map[string]string)
		for _, hint := range m.listHelpPairs() {
			labels[hint.Key] = helpHintText(m, hint)
			if !strings.Contains(footer, labels[hint.Key]) {
				dropped++
			}
		}
		for i, line := range lines {
			for x := range width {
				binding, ok := m.footerKeyAt(x, top+i)
				if !ok {
					continue
				}
				// The clicked cell must sit inside the hint's drawn text.
				text := labels[binding]
				require.NotEmpty(t, text)
				idx := strings.Index(line, text)
				require.NotEqual(t, -1, idx, "width=%d x=%d hits undrawn %q", width, x, binding)
				start := ansi.StringWidth(line[:idx])
				require.GreaterOrEqual(t, x, start, "width=%d x=%d", width, x)
				require.Less(t, x, start+ansi.StringWidth(text), "width=%d x=%d", width, x)
			}
		}
	}
	require.Positive(t, dropped, "some width drops a hint for the status")
}

// helpHintText is the plain "key desc" text the help bar draws for hint.
func helpHintText(m tuiModel, hint key.Hint) string {
	return strings.TrimPrefix(ansi.Strip(m.helpBar([]key.Hint{hint}).Render()), " ")
}

// Status text that happens to start where a dropped hint sat must not act as
// that hint, even when it starts with the hint's key.
func TestHelpStatusCannotImpersonateDroppedHint(t *testing.T) {
	m := numberedListTestModel(30)
	m.width, m.height = 80, 20
	plain := strings.Split(ansi.Strip(m.helpBar(m.listHelpPairs()).Render()), nl)
	last := plain[len(plain)-1]
	before, _, ok := strings.Cut(last, tuiHelpQuit)
	require.True(t, ok, "quit is the last hint")
	x := ansi.StringWidth(before)
	m.flash.Msg = "q" + strings.Repeat("x", m.width-2-x)
	h := newTUIHarness(t, m)
	status := h.Locate(reel.Contains(m.flash.Msg))
	require.Equal(t, x, status.X, "status sits where quit was")
	status.LeftClick().KeyDown().Wait("❯ 2 ")
}

func TestHelpHintHoverHighlightsTheHint(t *testing.T) {
	h := newTUIHarness(t, numberedListTestModel(3))
	hint := h.Locate(reel.Contains("? "+tuiHelpHelp)).Offset(2, 0)
	base := cellStyle(h, hint)
	text := h.View()
	hint.Hover()
	waitCellStyle(t, h, hint, func(s reel.CellStyle) bool { return s.Bg != base.Bg })
	requireScreenColor(t, cellStyle(h, hint).Bg, colorSelectionBG)
	require.Equal(t, text, h.View())
	h.Locate(reel.Contains("TITLE")).Hover()
	waitCellStyle(t, h, hint, func(s reel.CellStyle) bool { return s == base })
}

func TestReviewFooterCommentHoverAndClick(t *testing.T) {
	pr := approvalTestPR(1)
	for name, view := range map[string]tuiView{"diff": tuiViewDiff, "detail": tuiViewDetail} {
		t.Run(name, func(t *testing.T) {
			m := actionViewTestModel(view, pr)
			m.width = 48 // Wrap the shortcuts onto multiple lines.
			h := newTUIHarness(t, m)
			hint := h.Locate(reel.Contains(tuiHelpComment)).Offset(3, 0)
			base := cellStyle(h, hint)
			hint.Hover()
			waitCellStyle(t, h, hint, func(s reel.CellStyle) bool { return s.Bg != base.Bg })
			requireScreenColor(t, cellStyle(h, hint).Bg, colorSelectionBG)
			hint.LeftClick().Wait("Comment on " + pr.Ref() + "?")
		})
	}
}

func TestReviewFooterDuringNavigation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		view        tuiView
		loadingDiff bool
	}{
		{"diff", tuiViewDiff, true},
		{"detail", tuiViewDetail, false},
		{"opening diff from detail", tuiViewDetail, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := actionViewTestModel(tc.view, approvalTestPR(1))
			m.applyWindowSize(100, 30)
			m.diffLoading, m.detailLoading = tc.loadingDiff, !tc.loadingDiff
			m.View()
			frame := m.activeReviewViewport().root.Frame()
			for _, binding := range []string{key.ArrowsVertical, tuiKeybindComment} {
				rect, ok := frame.Rect(bloom.ID(footerKeyPrefix + binding))
				require.True(t, ok)
				click := tea.MouseClickMsg{X: rect.Min.X, Y: rect.Min.Y, Button: tea.MouseLeft}
				_, ok = m.footerKeyAt(click.X, click.Y)
				require.False(t, ok)
				m, _ = updateModel(t, m, click)
				require.Empty(t, m.confirmAction)
			}
			dismiss := tuiKeybindQuit
			if tc.view == tuiViewDiff {
				dismiss = tuiKeybindDiff
			}
			rect, ok := frame.Rect(bloom.ID(footerKeyPrefix + dismiss))
			require.True(t, ok)
			m, _ = updateModel(t, m, tea.MouseClickMsg{
				X: rect.Min.X, Y: rect.Min.Y, Button: tea.MouseLeft,
			})
			require.Equal(t, tuiViewList, m.view)
		})
	}
}

func TestReviewFooterKeepsMultilineErrorsOnLastRow(t *testing.T) {
	for name, view := range map[string]tuiView{"diff": tuiViewDiff, "detail": tuiViewDetail} {
		t.Run(name, func(t *testing.T) {
			m := actionViewTestModel(view, approvalTestPR(1))
			m.applyWindowSize(80, 20)
			before := strings.Split(ansi.Strip(m.View().Content), nl)
			m.flash.Msg, m.flash.Err = "Read failed\r\nretry\nwhen ready\ror cancel", true
			const want = "Read failed retry when ready or cancel"
			after := strings.Split(ansi.Strip(m.View().Content), nl)
			require.Len(t, after, len(before))
			last := len(after) - 1
			require.Equal(t, before[:last], after[:last])
			require.True(t, strings.HasSuffix(strings.TrimRight(after[last], " "), want))
			for x := m.width - 1 - len(want); x < m.width; x++ {
				_, ok := m.footerKeyAt(x, last)
				require.False(t, ok, "status cell %d hits a shortcut", x)
			}
		})
	}
}

func TestDisabledFooterHasNoHoverHighlight(t *testing.T) {
	m := screenRegressionModel(t, 120, 30)
	m.diffLoading = true
	before := m.View().Content
	button, ok := m.screen.root.Frame().Rect(bloom.ID(footerKeyPrefix + tuiKeybindHelp))
	require.True(t, ok)
	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: button.Min.X, Y: button.Min.Y})
	require.Equal(t, before, m.View().Content)
}
