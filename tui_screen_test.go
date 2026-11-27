package main

import (
	"image/color"
	"testing"

	"bloomfx.dev/bloom/canvas"
	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
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
