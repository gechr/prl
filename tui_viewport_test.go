package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/stretchr/testify/require"
)

func TestDiffSelectionScrollsBeyondViewportAndCopies(t *testing.T) {
	m := diffTestModel(testReviewPullRequest())
	m.p = m.p.withGraphemeWidth()
	lines := make([]string, 32)
	for i := range lines {
		lines[i] = fmt.Sprintf("+line %02d", i)
	}
	// Selection spans a soft wrap as well as rows outside the first frame.
	lines[1] += "\t" + strings.Repeat("x", m.width)
	m.diff = strings.Join(lines, nl)
	m.applyWindowSize(m.width, m.height)
	// Exercise the terminal clipboard without writing the host clipboard.
	m.diffView.root = bloom.New(bloom.WithWidthMethod(m.reviewWidthMethod()))
	h := newTUIHarness(t, m)
	start := h.Locate(reel.Contains(lines[0]))
	end := h.Locate(reel.Nth(-1, reel.Contains("┃")))
	h.Mouse(vt.MousePress, vt.MouseLeft, start.X, start.Y).
		Mouse(vt.MouseMotion, vt.MouseLeft, end.X-1, end.Y+1).
		Wait(lines[len(lines)-1]).
		Mouse(vt.MouseRelease, vt.MouseLeft, end.X-1, end.Y+1).
		KeyCtrlC()
	require.Eventually(t, func() bool { return h.Clipboard().Text == m.diff },
		steep.DefaultTimeout, 10*time.Millisecond)
}

func TestReviewSelectionBackground(t *testing.T) {
	for _, tc := range []struct {
		name  string
		light bool
		want  string
	}{
		{"dark", false, "#18283e"},
		{"light", true, "#ddeaf9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := paletteIsLight
			initPalette(tc.light)
			t.Cleanup(func() { initPalette(previous) })
			m := diffTestModel(testReviewPullRequest())
			m.diff = "+selected text"
			h := newTUIHarness(t, m)
			start := h.Locate(reel.Contains(m.diff))
			start.LeftDrag(start.Offset(6, 0))
			cell := start.Offset(2, 0)
			waitCellStyle(t, h, cell, func(s reel.CellStyle) bool { return s.Bg.Color != nil })
			requireScreenColor(t, cellStyle(h, cell).Bg, lg.Color(tc.want))
		})
	}
}

func TestSelectionScrollStopsOnReleaseOrPointerReturn(t *testing.T) {
	for _, lostRelease := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost=%t", lostRelease), func(t *testing.T) {
			m := diffTestModel(testReviewPullRequest())
			lines := make([]string, 200)
			for i := range lines {
				lines[i] = fmt.Sprintf("+row %03d", i)
			}
			m.diff = strings.Join(lines, nl)
			h := newTUIHarness(t, m)
			start := h.Locate(reel.Contains(lines[0]))
			end := h.Locate(reel.Nth(-1, reel.Contains("┃"))).Offset(-2, 1)
			h.Mouse(vt.MousePress, vt.MouseLeft, start.X, start.Y).
				Mouse(vt.MouseMotion, vt.MouseLeft, end.X, end.Y).
				Wait(lines[30])
			if lostRelease {
				// No release reached the tab. This is the first motion on re-entry.
				end.Hover()
			} else {
				h.Mouse(vt.MouseRelease, vt.MouseLeft, end.X, end.Y)
			}
			h.WaitSettle().AssertNot(lines[len(lines)-1])
			stopped := h.View()
			require.Never(t, func() bool { return h.View() != stopped }, 300*time.Millisecond, 15*time.Millisecond)
		})
	}
}

func TestListScrollbarTracksVisiblePRs(t *testing.T) {
	prs := make([]PullRequest, 40)
	for i := range prs {
		prs[i] = approvalTestPR(i + 1)
		prs[i].Title = fmt.Sprintf("Change %02d", i+1)
	}
	m := listTUIModel(prs...)
	m.sortColumn, m.sortAsc = colTitle, true
	m.height = 14
	h := newTUIHarness(t, m)
	top := h.Locate(reel.Contains("█"))
	bottom := h.Locate(reel.Nth(-1, reel.Contains("┃")))
	require.Equal(t, m.width-1, top.X)
	h.Key("O").Wait("enter apply")
	bottom.LeftClick().WaitNot("enter apply").Assert("Change 01").AssertNot("Change 40")
	h.Locate(reel.Contains("All PRs ▾")).LeftClick().Wait("✓ All PRs")
	top.LeftClick().WaitNot("✓ All PRs").Assert("Change 01").AssertNot("Change 40")
	bottom.LeftClick().Wait("Change 40").AssertNot("Change 01")
	bottom.LeftDrag(top).Wait("Change 01").AssertNot("Change 40")
	bottom.LeftClick().Wait("Change 40")
	h.Resize(m.width, len(prs)+8).Wait("Change 01").Assert("Change 40").AssertNot("┃")
	h.Resize(m.width, m.height).Wait("┃")
	h.Key("/").Paste("Change 40").WaitNot("┃").Assert("Change 40").AssertNot("█")
}

func TestListScrollbarAccountsForStackHeaders(t *testing.T) {
	m := newStackTestModel(t)
	m.height -= m.listViewport() - 3
	h := newTUIHarness(t, m)
	header := m.listLines()[0].header
	require.NotEmpty(t, header)
	bar := h.Locate(reel.Nth(-1, reel.Contains("┃")))
	bar.LeftClick().Wait("owner/repo#10").WaitNot(header).Key("g").Wait(header)
	// Folding the stack reduces five display lines to two.
	h.Locate(reel.Contains(header)).LeftClick().WaitNot("┃").AssertNot("█")
}

func TestReviewResizePreservesLogicalScrollPosition(t *testing.T) {
	m := diffTestModel(testReviewPullRequest())
	m.diff = strings.Repeat(strings.Repeat("x", 120)+nl, 40)
	m.applyWindowSize(80, 15)
	m.diffView.setOffset(20)
	prev, pos := m.diffView, m.diffView.pos
	m.applyWindowSize(40, 15)
	require.Same(t, prev, m.diffView)
	require.Equal(t, pos, m.diffView.geometry().Pos)
	require.Greater(t, m.diffView.offset(), 20)
}

func TestInterruptedScrollbarDragKeepsLastPosition(t *testing.T) {
	for _, end := range []tea.Msg{tea.BlurMsg{}, tea.MouseMotionMsg{X: 0, Y: 0}} {
		t.Run(fmt.Sprintf("%T", end), func(t *testing.T) {
			m := diffTestModel(testReviewPullRequest())
			m.diff = strings.Repeat("+line\n", 100)
			m.applyWindowSize(m.width, m.height)
			m.View()
			body, ok := m.diffView.root.Frame().Rect(reviewBodyID)
			require.True(t, ok)
			m, _ = updateModel(t, m, tea.MouseClickMsg{X: body.Max.X, Y: body.Min.Y, Button: tea.MouseLeft})
			m, _ = updateModel(t, m, tea.MouseMotionMsg{X: body.Max.X, Y: body.Max.Y - 3, Button: tea.MouseLeft})
			m.View()
			row := m.diffView.offset()
			require.Positive(t, row)
			m, _ = updateModel(t, m, end)
			require.Equal(t, row, m.diffView.offset())
		})
	}
}

func TestReviewCopyErrorSchedulesFlashClear(t *testing.T) {
	m := diffTestModel(testReviewPullRequest())
	got, cmd := updateModel(t, m, bloom.CopyMsg{Err: errors.New("clipboard unavailable")})
	require.True(t, got.flash.Err)
	require.NotNil(t, cmd)
}
