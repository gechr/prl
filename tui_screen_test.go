package main

import (
	"image/color"
	"regexp"
	"strings"
	"testing"

	"bloomfx.dev/bloom"
	"bloomfx.dev/bloom/canvas"
	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func screenRegressionModel(t *testing.T, width, height int) tuiModel {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := numberedListTestModel(40)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m.View()
	return m
}

func TestFilterFieldTracksCaretAcrossResize(t *testing.T) {
	m := screenRegressionModel(t, 10, 22)
	m.filterInput.Prompt = ""
	m.filterInput.Focus()
	const query = "0123456789abcdefghij"
	m, _ = updateModel(t, m, tea.PasteMsg{Content: query})
	line := func() string {
		return strings.Split(ansi.Strip(m.View().Content), nl)[m.listSeparatorY()-1]
	}
	require.Equal(t, "/cdefghij ", line())
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	require.Equal(t, "/012345678", line())
	m.filterInput.SetCursor(15)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 6, Height: 22})
	require.Equal(t, "/bcdef", line())
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 11, Height: 22})
	require.Equal(t, "/abcdefghij", line())
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "/012345678…", line())
	require.Equal(t, query, m.filterInput.Value())
	require.Equal(t, 15, m.filterInput.Position())
}

func TestFilterFieldKeepsWideGraphemesAndEndCaret(t *testing.T) {
	for _, grapheme := range []bool{false, true} {
		m := screenRegressionModel(t, 13, 22)
		m.filterInput.Prompt = ""
		count := 8
		if grapheme {
			m.p = m.p.withGraphemeWidth()
			m.screen.resetRoot(m.reviewWidthMethod())
			count = 4
		}
		m.filterInput.Focus()
		const sun = "\u2600\ufe0f"
		query := strings.Repeat(sun, 20) + "END"
		m, _ = updateModel(t, m, tea.PasteMsg{Content: query})
		lines := strings.Split(ansi.Strip(m.View().Content), nl)
		require.Equal(t, "/"+strings.Repeat(sun, count)+"END ", lines[m.listSeparatorY()-1])
		require.Equal(t, query, m.filterInput.Value())
	}
}

func TestFilterWindowClipsAJoinedEmojiAsOneUnit(t *testing.T) {
	const query = "ab👨‍👩‍👧xyz"
	var window filterWindow
	window.fit(query, 0, 7, ansi.WcWidth)
	require.Equal(t, "ab", window.visible)
	window.fit(query, -1, 7, ansi.WcWidth)
	require.Equal(t, "ab…", window.visible)
	window.fit(query, 0, 7, ansi.GraphemeWidth)
	require.Equal(t, query, window.visible)
}

func TestListDragDoesNotSelectText(t *testing.T) {
	m := screenRegressionModel(t, 80, 14)
	m, _ = updateModel(t, m, tea.MouseClickMsg{X: 8, Y: 2, Button: tea.MouseLeft})
	m.View()
	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: 40, Y: 5, Button: tea.MouseLeft})
	m.View()
	_, dragging := m.screen.root.Selection()
	m, _ = updateModel(t, m, tea.MouseReleaseMsg{X: 40, Y: 5, Button: tea.MouseLeft})
	m.View()
	_, released := m.screen.root.Selection()
	require.False(t, dragging)
	require.False(t, released)
}

func TestListScrollbarDropsDragLostInAnotherView(t *testing.T) {
	m := screenRegressionModel(t, 80, 14)
	x := m.width - 1
	m, _ = updateModel(t, m, tea.MouseClickMsg{X: x, Y: m.listViewport(), Button: tea.MouseLeft})
	m.View()
	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: x, Y: 7, Button: tea.MouseLeft})
	m.View()
	require.Positive(t, m.offset)
	// The button is still held when the detail view takes the mouse.
	m.view = tuiViewDetail
	m.detailKey = makePRKey(m.rows[m.cursor].Item.PR)
	m.detailLines = m.renderDetailContent()
	m.View()
	m, _ = updateModel(t, m, tea.MouseReleaseMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	m.View()
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, tuiViewList, m.view)
	m.setListScrollOffset(0)
	m.View()
	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: 10, Y: 3})
	require.Equal(t, 0, m.offset)
}

func TestPresetMenuHidesListHighlight(t *testing.T) {
	m := screenRegressionModel(t, 80, 14)
	for range 4 {
		m, _ = pressKey(t, m, "j")
	}
	m.cli.preset = "Needs your review"
	m.header, m.rows, m.colWidths = m.rerender()
	m.openPresetMenu()
	screen := canvas.Parse(m.View().Content, m.reviewWidthMethod())
	rect, ok := m.screen.root.Frame().Rect(presetMenuID)
	require.True(t, ok)
	const cursorY = 5 // the header, then PRs 1-4
	require.Less(t, rect.Min.Y, cursorY)
	require.Greater(t, rect.Max.Y, cursorY)
	for x := rect.Min.X; x < rect.Max.X; x++ {
		bg := screen.CellAt(x, cursorY).Style.Bg
		require.NotNil(t, bg)
		require.Equal(
			t,
			color.RGBA64Model.Convert(m.screen.bg),
			color.RGBA64Model.Convert(bg),
			"x=%d",
			x,
		)
	}
}

func TestFilterPanelWrapsAndFollowsSelection(t *testing.T) {
	m := screenRegressionModel(t, 40, 12)
	m.openOptions()
	h := newTUIHarness(t, m)
	h.Wait("ready  all").Assert("enter apply")
	h.Locate(reel.Contains("ready  all")).LeftClick()
	h.KeyDown(reel.Repeat(len(filterOptionDefs) - 1)).Wait("self-required")
	h.KeyRight().KeyUp(reel.Repeat(len(filterOptionDefs) - 1)).Wait("ready  all")
	ready := h.Locate(reel.Contains("ready  all"))
	require.NotZero(t, cellStyle(h, ready).Attrs&vt.AttrBold)
	h.KeyEsc().WaitNot("enter apply").Key("O").Wait("ready  all")
	require.Zero(t, cellStyle(h, h.Locate(reel.Contains("ready  all"))).Attrs&vt.AttrBold)
}

func TestFilterScrollbarLeavesRoomForChoices(t *testing.T) {
	m := screenRegressionModel(t, 120, 9)
	m.openOptions()
	m.optionsPicker.Cursor = int(filterRowReview)
	h := newTUIHarness(t, m)
	review := h.Locate(reel.Contains("Review  required"))
	last := h.Locate(reel.After(reel.Contains("self-required"), reel.Contains("none  all")))
	require.Equal(t, review.Y, last.Y)
}

func TestHeaderlessListClicksUseRenderedRows(t *testing.T) {
	m := screenRegressionModel(t, 80, 12)
	m.header = ""
	m.View()
	rect, ok := m.screen.root.Frame().Rect(prListID)
	require.True(t, ok)
	require.Zero(t, rect.Min.Y)
	m, _ = updateModel(t, m, tea.MouseClickMsg{X: 4, Y: 1, Button: tea.MouseLeft})
	require.Equal(t, 1, m.cursor)
	m, _ = updateModel(t, m, tea.MouseClickMsg{X: 4, Y: 0, Button: tea.MouseLeft})
	require.Zero(t, m.cursor)
}

func TestEmptyPanelWrapsQueryAndRecoveryHint(t *testing.T) {
	m := numberedListTestModel(1)
	m.width, m.height = 40, 22
	h := newTUIHarness(t, m)
	const query = "🌲é🌲é"
	h.Key("/").
		Paste(query).
		Wait("No pull requests match").
		Wait(query + "\"").
		Assert("clear the filter")
	h.Paste(strings.Repeat(query, 100)).Wait("…").Assert("clear the filter").AssertNot("�")
	h.KeyEsc().Wait("❯ 1")
}

func TestEmptyPanelKeepsFilterInputVisible(t *testing.T) {
	m := numberedListTestModel(1)
	m.width, m.height = 40, 10
	h := newTUIHarness(t, m)
	input := reel.Matches(regexp.MustCompile("(?m)^/[^\n]*nomatch"))
	h.Key("/").Paste("nomatch").WaitFor(input).Assert("PR")
	h.Resize(40, 8).Wait("clear the filter").WaitFor(input)
	h.Resize(40, 6).Wait("esc clear filter").WaitFor(input)
	h.KeyEsc().Wait("❯ 1")
}

func TestEmptyPanelEllipsizesLongUnbrokenQuery(t *testing.T) {
	m := numberedListTestModel(1)
	m.width = 30
	m.filterInput.SetValue(strings.Repeat("x", 150))
	panel := bloom.Overlay(nil, m.emptyLayer(3))
	screen := ansi.Strip(bloom.Render(m.width, 3, panel))
	require.Regexp(t, `x+…`, screen)
	require.Regexp(t, `clear the filter`, screen)
}
