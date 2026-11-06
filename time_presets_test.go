package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/clib/theme"
	"github.com/gechr/primer/scrollwheel"
	"github.com/stretchr/testify/require"
)

func TestTimePresetMenuFiltersUpdatedAndPersists(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now()
	recent, stale := approvalTestPR(1), approvalTestPR(2)
	recent.CreatedAt, recent.UpdatedAt = now.Add(-365*24*time.Hour), now
	stale.CreatedAt, stale.UpdatedAt = now, now.Add(-time.Hour)
	m := approvalTestModel("", recent, stale)
	m.cfg = &Config{}
	m.cli.NoBot = false
	line := ansi.Strip(m.renderListSeparator())
	require.Equal(t, strings.Repeat("─", 53)+"┤ All Time ▾ │ All PRs ▾ ├─", line)
	require.Len(t, m.visibleIndices(), 2)
	m, _ = clickPresetText(t, m, "All Time")
	require.True(t, m.presetMenu.open)
	require.True(t, m.presetMenu.timeFilter)
	start, end := m.timeTagBounds()
	menu := m.presetMenuGeometry()
	require.InDelta(t, float64(start+end)/2, float64(menu.x)+float64(menu.width)/2, 0.5)
	prStart, _ := m.presetTagBounds()
	require.Equal(t, " │ ", ansi.Cut(line, end, prStart))
	require.Equal(t, []string{
		"✓ All Time", "10 minutes", "1 hour", "1 day", "1 week", "1 month", "1 year",
	}, renderedPresetMenuRows(m))
	m, cmd := clickPresetText(t, m, "10 minutes")
	require.Nil(t, cmd, "time filters apply locally")
	require.False(t, m.presetMenu.open)
	require.Equal(t, []int{0}, m.visibleIndices(), "updated time determines visibility")
	require.Equal(
		t,
		strings.Repeat("─", 51)+"┤ 10 minutes ▾ │ All PRs ▾ ├─",
		ansi.Strip(m.renderListSeparator()),
	)
	st, err := loadTUIState()
	require.NoError(t, err)
	cfg := &Config{}
	applyTUIState(cfg, st)
	cli := testCLI()
	applyTUIFilterDefaults(cli, cfg)
	require.Equal(t, "10 minutes", cli.timePreset)
	m, _ = clickPresetText(t, m, "10 minutes")
	m, _ = clickPresetText(t, m, "All Time")
	require.Empty(t, m.cli.timePreset)
	require.Len(t, m.visibleIndices(), 2)
	st, err = loadTUIState()
	require.NoError(t, err)
	require.Empty(t, st.Filters.Time)
}

func TestTimePresetCombinesWithStatusPreset(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Now()
	recent, stale, failing := approvalTestPR(1), approvalTestPR(2), approvalTestPR(3)
	recent.UpdatedAt, stale.UpdatedAt, failing.UpdatedAt = now, now.Add(-2*time.Hour), now
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", recent, stale, failing)
	m.cfg = &Config{}
	m, _ = pressKey(t, m, tuiKeybindCyclePreset)
	m, _ = clickPresetText(t, m, "All Time")
	m, _ = clickPresetText(t, m, "1 hour")
	require.Equal(t, []int{0}, m.visibleIndices())
	require.Equal(t, "Needs your review", m.cli.preset)
	m, _ = clickPresetText(t, m, "Needs your review")
	require.False(t, m.presetMenu.timeFilter)
	m, _ = clickPresetText(t, m, "All PRs")
	require.Equal(t, []int{0, 2}, m.visibleIndices(), "clearing status keeps the time filter")
}

func TestTimePresetUsesConfiguredFieldAndBoundary(t *testing.T) {
	now := time.Now()
	pr := approvalTestPR(1)
	pr.CreatedAt, pr.UpdatedAt = now.Add(-2*time.Hour), now
	m := approvalTestModel("", pr)
	m.cfg = &Config{}
	m.cfg.TUI.Presets.Time.Field = "created"
	m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{{Name: "Recent", MaxAge: "1h"}}
	m.cli.timePreset = "Recent"
	require.Empty(t, m.visibleIndices())
	require.Equal(t, "Recent", m.timeTagLabel())
	df := m.displayFilter()
	pr.CreatedAt = df.cutoff
	require.True(t, m.shows(df, PRRowModel{PR: pr}), "the cutoff is inclusive")
	pr.CreatedAt = df.cutoff.Add(-time.Nanosecond)
	require.False(t, m.shows(df, PRRowModel{PR: pr}))
}

func TestTimePresetMenuKeyboardScrollAndResize(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.height = 10
	m, _ = clickPresetText(t, m, m.timeTagLabel())
	require.True(t, m.presetMenu.open)
	m, _ = updateModel(t, m, scrollwheel.Msg[wheelTarget]{Target: wheelTargetPresetMenu, Delta: 6})
	require.Equal(t, 6, m.presetMenu.cursor)
	require.Contains(t, renderedPresetMenuRows(m), "1 year")
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "1 year", m.cli.timePreset)
	m, _ = clickPresetText(t, m, m.timeTagLabel())
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, m.presetMenu.open)
	require.Equal(t, "1 year", m.cli.timePreset)
	m, _ = clickPresetText(t, m, m.timeTagLabel())
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 3, Height: 3})
	require.False(t, m.presetMenu.open)
}

func TestLoadConfigTimePresets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(t, os.WriteFile(cp, []byte(`tui:
  filters:
    time: Fortnight
  presets:
    time:
      field: created
      filters:
        - name: Fortnight
          max_age: 2w
          color: blue
`), 0o600))
	cfg, err := loadConfig()
	require.NoError(t, err)
	require.Equal(t, "created", configuredTimeField(cfg))
	require.Equal(
		t,
		[]TUITimePresetConfig{{Name: "Fortnight", MaxAge: "2w", Color: "blue"}},
		configuredTimePresets(cfg),
	)
	cli := testCLI()
	applyTUIFilterDefaults(cli, cfg)
	require.Equal(t, "Fortnight", cli.timePreset)
}

func TestValidateTimePresets(t *testing.T) {
	require.NoError(t, validateTimePresets(TUITimePresetsConfig{}))
	require.NoError(t, validateTimePresets(TUITimePresetsConfig{Filters: defaultTimePresets}))
	for _, cfg := range []TUITimePresetsConfig{
		{Field: "merged"},
		{Filters: []TUITimePresetConfig{{Name: "", MaxAge: "1w"}}},
		{Filters: []TUITimePresetConfig{{Name: allTimeLabel, MaxAge: "1w"}}},
		{Filters: []TUITimePresetConfig{{Name: "Same", MaxAge: "1w"}, {Name: "Same", MaxAge: "1d"}}},
		{Filters: []TUITimePresetConfig{{Name: "Bad", MaxAge: "0"}}},
		{Filters: []TUITimePresetConfig{{Name: "Bad", MaxAge: "-1w"}}},
		{Filters: []TUITimePresetConfig{{Name: "Bad", MaxAge: "invalid"}}},
		{Filters: []TUITimePresetConfig{{Name: "Bad", MaxAge: "1w", Color: "invalid"}}},
	} {
		require.Error(t, validateTimePresets(cfg))
	}
}

func TestTimePresetColoursFollowTableTheme(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	p := *testPRL
	p.theme = theme.Dark()
	m.p = &p
	for _, preset := range defaultTimePresets {
		t.Run(preset.Name, func(t *testing.T) {
			age, err := parseTimePresetAge(preset.MaxAge)
			require.NoError(t, err)
			tableTime := p.theme.RenderTimeAgo(time.Now().Add(-age), true)
			expected := strings.Replace(tableTime, ansi.Strip(tableTime), preset.Name, 1)
			require.Equal(t, expected, m.timePresetStyle(preset).Render(preset.Name))
			m.cli.timePreset = preset.Name
			require.Equal(
				t,
				m.timePresetStyle(preset).
					Bold(true).
					Render(preset.Name)+
					m.styles.separator.Render(
						presetControlSuffix,
					),
				m.renderTimeTag(),
			)
		})
	}
	custom := TUITimePresetConfig{Name: "Custom", MaxAge: "1w", Color: "blue"}
	require.Equal(t, lg.Color("4"), m.timePresetStyle(custom).GetForeground())
}

func TestTimePresetHitboxesFollowLabels(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	for _, width := range []int{24, 40, 80} {
		m.width = width
		for _, preset := range []string{"", "Needs your review"} {
			m.cli.preset = preset
			line := ansi.Strip(m.renderListSeparator())
			require.Equal(t, width, ansi.StringWidth(line))
			start, end := m.timeTagBounds()
			prStart, prEnd := m.presetTagBounds()
			if start != end {
				require.Equal(t, allTimeLabel+presetControlSuffix, ansi.Cut(line, start, end))
				require.Equal(t, timePresetSeparator, ansi.Cut(line, end, prStart))
				require.True(t, m.timeTagAt(start, m.listSeparatorY()))
				require.False(t, m.presetTagAt(start, m.listSeparatorY()))
			}
			label := noPresetLabel
			if preset != "" {
				label = preset
			}
			require.Equal(t, label+presetControlSuffix, ansi.Cut(line, prStart, prEnd))
			require.False(t, m.timeTagAt(prStart, m.listSeparatorY()))
			require.True(t, m.presetTagAt(prStart, m.listSeparatorY()))
		}
	}
}

func TestCycleTimePresets(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.cli.preset = "Needs your review"
	for _, preset := range defaultTimePresets {
		var cmd tea.Cmd
		m, cmd = pressKey(t, m, tuiKeybindCycleTimePreset)
		require.Nil(t, cmd)
		require.Equal(t, preset.Name, m.cli.timePreset)
		require.Equal(t, "Needs your review", m.cli.preset)
	}
	m, _ = pressKey(t, m, tuiKeybindCycleTimePreset)
	require.Empty(t, m.cli.timePreset)
	m, _ = pressKey(t, m, tuiKeybindCycleTimePresetBack)
	require.Equal(t, "1 year", m.cli.timePreset)
	st, err := loadTUIState()
	require.NoError(t, err)
	require.Equal(t, "1 year", st.Filters.Time)
	m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{{Name: "Fortnight", MaxAge: "2w"}}
	m, _ = pressKey(t, m, tuiKeybindCycleTimePreset)
	require.Equal(t, "Fortnight", m.cli.timePreset, "an unknown selection cycles from All Time")
	m, _ = pressKey(t, m, tuiKeybindCycleTimePresetBack)
	require.Empty(t, m.cli.timePreset)
}

func TestClearFilterPresets(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, openMenu := range []bool{false, true} {
		pr := approvalTestPR(1)
		pr.UpdatedAt = time.Now().Add(-2 * time.Hour)
		m := approvalTestModel("", pr)
		m.cfg = &Config{}
		m.cli.preset, m.cli.timePreset = "Needs your review", "10 minutes"
		m.cfg.TUI.Filters.Preset, m.cfg.TUI.Filters.Time = m.cli.preset, m.cli.timePreset
		m.cli.NoBot = true
		require.Empty(t, m.visibleIndices())
		if openMenu {
			m.openFilterPresetMenu(true)
		}
		m, cmd := updateModel(t, m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
		require.Nil(t, cmd)
		require.False(t, m.presetMenu.open)
		require.Empty(t, m.cli.preset)
		require.Empty(t, m.cli.timePreset)
		require.True(t, m.cli.NoBot, "other filters keep their values")
		require.Equal(t, []int{0}, m.visibleIndices())
		st, err := loadTUIState()
		require.NoError(t, err)
		require.Empty(t, st.Filters.Preset)
		require.Empty(t, st.Filters.Time)
	}
}

func TestActiveTimePresetStaysVisibleInNarrowTerminal(t *testing.T) {
	now := time.Now()
	recent, stale := approvalTestPR(1), approvalTestPR(2)
	recent.UpdatedAt, stale.UpdatedAt = now, now.Add(-time.Hour)
	m := approvalTestModel("", recent, stale)
	m.cfg = &Config{}
	m.cli.preset, m.cli.timePreset = "Needs your review", "10 minutes"
	for _, tc := range []struct {
		width     int
		line      string
		prLabel   string
		timeLabel string
	}{
		{39, "┤ 10 minutes ▾ │ Needs your review ▾ ├─", "Needs your review ▾", "10 minutes ▾"},
		{35, "───┤ 10m ▾ │ Needs your review ▾ ├─", "Needs your review ▾", "10m ▾"},
		{32, "┤ 10m ▾ │ Needs your review ▾ ├─", "Needs your review ▾", "10m ▾"},
		{20, "┤ 10m ▾ │ Need… ▾ ├─", "Need… ▾", "10m ▾"},
		{16, "┤ 10m ▾ │ … ▾ ├─", "… ▾", "10m ▾"},
		{15, "─────┤ 10m ▾ ├─", "", "10m ▾"},
		{10, "┤ 10m ▾ ├─", "", "10m ▾"},
		{7, "┤ 1… ├─", "", "1…"},
		{6, "┤ … ├─", "", "…"},
	} {
		m.width = tc.width
		line := ansi.Strip(m.renderListSeparator())
		require.Equal(t, tc.line, line, "width=%d", tc.width)
		prStart, prEnd := m.presetTagBounds()
		timeStart, timeEnd := m.timeTagBounds()
		require.Equal(t, tc.prLabel, ansi.Cut(line, prStart, prEnd))
		require.Equal(t, tc.timeLabel, ansi.Cut(line, timeStart, timeEnd))
		require.Equal(t, []int{0}, m.visibleIndices())

		m, _ = updateModel(t, m, tea.MouseClickMsg{
			X: timeStart, Y: m.listSeparatorY(), Button: tea.MouseLeft,
		})
		require.True(t, m.presetMenu.open)
		require.True(t, m.presetMenu.timeFilter)
		require.Equal(t, "10 minutes", m.menuSelection())
		m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	}
}

func TestTimePresetLabelsCompressBeforePRLabel(t *testing.T) {
	for _, tc := range []struct {
		name, compact string
	}{
		{"10 seconds", "10s"},
		{"10 minutes", "10m"},
		{"2 hours", "2h"},
		{"2 days", "2d"},
		{"2 weeks", "2w"},
		{"2 months", "2mo"},
		{"2 years", "2y"},
		{"1 hour", "1h"},
		{"1 day", "1d"},
		{"1 week", "1w"},
		{"1 month", "1mo"},
		{"1 year", "1y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := approvalTestModel("", approvalTestPR(1))
			m.cfg = &Config{}
			m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{
				{Name: tc.name, MaxAge: "1h"},
			}
			m.cli.preset, m.cli.timePreset = "Needs your review", tc.name
			m.cli.NoBot = false
			fullWidth := ansi.StringWidth("┤ " + tc.name + " ▾ │ Needs your review ▾ ├─")
			compactWidth := ansi.StringWidth("┤ " + tc.compact + " ▾ │ Needs your review ▾ ├─")
			for _, width := range []int{fullWidth, fullWidth - 1, compactWidth} {
				label := tc.compact
				if width == fullWidth {
					label = tc.name
				}
				expected := "┤ " + label + " ▾ │ Needs your review ▾ ├─"
				expected = strings.Repeat("─", width-ansi.StringWidth(expected)) + expected
				m.width = width
				line := ansi.Strip(m.renderListSeparator())
				require.Equal(t, expected, line)
				start, end := m.timeTagBounds()
				require.Equal(t, label+presetControlSuffix, ansi.Cut(line, start, end))
				require.True(t, m.timeTagAt(start, m.listSeparatorY()))
				require.False(t, m.presetTagAt(start, m.listSeparatorY()))
			}
			m, _ = clickPresetText(t, m, tc.compact)
			require.True(t, m.presetMenu.timeFilter)
			require.Contains(t, renderedPresetMenuRows(m), "✓ "+tc.name)
			require.Equal(t, tc.name, m.cli.timePreset)
		})
	}
}

func TestCustomTimePresetLabelKeepsItsName(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{
		{Name: "Last 10 minutes", MaxAge: "10m"},
	}
	m.cli.preset, m.cli.timePreset = "Needs your review", "Last 10 minutes"
	m.width = 35
	line := ansi.Strip(m.renderListSeparator())
	start, end := m.timeTagBounds()
	require.Equal(t, "Last 10 minutes ▾", ansi.Cut(line, start, end))
	m, _ = clickPresetText(t, m, "Last 10 minutes")
	require.True(t, m.presetMenu.timeFilter)
	require.Contains(t, renderedPresetMenuRows(m), "✓ Last 10 minutes")
}
