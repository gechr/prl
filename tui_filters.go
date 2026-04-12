package main

import (
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/clog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/picker"
)

// newFilterPicker builds a picker.Model from the current filter state.
func (m tuiModel) newFilterPicker() picker.Model {
	rows := make([]picker.Row, len(filterOptionDefs))
	for i, def := range filterOptionDefs {
		choices := make([]string, len(def.choices))
		for j, c := range def.choices {
			choices[j] = c.label
		}
		rows[i] = picker.Row{Label: def.label, Choices: choices}
	}

	locked := make([]bool, len(filterOptionDefs))
	for i := range filterOptionDefs {
		locked[i] = m.isFilterRowLocked(filterRow(i))
	}

	defaults := make([]int, len(filterOptionDefs))
	for i := range filterOptionDefs {
		defaults[i] = m.defaultFilterChoice(filterRow(i))
	}

	p := picker.New(
		rows,
		m.currentFilterValues(),
		defaults,
		locked,
		make([]bool, len(filterOptionDefs)),
		picker.Styles{
			Box:            m.styles.overlayBox.Padding(tuiOptionsPadY, tuiOptionsPadX),
			Cursor:         m.styles.cursor.Render("❯ "),
			CursorLineBG:   cursorLineBG,
			CursorPad:      "  ",
			Default:        m.styles.defaultChoice,
			HelpKey:        m.styles.helpKey,
			HelpText:       m.styles.helpText,
			Inactive:       lg.NewStyle().Faint(true),
			Label:          m.styles.helpKey,
			LockedInactive: styleDim.Faint(true),
			LockedLabel:    lg.NewStyle().Faint(true),
			LockedSuffix:   "  (CLI)",
			Selected:       styleTitle.Bold(true),
		},
	)
	p.Hints = []picker.HelpHint{
		{Key: key.ArrowsLeftRight, Desc: "select"},
		{Key: "space", Desc: "cycle"},
		{Key: "⌫", Desc: "reset"},
		{Key: "enter", Desc: "apply"},
		{Key: "esc", Desc: "cancel"},
	}
	return p
}

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
func (m tuiModel) currentFilterValues() []int {
	vals := make([]int, len(filterOptionDefs))

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
	return filterOptionDefs[row].choices[m.optionsPicker.Values[row]].value
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

func (m *tuiModel) applyFilterRow(row filterRow) {
	switch row {
	case filterRowState:
		if m.optionsPicker.IsReset[row] {
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
		if m.optionsPicker.IsReset[row] {
			m.cli.NoBot = m.defaultNoBotValue()
			return
		}
		m.cli.NoBot = m.selectedFilterValue(row) == filterChoiceTrue
	case filterRowArchived:
		if m.optionsPicker.IsReset[row] {
			m.cli.Archived = false
			return
		}
		m.cli.Archived = m.selectedFilterValue(row) == filterChoiceTrue
	case filterRowCI:
		if m.optionsPicker.IsReset[row] {
			m.cli.CI = ""
			return
		}
		m.cli.CI = m.selectedFilterValue(row)
	case filterRowReview:
		if m.optionsPicker.IsReset[row] {
			m.cli.Review = ""
			return
		}
		m.cli.Review = m.selectedFilterValue(row)
	}
}

func (m tuiModel) persistedFilterValue(row filterRow) any {
	switch row {
	case filterRowState:
		if m.optionsPicker.IsReset[row] {
			return ""
		}
		return m.cli.State
	case filterRowDraft:
		return m.cli.Draft
	case filterRowBots:
		if m.optionsPicker.IsReset[row] {
			return (*bool)(nil)
		}
		return new(m.cli.NoBot)
	case filterRowArchived:
		if m.optionsPicker.IsReset[row] {
			return (*bool)(nil)
		}
		return new(m.cli.Archived)
	case filterRowCI:
		if m.optionsPicker.IsReset[row] {
			return ""
		}
		return m.cli.CI
	case filterRowReview:
		if m.optionsPicker.IsReset[row] {
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
	case key.Esc, tuiKeybindQuit:
		m.showOptions = false
		return m, nil
	case key.Enter, tuiKeybindOptions:
		return m.applyFilterOptions()
	case tuiKeybindVimDown, key.Down:
		m.optionsPicker.Down()
	case tuiKeybindVimUp, key.Up:
		m.optionsPicker.Up()
	case tuiKeybindVimRight, key.Right:
		m.optionsPicker.Right()
	case key.Space:
		m.optionsPicker.Cycle()
	case tuiKeybindVimLeft, key.Left:
		m.optionsPicker.Left()
	case "backspace", "delete":
		m.optionsPicker.Reset()
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
	return m.optionsPicker.View()
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
