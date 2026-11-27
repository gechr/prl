package main

import (
	"slices"
	"strings"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
)

const (
	noPresetLabel          = "All PRs"
	presetMenuID  bloom.ID = "preset-menu"
	presetRowsID  bloom.ID = "preset-rows"
	filterTagsID  bloom.ID = "filter-tags"
	prPresetID    bloom.ID = "pr-preset"
	timePresetID  bloom.ID = "time-preset"
)

const presetControlSuffix = " ▾"

const (
	presetIndicatorPrefix = "┤ "
	presetIndicatorSuffix = " ├─"
)

// hoverState tracks what the pointer is over in the list view.
type hoverState struct {
	control bloom.ID
	rowY    int  // pointer line, valid when rowOK
	rowOK   bool // the pointer is over the list rows
}

func (m tuiModel) renderPresetControl(name string, style lg.Style, hovered bool, width int) string {
	if width <= 0 {
		return ""
	}
	method := m.reviewWidthMethod()
	labelWidth := width
	caret := ""
	if caretWidth := method.StringWidth(presetControlSuffix); width > caretWidth {
		labelWidth -= caretWidth
		caret = m.styles.separator.Render(presetControlSuffix)
	}
	if method.StringWidth(name) > labelWidth {
		name = method.Truncate(name, labelWidth, valueEllipsis)
	}
	rendered := style.Bold(true).Render(name) + caret
	if hovered {
		return layout.PreserveBackground(rendered, hoverBG)
	}
	return rendered
}

type presetMenuState struct {
	open       bool
	timeFilter bool
	cursor     int
	offset     int
}

// Both separator menus share navigation, scrolling, rendering and dismissal.
func (m tuiModel) menuPresets() []TUIPresetConfig {
	if !m.presetMenu.timeFilter {
		return availablePresets(m.cfg, m.searchesSelf())
	}
	var presets []TUIPresetConfig
	for _, preset := range configuredTimePresets(m.cfg) {
		presets = append(presets, TUIPresetConfig{Name: preset.Name, Color: preset.Color})
	}
	return presets
}

func (m tuiModel) menuSelection() string {
	if m.presetMenu.timeFilter {
		if preset, ok := timePresetByName(m.cfg, m.cli.timePreset); ok {
			return preset.Name
		}
		return ""
	}
	if preset, _, ok := m.activePreset(m.searchesSelf()); ok {
		return preset.Name
	}
	return ""
}

func (m tuiModel) menuAllLabel() string {
	if m.presetMenu.timeFilter {
		return allTimeLabel
	}
	return noPresetLabel
}

func (m tuiModel) listSeparatorY() int {
	y := m.listViewport()
	if m.header != "" {
		y++
	}
	if m.filterInput.Focused() || m.filterInput.Value() != "" {
		y++
	}
	return y
}

type presetControls struct {
	pr, time string
}

func (p presetControls) anchor(timeFilter bool) (bloom.ID, string) {
	if timeFilter {
		return timePresetID, p.time
	}
	return prPresetID, p.pr
}

func (m tuiModel) presetControls() presetControls {
	if m.cli == nil {
		return presetControls{}
	}
	name, style := noPresetLabel, m.styles.separator.Faint(false)
	if preset, _, ok := m.activePreset(m.searchesSelf()); ok {
		name, style = preset.Name, styleFilterTag.Faint(false)
		if c, err := parsePresetColor(preset.Color); err == nil && c != nil {
			style = lg.NewStyle().Foreground(c)
		}
	}
	prWidth, timeWidth := m.presetControlWidths(name)
	return presetControls{
		pr:   m.renderPresetControl(name, style, m.hover.control == prPresetID, prWidth),
		time: m.renderTimeTag(timeWidth),
	}
}

// Shorten duration labels before truncating PR labels or hiding controls.
// If only one control fits, the active time range takes priority.
func (m tuiModel) presetControlWidths(prLabel string) (int, int) {
	method := m.reviewWidthMethod()
	available := max(0, m.width-method.StringWidth(presetIndicatorPrefix+presetIndicatorSuffix))
	prWidth := min(available, method.StringWidth(prLabel+presetControlSuffix))
	if available == 0 {
		return prWidth, 0
	}
	gapWidth := method.StringWidth(timePresetSeparator)
	timeWidth := method.StringWidth(
		m.timeControlLabel(available-prWidth-gapWidth) + presetControlSuffix,
	)
	if prWidth+gapWidth+timeWidth <= available {
		return prWidth, timeWidth
	}
	if _, active := timePresetByName(m.cfg, m.cli.timePreset); !active {
		return prWidth, 0
	}
	room := available - timeWidth - gapWidth
	if room >= method.StringWidth(valueEllipsis+presetControlSuffix) {
		return min(prWidth, room), timeWidth
	}
	return 0, min(timeWidth, available)
}

func (m tuiModel) canOpenPresetMenu() bool {
	if m.view != tuiViewList || m.cli == nil || m.cfg == nil || m.help != nil || m.showOptions ||
		m.confirmAction != "" || m.detailLoading || m.diffLoading || m.width < 5 {
		return false
	}
	y := m.listSeparatorY()
	return y >= 3 && y < m.height
}

func (m tuiModel) canKeepPresetMenuOpen() bool {
	if !m.canOpenPresetMenu() {
		return false
	}
	_, label := m.presetControls().anchor(m.presetMenu.timeFilter)
	return label != ""
}

func (m *tuiModel) openPresetMenu() {
	m.openFilterPresetMenu(false)
}

func (m *tuiModel) openFilterPresetMenu(timeFilter bool) {
	m.presetMenu = presetMenuState{open: true, timeFilter: timeFilter}
	presets := m.menuPresets()
	idx := slices.IndexFunc(
		presets,
		func(p TUIPresetConfig) bool { return p.Name == m.menuSelection() },
	)
	m.presetMenu.cursor = idx + 1
	m.movePresetMenu(0)
}

const presetMenuBorders = 2

func (m tuiModel) presetMenuRows(count int) int {
	return min(count, max(0, m.listSeparatorY()-presetMenuBorders))
}

func (m *tuiModel) movePresetMenu(delta int) {
	count := len(m.menuPresets()) + 1
	rows := max(1, m.presetMenuRows(count))
	m.presetMenu.cursor = max(0, min(m.presetMenu.cursor+delta, count-1))
	m.presetMenu.offset = max(0,
		m.presetMenu.cursor-rows+1,
		min(m.presetMenu.offset, m.presetMenu.cursor, count-rows),
	)
}

func (m tuiModel) presetMenuIndexAt(x, y int) (int, bool) {
	if !m.screen.contains(presetRowsID, x, y) {
		return 0, false
	}
	frame := m.screen.root.Frame()
	rect, _ := frame.Rect(presetRowsID)
	list, _ := frame.List(presetRowsID)
	return list.First + y - rect.Min.Y, true
}

func (m tuiModel) choosePresetMenuItem(idx int) (tea.Model, tea.Cmd) {
	presets := m.menuPresets()
	name := ""
	if idx > 0 && idx <= len(presets) {
		name = presets[idx-1].Name
	}
	if m.presetMenu.timeFilter {
		return m.selectTimePreset(name)
	}
	return m.selectPreset(name)
}

func (m tuiModel) updatePresetMenuClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}
	if idx, ok := m.presetMenuIndexAt(msg.X, msg.Y); ok {
		return m.choosePresetMenuItem(idx)
	}
	if !m.screen.contains(presetMenuID, msg.X, msg.Y) {
		m.presetMenu.open = false
	}
	return m, nil
}

func (m tuiModel) updatePresetMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case key.Esc, tuiKeybindQuit:
		m.presetMenu.open = false
	case key.Down, tuiKeybindVimDown:
		m.movePresetMenu(1)
	case key.Up, tuiKeybindVimUp:
		m.movePresetMenu(-1)
	case key.Home:
		m.movePresetMenu(-m.presetMenu.cursor)
	case key.End:
		m.movePresetMenu(len(m.menuPresets()))
	case key.Enter, key.Space:
		return m.choosePresetMenuItem(m.presetMenu.cursor)
	case tuiKeybindCycleTimePreset:
		return m.cycleTimePreset(1)
	case tuiKeybindCycleTimePresetBack:
		return m.cycleTimePreset(-1)
	case tuiKeybindClearPresets:
		return m.clearFilterPresets()
	case tuiKeybindCyclePreset:
		return m.cyclePreset(1)
	case tuiKeybindCyclePresetBack:
		return m.cyclePreset(-1)
	}
	return m, nil
}

func (m tuiModel) presetMenuLayer(controls presetControls) bloom.LayerNode {
	presets := m.menuPresets()
	activeName := m.menuSelection()
	method := m.reviewWidthMethod()
	labelWidth := method.StringWidth(m.menuAllLabel())
	for _, preset := range presets {
		labelWidth = max(labelWidth, method.StringWidth(preset.Name))
	}
	const frameWidth = 4 // borders and padding
	const markerWidth = 2
	width := min(m.width, labelWidth+frameWidth+markerWidth)
	innerWidth := max(0, width-frameWidth)
	rows := bloom.List(len(presets)+1, func(idx int) bloom.Child {
		name := m.menuAllLabel()
		style := m.styles.separator.Faint(false)
		active := activeName == ""
		if idx > 0 {
			preset := presets[idx-1]
			name = preset.Name
			active = preset.Name == activeName
			style = m.styles.helpText
			if m.presetMenu.timeFilter {
				if timePreset, ok := timePresetByName(m.cfg, preset.Name); ok {
					style = m.timePresetStyle(timePreset)
				}
			} else if c, err := parsePresetColor(preset.Color); err == nil && c != nil {
				style = lg.NewStyle().Foreground(c)
			}
		}
		prefix := "  "
		if active {
			prefix = "✓ "
		}
		line := prefix + name
		if method.StringWidth(line) > innerWidth {
			line = method.Truncate(line, innerWidth, valueEllipsis)
		}
		line += strings.Repeat(" ", max(0, innerWidth-method.StringWidth(line)))
		line = style.Bold(active).Render(line)
		if idx == m.presetMenu.cursor {
			line = layout.PreserveBackground(line, hoverBG)
		}
		return bloom.Text(" " + line + " ").NoWrap()
	}).Offset(m.presetMenu.offset).ID(presetRowsID)
	box := bloom.Box(m.styles.overlayBox.Padding(0), rows)
	anchor, label := controls.anchor(m.presetMenu.timeFilter)
	dx := (method.StringWidth(label) - width) / 2 //nolint:mnd // center the menu on its control
	return m.screen.panelLayer(box).ID(presetMenuID).AnchorTo(anchor, bloom.Top).
		Offset(dx, 0).Size(width, m.presetMenuRows(len(presets)+1)+presetMenuBorders)
}
