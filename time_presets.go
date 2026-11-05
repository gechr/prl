package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/x/human"
)

// TUITimePresetsConfig controls the independent time filter beside the PR preset.
type TUITimePresetsConfig struct {
	Field   string                `koanf:"field"   yaml:"field"`
	Filters []TUITimePresetConfig `koanf:"filters" yaml:"filters"`
}

type TUITimePresetConfig struct {
	Name   string `koanf:"name"    yaml:"name"`
	MaxAge string `koanf:"max_age" yaml:"max_age"`
	Color  string `koanf:"color"   yaml:"color,omitempty"`
}

const allTimeLabel = "All Time"

const timePresetSeparator = " │ "

var defaultTimePresets = []TUITimePresetConfig{
	{Name: "10 minutes", MaxAge: "10m"},
	{Name: "1 hour", MaxAge: "1h"},
	{Name: "1 day", MaxAge: "1d"},
	{Name: "1 week", MaxAge: "1w"},
	{Name: "1 month", MaxAge: "30d"},
	{Name: "1 year", MaxAge: "1y"},
}

func configuredTimeField(cfg *Config) string {
	if cfg != nil && cfg.TUI.Presets.Time.Field != "" {
		return cfg.TUI.Presets.Time.Field
	}
	return valueUpdated
}

func configuredTimePresets(cfg *Config) []TUITimePresetConfig {
	if cfg != nil && len(cfg.TUI.Presets.Time.Filters) > 0 {
		return cfg.TUI.Presets.Time.Filters
	}
	return defaultTimePresets
}

func timePresetByName(cfg *Config, name string) (TUITimePresetConfig, bool) {
	for _, preset := range configuredTimePresets(cfg) {
		if preset.Name == name {
			return preset, true
		}
	}
	return TUITimePresetConfig{}, false
}

func parseTimePresetAge(value string) (time.Duration, error) {
	age, err := human.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid max_age: %w", err)
	}
	if age <= 0 {
		return 0, fmt.Errorf("max_age must be positive")
	}
	return age, nil
}

func validateTimePresets(cfg TUITimePresetsConfig) error {
	if cfg.Field != "" && cfg.Field != valueUpdated && cfg.Field != valueCreated {
		return fmt.Errorf(
			"invalid tui.presets.time.field %q (expected updated or created)",
			cfg.Field,
		)
	}
	seen := map[string]bool{allTimeLabel: true}
	for i, preset := range cfg.Filters {
		if strings.TrimSpace(preset.Name) == "" {
			return fmt.Errorf("tui.presets.time.filters[%d]: name is required", i)
		}
		if seen[preset.Name] {
			return fmt.Errorf(
				"tui.presets.time.filters: duplicate or reserved name %q",
				preset.Name,
			)
		}
		seen[preset.Name] = true
		if _, err := parseTimePresetAge(preset.MaxAge); err != nil {
			return fmt.Errorf("tui.presets.time.filters %q: %w", preset.Name, err)
		}
		if _, err := parsePresetColor(preset.Color); err != nil {
			return fmt.Errorf("tui.presets.time.filters %q: %w", preset.Name, err)
		}
	}
	return nil
}

func (m tuiModel) timeTagLabel() string {
	name := allTimeLabel
	if m.cli != nil {
		if preset, ok := timePresetByName(m.cfg, m.cli.timePreset); ok {
			name = preset.Name
		}
	}
	return name
}

// timeTagBounds anchors the time control after the PR preset.
func (m tuiModel) timeTagBounds() (int, int) {
	_, width := m.presetControlWidths()
	if width == 0 {
		return 0, 0
	}
	end := m.width - lg.Width(presetIndicatorSuffix)
	return end - width, end
}

func (m tuiModel) timeTagAt(x, y int) bool {
	start, end := m.timeTagBounds()
	return y == m.listSeparatorY() && x >= start && x < end
}

// timePresetStyle uses the same age thresholds as the created/updated columns.
// An explicit preset colour overrides the theme's age colour.
func (m tuiModel) timePresetStyle(preset TUITimePresetConfig) lg.Style {
	style := m.styles.helpText
	if age, err := parseTimePresetAge(preset.MaxAge); err == nil && m.p != nil && m.p.theme != nil {
		if m.p.theme.Red != nil {
			style = *m.p.theme.Red
		}
		for _, threshold := range m.p.theme.TimeAgoThresholds {
			if age < threshold.MaxAge {
				style = threshold.Style
				break
			}
		}
	}
	if color, err := parsePresetColor(preset.Color); err == nil && color != nil {
		style = style.Foreground(color)
	}
	return style
}

func (m tuiModel) renderTimeTag() string {
	start, end := m.timeTagBounds()
	if start == end {
		return ""
	}
	style := m.styles.separator.Faint(false)
	if preset, ok := timePresetByName(m.cfg, m.cli.timePreset); ok {
		style = m.timePresetStyle(preset)
	}
	return m.renderPresetControl(m.timeTagLabel(), style, m.hover.time, end-start)
}

func (m tuiModel) selectTimePreset(name string) (tea.Model, tea.Cmd) {
	m.presetMenu.open = false
	m.cli.timePreset = name
	m.cfg.TUI.Filters.Time = name
	if err := saveTUIState(m.cfg); err != nil {
		warnStateSaveErr(err, "time filter")
	}
	m.resyncCursorAndOffset()
	return m, nil
}

// cycleTimePreset follows configured order and includes All Time after the last range.
func (m tuiModel) cycleTimePreset(dir int) (tea.Model, tea.Cmd) {
	presets := configuredTimePresets(m.cfg)
	idx := slices.IndexFunc(presets, func(p TUITimePresetConfig) bool {
		return p.Name == m.cli.timePreset
	})
	if idx < 0 {
		idx = len(presets)
	}
	n := len(presets) + 1
	idx = (idx + dir + n) % n
	name := ""
	if idx < len(presets) {
		name = presets[idx].Name
	}
	return m.selectTimePreset(name)
}

func (m tuiModel) clearFilterPresets() (tea.Model, tea.Cmd) {
	m.presetMenu.open = false
	m.cli.preset, m.cli.timePreset = "", ""
	m.cfg.TUI.Filters.Preset, m.cfg.TUI.Filters.Time = "", ""
	if err := saveTUIState(m.cfg); err != nil {
		warnStateSaveErr(err, "filter settings")
	}
	m.resyncCursorAndOffset()
	return m, nil
}
