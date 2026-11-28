package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	lg "charm.land/lipgloss/v2"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/clib/theme"
	"github.com/stretchr/testify/require"
)

func TestTimePresetMenuFiltersUpdatedAndPersists(t *testing.T) {
	now := time.Now()
	recent, stale := approvalTestPR(1), approvalTestPR(2)
	recent.Title, stale.Title = "Active change", "Older change"
	recent.CreatedAt, recent.UpdatedAt = now.Add(-365*24*time.Hour), now
	stale.CreatedAt, stale.UpdatedAt = now, now.Add(-time.Hour)
	m := listTUIModel(recent, stale)
	m.cli.NoBot = false
	h := newTUIHarness(t, m)
	h.Wait(strings.Repeat("─", 53) + "┤ All Time ▾ │ All PRs ▾ ├─").
		Assert([]string{recent.Title, stale.Title})
	control := h.Locate(reel.Contains("All Time ▾"))
	control.LeftClick()
	assertPresetRows(
		t,
		h,
		[]string{"✓ All Time", "10 minutes", "1 hour", "1 day", "1 week", "1 month", "1 year"},
	)
	left, right := h.Locate(reel.Contains("╭─")), h.Locate(reel.Contains("─╮")).Offset(1, 0)
	require.InDelta(
		t,
		float64(2*control.X+ansi.StringWidth("All Time ▾")-1)/2,
		float64(left.X+right.X)/2,
		0.5,
	)
	h.Locate(reel.Contains("10 minutes")).LeftClick().
		Wait(strings.Repeat("─", 51) + "┤ 10 minutes ▾ │ All PRs ▾ ├─").
		Assert(recent.Title).AssertNot(stale.Title)
	st, err := loadTUIState()
	require.NoError(t, err)
	cfg := &Config{}
	applyTUIState(cfg, st)
	cli := testCLI()
	applyTUIFilterDefaults(cli, cfg)
	require.Equal(t, "10 minutes", cli.timePreset)
	h.Locate(reel.Contains("10 minutes ▾")).LeftClick().Wait("✓ 10 minutes")
	h.Locate(reel.Contains("All Time")).LeftClick().Wait("All Time ▾").Assert(stale.Title)
	st, err = loadTUIState()
	require.NoError(t, err)
	require.Empty(t, st.Filters.Time)
}

func TestTimePresetCombinesWithStatusPreset(t *testing.T) {
	now := time.Now()
	recent, stale, failing := approvalTestPR(1), approvalTestPR(2), approvalTestPR(3)
	recent.Title, stale.Title, failing.Title = "Recent review", "Older review", "Failing check"
	recent.UpdatedAt, stale.UpdatedAt, failing.UpdatedAt = now, now.Add(-2*time.Hour), now
	failing.MergeStatus = MergeStatusCIFailed
	h := newTUIHarness(t, listTUIModel(recent, stale, failing))
	h.Key("f").Wait("Needs your review ▾")
	h.Locate(reel.Contains("All Time ▾")).LeftClick().Wait("✓ All Time")
	h.Locate(reel.Contains("1 hour")).LeftClick().Wait("1 hour ▾ │ Needs your review ▾").
		Assert(recent.Title).AssertNot([]string{stale.Title, failing.Title})
	h.Locate(reel.Contains("Needs your review ▾")).LeftClick().Wait("✓ Needs your review")
	h.Locate(reel.Contains("All PRs")).LeftClick().Wait("1 hour ▾ │ All PRs ▾").
		Assert([]string{recent.Title, failing.Title}).AssertNot(stale.Title)
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
	m := listTUIModel(approvalTestPR(1))
	m.height = 10
	h := newTUIHarness(t, m)
	h.Locate(reel.Contains("All Time ▾")).LeftClick().Wait("✓ All Time")
	h.Locate(reel.Contains("✓ All Time")).Wheel(vt.MouseWheelDown, reel.Repeat(6)).
		Wait("1 year").Key("enter").Wait("1 year ▾")
	h.Locate(reel.Contains("1 year ▾")).LeftClick().Wait("✓ 1 year").
		KeyEsc().WaitNot("✓ 1 year").Assert("1 year ▾")
	h.Locate(reel.Contains("1 year ▾")).LeftClick().Wait("✓ 1 year")
	h.Resize(3, 3).WaitNot("1 year ▾").Resize(80, 20).Wait("1 year ▾").AssertNot("✓ 1 year")
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
				m.presetControls().time,
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
			line := testListSeparator(&m)
			require.Equal(t, width, ansi.StringWidth(line))
			start, end := testControlBounds(m, timePresetID)
			prStart, prEnd := testControlBounds(m, prPresetID)
			if start != end {
				require.Equal(t, allTimeLabel+presetControlSuffix, ansi.Cut(line, start, end))
				require.Equal(t, timePresetSeparator, ansi.Cut(line, end, prStart))
				require.Equal(t, timePresetID, hitID(m.screen.root, start, m.listSeparatorY()))
			}
			label := noPresetLabel
			if preset != "" {
				label = preset
			}
			require.Equal(t, label+presetControlSuffix, ansi.Cut(line, prStart, prEnd))
			require.Equal(t, prPresetID, hitID(m.screen.root, prStart, m.listSeparatorY()))
		}
	}
}

func TestCycleTimePresets(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.cli.preset = "Needs your review"
	h := newTUIHarness(t, m)
	for _, preset := range defaultTimePresets {
		h.Key("t").Wait(preset.Name + " ▾ │ Needs your review ▾")
	}
	h.Key("t").Wait("All Time ▾").Key("T").Wait("1 year ▾")
	st, err := loadTUIState()
	require.NoError(t, err)
	require.Equal(t, "1 year", st.Filters.Time)
	h.Close()

	custom := listTUIModel(approvalTestPR(1))
	custom.cli.timePreset = "1 year"
	custom.cfg.TUI.Presets.Time.Filters = []TUITimePresetConfig{{Name: "Fortnight", MaxAge: "2w"}}
	h = newTUIHarness(t, custom)
	h.Key("t").Wait("Fortnight ▾").Key("T").Wait("All Time ▾")
}

func TestClearFilterPresets(t *testing.T) {
	for _, open := range []bool{false, true} {
		t.Run(strconv.FormatBool(open), func(t *testing.T) {
			pr := approvalTestPR(1)
			pr.Title, pr.UpdatedAt = "Earlier change", time.Now().Add(-2*time.Hour)
			m := listTUIModel(pr)
			m.cli.preset, m.cli.timePreset = "Needs your review", "10 minutes"
			m.cfg.TUI.Filters.Preset, m.cfg.TUI.Filters.Time = m.cli.preset, m.cli.timePreset
			h := newTUIHarness(t, m)
			h.Wait("10 minutes ▾").AssertNot(pr.Title)
			if open {
				h.Locate(reel.Contains("10 minutes ▾")).LeftClick().Wait("✓ 10 minutes")
			}
			h.Key("ctrl+x").
				Wait(pr.Title).
				Assert("bots:hide │ All Time ▾ │ All PRs ▾").
				AssertNot("✓ 10 minutes")
			st, err := loadTUIState()
			require.NoError(t, err)
			require.Empty(t, st.Filters.Preset)
			require.Empty(t, st.Filters.Time)
		})
	}
}

func TestActiveTimePresetStaysVisibleInNarrowTerminal(t *testing.T) {
	now := time.Now()
	recent, stale := approvalTestPR(1), approvalTestPR(2)
	recent.UpdatedAt, stale.UpdatedAt = now, now.Add(-time.Hour)
	recent.Title, stale.Title = "Newer change", "Older change"
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
		t.Run(strconv.Itoa(tc.width), func(t *testing.T) {
			m := listTUIModel(recent, stale)
			m.cli.preset, m.cli.timePreset = "Needs your review", "10 minutes"
			m.width = tc.width
			testListSeparator(&m)
			prStart, prEnd := testControlBounds(m, prPresetID)
			timeStart, timeEnd := testControlBounds(m, timePresetID)
			require.Equal(t, tc.prLabel, ansi.Cut(tc.line, prStart, prEnd))
			require.Equal(t, tc.timeLabel, ansi.Cut(tc.line, timeStart, timeEnd))
			h := newTUIHarness(t, m)
			h.Locate(reel.Contains(tc.line)).Offset(timeStart, 0).LeftClick().Wait("╭─").
				Resize(80, 20).Wait("✓ 10 minutes").KeyEsc().WaitNot("✓ 10 minutes").
				Assert(recent.Title).AssertNot(stale.Title)
		})
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
				line := testListSeparator(&m)
				require.Equal(t, expected, line)
				start, end := testControlBounds(m, timePresetID)
				require.Equal(t, label+presetControlSuffix, ansi.Cut(line, start, end))
				require.Equal(t, timePresetID, hitID(m.screen.root, start, m.listSeparatorY()))
			}
			h := newTUIHarness(t, m)
			h.Locate(reel.Contains(tc.compact + " ▾")).LeftClick().Wait("✓ " + tc.name)
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
	line := testListSeparator(&m)
	start, end := testControlBounds(m, timePresetID)
	require.Equal(t, "Last 10 minutes ▾", ansi.Cut(line, start, end))
	h := newTUIHarness(t, m)
	h.Locate(reel.Contains("Last 10 minutes ▾")).LeftClick().Wait("✓ Last 10 minutes")
}
