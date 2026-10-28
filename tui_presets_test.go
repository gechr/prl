package main

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/scrollwheel"
	"github.com/stretchr/testify/require"
)

func clickPresetText(t *testing.T, m tuiModel, label string) (tuiModel, tea.Cmd) {
	t.Helper()
	x, y := buttonAt(t, m.View().Content, label)
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
	m.cli.preset = "Approvable"
	m.header = "Pull requests"
	m.filterInput = textinput.New()
	m.filterInput.Focus()
	_, labelY := buttonAt(t, m.View().Content, "Approvable")

	m, cmd := clickPresetText(t, m, "Approvable")
	require.Nil(t, cmd)
	require.True(t, m.presetMenu.open)
	menu := m.View().Content
	for _, preset := range defaultPresets {
		_, y := buttonAt(t, menu, preset.Name)
		require.Less(t, y, labelY, "the menu opens above the label")
	}
	require.Equal(t, []string{
		"All",
		"✓ Approvable",
		"Waiting for CI",
		"Approved by me",
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
	m, _ = clickPresetText(t, m, "All")
	require.Empty(t, m.cli.preset)
	require.Empty(t, m.cfg.TUI.Filters.Preset)
	require.Len(t, m.visibleIndices(), 2)
	m, _ = clickPresetText(t, m, "All")
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
	m, _ = clickPresetText(t, m, "All")
	content := ansi.Strip(m.View().Content)
	require.Equal(t, []string{"✓ All", "\u5f85\u6a5f\u4e2d"}, renderedPresetMenuRows(m))
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
			expected = append(expected, "All")
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
	m, _ = clickPresetText(t, m, "All")
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "Waiting for CI", m.cli.preset)
	require.False(t, m.presetMenu.open)
	m, _ = clickPresetText(t, m, "Waiting for CI")
	m, _ = pressKey(t, m, tuiKeybindCyclePresetBack)
	require.Equal(t, "Approvable", m.cli.preset)
	require.False(t, m.presetMenu.open)
	m, _ = clickPresetText(t, m, "Approvable")
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 3, Height: 3})
	require.False(t, m.presetMenu.open)
}

func TestPresetMenuDoesNotOpenOverModal(t *testing.T) {
	for _, modal := range []string{"help", "options", "confirm"} {
		t.Run(modal, func(t *testing.T) {
			m := approvalTestModel("", approvalTestPR(1))
			m.cfg = &Config{}
			x, y := buttonAt(t, m.View().Content, "All")
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
