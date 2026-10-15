package main

import (
	"strconv"
	"strings"

	xslices "github.com/gechr/x/slices"
)

const (
	stackTreeWidth    = 3 // tree connector plus a space
	stackAlphabetSize = 26
)

type tuiListLine struct {
	row      int
	label    string
	header   string
	tree     string
	stackKey prKey
}

func (m tuiModel) stacksEnabled() bool {
	return m.cfg != nil && m.cfg.TUI.Stacks.Enabled
}

func (m tuiModel) stackSuffixWidth() int {
	if !m.stacksEnabled() {
		return 0
	}
	rows := make([]TableRow, len(m.items))
	for i, item := range m.items {
		rows[i].Item = item
	}
	width := 0
	for _, group := range stackGroups(rows) {
		if len(group) > 1 {
			width = max(width, len(stackLetter(len(group)-1)))
		}
	}
	return width
}

func (m tuiModel) stackExtraWidth() int {
	if suffix := m.stackSuffixWidth(); suffix > 0 {
		return suffix + stackTreeWidth
	}
	return 0
}

func (m tuiModel) listIndexWidth() int {
	return tuiIndexWidth(len(m.rows)) + m.stackSuffixWidth()
}

// stackLetter supports stacks beyond 26 entries: a..z, aa..az, ba...
func stackLetter(index int) string {
	label := ""
	for index++; index > 0; index = (index - 1) / stackAlphabetSize {
		label = string(rune('a'+(index-1)%stackAlphabetSize)) + label
	}
	return label
}

// listLines keeps headers separate from PR rows, so headers never become action
// targets or selection entries. Offsets and mouse hit-testing use screen lines.
func (m tuiModel) listLines() []tuiListLine {
	visible := m.visibleIndices()
	lines := make([]tuiListLine, 0, len(visible))
	group := 0
	indent := m.stackExtraWidth() > 0
	for pos := 0; pos < len(visible); {
		idx := visible[pos]
		item := m.rows[idx].Item
		group++
		label := strconv.Itoa(group)
		if !m.stacksEnabled() || item.StackKey == "" {
			tree := ""
			if indent {
				tree = strings.Repeat(" ", stackTreeWidth)
			}
			lines = append(lines, tuiListLine{row: idx, label: label, tree: tree})
			pos++
			continue
		}
		end := pos + 1
		for end < len(visible) && m.rows[visible[end]].Item.StackKey == item.StackKey {
			end++
		}
		if m.collapsedStacks[item.StackKey] {
			lines = append(
				lines,
				tuiListLine{
					row:      idx,
					label:    label,
					header:   item.StackHeader,
					tree:     activeIcons.StackCollapsed + "  ",
					stackKey: item.StackKey,
				},
			)
			pos = end
			continue
		}
		lines = append(
			lines,
			tuiListLine{
				row:      -1,
				label:    label,
				header:   item.StackHeader,
				tree:     activeIcons.StackExpanded + "  ",
				stackKey: item.StackKey,
			},
		)
		for i := pos; i < end; i++ {
			tree := "├─ "
			if i == end-1 {
				tree = "╰─ "
			}
			lines = append(
				lines,
				tuiListLine{
					row:      visible[i],
					label:    label + stackLetter(i-pos),
					tree:     tree,
					stackKey: item.StackKey,
				},
			)
		}
		pos = end
	}
	return lines
}

func (m tuiModel) cursorLinePosition() int {
	for pos, line := range m.listLines() {
		if line.row == m.cursor {
			return pos
		}
	}
	return 0
}

// Numeric jumps use the displayed group number and target its top PR.
func (m tuiModel) jumpIndices() []int {
	var indices []int
	last := ""
	for _, line := range m.listLines() {
		group := strings.TrimRight(line.label, "abcdefghijklmnopqrstuvwxyz")
		if line.row >= 0 && group != last {
			indices = append(indices, line.row)
			last = group
		}
	}
	return indices
}

func (m tuiModel) visibleStackIndices(indices []int) []int {
	if !m.stacksEnabled() || len(m.collapsedStacks) == 0 {
		return indices
	}
	seen := make(prKeys)
	result := make([]int, 0, len(indices))
	for _, idx := range indices {
		key := m.rows[idx].Item.StackKey
		if key != "" && m.collapsedStacks[key] {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		result = append(result, idx)
	}
	return result
}

func (m tuiModel) cursorStackKey() prKey {
	if !m.stacksEnabled() || m.cursor < 0 || m.cursor >= len(m.rows) {
		return ""
	}
	return m.rows[m.cursor].Item.StackKey
}

func (m tuiModel) isCollapsedRow(idx int) bool {
	return m.stacksEnabled() && idx >= 0 && idx < len(m.rows) &&
		m.collapsedStacks[m.rows[idx].Item.StackKey]
}

func (m *tuiModel) toggleStack(key prKey) {
	if m.collapsedStacks == nil {
		m.collapsedStacks = make(prKeys)
	}
	if m.collapsedStacks[key] {
		delete(m.collapsedStacks, key)
	} else {
		m.collapsedStacks[key] = true
	}
	if m.cursorStackKey() == key {
		for _, idx := range m.visibleIndices() {
			if m.rows[idx].Item.StackKey == key {
				m.cursor = idx
				break
			}
		}
	}
	m.resyncCursorAndOffset()
	m.persistStackFolds()
}

func (m tuiModel) stackGlyphAt(x, y int) prKey {
	line := y - 1 + m.offset // one column-header line
	lines := m.listLines()
	if y < 1 || y > m.listViewport() || line < 0 || line >= len(lines) {
		return ""
	}
	entry := lines[line]
	treeX := m.listPrefixWidth() - stackTreeWidth
	if entry.stackKey != "" && x >= treeX && (entry.header != "" || x < treeX+stackTreeWidth) {
		return entry.stackKey
	}
	return ""
}

func foldedStackKeys(keys []string) prKeys {
	folded := make(prKeys, len(keys))
	for _, key := range keys {
		folded[prKey(key)] = true
	}
	return folded
}

func (m tuiModel) persistStackFolds() {
	if m.cfg == nil {
		return
	}
	keys := make([]string, 0, len(m.collapsedStacks))
	for key, folded := range m.collapsedStacks {
		if folded {
			keys = append(keys, string(key))
		}
	}
	xslices.SortNatural(keys)
	m.cfg.TUI.FoldedStacks = keys
	if err := saveTUIState(m.cfg); err != nil {
		warnStateSaveErr(err, "folded stacks")
	}
}

// A refreshed stack can acquire a new top PR while its old top still exists.
// Keep the cursor on the folded header instead of a now-hidden child row.
func (m *tuiModel) restoreFoldedCursor() {
	if !m.isCollapsedRow(m.cursor) {
		return
	}
	key := m.cursorStackKey()
	for _, idx := range m.visibleIndices() {
		if m.rows[idx].Item.StackKey == key {
			m.cursor = idx
			return
		}
	}
}
