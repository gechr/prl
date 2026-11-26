package main

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"github.com/stretchr/testify/require"
)

func cellStyle(h *steep.Harness, at steep.Location) reel.CellStyle {
	cell := h.Session().Screen().Cells[at.Y][at.X]
	if cell == nil {
		return reel.CellStyle{}
	}
	return cell.Style
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
	m.rerenderList()
	return m
}

func newTUIHarness(t *testing.T, m tuiModel) *steep.Harness {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return steep.NewHarness(
		t,
		m,
		steep.WithWindowSize(m.width, m.height),
		steep.WithColor(steep.ColorTrueColor),
	)
}
