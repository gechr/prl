package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/scrollwheel"
	"github.com/stretchr/testify/require"
)

func clickPresetText(t *testing.T, m tuiModel, label string) (tuiModel, tea.Cmd) {
	t.Helper()
	x, y := buttonAt(t, m.View().Content, label)
	if label == noPresetLabel && !m.presetMenu.open {
		x, _ = m.presetTagBounds()
		y = m.listSeparatorY()
	}
	return updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func renderedPresetMenuRows(m tuiModel) []string {
	l := m.presetMenuGeometry()
	lines := strings.Split(ansi.Strip(m.View().Content), nl)
	rows := make([]string, 0, l.rows)
	for y := l.y + 1; y < l.y+1+l.rows; y++ {
		rows = append(rows, strings.TrimSpace(ansi.Cut(lines[y], l.x+1, l.x+l.width-1)))
	}
	return rows
}

func TestPresetMenuClickSelectsAndPersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	failing := approvalTestPR(1)
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", failing, approvalTestPR(2))
	m.cfg = &Config{}
	m.cli.preset = "Needs your review"
	m.header = "Pull requests"
	m.filterInput = textinput.New()
	m.filterInput.Focus()
	_, labelY := buttonAt(t, m.View().Content, "Needs your review")

	m, cmd := clickPresetText(t, m, "Needs your review")
	require.Nil(t, cmd)
	require.True(t, m.presetMenu.open)
	menu := m.View().Content
	for _, preset := range defaultPresets {
		_, y := buttonAt(t, menu, preset.Name)
		require.Less(t, y, labelY, "the menu opens above the label")
	}
	require.Equal(t, []string{
		"All PRs",
		"✓ Needs your review",
		"Waiting for CI",
		"Approved by you",
		"Mergeable",
		"Outdated",
		"Blocked",
	}, renderedPresetMenuRows(m))

	m, cmd = clickPresetText(t, m, "Blocked")
	require.Nil(t, cmd, "selecting a smart filter does not refetch")
	require.False(t, m.presetMenu.open)
	require.Equal(t, "Blocked", m.cli.preset)
	require.True(t, isShown(m, makePRKey(failing)))
	require.False(t, isShown(m, makePRKey(approvalTestPR(2))))
	st, err := loadTUIState()
	require.NoError(t, err)
	require.Equal(t, "Blocked", st.Filters.Preset)

	m, _ = clickPresetText(t, m, "Blocked")
	m, _ = clickPresetText(t, m, "All PRs")
	require.Empty(t, m.cli.preset)
	require.Empty(t, m.cfg.TUI.Filters.Preset)
	require.Len(t, m.visibleIndices(), 2)
	m, _ = clickPresetText(t, m, "All PRs")
	require.True(t, m.presetMenu.open, "the menu can be reopened without a selected filter")
}

func TestPresetMenuDismissesWithoutChangingSelection(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2))
	m.cfg = &Config{}
	m.cli.preset = "Blocked"
	m, _ = clickPresetText(t, m, "Blocked")
	m, cmd := updateModel(t, m, tea.MouseClickMsg{X: 0, Y: 1, Button: tea.MouseLeft})
	require.Nil(t, cmd)
	require.False(t, m.presetMenu.open)
	require.Equal(t, "Blocked", m.cli.preset)
	require.Zero(t, m.cursor, "an outside click does not reach the list underneath")

	m, _ = clickPresetText(t, m, "Blocked")
	m, cmd = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, cmd, "Escape dismisses the menu without quitting")
	require.False(t, m.presetMenu.open)
	require.Equal(t, "Blocked", m.cli.preset)
}

func TestPresetMenuRespectsConfiguredAvailability(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.cfg.TUI.Presets.Filters = []TUIPresetConfig{
		{Name: "Other authors", Filter: "!mine", When: presetNotSelf},
		{Name: "\u5f85\u6a5f\u4e2d", Filter: "ci:pending"},
	}
	m.cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	m.cli.preset = "Other authors"
	m.width = 16
	m, _ = clickPresetText(t, m, "All PRs")
	content := ansi.Strip(m.View().Content)
	require.Equal(t, []string{"✓ All PRs", "\u5f85\u6a5f\u4e2d"}, renderedPresetMenuRows(m))
	for line := range strings.SplitSeq(content, nl) {
		require.LessOrEqual(t, ansi.StringWidth(line), m.width)
	}
	m, _ = clickPresetText(t, m, "\u5f85\u6a5f\u4e2d")
	require.Equal(t, "\u5f85\u6a5f\u4e2d", m.cli.preset)
	m, _ = clickPresetText(t, m, "\u5f85\u6a5f\u4e2d")
	require.True(t, m.presetMenu.open)
}

func TestPresetMenuScrollsInShortTerminal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2))
	m.cfg = &Config{}
	for i := range 20 {
		m.cfg.TUI.Presets.Filters = append(m.cfg.TUI.Presets.Filters,
			TUIPresetConfig{Name: fmt.Sprintf("Filter %02d", i), Filter: "draft"})
	}
	m.height = 10
	m.cli.preset = "Filter 00"
	m, _ = clickPresetText(t, m, "Filter 00")
	before := renderedPresetMenuRows(m)
	expected := make([]string, 0, len(before))
	for i := range before {
		if i == 0 {
			expected = append(expected, "All PRs")
			continue
		}
		prefix := ""
		if i == 1 {
			prefix = "✓ "
		}
		expected = append(expected, fmt.Sprintf("%sFilter %02d", prefix, i-1))
	}
	require.Equal(t, expected, before)
	m, _ = updateModel(t, m, scrollwheel.Msg[wheelTarget]{Target: wheelTargetPresetMenu, Delta: 19})
	require.Zero(t, m.cursor, "scrolling the menu leaves the list cursor alone")
	expected = expected[:0]
	for i := 20 - len(before); i < 20; i++ {
		expected = append(expected, fmt.Sprintf("Filter %02d", i))
	}
	require.Equal(t, expected, renderedPresetMenuRows(m))
	_, y := buttonAt(t, m.View().Content, "Filter 19")
	require.GreaterOrEqual(t, y, 0)
	m, _ = clickPresetText(t, m, "Filter 19")
	require.Equal(t, "Filter 19", m.cli.preset)
	require.False(t, m.presetMenu.open)
}

func TestPresetMenuKeyboardAndResize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m, _ = clickPresetText(t, m, "All PRs")
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "Waiting for CI", m.cli.preset)
	require.False(t, m.presetMenu.open)
	m, _ = clickPresetText(t, m, "Waiting for CI")
	m, _ = pressKey(t, m, tuiKeybindCyclePresetBack)
	require.Equal(t, "Needs your review", m.cli.preset)
	require.False(t, m.presetMenu.open)
	m, _ = clickPresetText(t, m, "Needs your review")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 3, Height: 3})
	require.False(t, m.presetMenu.open)
}

func TestPresetMenuDoesNotOpenOverModal(t *testing.T) {
	for _, modal := range []string{"help", "options", "confirm"} {
		t.Run(modal, func(t *testing.T) {
			m := approvalTestModel("", approvalTestPR(1))
			m.cfg = &Config{}
			x, y := buttonAt(t, m.View().Content, "All PRs")
			switch modal {
			case "help":
				m.showHelp = true
			case "options":
				m.showOptions = true
			case "confirm":
				m.confirmAction = tuiActionInfo
			}
			m, _ = updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			require.False(t, m.presetMenu.open)
		})
	}
}

func TestPresetControlsHoverAndCaretClicks(t *testing.T) {
	for _, timeFilter := range []bool{false, true} {
		m := approvalTestModel("", approvalTestPR(1))
		m.cfg = &Config{}
		require.Equal(t, tea.MouseModeAllMotion, m.View().MouseMode)
		base := m.renderListSeparator()
		_, end := m.presetTagBounds()
		if timeFilter {
			_, end = m.timeTagBounds()
		}
		y := m.listSeparatorY()
		m, _ = updateModel(t, m, tea.MouseMotionMsg{X: end - 1, Y: y})
		require.Equal(t, hoverState{time: timeFilter, pr: !timeFilter}, m.hover)
		require.NotEqual(t, base, m.renderListSeparator())
		require.Equal(t, ansi.Strip(base), ansi.Strip(m.renderListSeparator()))
		m, _ = updateModel(t, m, tea.MouseMotionMsg{X: 0, Y: 0})
		require.Equal(t, base, m.renderListSeparator())
		m, _ = updateModel(t, m, tea.MouseMotionMsg{X: end - 1, Y: y})
		m, _ = updateModel(t, m, tea.MouseClickMsg{X: end - 1, Y: y, Button: tea.MouseLeft})
		require.True(t, m.presetMenu.open, "the caret opens the dropdown")
		require.Equal(t, timeFilter, m.presetMenu.timeFilter)
		require.Equal(t, hoverState{}, m.hover)
	}
}

func TestPresetMenuUnknownTimePresetChecksAllTime(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.cli.timePreset = "Removed"
	m.openFilterPresetMenu(true)
	require.Equal(t, []string{
		"✓ All Time", "10 minutes", "1 hour", "1 day", "1 week", "1 month", "1 year",
	}, renderedPresetMenuRows(m))
}

func TestPresetHoverPreservesIdleRefreshTimer(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	idleSince := time.Now().Add(-time.Hour)
	m.lastInteraction = idleSince
	start, _ := m.presetTagBounds()
	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: start, Y: m.listSeparatorY()})
	require.True(t, m.hover.pr)
	require.Equal(t, idleSince, m.lastInteraction, "hover alone keeps the idle timer")
	m, _ = updateModel(
		t,
		m,
		tea.MouseMotionMsg{X: start, Y: m.listSeparatorY(), Button: tea.MouseLeft},
	)
	require.True(t, m.lastInteraction.After(idleSince), "dragging is an interaction")
}

func TestPresetMenusCenterOverTheirControls(t *testing.T) {
	for _, timeFilter := range []bool{false, true} {
		m := approvalTestModel("", approvalTestPR(1))
		m.cfg = &Config{}
		if timeFilter {
			m.cli.timePreset = "10 minutes"
		}
		m.openFilterPresetMenu(timeFilter)
		start, end := m.presetTagBounds()
		if timeFilter {
			start, end = m.timeTagBounds()
		}
		menu := m.presetMenuGeometry()
		require.InDelta(t, float64(start+end)/2, float64(menu.x)+float64(menu.width)/2, 0.5)
		require.GreaterOrEqual(t, menu.x, 0)
		require.LessOrEqual(t, menu.x+menu.width, m.width)
	}
}

func TestPresetMenuCenteringClampsToTerminal(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	// A long time label moves the PR control near the left edge.
	name := strings.Repeat("t", 61)
	m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{{Name: name, MaxAge: "1h"}}
	m.cli.timePreset = name
	m.openPresetMenu()
	require.Equal(t, 0, m.presetMenuGeometry().x)
	// A long menu entry extends past the right edge of a short time control.
	m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{
		{Name: strings.Repeat("t", 30), MaxAge: "1h"},
	}
	m.cli.timePreset = ""
	m.openFilterPresetMenu(true)
	menu := m.presetMenuGeometry()
	require.Equal(t, m.width, menu.x+menu.width)
}

func TestPresetMenuHoverHighlightsWithoutSelecting(t *testing.T) {
	for _, timeFilter := range []bool{false, true} {
		m := approvalTestModel("", approvalTestPR(1))
		m.cfg = &Config{}
		m.openFilterPresetMenu(timeFilter)
		before := m.View().Content
		menu := m.presetMenuGeometry()
		m, _ = updateModel(t, m, tea.MouseMotionMsg{X: menu.x + 2, Y: menu.y + 2})
		require.Equal(t, 1, m.presetMenu.cursor)
		after := m.View().Content
		require.NotEqual(t, before, after, "hover moves the row highlight")
		require.Equal(
			t,
			ansi.Strip(before),
			ansi.Strip(after),
			"hover keeps labels and check marks",
		)
		require.Empty(t, m.cli.preset)
		require.Empty(t, m.cli.timePreset)
	}
}

func TestPresetKeysKeepTheirMeaningInEitherMenu(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, timeMenu := range []bool{false, true} {
		for _, tc := range []struct {
			key  string
			pr   string
			time string
		}{
			{key: tuiKeybindCyclePreset, pr: "Needs your review"},
			{key: tuiKeybindCyclePresetBack, pr: "Blocked"},
			{key: tuiKeybindCycleTimePreset, time: "10 minutes"},
			{key: tuiKeybindCycleTimePresetBack, time: "1 year"},
		} {
			m := approvalTestModel("", approvalTestPR(1))
			m.cfg = &Config{}
			m.openFilterPresetMenu(timeMenu)
			m, cmd := pressKey(t, m, tc.key)
			require.Nil(t, cmd)
			require.Equal(t, tc.pr, m.cli.preset)
			require.Equal(t, tc.time, m.cli.timePreset)
			require.False(t, m.presetMenu.open)
		}
	}
}

func TestPresetHoverClearsOnKeysAndResize(t *testing.T) {
	for _, timeControl := range []bool{false, true} {
		for _, msg := range []tea.Msg{
			tea.KeyPressMsg{Code: 'z', Text: "z"},
			tea.WindowSizeMsg{Width: 120, Height: 30},
		} {
			m := approvalTestModel("", approvalTestPR(1))
			m.cfg = &Config{}
			start, _ := m.presetTagBounds()
			if timeControl {
				start, _ = m.timeTagBounds()
			}
			m, _ = updateModel(t, m, tea.MouseMotionMsg{X: start, Y: m.listSeparatorY()})
			require.NotEqual(t, hoverState{}, m.hover)
			m, _ = updateModel(t, m, msg)
			require.Equal(t, hoverState{}, m.hover)
		}
	}
}

func TestTimeMenuClosesWhenItsControlDisappears(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.openFilterPresetMenu(true)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 21, Height: 20})
	require.False(t, m.presetMenu.open)
}

func TestFilterTagsHoverAndClickOpenOptions(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.cli.NoBot = false
	start, end := m.filterTagsBounds()
	require.Equal(t, start, end, "no option filters, no target")

	m.cli.State, m.cli.stateExplicit = valueClosed, true
	start, end = m.filterTagsBounds()
	y := m.listSeparatorY()
	base := m.renderListSeparator()
	require.Equal(t, "state:closed", ansi.Cut(ansi.Strip(base), start, end))

	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: start, Y: y})
	require.Equal(t, hoverState{filters: true}, m.hover)
	hovered := m.renderListSeparator()
	require.NotEqual(t, base, hovered, "hover highlights the tags")
	require.Equal(t, ansi.Strip(base), ansi.Strip(hovered))

	m, _ = updateModel(t, m, tea.MouseClickMsg{X: end - 1, Y: y, Button: tea.MouseLeft})
	require.True(t, m.showOptions, "clicking the tags opens the filter dialog")
	require.Equal(t, hoverState{}, m.hover)

	m, _ = updateModel(t, m, tea.MouseClickMsg{X: start, Y: y, Button: tea.MouseLeft})
	require.False(t, m.showOptions, "clicking the tags again closes it, like Shift+O")
}

func TestRowHoverHighlightsWithoutMovingCursor(t *testing.T) {
	t.Cleanup(func() { initPalette(false) })
	for _, light := range []bool{false, true} {
		initPalette(light)
		require.NotEqual(t, cursorLineBG, rowHoverBG, "hover differs from the cursor line")
		m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2), approvalTestPR(3))
		m.cfg = &Config{}
		m.header = "PR"
		base := m.View().Content
		require.False(t, strings.HasPrefix(strings.Split(base, nl)[2], rowHoverBG))

		m, _ = updateModel(t, m, tea.MouseMotionMsg{X: 5, Y: 2})
		require.Equal(t, hoverState{rowY: 2, rowOK: true}, m.hover)
		require.Equal(t, 0, m.cursor, "hover leaves the cursor alone")
		line := strings.Split(m.View().Content, nl)[2]
		require.True(t, strings.HasPrefix(line, rowHoverBG), "the hovered row is tinted")
		require.Equal(t, "2", strings.TrimSpace(ansi.Strip(line)))

		m, _ = updateModel(t, m, tea.MouseMotionMsg{X: 5, Y: m.listSeparatorY()})
		require.Equal(t, hoverState{}, m.hover, "lines below the list are not rows")
		require.Equal(t, base, m.View().Content)
	}
}

func TestRowHoverFollowsTheRowUnderThePointer(t *testing.T) {
	prs := make([]PullRequest, 30)
	for i := range prs {
		prs[i] = approvalTestPR(i + 1)
	}
	m := approvalTestModel("", prs...)
	m.cfg = &Config{}
	m.header, m.height = "PR", 12
	m, _ = updateModel(t, m, tea.MouseMotionMsg{X: 5, Y: 2})
	m, _ = updateModel(t, m, scrollwheel.Msg[wheelTarget]{Target: wheelTargetList, Delta: 12})
	require.Positive(t, m.offset)
	want, ok := m.rowIndexAt(2)
	require.True(t, ok)
	got, ok := m.hoveredRow()
	require.True(t, ok)
	require.Equal(t, want, got, "the highlight stays under the pointer after scrolling")
	require.True(t, strings.HasPrefix(strings.Split(m.View().Content, nl)[2], rowHoverBG))
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
