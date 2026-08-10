package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
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
		{valueOpen, valueOpen},
		{valueClosed, valueClosed},
		{valueMerged, valueMerged},
		{valueReady, valueReady},
		{valueAll, valueAll},
	}},
	{
		"Drafts",
		[]filterChoice{{tuiHelpShow, ""}, {valueHide, filterChoiceFalse}},
	},
	{"Bots", []filterChoice{{tuiHelpShow, filterChoiceFalse}, {valueHide, filterChoiceTrue}}},
	{"Archived", []filterChoice{{tuiHelpShow, filterChoiceTrue}, {valueHide, filterChoiceFalse}}},
	{
		"CI",
		[]filterChoice{
			{
				ciStatusSuccess,
				ciStatusSuccess,
			},
			{ciStatusFailure, ciStatusFailure},
			{ciStatusPending, ciStatusPending},
			{valueAll, ""},
		},
	},
	{"Review", []filterChoice{
		{valueReviewFilterRequired, valueReviewFilterRequired},
		{"self", valueReviewFilterSelfRequired},
		{valueReviewFilterApproved, valueReviewFilterApproved},
		{"changes", valueReviewFilterChanges},
		{valueReviewFilterNone, valueReviewFilterNone},
		{valueAll, ""},
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

	m.persistFilterState()

	// Hide rows that clearly no longer match so the list updates immediately;
	// the background refresh replaces this with authoritative results.
	m.filterHidden = m.locallyFilteredKeys()

	// Recompute cursor/offset since viewport may change (filter indicator line).
	m.resyncCursorAndOffset()

	m.invalidateRefresh()
	return m, m.startRefresh(true)
}

// persistFilterState mirrors the current non-explicit filter values into config
// and writes a state snapshot to disk. CLI-explicit rows are session-only, so
// they're left untouched, preserving the user's persisted choice. If every row
// is CLI-locked there's nothing to persist.
func (m tuiModel) persistFilterState() {
	if m.cli.stateExplicit && m.cli.ciExplicit && m.cli.reviewExplicit &&
		m.cli.draftExplicit && m.cli.noBotExplicit && m.cli.archivedExplicit {
		return
	}
	f := &m.cfg.TUI.Filters
	if !m.cli.stateExplicit {
		f.State, _ = m.persistedFilterValue(filterRowState).(string)
	}
	if !m.cli.ciExplicit {
		f.CI, _ = m.persistedFilterValue(filterRowCI).(string)
	}
	if !m.cli.reviewExplicit {
		f.Review, _ = m.persistedFilterValue(filterRowReview).(string)
	}
	if !m.cli.draftExplicit {
		f.Draft, _ = m.persistedFilterValue(filterRowDraft).(*bool)
	}
	if !m.cli.noBotExplicit {
		f.Bots, _ = m.persistedFilterValue(filterRowBots).(*bool)
	}
	if !m.cli.archivedExplicit {
		f.Archived, _ = m.persistedFilterValue(filterRowArchived).(*bool)
	}
	if err := saveTUIState(m.cfg); err != nil {
		warnStateSaveErr(err, "filter settings")
	}
}

// locallyFilteredKeys returns the keys of loaded rows that no longer match the
// active filters, judged from data already in memory. It is deliberately
// one-directional and conservative: it only ever hides rows, and skips any
// criterion it can't evaluate offline (archived repos, unhydrated review
// decisions), leaving those rows visible until the refresh settles it.
func (m tuiModel) locallyFilteredKeys() prKeys {
	if m.cli == nil {
		return nil
	}
	hidden := make(prKeys)
	for i := range m.rows {
		if !m.matchesLocalFilters(m.rows[i].Item) {
			hidden[m.rowKeyAt(i)] = true
		}
	}
	if len(hidden) == 0 {
		return nil
	}
	return hidden
}

// matchesLocalFilters reports whether a loaded row still satisfies the filters
// that can be evaluated without hitting the API.
func (m tuiModel) matchesLocalFilters(item PRRowModel) bool {
	pr := item.PR
	switch m.cli.PRState() {
	case StateOpen:
		if !strings.EqualFold(pr.State, valueOpen) {
			return false
		}
	case StateClosed:
		if !strings.EqualFold(pr.State, valueClosed) {
			return false
		}
	case StateMerged:
		if !strings.EqualFold(pr.State, valueMerged) {
			return false
		}
	case StateReady:
		if pr.MergeStatus != MergeStatusReady {
			return false
		}
	case StateAll:
	}
	if m.cli.Draft != nil && !*m.cli.Draft && pr.IsDraft {
		return false
	}
	if m.cli.NoBot && item.Author.IsBot {
		return false
	}
	if ci := m.cli.CIStatus(); ci != CINone && !matchesCI(pr, ci) {
		return false
	}
	return m.matchesLocalReviewFilter(pr)
}

// matchesLocalReviewFilter maps the review filter onto the PR's cached review
// decision. PRs without a hydrated decision are kept, since we can't tell.
func (m tuiModel) matchesLocalReviewFilter(pr PullRequest) bool {
	if m.cli.Review == "" || !pr.reviewDecisionLoaded {
		return true
	}
	switch m.cli.Review {
	case valueReviewFilterApproved:
		return pr.ReviewDecision == valueReviewApproved
	case valueReviewFilterChanges:
		return pr.ReviewDecision == valueReviewChanges
	case valueReviewFilterNone:
		return pr.ReviewDecision != valueReviewApproved && pr.ReviewDecision != valueReviewChanges
	case valueReviewFilterRequired, valueReviewFilterSelfRequired:
		if pr.ReviewDecision != valueReviewRequired {
			return false
		}
		if m.cli.Review != valueReviewFilterSelfRequired {
			return true
		}
		// Self-required additionally drops PRs the viewer authored or has
		// already approved.
		if m.isCurrentUserPR(pr) {
			return false
		}
		return !pr.viewerApprovalLoaded || !pr.viewerApproved
	}
	return true
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
		tags = append(tags, "review:"+strings.ReplaceAll(m.cli.Review, "_", "-"))
	}
	return tags
}
