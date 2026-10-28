package main

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/key"
	xansi "github.com/gechr/x/ansi"
)

const noPresetLabel = "All"

type presetMenuState struct {
	open   bool
	cursor int
	offset int
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

// presetTagBounds mirrors the right-aligned preset and suffix in renderTagSeparator.
func (m tuiModel) presetTagBounds() (int, int) {
	name := noPresetLabel
	if preset, _, ok := m.activePreset(m.searchesSelf()); ok {
		name = preset.Name
	}
	const suffixWidth = 3 // space and two separator characters
	end := m.width - suffixWidth
	return max(1, end-lg.Width(name)), end
}

func (m tuiModel) presetTagAt(x, y int) bool {
	start, end := m.presetTagBounds()
	return y == m.listSeparatorY() && x >= start && x < end
}

func (m tuiModel) canOpenPresetMenu() bool {
	return m.view == tuiViewList && m.cli != nil && m.cfg != nil &&
		!m.showHelp && !m.showOptions && m.confirmAction == "" &&
		!m.detailLoading && !m.diffLoading && m.width >= 5 && m.listSeparatorY() >= 3
}

func (m *tuiModel) openPresetMenu() {
	presets := availablePresets(m.cfg, m.searchesSelf())
	idx := slices.IndexFunc(presets, func(p TUIPresetConfig) bool { return p.Name == m.cli.preset })
	m.presetMenu = presetMenuState{open: true, cursor: idx + 1}
	m.movePresetMenu(0)
}

func (m tuiModel) presetMenuGeometry() presetMenuLayout {
	presets := availablePresets(m.cfg, m.searchesSelf())
	width := lg.Width(noPresetLabel)
	for _, preset := range presets {
		width = max(width, lg.Width(preset.Name))
	}
	const frameWidth = 6 // borders, padding, and selection marker
	width = min(m.width, width+frameWidth)
	const borders = 2
	rows := min(len(presets)+1, max(0, m.listSeparatorY()-borders))
	first := min(m.presetMenu.offset, len(presets)+1-rows)
	start, _ := m.presetTagBounds()
	return presetMenuLayout{
		x: max(0, min(start, m.width-width)), y: m.listSeparatorY() - rows - borders,
		width: width, first: first, rows: rows,
	}
}

func (m *tuiModel) movePresetMenu(delta int) {
	count := len(availablePresets(m.cfg, m.searchesSelf())) + 1
	m.presetMenu.cursor = max(0, min(m.presetMenu.cursor+delta, count-1))
	l := m.presetMenuGeometry()
	m.presetMenu.offset = max(0, min(l.first, m.presetMenu.cursor))
	if m.presetMenu.cursor >= m.presetMenu.offset+l.rows {
		m.presetMenu.offset = m.presetMenu.cursor - l.rows + 1
	}
}

func (m tuiModel) presetMenuIndexAt(x, y int) (int, bool) {
	l := m.presetMenuGeometry()
	row := y - l.y - 1
	return l.first + row, x > l.x && x < l.x+l.width-1 && row >= 0 && row < l.rows
}

func (m tuiModel) choosePresetMenuItem(idx int) (tea.Model, tea.Cmd) {
	presets := availablePresets(m.cfg, m.searchesSelf())
	name := ""
	if idx > 0 && idx <= len(presets) {
		name = presets[idx-1].Name
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
	l := m.presetMenuGeometry()
	if msg.X < l.x || msg.X >= l.x+l.width || msg.Y < l.y || msg.Y >= l.y+l.rows+2 {
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
		m.movePresetMenu(len(availablePresets(m.cfg, m.searchesSelf())))
	case key.Enter, key.Space:
		return m.choosePresetMenuItem(m.presetMenu.cursor)
	case tuiKeybindCyclePreset:
		return m.cyclePreset(1)
	case tuiKeybindCyclePresetBack:
		return m.cyclePreset(-1)
	}
	return m, nil
}

func (m tuiModel) viewPresetMenu(background string) string {
	presets := availablePresets(m.cfg, m.searchesSelf())
	activeName := ""
	if preset, _, ok := m.activePreset(m.searchesSelf()); ok {
		activeName = preset.Name
	}
	l := m.presetMenuGeometry()
	const frameWidth = 4 // borders and padding
	innerWidth := max(0, l.width-frameWidth)
	lines := make([]string, 0, l.rows)
	for idx := l.first; idx < l.first+l.rows; idx++ {
		name := noPresetLabel
		style := m.styles.separator.Faint(false)
		active := activeName == ""
		if idx > 0 {
			preset := presets[idx-1]
			name = preset.Name
			active = preset.Name == activeName
			style = m.styles.helpText
			if c, err := parsePresetColor(preset.Color); err == nil && c != nil {
				style = lg.NewStyle().Foreground(c)
			}
		}
		prefix := "  "
		if active {
			prefix = "✓ "
		}
		line := xansi.Truncate(prefix+name, innerWidth, valueEllipsis)
		line += strings.Repeat(" ", max(0, innerWidth-lg.Width(line)))
		style = style.Bold(active)
		if idx == m.presetMenu.cursor {
			style = style.Background(lg.Color(cursorLineBG))
		}
		lines = append(lines, style.Render(line))
	}
	box := m.styles.overlayBox.Padding(0, 1).Render(strings.Join(lines, nl))
	return lg.NewCompositor(
		lg.NewLayer(background),
		lg.NewLayer(box).X(l.x).Y(l.y).Z(1),
	).Render()
}
