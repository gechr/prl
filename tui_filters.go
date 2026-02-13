package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/clog"
)

// filterChoiceTrue/False are canonical string values for bool filter choices.
const (
	filterChoiceTrue  = "true"
	filterChoiceFalse = "false"
)

// filterRow identifies a row in the filter options overlay.
type filterRow int

// Filter row indices (correspond to filterOptionDefs entries).
const (
	filterRowState filterRow = iota
	filterRowDraft
	filterRowBots
	filterRowArchived
	filterRowCI
	filterRowReview
)

// filterChoice represents a single choice for a filter option in the overlay.
type filterChoice struct {
	label string // display text in overlay
	value string // canonical internal value
}

// filterOptionDef defines a filter option row in the overlay.
type filterOptionDef struct {
	label   string
	choices []filterChoice
}

// filterOptionDefs defines the filter options available in the overlay.
// Bots value represents NoBot (true=hide). Archived value represents Archived flag (true=show).
var filterOptionDefs = [...]filterOptionDef{
	{"State", []filterChoice{
		{"open", "open"},
		{"closed", "closed"},
		{"merged", "merged"},
		{"ready", "ready"},
		{"all", "all"},
	}},
	{
		"Drafts",
		[]filterChoice{{"show", ""}, {"hide", filterChoiceFalse}},
	},
	{"Bots", []filterChoice{{"show", filterChoiceFalse}, {"hide", filterChoiceTrue}}},
	{"Archived", []filterChoice{{"show", filterChoiceTrue}, {"hide", filterChoiceFalse}}},
	{"CI", []filterChoice{
		{"success", "success"}, {"failure", "failure"}, {"pending", "pending"}, {"all", ""},
	}},
	{"Review", []filterChoice{
		{"required", valueReviewFilterRequired},
		{"approved", valueReviewFilterApproved},
		{"changes", valueReviewFilterChanges},
		{"none", valueReviewFilterNone},
		{"all", ""},
	}},
}

// currentFilterValues maps the current CLI filter state to choice indices
// for the filter overlay.
func (m tuiModel) currentFilterValues() [6]int {
	var vals [6]int

	// State - use canonical string from the parsed enum.
	vals[0] = filterChoiceIndex(filterRowState, m.cli.PRState().String())

	// Draft
	switch {
	case m.cli.Draft == nil:
		vals[1] = filterChoiceIndex(filterRowDraft, "")
	case *m.cli.Draft:
		vals[1] = filterChoiceIndex(filterRowDraft, "")
	default:
		vals[1] = filterChoiceIndex(filterRowDraft, filterChoiceFalse)
	}

	// Bots (NoBot: true=hide=index 1, false=show=index 0)
	if m.cli.NoBot {
		vals[2] = filterChoiceIndex(filterRowBots, filterChoiceTrue)
	} else {
		vals[2] = filterChoiceIndex(filterRowBots, filterChoiceFalse)
	}

	// Archived (true=show=index 0, false=hide=index 1)
	if m.cli.Archived {
		vals[3] = filterChoiceIndex(filterRowArchived, filterChoiceTrue)
	} else {
		vals[3] = filterChoiceIndex(filterRowArchived, filterChoiceFalse)
	}

	// CI - normalize from canonical CIStatus
	vals[4] = filterChoiceIndex(filterRowCI, "")
	if ci := m.cli.CI; ci != "" {
		if parsed, ok := parseCIStatus(ci); ok {
			vals[4] = filterChoiceIndex(filterRowCI, parsed.String())
		}
	}

	// Review
	vals[5] = filterChoiceIndex(filterRowReview, m.cli.Review)

	return vals
}

// selectedFilterValue returns the canonical value string for the given filter row.
func (m tuiModel) selectedFilterValue(row filterRow) string {
	return filterOptionDefs[row].choices[m.optionsValues[row]].value
}

func filterChoiceIndex(row filterRow, value string) int {
	for i, c := range filterOptionDefs[row].choices {
		if c.value == value {
			return i
		}
	}
	return 0
}

func (m tuiModel) defaultStateValue() string {
	if m.cfg != nil {
		if parsed, ok := parsePRState(m.cfg.Default.State); ok {
			return parsed.String()
		}
	}
	return valueOpen
}

func (m tuiModel) defaultNoBotValue() bool {
	return m.cfg != nil && !m.cfg.Default.Bots
}

func (m tuiModel) defaultFilterChoice(row filterRow) int {
	switch row {
	case filterRowState:
		return filterChoiceIndex(row, m.defaultStateValue())
	case filterRowDraft:
		return filterChoiceIndex(row, "")
	case filterRowBots:
		if m.defaultNoBotValue() {
			return filterChoiceIndex(row, filterChoiceTrue)
		}
		return filterChoiceIndex(row, filterChoiceFalse)
	case filterRowArchived:
		return filterChoiceIndex(row, filterChoiceFalse)
	case filterRowCI, filterRowReview:
		return filterChoiceIndex(row, "")
	}
	return 0
}

func (m *tuiModel) resetFilterRow(row filterRow) {
	m.optionsReset[row] = true
	m.optionsValues[row] = m.defaultFilterChoice(row)
}

func (m *tuiModel) applyFilterRow(row filterRow) {
	switch row {
	case filterRowState:
		if m.optionsReset[row] {
			m.cli.State = m.defaultStateValue()
			return
		}
		m.cli.State = m.selectedFilterValue(row)
	case filterRowDraft:
		switch m.selectedFilterValue(row) {
		case "":
			m.cli.Draft = nil
		case filterChoiceFalse:
			m.cli.Draft = new(false)
		}
	case filterRowBots:
		if m.optionsReset[row] {
			m.cli.NoBot = m.defaultNoBotValue()
			return
		}
		m.cli.NoBot = m.selectedFilterValue(row) == filterChoiceTrue
	case filterRowArchived:
		if m.optionsReset[row] {
			m.cli.Archived = false
			return
		}
		m.cli.Archived = m.selectedFilterValue(row) == filterChoiceTrue
	case filterRowCI:
		if m.optionsReset[row] {
			m.cli.CI = ""
			return
		}
		m.cli.CI = m.selectedFilterValue(row)
	case filterRowReview:
		if m.optionsReset[row] {
			m.cli.Review = ""
			return
		}
		m.cli.Review = m.selectedFilterValue(row)
	}
}

func (m tuiModel) persistedFilterValue(row filterRow) any {
	switch row {
	case filterRowState:
		if m.optionsReset[row] {
			return ""
		}
		return m.cli.State
	case filterRowDraft:
		return m.cli.Draft
	case filterRowBots:
		if m.optionsReset[row] {
			return (*bool)(nil)
		}
		return new(m.cli.NoBot)
	case filterRowArchived:
		if m.optionsReset[row] {
			return (*bool)(nil)
		}
		return new(m.cli.Archived)
	case filterRowCI:
		if m.optionsReset[row] {
			return ""
		}
		return m.cli.CI
	case filterRowReview:
		if m.optionsReset[row] {
			return ""
		}
		return m.cli.Review
	default:
		return nil
	}
}

// isFilterRowLocked returns true if the given filter row was explicitly set on CLI.
func (m tuiModel) isFilterRowLocked(row filterRow) bool {
	switch row {
	case filterRowState:
		return m.cli.stateExplicit
	case filterRowDraft:
		return m.cli.draftExplicit
	case filterRowBots:
		return m.cli.noBotExplicit
	case filterRowArchived:
		return m.cli.archivedExplicit
	case filterRowCI:
		return m.cli.ciExplicit
	case filterRowReview:
		return m.cli.reviewExplicit
	}
	return false
}

func (m tuiModel) updateOptionsOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case tuiKeyEsc, tuiKeybindQuit:
		m.showOptions = false
		return m, nil
	case tuiKeyEnter, tuiKeybindOptions:
		return m.applyFilterOptions()
	case tuiKeybindVimDown, tuiKeyDown:
		m.optionsCursor = min(m.optionsCursor+1, filterRow(len(filterOptionDefs)-1))
	case tuiKeybindVimUp, tuiKeyUp:
		m.optionsCursor = max(m.optionsCursor-1, 0)
	case tuiKeybindVimRight, tuiKeyRight:
		if !m.isFilterRowLocked(m.optionsCursor) {
			m.optionsReset[m.optionsCursor] = false
			n := len(filterOptionDefs[m.optionsCursor].choices)
			m.optionsValues[m.optionsCursor] = min(m.optionsValues[m.optionsCursor]+1, n-1)
		}
	case tuiKeySpace:
		if !m.isFilterRowLocked(m.optionsCursor) {
			m.optionsReset[m.optionsCursor] = false
			n := len(filterOptionDefs[m.optionsCursor].choices)
			if n > 0 {
				m.optionsValues[m.optionsCursor] = (m.optionsValues[m.optionsCursor] + 1) % n
			}
		}
	case tuiKeybindVimLeft, tuiKeyLeft:
		if !m.isFilterRowLocked(m.optionsCursor) {
			m.optionsReset[m.optionsCursor] = false
			m.optionsValues[m.optionsCursor] = max(m.optionsValues[m.optionsCursor]-1, 0)
		}
	case "backspace", "delete":
		if !m.isFilterRowLocked(m.optionsCursor) {
			m.resetFilterRow(m.optionsCursor)
		}
	}
	return m, nil
}

func (m tuiModel) applyFilterOptions() (tea.Model, tea.Cmd) {
	m.showOptions = false

	// Map overlay values back to CLI fields, skipping CLI-explicit ones.
	if !m.cli.stateExplicit {
		m.applyFilterRow(filterRowState)
	}
	if !m.cli.draftExplicit {
		m.applyFilterRow(filterRowDraft)
	}
	if !m.cli.noBotExplicit {
		m.applyFilterRow(filterRowBots)
	}
	if !m.cli.archivedExplicit {
		m.applyFilterRow(filterRowArchived)
	}
	if !m.cli.ciExplicit {
		m.applyFilterRow(filterRowCI)
	}
	if !m.cli.reviewExplicit {
		m.applyFilterRow(filterRowReview)
	}

	// Rebuild search params.
	params, err := buildSearchQuery(m.cli, m.cfg)
	if err != nil {
		return m, flashResult(&m, err.Error(), "", "", true)
	}
	m.params = params

	// Persist non-explicit values to config.
	type persistItem struct {
		explicit bool
		key      string
		value    any
	}
	for _, p := range []persistItem{
		{m.cli.stateExplicit, keyTUIFilterState, m.persistedFilterValue(filterRowState)},
		{m.cli.ciExplicit, keyTUIFilterCI, m.persistedFilterValue(filterRowCI)},
		{m.cli.reviewExplicit, keyTUIFilterReview, m.persistedFilterValue(filterRowReview)},
		{m.cli.draftExplicit, keyTUIFilterDraft, m.persistedFilterValue(filterRowDraft)},
		{m.cli.noBotExplicit, keyTUIFilterBots, m.persistedFilterValue(filterRowBots)},
		{m.cli.archivedExplicit, keyTUIFilterArchived, m.persistedFilterValue(filterRowArchived)},
	} {
		if !p.explicit {
			if err := saveConfigKey(p.key, p.value); err != nil {
				clog.Warn().Err(err).Str("key", p.key).Msg("Failed to persist filter setting")
			}
		}
	}

	// Recompute cursor/offset since viewport may change (filter indicator line).
	m.resyncCursorAndOffset()

	m.invalidateRefresh()
	return m, m.startRefresh(true)
}

func (m tuiModel) renderOptionsOverlay() string {
	var b strings.Builder

	labelWidth := 0
	for _, def := range filterOptionDefs {
		if w := len(def.label); w > labelWidth {
			labelWidth = w
		}
	}

	lines := make([]string, 0, len(filterOptionDefs))
	for i, def := range filterOptionDefs {
		var line strings.Builder
		row := filterRow(i)
		locked := m.isFilterRowLocked(row)

		// Cursor prefix.
		if row == m.optionsCursor {
			line.WriteString(m.styles.cursor.Render("❯ "))
		} else {
			line.WriteString("  ")
		}

		// Label.
		pad := strings.Repeat(" ", labelWidth-len(def.label))
		label := pad + def.label + "  "
		if locked {
			line.WriteString(lg.NewStyle().Faint(true).Render(label))
		} else {
			line.WriteString(m.styles.helpKey.Render(label))
		}

		// Choices.
		for i, c := range def.choices {
			if i > 0 {
				line.WriteString("  ")
			}
			selected := m.optionsValues[row] == i
			isDefault := i == m.defaultFilterChoice(row)
			switch {
			case selected:
				line.WriteString(styleTitle.Bold(true).Render(c.label))
			case isDefault:
				line.WriteString(m.styles.defaultChoice.Render(c.label))
			case locked:
				if selected {
					line.WriteString(styleTitle.Bold(true).Render(c.label))
				} else {
					line.WriteString(styleDim.Faint(true).Render(c.label))
				}
			default:
				line.WriteString(lg.NewStyle().Faint(true).Render(c.label))
			}
		}

		if locked {
			line.WriteString(lg.NewStyle().Faint(true).Render("  (CLI)"))
		}
		lines = append(lines, line.String())
	}

	// Footer.
	footer := m.styles.helpKey.Render("←/→") + m.styles.helpText.Render(" select") +
		"  " + m.styles.helpKey.Render("space") + m.styles.helpText.Render(" cycle") +
		"  " + m.styles.helpKey.Render("⌫") + m.styles.helpText.Render(" reset") +
		"  " + m.styles.helpKey.Render("enter") + m.styles.helpText.Render(" apply") +
		"  " + m.styles.helpKey.Render("esc") + m.styles.helpText.Render(" cancel")

	contentWidth := lg.Width(footer)
	for _, line := range lines {
		contentWidth = max(contentWidth, lg.Width(line))
	}

	for i, line := range lines {
		if filterRow(i) == m.optionsCursor {
			b.WriteString(injectLineBackground(line, contentWidth, cursorLineBG))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(footer)

	return m.styles.overlayBox.Padding(tuiOptionsPadY, tuiOptionsPadX).Render(b.String())
}

func applyTUIFilterDefaults(cli *CLI, cfg *Config) bool {
	f := cfg.TUI.Filters
	changed := false
	if !cli.stateExplicit && f.State != "" {
		cli.State = f.State
		changed = true
	}
	if !cli.draftExplicit && f.Draft != nil {
		if !*f.Draft {
			cli.Draft = f.Draft
			changed = true
		}
	}
	if !cli.noBotExplicit && f.Bots != nil {
		cli.NoBot = *f.Bots
		changed = true
	}
	if !cli.archivedExplicit && f.Archived != nil {
		cli.Archived = *f.Archived
		changed = true
	}
	if !cli.ciExplicit && f.CI != "" {
		cli.CI = f.CI
		changed = true
	}
	if !cli.reviewExplicit && f.Review != "" {
		cli.Review = f.Review
		changed = true
	}
	return changed
}

// activeFilterTags returns display tags for all active filters that differ
// from the most permissive baseline (state:open, draft:any, bots:show,
// archived:hide, ci:any, review:any).
func (m tuiModel) activeFilterTags() []string {
	if m.cli == nil {
		return nil
	}
	var tags []string
	if s := m.cli.PRState(); s != StateOpen {
		tags = append(tags, "state:"+s.String())
	}
	if m.cli.Draft != nil {
		if *m.cli.Draft {
			tags = append(tags, "drafts:show")
		} else {
			tags = append(tags, "drafts:hide")
		}
	}
	if m.cli.NoBot {
		tags = append(tags, "bots:hide")
	}
	if m.cli.Archived {
		tags = append(tags, "archived")
	}
	if ci := m.cli.CIStatus(); ci != CINone {
		if ci == CIFailure {
			tags = append(tags, "ci:fail")
		} else {
			tags = append(tags, "ci:"+ci.String())
		}
	}
	if m.cli.Review != "" {
		tags = append(tags, "review:"+m.cli.Review)
	}
	return tags
}
