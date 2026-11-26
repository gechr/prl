package main

import (
	"image/color"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"github.com/gechr/primer/scrollwheel"
	"github.com/stretchr/testify/require"
)

func cellStyle(h *steep.Harness, at steep.Location) reel.CellStyle {
	cell := h.Session().Screen().Cells[at.Y][at.X]
	if cell == nil {
		return reel.CellStyle{}
	}
	return cell.Style
}

func requireScreenColor(t *testing.T, actual reel.ScreenColor, expected color.Color) {
	t.Helper()
	want, err := (reel.ScreenColor{Color: expected}).MarshalText()
	require.NoError(t, err)
	got, err := actual.MarshalText()
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

func waitCellStyle(
	t *testing.T,
	h *steep.Harness,
	at steep.Location,
	match func(reel.CellStyle) bool,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		return match(cellStyle(h, at))
	}, steep.DefaultTimeout, 10*time.Millisecond, "style at (%d, %d)", at.X, at.Y)
}

func listTUIModel(prs ...PullRequest) tuiModel {
	m := approvalTestModel("", prs...)
	m.cfg = &Config{}
	m.resolver = &AuthorResolver{}
	m.cli.Columns.Values = []string{colTitle, colRef}
	m.filterInput = textinput.New()
	m.replaceListItems(buildPRRowModels(prs, "", m.resolver))
	m.header, m.rows, m.colWidths = m.rerender()
	return m
}

// holdTUIRequest lets the test observe the pending action before its HTTP response arrives.
func holdTUIRequest() (<-chan struct{}, func()) {
	pending := make(chan struct{})
	return pending, sync.OnceFunc(func() { close(pending) })
}

func actionViewTestModel(view tuiView, pr PullRequest) tuiModel {
	m := listTUIModel(pr)
	m.view, m.width, m.height = view, 160, 30
	switch view {
	case tuiViewList:
		return m
	case tuiViewDetail:
		m.detailKey, m.detailView = makePRKey(pr), newScrollView()
		m.detailLines = m.renderDetailContent()
	case tuiViewDiff:
		m.diffKey, m.diffView = makePRKey(pr), newScrollView()
		m.diff = "+change"
	}
	return m
}

func newTUIHarness(t *testing.T, m tuiModel) *steep.Harness {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var h *steep.Harness
	wheel := scrollwheel.New(resolveWheelTarget, func(msg tea.Msg) { h.SendProgram(msg) })
	h = steep.NewHarness(
		t,
		m,
		steep.WithWindowSize(m.width, m.height),
		steep.WithColor(steep.ColorTrueColor),
		steep.WithProgramOptions(tea.WithFilter(wheel.Filter)),
	)
	t.Cleanup(wheel.Stop)
	return h
}
