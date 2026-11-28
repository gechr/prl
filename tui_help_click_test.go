package main

import (
	"errors"
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

func TestFilterFooterApplyAndExit(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	first.Title, second.Title = "Cedar", "Birch"
	m := listTUIModel(first, second)
	h := newTUIHarness(t, m)
	h.Key("/").Paste("Cedar").WaitNot("Birch").Assert("enter apply")
	h.Resize(32, 12).
		Wait("enter apply").
		Wait("↕ prev/next").
		Assert([]string{"^ start", "$ end", "! negate"})
	apply := h.Locate(reel.Contains("enter apply")).Offset(6, 0)
	exit := h.Locate(reel.Contains("esc exit"))
	require.Less(t, exit.Y, h.Locate(reel.Contains("! negate")).Y)
	base := cellStyle(h, apply)
	apply.Hover()
	waitCellStyle(t, h, apply, func(s reel.CellStyle) bool { return s.Bg != base.Bg })
	requireScreenColor(t, cellStyle(h, apply).Bg, colorSelectionBG)
	apply.LeftClick().WaitNot("enter apply").Resize(120, 25).Wait("Cedar").AssertNot("Birch")
	h.Key("/").Wait("esc exit")
	h.Locate(reel.Contains("esc exit")).Offset(4, 0).LeftClick().Wait("Birch").AssertNot("esc exit")
}

func TestFilterFooterStatusKeepsActionsClickable(t *testing.T) {
	for _, width := range []int{14, 36, 120} {
		m := numberedListTestModel(30)
		m.width, m.height = width, 14
		m.filterInput.Focus()
		m.ensureScreen()
		m.flash.Msg = strings.Repeat("x", width)
		lines, top := footerLines(m)
		for _, hint := range []key.Hint{{Key: key.Enter, Desc: "apply"}, {Key: key.Esc, Desc: "exit"}} {
			rect, ok := m.screen.root.Frame().Rect(bloom.ID(filterKeyPrefix + hint.Key))
			require.True(t, ok)
			require.Equal(
				t,
				hint.Key+" "+hint.Desc,
				ansi.Cut(lines[rect.Min.Y-top], rect.Min.X, rect.Max.X),
			)
			for x := rect.Min.X; x < rect.Max.X; x++ {
				binding, hit := m.footerKeyAt(x, rect.Min.Y)
				require.True(t, hit)
				require.Equal(t, hint.Key, binding)
			}
		}
		status := strings.TrimSpace(lines[len(lines)-1])
		require.Equal(t, strings.Repeat("x", width-2)+valueEllipsis, status)
		for x := range width {
			_, hit := m.footerKeyAt(x, m.height-1)
			require.False(t, hit)
		}
	}
}

func TestFilterFooterFitsOneRow(t *testing.T) {
	m := numberedListTestModel(2)
	m.width, m.height = 14, 5
	m.filterInput.Focus()
	m.ensureScreen()
	lines, _ := footerLines(m)
	require.Len(t, lines, 1)
	require.Equal(t, "apply  exit", strings.TrimSpace(lines[0]))
	for x, want := range map[int]string{1: key.Enter, 8: key.Esc} {
		binding, ok := m.footerKeyAt(x, m.height-1)
		require.True(t, ok)
		require.Equal(t, want, binding)
	}
}

func TestFilterFooterIgnoresStaleHitTargets(t *testing.T) {
	m := numberedListTestModel(2)
	m.width = 120
	m.ensureScreen()
	m.View()
	frame := m.screen.root.Frame()
	listEnter, ok := frame.Rect(bloom.ID(footerKeyPrefix + key.Enter))
	require.True(t, ok)
	m, _ = pressKey(t, m, tuiKeybindFilter)
	_, ok = m.footerKeyAt(listEnter.Min.X, listEnter.Min.Y)
	require.False(t, ok)

	m.View()
	apply, ok := m.screen.root.Frame().Rect(bloom.ID(filterKeyPrefix + key.Enter))
	require.True(t, ok)
	for _, cover := range []func(*tuiModel){
		func(m *tuiModel) { m.help = &helpSheet{} },
		func(m *tuiModel) { m.showOptions = true },
		func(m *tuiModel) { m.presetMenu.open = true },
		func(m *tuiModel) { m.confirmAction = tuiActionInfo },
	} {
		covered := m
		cover(&covered)
		_, ok = covered.footerKeyAt(apply.Min.X, apply.Min.Y)
		require.False(t, ok)
	}
	m, _ = pressKey(t, m, key.Enter)
	_, ok = m.footerKeyAt(apply.Min.X, apply.Min.Y)
	require.False(t, ok)
}

func TestFilterStatusKeepsCursorVisible(t *testing.T) {
	m := numberedListTestModel(40)
	m.width, m.height = 80, 14
	m.filterInput.Focus()
	for range 30 {
		m, _ = pressKey(t, m, key.Down)
		require.Less(t, m.cursor, m.offset+m.listViewport())
	}
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 14})
	m, _ = pressKey(t, m, key.Down)
	m, _ = updateModel(t, m, bloom.CopyMsg{Err: errors.New("clipboard unavailable")})
	require.Less(t, m.cursor, m.offset+m.listViewport())
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
	return ansi.Strip(m.shortcutRenderer().Render([]key.Hint{hint}))
}

func TestShortcutLayoutTracksWidthModeAndLabels(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.ensureScreen()
	hints := []key.Hint{{Key: "x", Desc: "\u2600\ufe0f"}, {Key: "y", Desc: "go"}}
	m.width = ansi.WcWidth.StringWidth(" x \u2600\ufe0f" + helpGap + "y go")
	require.Equal(t, 1, m.helpLines(hints))
	m.p = m.p.withGraphemeWidth()
	require.Equal(t, 2, m.helpLines(hints))
	m.width++
	require.Equal(t, 1, m.helpLines(hints))
	hints[1].Desc = "continue"
	require.Equal(t, 2, m.helpLines(hints))
	root := bloom.New(bloom.WithWidthMethod(m.reviewWidthMethod()))
	root.Render(m.width, 2, m.shortcutFooter(hints, ""))
	visible, ok := root.Frame().Rect(footerKeyPrefix + "y")
	require.True(t, ok)
	require.Equal(t, m.helpLines(hints)-1, visible.Min.Y)
}

func TestReviewFooterSurvivesListSubscriptionUpdates(t *testing.T) {
	for _, view := range []tuiView{tuiViewDiff, tuiViewDetail} {
		m := actionViewTestModel(view, approvalTestPR(1))
		m.ensureScreen()
		m.eventStreams = newEventStreams(true, nil)
		hints := m.diffHelpPairs()
		if view == tuiViewDetail {
			hints = m.detailHelpPairs()
		}
		review := m.shortcutLayout(hints)
		for range 2 {
			m, _ = updateModel(t, m, tea.MouseMotionMsg{X: 1, Y: 1})
			m.View()
			require.Same(t, review, m.shortcutLayout(hints))
		}
	}
}

// Status text that happens to start where a dropped hint sat must not act as
// that hint, even when it starts with the hint's key.
func TestHelpStatusCannotImpersonateDroppedHint(t *testing.T) {
	m := numberedListTestModel(3)
	m.width, m.height = 80, 20
	lines, _ := footerLines(m)
	before, _, ok := strings.Cut(lines[len(lines)-1], tuiHelpQuit)
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
