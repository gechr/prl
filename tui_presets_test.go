package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"bloomfx.dev/bloom"
	"bloomfx.dev/bloom/canvas"
	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Render before inspecting the controls so positions come from the frame on screen.
func testListSeparator(m *tuiModel) string {
	m.ensureScreen()
	return ansi.Strip(strings.Split(m.View().Content, nl)[m.listSeparatorY()])
}

func testControlBounds(m tuiModel, id bloom.ID) (int, int) {
	rect, _ := m.screen.root.Frame().Rect(id)
	return rect.Min.X, rect.Max.X
}

func assertPresetRows(t *testing.T, h *steep.Harness, labels []string) {
	t.Helper()
	first := h.Locate(reel.Contains(labels[0]))
	for i, label := range labels {
		require.Equal(t, first.Y+i, h.Locate(reel.Contains(label)).Y, label)
	}
}

func TestPresetMenuClickSelectsAndPersists(t *testing.T) {
	failing, other := approvalTestPR(1), approvalTestPR(2)
	failing.Title, other.Title = "Failing build", "Passing build"
	failing.MergeStatus = MergeStatusCIFailed
	m := listTUIModel(failing, other)
	m.cli.preset = "Needs your review"
	m.filterInput.Focus()
	h := newTUIHarness(t, m)
	tag := h.Locate(reel.Contains("Needs your review ▾"))
	tag.LeftClick()
	labels := []string{
		"All PRs",
		"✓ Needs your review",
		"Waiting for CI",
		"Approved by you",
		"Mergeable",
		"Outdated",
		"Blocked",
	}
	assertPresetRows(t, h, labels)
	require.Less(t, h.Locate(reel.Contains("Blocked")).Y, tag.Y)
	h.Locate(reel.Contains("Blocked")).
		LeftClick().
		Wait("Blocked ▾").
		Assert(failing.Title).
		AssertNot(other.Title)
	st, err := loadTUIState()
	require.NoError(t, err)
	require.Equal(t, "Blocked", st.Filters.Preset)

	h.Locate(reel.Contains("Blocked ▾")).LeftClick()
	h.Locate(reel.Contains("All PRs")).
		LeftClick().
		Wait("All PRs ▾").
		Assert([]string{failing.Title, other.Title})
	st, err = loadTUIState()
	require.NoError(t, err)
	require.Empty(t, st.Filters.Preset)
	h.Locate(reel.Contains("All PRs ▾")).LeftClick().Wait("✓ All PRs")
}

func TestPresetMenuDismissesWithoutChangingSelection(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	first.MergeStatus, second.MergeStatus = MergeStatusCIFailed, MergeStatusCIFailed
	m := listTUIModel(first, second)
	m.cli.preset = "Blocked"
	h := newTUIHarness(t, m)
	tag := h.Locate(reel.Contains("Blocked ▾"))
	tag.LeftClick().Wait("✓ Blocked").LeftClick(0, 2).WaitNot("✓ Blocked").Assert("❯ 1")
	tag.LeftClick().Wait("✓ Blocked").KeyEsc().WaitNot("✓ Blocked").Assert("Blocked ▾")
}

func TestPresetMenuRespectsConfiguredAvailability(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	const available = "\u5f85\u6a5f\u4e2d"
	m.cfg.TUI.Presets.Filters = []TUIPresetConfig{
		{Name: "Other authors", Filter: "!mine", When: presetNotSelf},
		{Name: available, Filter: "ci:pending"},
	}
	m.cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	m.cli.preset, m.width = "Other authors", 16
	h := newTUIHarness(t, m)
	h.Locate(reel.Contains("All PRs ▾")).LeftClick()
	assertPresetRows(t, h, []string{"✓ All PRs", available})
	h.AssertNot("Other authors")
	h.Locate(reel.Contains(available)).LeftClick().Wait(available + " ▾")
	h.Locate(reel.Contains(available + " ▾")).LeftClick().Wait("✓ " + available)
}

func TestPresetMenuScrollsInShortTerminal(t *testing.T) {
	pr := approvalTestPR(1)
	pr.IsDraft = true
	m := listTUIModel(pr)
	for i := range 20 {
		m.cfg.TUI.Presets.Filters = append(m.cfg.TUI.Presets.Filters,
			TUIPresetConfig{Name: fmt.Sprintf("Filter %02d", i), Filter: "draft"})
	}
	m.height, m.cli.preset = 10, "Filter 00"
	h := newTUIHarness(t, m)
	h.Locate(reel.Contains("Filter 00 ▾")).LeftClick().Wait("✓ Filter 00").AssertNot("Filter 19")
	menuRow := regexp.MustCompile(`│ +((?:✓ )?Filter [0-9]{2}|All PRs) +│`)
	initial := menuRow.FindAllStringSubmatch(h.View(), -1)
	require.Greater(t, len(initial), 1)
	for i, match := range initial {
		switch i {
		case 0:
			require.Equal(t, "All PRs", match[1])
		case 1:
			require.Equal(t, "✓ Filter 00", match[1])
		default:
			require.Equal(t, fmt.Sprintf("Filter %02d", i-1), match[1])
		}
	}
	h.Locate(reel.Contains("✓ Filter 00")).
		Wheel(vt.MouseWheelDown, reel.Repeat(19)).
		Wait("Filter 19")
	scrolled := menuRow.FindAllStringSubmatch(h.View(), -1)
	require.Len(t, scrolled, len(initial))
	for i, match := range scrolled {
		require.Equal(t, fmt.Sprintf("Filter %02d", 20-len(scrolled)+i), match[1])
	}
	h.Locate(reel.Contains("Filter 19")).LeftClick().Wait("Filter 19 ▾").Assert("❯ 1")
}

func TestPresetMenuKeyboardAndResize(t *testing.T) {
	h := newTUIHarness(t, listTUIModel(approvalTestPR(1)))
	h.Locate(reel.Contains("All PRs ▾")).LeftClick().Wait("✓ All PRs").
		Key("home").KeyDown(reel.Repeat(2)).Key("enter").Wait("Waiting for CI ▾")
	h.Locate(reel.Contains("Waiting for CI ▾")).LeftClick().Wait("✓ Waiting for CI").
		Key(tuiKeybindCyclePresetBack).Wait("Needs your review ▾")
	h.Locate(reel.Contains("Needs your review ▾")).LeftClick().Wait("✓ Needs your review")
	h.Resize(3, 3).Resize(80, 20).Wait("Needs your review ▾").AssertNot("✓ Needs your review")
}

func TestPresetMenuClosesWhenFilterMovesAnchorOffscreen(t *testing.T) {
	for _, label := range []string{"All PRs", "All Time"} {
		t.Run(label, func(t *testing.T) {
			m := listTUIModel(approvalTestPR(1))
			m.filterInput.SetValue("pr")
			h := newTUIHarness(t, m)
			h.Locate(reel.Contains(label + " ▾")).LeftClick().Wait("✓ " + label)
			h.Resize(80, 3).
				WaitNot(label+" ▾").
				Resize(80, 20).
				Wait(label + " ▾").
				AssertNot("✓ " + label)
		})
	}
}

func TestPresetMenuDoesNotOpenOverModal(t *testing.T) {
	for _, tc := range []struct{ key, dialog string }{
		{"?", "Press any key to dismiss"}, {"O", "enter apply"}, {"a", "Approve owner/repo#1?"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			m := listTUIModel(approvalTestPR(1))
			m.width, m.height = 120, 35
			h := newTUIHarness(t, m)
			control := h.Locate(reel.Contains("All PRs ▾"))
			h.Key(tc.key).Wait(tc.dialog)
			control.LeftClick().WaitSettle().AssertNot("✓ All PRs")
		})
	}
}

func TestPresetControlsHoverAndCaretClicks(t *testing.T) {
	for _, label := range []string{"All PRs", "All Time"} {
		t.Run(label, func(t *testing.T) {
			h := newTUIHarness(t, listTUIModel(approvalTestPR(1)))
			control := h.Locate(reel.Contains(label + " ▾"))
			caret := control.Offset(ansi.StringWidth(label)+1, 0)
			base := cellStyle(h, caret)
			caret.Hover()
			waitCellStyle(t, h, caret, func(s reel.CellStyle) bool { return s.Bg != base.Bg })
			requireScreenColor(t, cellStyle(h, caret).Bg, colorSelectionBG)
			h.Locate(reel.Contains("TITLE")).Hover()
			waitCellStyle(t, h, caret, func(s reel.CellStyle) bool { return s == base })
			caret.Hover().LeftClick(caret.X, caret.Y).Wait("✓ " + label)
			waitCellStyle(t, h, caret, func(s reel.CellStyle) bool { return s == base })
		})
	}
}

func TestPresetMenuUnknownTimePresetChecksAllTime(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.cli.timePreset = "Removed"
	h := newTUIHarness(t, m)
	h.Locate(reel.Contains("All Time ▾")).LeftClick()
	assertPresetRows(
		t,
		h,
		[]string{"✓ All Time", "10 minutes", "1 hour", "1 day", "1 week", "1 month", "1 year"},
	)
}

func TestPresetHoverPreservesIdleRefreshTimer(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	idleSince := time.Now().Add(-time.Hour)
	m.lastInteraction = idleSince
	testListSeparator(&m)
	start, _ := testControlBounds(m, prPresetID)
	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: start, Y: m.listSeparatorY()})
	require.Equal(t, prPresetID, m.hover.control)
	require.Equal(t, idleSince, m.lastInteraction, "hover alone keeps the idle timer")
	m, _ = updateModel(
		t,
		m,
		tea.MouseMotionMsg{X: start, Y: m.listSeparatorY(), Button: tea.MouseLeft},
	)
	require.True(t, m.lastInteraction.After(idleSince), "dragging is an interaction")
}

func TestPresetHitsFollowWideLabels(t *testing.T) {
	const name = "\u2600\ufe0f \u754c\u9762"
	for _, grapheme := range []bool{false, true} {
		m := listTUIModel(approvalTestPR(1))
		if grapheme {
			m.p = m.p.withGraphemeWidth()
		}
		m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{{Name: name, MaxAge: "1h"}}
		m.cli.timePreset = name
		method := m.reviewWidthMethod()
		for _, width := range []int{80, 13, 10} {
			m.width = width
			line := testListSeparator(&m)
			start, end := testControlBounds(m, timePresetID)
			if width == 80 {
				before, _, found := strings.Cut(line, name+presetControlSuffix)
				require.True(t, found)
				require.Equal(t, method.StringWidth(before), start)
				require.Equal(t, method.StringWidth(name+presetControlSuffix), end-start)
			}
			require.Equal(t, "▾", canvas.Parse(line, method).CellAt(end-1, 0).Content)
			y := m.listSeparatorY()
			for x := start; x < end; x++ {
				require.Equal(t, timePresetID, m.screen.hit(x, y))
			}
			require.NotEqual(t, timePresetID, m.screen.hit(start-1, y))
			require.NotEqual(t, timePresetID, m.screen.hit(end, y))
		}
	}
}

func TestPresetMenusCenterOverTheirControls(t *testing.T) {
	for _, timeFilter := range []bool{false, true} {
		m := approvalTestModel("", approvalTestPR(1))
		m.cfg = &Config{}
		if timeFilter {
			m.cli.timePreset = "10 minutes"
		} else {
			m.cli.preset = "Needs your review"
		}
		m.openFilterPresetMenu(timeFilter)
		testListSeparator(&m)
		id := prPresetID
		if timeFilter {
			id = timePresetID
		}
		start, end := testControlBounds(m, id)
		left, right := testControlBounds(m, presetMenuID)
		require.Greater(t, end, start)
		require.Greater(t, right, left)
		require.InDelta(t, float64(start+end)/2, float64(left+right)/2, 0.5)
		require.GreaterOrEqual(t, left, 0)
		require.LessOrEqual(t, right, m.width)
	}
}

func TestPresetMenuCenteringClampsToTerminal(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	// A long time control places its menu against the left edge.
	name := strings.Repeat("t", 61)
	m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{{Name: name, MaxAge: "1h"}}
	m.cli.timePreset = name
	m.openFilterPresetMenu(true)
	testListSeparator(&m)
	left, right := testControlBounds(m, presetMenuID)
	require.Greater(t, right, left)
	require.Zero(t, left)
	// A long menu entry extends past the right edge of the PR control.
	m.cfg.TUI.Presets.Filters = []TUIPresetConfig{
		{Name: strings.Repeat("p", 30), Filter: "ci:pending"},
	}
	m.cli.timePreset = ""
	m.openPresetMenu()
	testListSeparator(&m)
	_, right = testControlBounds(m, presetMenuID)
	require.Equal(t, m.width, right)
}

func TestPresetMenuHoverHighlightsWithoutSelecting(t *testing.T) {
	for _, tc := range []struct{ control, option string }{{"All PRs", "Needs your review"}, {"All Time", "10 minutes"}} {
		t.Run(tc.control, func(t *testing.T) {
			h := newTUIHarness(t, listTUIModel(approvalTestPR(1)))
			h.Locate(reel.Contains(tc.control + " ▾")).LeftClick().Wait("✓ " + tc.control)
			option := h.Locate(reel.Contains(tc.option))
			before := cellStyle(h, option)
			option.Hover()
			waitCellStyle(t, h, option, func(s reel.CellStyle) bool { return s.Bg != before.Bg })
			requireScreenColor(t, cellStyle(h, option).Bg, colorSelectionBG)
			h.Assert("✓ " + tc.control).
				KeyEsc().
				WaitNot("✓ " + tc.control).
				Assert(tc.control + " ▾")
		})
	}
}

func TestPresetKeysKeepTheirMeaningInEitherMenu(t *testing.T) {
	for _, menu := range []string{"All PRs", "All Time"} {
		for _, tc := range []struct{ key, pr, time string }{
			{"f", "Needs your review", "All Time"},
			{"F", "Blocked", "All Time"},
			{"t", "All PRs", "10 minutes"},
			{"T", "All PRs", "1 year"},
		} {
			t.Run(menu+"/"+tc.key, func(t *testing.T) {
				h := newTUIHarness(t, listTUIModel(approvalTestPR(1)))
				h.Locate(reel.Contains(menu + " ▾")).LeftClick().Wait("✓ " + menu).
					Key(tc.key).Wait(tc.time + " ▾ │ " + tc.pr + " ▾").AssertNot("✓ " + menu)
			})
		}
	}
}

func TestPresetHoverClearsOnKeysAndResize(t *testing.T) {
	for _, label := range []string{"All PRs ▾", "All Time ▾"} {
		t.Run(label, func(t *testing.T) {
			h := newTUIHarness(t, listTUIModel(approvalTestPR(1)))
			control := h.Locate(reel.Contains(label))
			base := cellStyle(h, control)
			for _, resize := range []bool{false, true} {
				control.Hover()
				waitCellStyle(t, h, control, func(s reel.CellStyle) bool { return s != base })
				if resize {
					h.Resize(120, 30).WaitFor(reel.ScreenFunc(func(screen string) bool {
						return !strings.Contains(strings.Split(screen, nl)[control.Y], label)
					}))
					control = h.Locate(reel.Contains(label))
				} else {
					h.Key("z")
				}
				waitCellStyle(t, h, control, func(s reel.CellStyle) bool { return s == base })
			}
		})
	}
}

func TestTimeMenuClosesWhenItsControlDisappears(t *testing.T) {
	h := newTUIHarness(t, listTUIModel(approvalTestPR(1)))
	h.Locate(reel.Contains("All Time ▾")).LeftClick().Wait("✓ All Time")
	h.Resize(21, 20).WaitNot("All Time ▾").Resize(80, 20).Wait("All Time ▾").AssertNot("✓ All Time")
}

func TestFilterTagsHoverAndClickOpenOptions(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.cli.NoBot = false
	testListSeparator(&m)
	start, end := testControlBounds(m, filterTagsID)
	require.Equal(t, start, end)
	m.cli.State, m.cli.stateExplicit = valueClosed, true
	h := newTUIHarness(t, m)
	tag := h.Locate(reel.Contains("state:closed"))
	base := cellStyle(h, tag)
	tag.Hover()
	waitCellStyle(t, h, tag, func(s reel.CellStyle) bool { return s.Bg != base.Bg })
	requireScreenColor(t, cellStyle(h, tag).Bg, colorSelectionBG)
	tag.Offset(len("state:closed")-1, 0).LeftClick().Wait("enter apply")
	dimmed := cellStyle(h, tag)
	require.Equal(t, base.Bg, dimmed.Bg)
	require.NotEqual(t, base.Fg, dimmed.Fg)
	tag.LeftClick().WaitNot("enter apply").Assert("state:closed")
}

func TestRowHoverHighlightsWithoutMovingCursor(t *testing.T) {
	for _, light := range []bool{false, true} {
		t.Run(fmt.Sprint(light), func(t *testing.T) {
			initPalette(light)
			t.Cleanup(func() { initPalette(false) })
			h := newTUIHarness(t, numberedListTestModel(3))
			cursor := h.Locate(reel.Contains("❯ 1"))
			row := h.Locate(reel.Contains("  2  ")).Offset(2, 0)
			base, cursorStyle := cellStyle(h, row), cellStyle(h, cursor.Offset(2, 0))
			row.Hover()
			waitCellStyle(
				t,
				h,
				row,
				func(s reel.CellStyle) bool { return s.Bg != base.Bg && s.Bg != cursorStyle.Bg },
			)
			requireScreenColor(t, cellStyle(h, row).Bg, colorSelectionBG)
			h.Assert("❯ 1")
			h.Locate(reel.Contains("All PRs ▾")).Hover()
			waitCellStyle(t, h, row, func(s reel.CellStyle) bool { return s == base })
		})
	}
}

func TestRowHoverFollowsTheRowUnderThePointer(t *testing.T) {
	m := numberedListTestModel(30)
	m.height = 12
	h := newTUIHarness(t, m)
	row := h.Locate(reel.Contains("  2  ")).Offset(2, 0)
	base := cellStyle(h, row)
	row.Hover()
	waitCellStyle(t, h, row, func(s reel.CellStyle) bool { return s.Bg != base.Bg })
	hover := cellStyle(h, row).Bg
	row.Wheel(vt.MouseWheelDown, reel.Repeat(12)).Wait("❯ 13")
	require.Equal(t, hover, cellStyle(h, row).Bg)
}

func TestClicksBelowTheListLeaveTheCursor(t *testing.T) {
	prs := make([]PullRequest, 30)
	for i := range prs {
		prs[i] = approvalTestPR(i + 1)
	}
	m := approvalTestModel("", prs...)
	m.cfg = &Config{}
	m.header, m.height = "PR", 12
	// Help hints are buttons with their own actions, so skip them.
	for y := m.listSeparatorY(); y < m.height; y++ {
		for x := range m.width {
			if _, ok := m.listHelpHintAt(x, y); ok {
				continue
			}
			after, _ := updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			require.Equal(t, 0, after.cursor, "x=%d y=%d is not a row", x, y)
		}
	}
}
