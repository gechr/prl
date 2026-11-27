package main

import (
	"image"
	"slices"
	"strings"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	xansi "github.com/gechr/x/ansi"
)

const (
	noPresetLabel          = "All PRs"
	presetMenuID  bloom.ID = "preset-menu"
	presetRowsID  bloom.ID = "preset-rows"
)

const presetControlSuffix = " ▾"

const (
	presetIndicatorPrefix = "┤ "
	presetIndicatorSuffix = " ├─"
)

// hoverState tracks what the pointer is over in the list view.
type hoverState struct {
	filters bool
	time    bool
	pr      bool
	rowY    int  // pointer line, valid when rowOK
	rowOK   bool // the pointer is over the list rows
	helpX   int  // pointer cell, valid when helpOK
	helpY   int
	helpOK  bool // the pointer is over a help hint
}

func (m tuiModel) renderPresetControl(name string, style lg.Style, hovered bool, width int) string {
	if width <= 0 {
		return ""
	}
	caretWidth := lg.Width(presetControlSuffix)
	var rendered string
	if width <= caretWidth {
		rendered = style.Bold(true).Render(xansi.Truncate(name, width, valueEllipsis))
	} else {
		label := xansi.Truncate(name, width-caretWidth, valueEllipsis)
		rendered = style.Bold(true).Render(label) + m.styles.separator.Render(presetControlSuffix)
	}
	if hovered {
		return layout.PreserveBackgroundWidth(rendered, cursorLineBG, lg.Width(rendered))
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

type presetMenuLayout struct {
	x, y, width int
	first, rows int
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

func (m tuiModel) presetTagLabel() string {
	if m.cli != nil {
		if preset, _, ok := m.activePreset(m.searchesSelf()); ok {
			return preset.Name
		}
	}
	return noPresetLabel
}

// Shorten duration labels before truncating PR labels or hiding controls.
// If only one control fits, the active time range takes priority.
func (m tuiModel) presetControlWidths() (int, int) {
	available := max(0, m.width-lg.Width(presetIndicatorPrefix)-lg.Width(presetIndicatorSuffix))
	prWidth := min(available, lg.Width(m.presetTagLabel()+presetControlSuffix))
	if m.cli == nil || available == 0 {
		return prWidth, 0
	}
	gapWidth := lg.Width(timePresetSeparator)
	timeWidth := lg.Width(m.timeControlLabel(available-prWidth-gapWidth) + presetControlSuffix)
	if prWidth+gapWidth+timeWidth <= available {
		return prWidth, timeWidth
	}
	if _, active := timePresetByName(m.cfg, m.cli.timePreset); !active {
		return prWidth, 0
	}
	room := available - timeWidth - gapWidth
	if room >= lg.Width(valueEllipsis+presetControlSuffix) {
		return min(prWidth, room), timeWidth
	}
	return 0, min(timeWidth, available)
}

// presetTagBounds mirrors the PR control's position in renderTagSeparator.
func (m tuiModel) presetTagBounds() (int, int) {
	prWidth, _ := m.presetControlWidths()
	if prWidth == 0 {
		return 0, 0
	}
	end := m.width - lg.Width(presetIndicatorSuffix)
	return end - prWidth, end
}

func (m tuiModel) presetTagAt(x, y int) bool {
	start, end := m.presetTagBounds()
	return y == m.listSeparatorY() && x >= start && x < end
}

// filterOptionTags returns the option filter tags shown before the preset controls.
func (m tuiModel) filterOptionTags() []string {
	tags := m.activeFilterTags()
	if preset, _, ok := m.activePreset(m.searchesSelf()); ok && len(tags) > 0 &&
		tags[0] == preset.Name {
		return tags[1:]
	}
	return tags
}

// filterTagsBounds mirrors the option tags' position in renderTagSeparator:
// one gap before the preset controls, truncated at the separator prefix.
func (m tuiModel) filterTagsBounds() (int, int) {
	prStart, prEnd := m.presetTagBounds()
	tags := m.filterOptionTags()
	if prStart == prEnd || len(tags) == 0 {
		return 0, 0
	}
	controlsStart := prStart
	if timeStart, timeEnd := m.timeTagBounds(); timeStart < timeEnd {
		controlsStart = timeStart
	}
	end := controlsStart - lg.Width(timePresetSeparator)
	start := max(
		end-lg.Width(strings.Join(tags, dotSeparator)),
		lg.Width(presetIndicatorPrefix),
	)
	if start >= end {
		return 0, 0
	}
	return start, end
}

func (m tuiModel) filterTagsAt(x, y int) bool {
	start, end := m.filterTagsBounds()
	return y == m.listSeparatorY() && x >= start && x < end
}

func (m tuiModel) canOpenPresetMenu() bool {
	return m.view == tuiViewList && m.cli != nil && m.cfg != nil &&
		!m.showHelp && !m.showOptions && m.confirmAction == "" &&
		!m.detailLoading && !m.diffLoading && m.width >= 5 && m.listSeparatorY() >= 3
}

func (m tuiModel) canKeepPresetMenuOpen() bool {
	if !m.canOpenPresetMenu() {
		return false
	}
	start, end := m.presetTagBounds()
	if m.presetMenu.timeFilter {
		start, end = m.timeTagBounds()
	}
	return end > start
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

func (m tuiModel) presetMenuGeometry() presetMenuLayout {
	presets := m.menuPresets()
	method := m.reviewWidthMethod()
	width := method.StringWidth(m.menuAllLabel())
	for _, preset := range presets {
		width = max(width, method.StringWidth(preset.Name))
	}
	const frameWidth = 6 // borders, padding, and selection marker
	width = min(m.width, width+frameWidth)
	const borders = 2
	rows := min(len(presets)+1, max(0, m.listSeparatorY()-borders))
	first := min(m.presetMenu.offset, len(presets)+1-rows)
	start, end := m.presetTagBounds()
	if m.presetMenu.timeFilter {
		start, end = m.timeTagBounds()
	}
	x := (start + end - width) / 2 //nolint:mnd // midpoint centers the menu over its control
	return presetMenuLayout{
		x: max(0, min(x, m.width-width)), y: m.listSeparatorY() - rows - borders,
		width: width, first: first, rows: rows,
	}
}

func (m *tuiModel) movePresetMenu(delta int) {
	count := len(m.menuPresets()) + 1
	m.presetMenu.cursor = max(0, min(m.presetMenu.cursor+delta, count-1))
	l := m.presetMenuGeometry()
	m.presetMenu.offset = max(0, min(l.first, m.presetMenu.cursor))
	if m.presetMenu.cursor >= m.presetMenu.offset+l.rows {
		m.presetMenu.offset = m.presetMenu.cursor - l.rows + 1
	}
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

func (m tuiModel) presetMenuLayer() bloom.LayerNode {
	presets := m.menuPresets()
	activeName := m.menuSelection()
	l := m.presetMenuGeometry()
	const frameWidth = 4 // borders and padding
	innerWidth := max(0, l.width-frameWidth)
	method := m.reviewWidthMethod()
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
		line := method.Truncate(prefix+name, innerWidth, valueEllipsis)
		line += strings.Repeat(" ", max(0, innerWidth-method.StringWidth(line)))
		line = style.Bold(active).Render(line)
		if idx == m.presetMenu.cursor {
			line = layout.PreserveBackgroundWidth(line, cursorLineBG, innerWidth)
		}
		return bloom.Text(" " + line + " ").NoWrap()
	}).Offset(l.first).ID(presetRowsID)
	box := bloom.Box(m.styles.overlayBox.Padding(0), rows)
	rect := image.Rect(l.x, l.y, l.x+l.width, m.listSeparatorY())
	return m.screen.panelLayer(box).ID(presetMenuID).Place(rect)
}
