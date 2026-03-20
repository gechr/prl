package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	"github.com/gechr/prl/internal/table"
	"github.com/gechr/prl/internal/term"
)

type claudeReviewLauncher string

const (
	claudeLauncherNone    claudeReviewLauncher = ""
	claudeLauncherGhostty claudeReviewLauncher = "ghostty"
	claudeLauncherITerm2  claudeReviewLauncher = "iterm2"
)

func currentClaudeReviewLauncher() claudeReviewLauncher {
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty":
		return claudeLauncherGhostty
	case "iTerm.app":
		return claudeLauncherITerm2
	default:
		return claudeLauncherNone
	}
}

func hasClaudeReviewLauncher() bool { return currentClaudeReviewLauncher() != claudeLauncherNone }

var (
	styledOn  = lg.NewStyle().Foreground(lg.Color("118")).Render("on")
	styledOff = lg.NewStyle().Foreground(lg.Color("197")).Render("off")
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

// tuiView tracks which view is active in the interactive TUI.
type tuiView int

const (
	tuiViewList tuiView = iota
	tuiViewDiff
	tuiViewDetail
)

// tuiStyles holds all styles for the interactive TUI.
type tuiStyles struct {
	confirmNo     lg.Style
	confirmNoDim  lg.Style
	confirmYes    lg.Style
	confirmYesDim lg.Style
	cursor        lg.Style
	defaultChoice lg.Style
	diffHead      lg.Style
	helpKey       lg.Style
	helpText      lg.Style
	overlayBox    lg.Style
	selectedIndex lg.Style
	separator     lg.Style
	statusAction  lg.Style
	statusErr     lg.Style
	statusOK      lg.Style
	statusPending lg.Style
}

func newTuiStyles() tuiStyles {
	return tuiStyles{
		cursor:        lg.NewStyle().Foreground(lg.Color("198")).Bold(true),
		defaultChoice: lg.NewStyle().Foreground(lg.Color("75")).Faint(true),
		selectedIndex: lg.NewStyle().Foreground(lg.Color("118")).Bold(true),
		statusOK:      lg.NewStyle().Foreground(lg.Color("48")),
		statusErr:     lg.NewStyle().Foreground(lg.Color("196")),
		statusAction:  lg.NewStyle().Foreground(lg.Color("2")).Bold(true),
		statusPending: lg.NewStyle().Foreground(lg.Color("214")).Bold(true),
		helpText:      lg.NewStyle().Foreground(lg.Color("175")),
		helpKey:       lg.NewStyle().Foreground(lg.Color("198")).Bold(true),
		separator:     lg.NewStyle().Foreground(lg.Color("198")).Faint(true),
		diffHead:      lg.NewStyle().Foreground(lg.Color("208")).Bold(true),
		overlayBox: lg.NewStyle().
			Border(lg.RoundedBorder()).
			BorderForeground(lg.Color("198")).
			Padding(tuiConfirmPadY, tuiConfirmPadX),
		confirmYes: lg.NewStyle().
			Background(lg.Color("48")).
			Foreground(lg.Color("#000000")).
			Bold(true).
			Padding(0, 1),
		confirmYesDim: lg.NewStyle().
			Foreground(lg.Color("48")).
			Padding(0, 1),
		confirmNo: lg.NewStyle().
			Background(lg.Color("196")).
			Foreground(lg.Color("#000000")).
			Bold(true).
			Padding(0, 1),
		confirmNoDim: lg.NewStyle().
			Foreground(lg.Color("196")).
			Padding(0, 1),
	}
}

func tuiIndexWidth(total int) int {
	return len(strconv.Itoa(max(1, total)))
}

func tuiListPrefixWidth(total int) int {
	return lg.Width(
		tuiNonCursorPrefix,
	) + tuiIndexWidth(
		total,
	) + lg.Width(
		tuiNonCursorPrefix,
	)
}

func (m tuiModel) listPrefixWidth() int {
	return tuiListPrefixWidth(len(m.rows))
}

func (m tuiModel) renderTuiIndex(num int, selected bool) string {
	text := fmt.Sprintf("%*d", tuiIndexWidth(len(m.rows)), num)
	if selected {
		return m.styles.selectedIndex.Render(text)
	}
	if m.p != nil && m.p.theme != nil {
		return m.p.RenderDim(text)
	}
	return lg.NewStyle().Foreground(lg.Color("240")).Render(text)
}

// helpPair is a key-description pair for rendering help text.
type helpPair struct{ key, desc string }

// tuiAction identifies the type of action performed on a PR.
type tuiAction int

const (
	tuiActionApproved tuiAction = iota
	tuiActionAutomerged
	tuiActionBranchUpdated
	tuiActionClosed
	tuiActionCommented
	tuiActionEnqueued
	tuiActionForceMerged
	tuiActionMarkedDraft
	tuiActionMarkedReady
	tuiActionMerged
	tuiActionOpened
	tuiActionReopened
	tuiActionReviewRequested
	tuiActionUnsubscribed
)

func (a tuiAction) String() string {
	switch a {
	case tuiActionApproved:
		return resultApproved
	case tuiActionAutomerged:
		return resultAutomerged
	case tuiActionBranchUpdated:
		return resultBranchUpdated
	case tuiActionClosed:
		return resultClosed
	case tuiActionCommented:
		return resultCommented
	case tuiActionEnqueued:
		return resultEnqueued
	case tuiActionForceMerged:
		return resultForceMerged
	case tuiActionMarkedDraft:
		return resultMarkedDraft
	case tuiActionMarkedReady:
		return resultMarkedReady
	case tuiActionMerged:
		return resultMerged
	case tuiActionOpened:
		return resultOpened
	case tuiActionReopened:
		return resultReopened
	case tuiActionReviewRequested:
		return resultReviewRequested
	case tuiActionUnsubscribed:
		return resultUnsubscribed
	default:
		return resultUnknown
	}
}

// removes returns true if this action removes a PR from the list.
func (a tuiAction) removes() bool {
	switch a {
	case tuiActionClosed, tuiActionMerged, tuiActionAutomerged, tuiActionEnqueued,
		tuiActionForceMerged, tuiActionUnsubscribed:
		return true
	case tuiActionApproved,
		tuiActionBranchUpdated,
		tuiActionCommented,
		tuiActionMarkedDraft,
		tuiActionMarkedReady,
		tuiActionOpened,
		tuiActionReopened,
		tuiActionReviewRequested:
		return false
	}
	return false
}

// parseMergeResult converts a mergeOrAutomerge result string to a tuiAction.
func parseMergeResult(result string) tuiAction {
	switch result {
	case resultAutomerged:
		return tuiActionAutomerged
	case resultEnqueued:
		return tuiActionEnqueued
	default:
		return tuiActionMerged
	}
}

// draftToggleHelp returns "mark ready" for draft PRs and "mark draft" otherwise.
func draftToggleHelp(pr *PullRequest) string {
	if pr != nil && pr.IsDraft {
		return tuiHelpMarkReady
	}
	return tuiHelpMarkDraft
}

// closeReopenHelp returns "reopen" for closed PRs and "close" otherwise.
func closeReopenHelp(state string) string {
	if state == valueClosed {
		return tuiHelpReopen
	}
	return tuiHelpClose
}

// mergeHelpForPR returns "merge" for ready PRs and "automerge" otherwise.
func mergeHelpForPR(pr *PullRequest) string {
	if pr != nil && pr.MergeStatus == MergeStatusReady {
		return tuiHelpMerge
	}
	return tuiHelpAutomerge
}

// batchMergeVerb returns "Merge", "Automerge", or "Merge/Automerge"
// depending on the ready-state mix of the batch.
func batchMergeVerb(targets []targetPR) string {
	var ready, notReady int
	for _, t := range targets {
		if t.pr.MergeStatus == MergeStatusReady {
			ready++
		} else {
			notReady++
		}
	}
	switch {
	case notReady == 0:
		return "Merge"
	case ready == 0:
		return "Automerge"
	default:
		return "Merge/Automerge"
	}
}

// actionMsg is sent when an async action completes.
type actionMsg struct {
	index  int
	key    prKey // stable lookup after refresh
	action tuiAction
	err    error
}

// detailFetchedMsg is sent when PR detail has been fetched.
type detailFetchedMsg struct {
	index  int
	key    prKey // stable lookup after refresh
	detail PRDetail
	err    error
}

// diffFetchedMsg is sent when a diff has been fetched.
type diffFetchedMsg struct {
	index   int
	key     prKey // stable lookup after refresh
	diff    string
	headSHA string
	err     error
}

// batchActionMsg is sent when a batch action (multi-select) completes.
type batchActionMsg struct {
	action   tuiAction
	count    int
	failed   int
	keys     []prKey
	failures []batchResult
}

type batchResult struct {
	key prKey
	ref string
	url string
	err error
}

// clearStatusMsg clears the status bar after a timeout.
type clearStatusMsg struct{ id int }

// claudeReviewMsg is sent when the Claude review clone+launch completes.
type claudeReviewMsg struct {
	index int
	key   prKey // stable lookup after refresh
	err   error
}

// slackSentMsg is sent when a Slack send completes.
type slackSentMsg struct {
	count  int
	output string // first line of CLI output (e.g. "Message sent to #channel")
	err    error
}

// jumpTimeoutMsg fires when the digit-input window expires.
type jumpTimeoutMsg struct{ id int }

// refreshTickMsg fires when it's time to start a background refresh.
type refreshTickMsg struct{ id int }

// spinnerTickMsg fires to advance the spinner animation frame.
type spinnerTickMsg struct{ id int }

// screenCheckMsg fires periodically to probe the cursor position and detect
// external screen clears (e.g. Cmd+K in iTerm2).
type screenCheckMsg struct{ id int }

// refreshResultMsg carries the result of a background data refresh.
type refreshResultMsg struct {
	rows      []TableRow
	items     []PRRowModel
	err       error
	filterGen int // generation at time of launch; stale results are discarded
	spinnerID int // refresh request generation; stale completions are discarded
}

// tuiModel is the Bubble Tea model for the interactive TUI.
//
//nolint:recvcheck // selection helpers use pointer receivers to mutate maps/fields in-place
type tuiModel struct {
	items         []PRRowModel // canonical data for rerender on resize/refresh
	rows          []TableRow   // current rendered order; row.Item is the action target
	header        string
	colWidths     []int // visible column widths for header click hit-testing
	sortColumn    string
	sortAsc       bool
	cursor        int
	offset        int
	view          tuiView
	diff          string
	diffLines     []string
	diffKey       prKey
	diffScroll    int
	detail        PRDetail
	detailLines   []string
	detailKey     prKey
	detailScroll  int
	detailLoading bool
	statusMsg     string
	statusErr     bool
	statusID      int
	actions       *ActionRunner
	width         int
	height        int
	styles        tuiStyles
	removed       prKeys
	selected      prKeys

	// Filter options overlay.
	showOptions   bool
	optionsCursor filterRow
	optionsValues [6]int // index into choices for each filterOptionDef
	optionsReset  [6]bool

	// Generation counter for discarding stale refresh results after filter changes.
	filterGen int

	// Diff queue for sequential multi-PR review.
	diffQueue      []prKey // remaining PR keys to diff through
	diffHistory    []prKey // previously viewed PR keys (for going back)
	diffQueueTotal int     // total PRs in the queue (for counter display)
	diffAdvanced   bool    // true when queue was advanced from diff view (skip actionMsg view switch)
	diffLoading    bool

	// Empty overlay dismissed (esc to dismiss, then esc again to quit).
	dismissedEmpty bool

	// Filter mode.
	filterInput textinput.Model

	// Pending digit jump (e.g. "1" waiting for second digit).
	jumpDigit int // first digit (1-9), 0 = no pending jump
	jumpID    int // timeout generation

	// Pending confirmation (e.g. close/merge).
	confirmAction     string               // "close", "merge", "diff"
	confirmPrompt     string               // prompt text for modal
	confirmSubject    string               // target description for progress (e.g. "data-team#8", "3 PRs")
	confirmURL        string               // optional URL for hyperlinking the subject in progress
	confirmCmd        tea.Cmd              // command to run on confirmation
	confirmCmdFn      func(string) tea.Cmd // command factory when confirmHasInput (receives input value)
	confirmYes        bool                 // true = Yes selected, false = No selected
	confirmHasInput   bool                 // true when modal includes a text input
	confirmInputLabel string               // label above the textarea (default: "Comment")
	confirmInput      textarea.Model       // optional text input (e.g. close comment)

	// Background auto-refresh.
	autoRefresh     bool
	refreshing      bool      // true while a background refresh is in-flight
	lastInteraction time.Time // tracks last user keypress for idle-based refresh decay
	// showRefreshStatus is true when the in-flight refresh was triggered by
	// applying options and should show a temporary "Refreshing..." status.
	showRefreshStatus bool
	refreshID         int     // generation counter to discard stale refresh ticks
	spinner           spinner // spinner animation frames
	spinnerTick       int     // current spinner frame index
	spinnerID         int     // generation counter to discard stale ticks
	screenCheckID     int     // generation counter for screen-check heartbeats
	repaintTick       bool    // toggled each heartbeat to invalidate the renderer cache
	showHelp          bool

	// Cached GitHub login of the authenticated user (resolved lazily).
	login string

	// Retained for re-rendering the table on resize and background refresh.
	p        *prl
	cli      *CLI
	cfg      *Config
	tty      bool
	resolver *AuthorResolver
	rest     *api.RESTClient
	params   *SearchParams
}

// isCurrentUserPR reports whether the given PR was authored by the authenticated user.
func (m tuiModel) isCurrentUserPR(pr PullRequest) bool {
	return m.login != "" && strings.EqualFold(pr.Author.Login, m.login)
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

func cloneCSVFlag(src CSVFlag) CSVFlag {
	return CSVFlag{Values: append([]string(nil), src.Values...)}
}

func cloneCSVFlagPtr(src *CSVFlag) *CSVFlag {
	if src == nil {
		return nil
	}
	cloned := cloneCSVFlag(*src)
	return &cloned
}

func cloneSearchParams(src *SearchParams) *SearchParams {
	if src == nil {
		return nil
	}
	cloned := *src
	return &cloned
}

func cloneCLI(src *CLI) *CLI {
	if src == nil {
		return nil
	}

	dst := *src
	dst.Query = append([]string(nil), src.Query...)
	dst.Organization = cloneCSVFlag(src.Organization)
	dst.Filter = append([]string(nil), src.Filter...)
	dst.Author = cloneCSVFlagPtr(src.Author)
	dst.Commenter = cloneCSVFlag(src.Commenter)
	dst.Team = cloneCSVFlag(src.Team)
	dst.Involves = cloneCSVFlag(src.Involves)
	dst.ReviewRequested = cloneCSVFlag(src.ReviewRequested)
	dst.ReviewedBy = cloneCSVFlag(src.ReviewedBy)
	dst.Columns = cloneCSVFlag(src.Columns)

	if src.Limit != nil {
		limit := *src.Limit
		dst.Limit = &limit
	}
	if src.Output != nil {
		output := *src.Output
		dst.Output = &output
	}
	if src.Sort != nil {
		sort := *src.Sort
		dst.Sort = &sort
	}
	if src.Merge != nil {
		merge := *src.Merge
		dst.Merge = &merge
	}
	if src.Draft != nil {
		draft := *src.Draft
		dst.Draft = &draft
	}

	return &dst
}

type refreshSnapshot struct {
	cli      *CLI
	cfg      *Config
	p        *prl
	tty      bool
	resolver *AuthorResolver
	rest     *api.RESTClient
	params   *SearchParams
	width    int
}

func newRefreshSnapshot(m tuiModel) refreshSnapshot {
	return refreshSnapshot{
		cli:      cloneCLI(m.cli),
		cfg:      m.cfg,
		p:        m.p,
		tty:      m.tty,
		resolver: m.resolver,
		rest:     m.rest,
		params:   cloneSearchParams(m.params),
		width:    m.width,
	}
}

func (r refreshSnapshot) run() refreshResultMsg {
	prs, err := executeSearch(r.rest, r.params)
	if err != nil {
		return refreshResultMsg{err: err}
	}
	prs, err = applyFilters(r.cli, prs)
	if err != nil {
		return refreshResultMsg{err: err}
	}

	// Post-fetch filters: --closed-by, --merged-by
	prs = applyTimelineFilters(r.rest, r.cli, prs)

	// Determine if post-enrichment filters require GraphQL data.
	needsEnrich := r.cli.PRState() == StateReady || r.cli.CIStatus() != CINone

	if len(prs) > 0 && (!r.cli.Quick || needsEnrich) {
		if gql, gqlErr := newGraphQLClient(withDebug(r.cli.Debug)); gqlErr == nil {
			enrichMergeStatus(gql, prs)
		}
	} else if len(prs) > 0 {
		for i := range prs {
			if prs[i].State == valueOpen {
				prs[i].MergeStatus = MergeStatusBlocked
			}
		}
	}

	// Post-enrichment filters.
	if r.cli.PRState() == StateReady {
		prs = filterReady(prs)
	}
	if ci := r.cli.CIStatus(); ci != CINone {
		prs = filterByCI(prs, ci)
	}

	orgFilter := singleOrg(r.cli.Organization.Values)
	items := buildPRRowModels(prs, orgFilter, r.resolver)
	termWidth := max(0, r.width-tuiListPrefixWidth(len(items)))
	renderer := r.p.newTableRenderer(r.cli, r.tty, termWidth, table.WithShowIndex(false))
	_, rows, _ := renderTUITable(r.p, renderer, items, "", false, termWidth)
	return refreshResultMsg{rows: rows, items: items}
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

func (m tuiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.scheduleScreenCheck()}
	if m.autoRefresh {
		cmds = append(
			cmds,
			scheduleRefresh(len(m.items), m.refreshID, time.Since(m.lastInteraction)),
		)
	}
	return tea.Batch(cmds...)
}

// scheduleRefresh returns a tea.Cmd that fires a refreshTickMsg after a delay
// scaled by the number of results (reusing watch-mode intervals) and further
// slowed by user inactivity. The idle parameter is time since the last
// keypress; after watchIdleDecay of inactivity the interval reaches
// watchIdleMax regardless of result count.
func scheduleRefresh(n, id int, idle time.Duration) tea.Cmd {
	d := watchInterval(n)
	if idle > 0 && idle < watchIdleDecay {
		// Linearly blend from the base interval toward watchIdleMax.
		frac := float64(idle) / float64(watchIdleDecay)
		d = time.Duration(float64(d)*(1-frac) + float64(watchIdleMax)*frac)
	} else if idle >= watchIdleDecay {
		d = watchIdleMax
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshTickMsg{id: id} })
}

// scheduleSpinnerTick returns a tea.Cmd that fires a spinnerTickMsg after the
// spinner's interval, scoped to the current generation (spinnerID).
func (m tuiModel) scheduleSpinnerTick() tea.Cmd {
	id := m.spinnerID
	d := m.spinner.interval
	return tea.Tick(d, func(time.Time) tea.Msg { return spinnerTickMsg{id: id} })
}

// scheduleScreenCheck returns a tea.Cmd that fires a screenCheckMsg after the
// heartbeat interval, scoped to the current generation (screenCheckID).
func (m tuiModel) scheduleScreenCheck() tea.Cmd {
	id := m.screenCheckID
	return tea.Tick(tuiScreenCheckInt, func(time.Time) tea.Msg {
		return screenCheckMsg{id: id}
	})
}

// applyRepaintMarker toggles the WindowTitle between two values so that
// the renderer's viewEquals check sees a changed View on every heartbeat tick.
// This forces a periodic full repaint, recovering from external screen clears
// (e.g. Cmd+K in iTerm2) that the differential renderer cannot detect.
func (m tuiModel) applyRepaintMarker(v *tea.View) {
	if m.repaintTick {
		v.WindowTitle = " "
	}
}

// touchInteraction records that the user interacted with the TUI,
// resetting the idle decay for auto-refresh scheduling.
func (m *tuiModel) touchInteraction() { m.lastInteraction = time.Now() }

// rescheduleRefresh invalidates older refresh ticks and schedules a new one.
func (m *tuiModel) rescheduleRefresh() tea.Cmd {
	if m.autoRefresh {
		m.refreshID++
		return scheduleRefresh(len(m.items), m.refreshID, time.Since(m.lastInteraction))
	}
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok &&
		(key.String() == tuiKeyCtrlC || key.String() == tuiKeyCtrlD) &&
		!m.filterInput.Focused() {
		return m, tea.Quit
	}

	// Dismiss help overlay on any keypress.
	if _, ok := msg.(tea.KeyMsg); ok && m.showHelp {
		m.showHelp = false
		return m, nil
	}

	// Handle filter options overlay keys.
	if key, ok := msg.(tea.KeyMsg); ok && m.showOptions {
		return m.updateOptionsOverlay(key)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.header, m.rows, m.colWidths = m.rerender()
		if m.view == tuiViewDiff {
			m.diffLines = wrapDiffLines(m.diff, m.width)
			m.diffScroll = min(m.diffScroll, m.diffMaxScroll())
		}
		if m.view == tuiViewDetail && len(m.detailLines) > 0 {
			m.detailLines = m.renderDetailContent()
		}
		return m, nil

	case tea.MouseClickMsg:
		if m.view == tuiViewList && msg.Button == tea.MouseLeft && msg.Y == 0 {
			col := m.headerColumnAt(msg.X)
			if col != "" {
				return m.toggleSort(col)
			}
		}
		return m, nil

	case tea.MouseWheelMsg:
		switch m.view {
		case tuiViewList:
			switch msg.Button {
			case tea.MouseWheelDown:
				if next, ok := m.nextVisible(1); ok {
					m.cursor = next
					m.offset = m.scrolledOffset()
				}
			case tea.MouseWheelUp:
				if next, ok := m.nextVisible(-1); ok {
					m.cursor = next
					m.offset = m.scrolledOffset()
				}
			}
		case tuiViewDiff:
			switch msg.Button {
			case tea.MouseWheelDown:
				if m.diffScroll < m.diffMaxScroll() {
					m.diffScroll++
				}
			case tea.MouseWheelUp:
				if m.diffScroll > 0 {
					m.diffScroll--
				}
			}
		case tuiViewDetail:
			viewport := m.detailViewport()
			switch msg.Button {
			case tea.MouseWheelDown:
				if m.detailScroll < len(m.detailLines)-viewport {
					m.detailScroll++
				}
			case tea.MouseWheelUp:
				if m.detailScroll > 0 {
					m.detailScroll--
				}
			}
		}
		return m, nil

	case actionMsg:
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil // PR no longer in list (removed by refresh)
		}
		pr := m.rows[idx].Item.PR
		// HintError: action succeeded but has a follow-up hint for the user.
		hint, isHint := errors.AsType[*HintError](msg.err)
		if isHint {
			msg.err = nil // treat as success
		}
		if msg.err != nil {
			flashCmd := flashResult(
				&m,
				msg.action.String()+" failed:",
				fmt.Sprintf("%v", msg.err),
				"",
				true,
			)
			// Queue already advanced from diff view - just flash, stay in diff.
			if m.diffAdvanced {
				m.diffAdvanced = false
				return m, flashCmd
			}
			// Advance diff queue even on failure so user can continue reviewing.
			if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
				m.diffHistory = append(m.diffHistory, msg.key)
				return m, tea.Batch(flashCmd, nextCmd)
			}
			if m.view == tuiViewDiff {
				m.view = tuiViewList
				m.diffKey = ""
				m.diffHistory = nil
				m.diffQueueTotal = 0
				return m, tea.Batch(flashCmd, m.rescheduleRefresh())
			}
			return m, flashCmd
		}
		if msg.action.removes() {
			m.removed[msg.key] = true
			m.cursor = m.adjustedCursor()
			m.offset = m.scrolledOffset()
		}
		flashCmd := flashResult(&m, msg.action.String(), pr.Ref(), pr.URL, false)
		if hint != nil {
			cmd := lg.NewStyle().Bold(true).Foreground(lg.Color("198")).Render(hint.Hint)
			m.confirmAction = tuiActionInfo
			m.confirmPrompt = "To also mute notifications, run:\n\n" + cmd
			m.confirmCmd = nil
		}
		// Queue already advanced from diff view - just flash, stay in diff.
		if m.diffAdvanced {
			m.diffAdvanced = false
			return m, flashCmd
		}
		if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
			m.diffHistory = append(m.diffHistory, msg.key)
			return m, tea.Batch(flashCmd, nextCmd)
		}
		if m.view == tuiViewDiff {
			m.view = tuiViewList
			m.diffKey = ""
			m.diffHistory = nil
			m.diffQueueTotal = 0
			return m, tea.Batch(flashCmd, m.rescheduleRefresh())
		}
		return m, flashCmd

	case batchActionMsg:
		if msg.action.removes() {
			for _, key := range msg.keys {
				m.removed[key] = true
				delete(m.selected, key)
			}
			m.cursor = m.adjustedCursor()
			m.offset = m.scrolledOffset()
		}
		m.selected = make(prKeys)
		status := fmt.Sprintf("%d/%d", msg.count-msg.failed, msg.count)
		if msg.failed > 0 {
			m.confirmAction = tuiActionInfo
			m.confirmYes = true
			m.confirmPrompt = renderBatchFailurePrompt(msg)
			m.confirmCmd = nil
			return m, flashResult(
				&m,
				msg.action.String(),
				status+" ("+fmt.Sprintf("%d failed", msg.failed)+")",
				"",
				true,
			)
		}
		return m, flashResult(&m, msg.action.String(), status+" PRs", "", false)

	case clearStatusMsg:
		if msg.id == m.statusID {
			m.statusMsg = ""
		}
		return m, nil

	case diffFetchedMsg:
		if !m.diffLoading {
			return m, nil // stale fetch from a dismissed diff view
		}
		m.diffLoading = false
		if msg.err != nil {
			flashCmd := flashResult(&m, "Diff failed:", fmt.Sprintf("%v", msg.err), "", true)
			// Skip to next in queue if available.
			if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
				return m, tea.Batch(flashCmd, nextCmd)
			}
			return m, flashCmd
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil // PR no longer in list
		}
		m.diffKey = msg.key
		pr := m.rows[idx].Item.PR
		m.diff = highlightDiff(msg.diff, pr.URL, msg.headSHA)
		m.diffLines = wrapDiffLines(m.diff, m.width)
		m.diffScroll = 0
		m.view = tuiViewDiff
		m.statusMsg = ""
		return m, nil

	case detailFetchedMsg:
		m.detailLoading = false
		if msg.err != nil {
			return m, flashResult(&m, "Detail failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil // PR no longer in list
		}
		m.detailKey = msg.key
		m.detail = msg.detail
		m.detailLines = m.renderDetailContent()
		m.detailScroll = 0
		m.view = tuiViewDetail
		m.statusMsg = ""
		return m, nil

	case claudeReviewMsg:
		if msg.err != nil {
			return m, flashResult(&m, "Claude failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		return m, flashResult(
			&m,
			"Claude review launched",
			pr.Ref(),
			pr.URL,
			false,
		)

	case slackSentMsg:
		if msg.err != nil {
			return m, flashResult(&m, "Slack failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		status := msg.output
		if status == "" {
			status = fmt.Sprintf("%d PRs", msg.count)
			if msg.count == 1 {
				status = "1 PR"
			}
		}
		return m, flashResult(&m, "Sent to Slack:", status, "", false)

	case jumpTimeoutMsg:
		if msg.id == m.jumpID && m.jumpDigit > 0 {
			visible := m.visibleIndices()
			target := m.jumpDigit - 1
			if target >= 0 && target < len(visible) {
				m.cursor = visible[target]
				m.offset = m.scrolledOffset()
			}
			m.jumpDigit = 0
		}
		return m, nil

	case screenCheckMsg:
		if msg.id != m.screenCheckID {
			return m, nil
		}
		return m, tea.Batch(tea.Raw(ansiDECXCPR), m.scheduleScreenCheck())

	case tea.CursorPositionMsg:
		// After an external screen clear (e.g. Cmd+K in iTerm2) the cursor
		// ends up on the first row, which should never happen in a full-screen
		// alt-screen view. Toggle repaintTick to invalidate viewEquals and
		// sequence a ClearScreen (erases the renderer's internal screen state)
		// followed by another cursor probe. On the second probe the scr is
		// already erased and the toggled view forces a full repaint.
		if msg.Y == 0 && m.height > 1 {
			m.touchInteraction()
			m.repaintTick = !m.repaintTick
			return m, tea.Sequence(tea.ClearScreen, tea.Raw(ansiDECXCPR))
		}
		return m, nil

	case refreshTickMsg:
		if !m.autoRefresh || m.view != tuiViewList || msg.id != m.refreshID || m.refreshing {
			return m, nil
		}
		m.refreshing = true
		m.spinnerTick = 0
		m.spinnerID++
		snapshot := newRefreshSnapshot(m)
		filterGen := m.filterGen
		spinnerID := m.spinnerID
		return m, tea.Batch(
			m.scheduleSpinnerTick(),
			func() tea.Msg {
				result := snapshot.run()
				result.filterGen = filterGen
				result.spinnerID = spinnerID
				return result
			},
		)

	case spinnerTickMsg:
		if !m.refreshing || msg.id != m.spinnerID {
			return m, nil
		}
		m.spinnerTick++
		return m, m.scheduleSpinnerTick()

	case refreshResultMsg:
		if msg.spinnerID != m.spinnerID {
			return m, nil
		}
		// Discard stale results from pre-filter-change refreshes.
		if msg.filterGen != m.filterGen {
			m.refreshing = false
			m.showRefreshStatus = false
			m.statusMsg = ""
			return m, m.rescheduleRefresh()
		}
		m.refreshing = false
		if msg.err != nil {
			m.showRefreshStatus = false
			return m, tea.Batch(
				tuiFlashMessage(&m, "Refresh failed: "+msg.err.Error(), true),
				m.rescheduleRefresh(),
			)
		}
		if m.showRefreshStatus {
			m.showRefreshStatus = false
			m.statusMsg = ""
		}
		m.dismissedEmpty = false
		// Re-apply sort to fresh rows before merging state.
		rows := msg.rows
		if m.sortColumn != "" {
			renderer := m.tableRendererFor(len(msg.items))
			rows = table.SortRows(rows, renderer.Columns(), m.sortColumn, m.sortAsc)
		}
		m = m.mergeRefresh(rows, msg.items)
		// Re-render header with current sort state; the background
		// goroutine may have captured a stale sortColumn/sortAsc.
		m.header, _, m.colWidths = m.rerender()
		return m, m.rescheduleRefresh()

	case tea.KeyMsg:
		m.touchInteraction()
		switch m.view {
		case tuiViewDiff:
			return m.updateDiffView(msg)
		case tuiViewDetail:
			return m.updateDetailView(msg)
		case tuiViewList:
			return m.updateListView(msg)
		}
	}

	return m, nil
}

func (m tuiModel) updateListView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle filter input mode.
	if m.filterInput.Focused() {
		switch msg.String() {
		case tuiKeyEnter:
			m.filterInput.Blur()
			return m, nil
		case tuiKeyEsc, tuiKeyCtrlC, tuiKeyCtrlD:
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.cursor = m.adjustedCursor()
			m.offset = m.scrolledOffset()
			return m, nil
		case tuiKeyUp, tuiKeyDown:
			dir := 1
			if msg.String() == tuiKeyUp {
				dir = -1
			}
			if next, ok := m.nextVisible(dir); ok {
				m.cursor = next
				m.offset = m.scrolledOffset()
			}
			return m, nil
		default:
			prev := m.filterInput.Value()
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			if m.filterInput.Value() == prev {
				return m, cmd
			}
			if vis := m.visibleIndices(); len(vis) > 0 {
				m.cursor = vis[0]
			}
			m.offset = 0
			return m, cmd
		}
	}

	// Handle pending confirmation.
	if m.confirmAction != "" {
		return m.updateConfirmOverlay(msg)
	}

	// Freeze interactions while a view is loading in the background.
	if m.detailLoading || m.diffLoading {
		switch msg.String() {
		case tuiKeyEsc, "q":
			return m, tea.Quit
		default:
			return m, nil
		}
	}

	switch msg.String() {
	case tuiKeyEsc:
		if m.filterInput.Value() != "" {
			m.filterInput.SetValue("")
			m.cursor = m.adjustedCursor()
			m.offset = m.scrolledOffset()
			return m, nil
		}
		if len(m.visibleIndices()) == 0 && !m.dismissedEmpty {
			m.dismissedEmpty = true
			return m, nil
		}
		return m, tea.Quit

	case tuiKeybindQuit:
		return m, tea.Quit

	case tuiKeybindFilter:
		// The filter bar takes one row from the viewport; bump offset so the
		// cursor row doesn't get pushed off-screen.
		visible := m.visibleIndices()
		viewport := m.listViewport() - 1 // account for incoming filter bar
		pos := slices.Index(visible, m.cursor)
		if pos >= 0 && pos >= m.offset+viewport {
			m.offset = pos - viewport + 1
		}
		return m, m.filterInput.Focus()

	case tuiKeyEnter:
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		idx := m.cursor
		actions := m.actions
		prCopy := *pr
		m.statusMsg = m.styles.statusPending.Render("Fetching") + " " +
			lg.NewStyle().Foreground(lg.Color("117")).Render(prCopy.Ref()) + valueEllipsis
		m.statusErr = false
		m.detailLoading = true
		key := makePRKey(prCopy)
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			detail, err := actions.fetchPRDetail(owner, repo, prCopy.Number)
			return detailFetchedMsg{index: idx, key: key, detail: detail, err: err}
		}

	case tuiKeybindVimDown, tuiKeyDown:
		if next, ok := m.nextVisible(1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case tuiKeybindVimUp, tuiKeyUp:
		if next, ok := m.nextVisible(-1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case tuiKeyCtrlF:
		viewport := m.listViewport()
		for range viewport {
			if next, ok := m.nextVisible(1); ok {
				m.cursor = next
			}
		}
		m.offset = m.scrolledOffset()
		return m, nil

	case tuiKeyCtrlB:
		viewport := m.listViewport()
		for range viewport {
			if next, ok := m.nextVisible(-1); ok {
				m.cursor = next
			}
		}
		m.offset = m.scrolledOffset()
		return m, nil

	case tuiKeybindTop:
		visible := m.visibleIndices()
		if len(visible) > 0 {
			m.cursor = visible[0]
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case tuiKeybindBottom:
		visible := m.visibleIndices()
		if len(visible) > 0 {
			m.cursor = visible[len(visible)-1]
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case tuiKeySpace:
		m.toggleCurrentSelection()
		return m, nil

	case tuiKeybindExtendSelectionDown:
		m.extendSelectionAndMove(1)
		return m, nil

	case tuiKeybindExtendSelectionUp:
		m.extendSelectionAndMove(-1)
		return m, nil

	case tuiKeybindSelectAll:
		visible := m.visibleIndices()
		allSelected := len(visible) > 0
		for _, idx := range visible {
			if !m.selected[m.rowKeyAt(idx)] {
				allSelected = false
				break
			}
		}
		if allSelected {
			m.selected = make(prKeys)
		} else {
			for _, idx := range visible {
				m.selected[m.rowKeyAt(idx)] = true
			}
		}
		return m, nil

	case tuiKeybindInvertSelection:
		for _, idx := range m.visibleIndices() {
			key := m.rowKeyAt(idx)
			if m.selected[key] {
				delete(m.selected, key)
			} else {
				m.selected[key] = true
			}
		}
		return m, nil

	case tuiKeybindApprove:
		targets := m.targetApprovablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		setupConfirmBatch(&m, targets, tuiActionApprove, tuiActionApproved, "Approve",
			func(a *ActionRunner, pr PullRequest) error {
				owner, repo := prOwnerRepo(pr)
				return a.approve(owner, repo, pr.Number)
			})
		return m, nil

	case tuiKeybindApproveNoConfirm:
		targets := m.targetApprovablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		return m, batchCmd(m.actions, targets, tuiActionApproved,
			func(a *ActionRunner, pr PullRequest) error {
				owner, repo := prOwnerRepo(pr)
				return a.approve(owner, repo, pr.Number)
			})

	case tuiKeybindDiff:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		if len(targets) > 1 {
			queue := make([]prKey, 0, len(targets)-1)
			for _, t := range targets[1:] {
				queue = append(queue, makePRKey(t.pr))
			}
			m.diffQueue = queue
			m.diffQueueTotal = len(targets)
		} else {
			m.diffQueue = nil
			m.diffQueueTotal = 0
		}
		first := targets[0]
		actions := m.actions
		m.diffLoading = true
		flashPending(&m, statusDiffing, &first.pr)
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(first.pr)
			diff, headSHA, err := actions.fetchDiff(owner, repo, first.pr.Number)
			return diffFetchedMsg{
				index:   first.index,
				key:     makePRKey(first.pr),
				diff:    diff,
				headSHA: headSHA,
				err:     err,
			}
		}

	case tuiKeybindMerge:
		targets := m.targetMergeablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionMerge
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			verb := "Automerge "
			if targets[0].pr.MergeStatus == MergeStatusReady {
				verb = "Merge "
			}
			m.confirmPrompt = verb + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				result, err := actions.mergeOrAutomerge(owner, repo, t.pr)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: parseMergeResult(result),
					err:    err,
				}
			}
		} else {
			m.confirmSubject = fmt.Sprintf("%d PRs", len(targets))
			m.confirmPrompt = fmt.Sprintf("%s %d PRs?", batchMergeVerb(batch), len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					tuiActionMerged,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
						_, err := a.mergeOrAutomerge(owner, repo, pr)
						return err
					},
				)
			}
		}
		return m, nil

	case tuiKeybindApproveMerge:
		targets := m.targetApprovablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionApproveMerge
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			m.confirmPrompt = "Approve & merge " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				if err := actions.approve(owner, repo, t.pr.Number); err != nil {
					return actionMsg{
						index:  t.index,
						key:    makePRKey(t.pr),
						action: tuiActionApproved,
						err:    err,
					}
				}
				result, err := actions.mergeOrAutomerge(owner, repo, t.pr)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: parseMergeResult(result),
					err:    err,
				}
			}
		} else {
			m.confirmSubject = fmt.Sprintf("%d PRs", len(targets))
			m.confirmPrompt = fmt.Sprintf("Approve & merge %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					tuiActionMerged,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
						if err := a.approve(owner, repo, pr.Number); err != nil {
							return err
						}
						_, err := a.mergeOrAutomerge(owner, repo, pr)
						return err
					},
				)
			}
		}
		return m, nil

	case tuiKeybindForceMerge:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionForceMerge
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			m.confirmPrompt = "Force-merge " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				err := actions.forceMergePR(t.pr.NodeID)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionForceMerged,
					err:    err,
				}
			}
		} else {
			m.confirmSubject = fmt.Sprintf("%d PRs", len(targets))
			m.confirmPrompt = fmt.Sprintf("Force-merge %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					tuiActionForceMerged,
					func(a *ActionRunner, pr PullRequest) error {
						return a.forceMergePR(pr.NodeID)
					},
				)
			}
		}
		return m, nil

	case tuiKeybindClose:
		// Dynamic close/reopen: if the current PR is closed, reopen; otherwise close.
		pr := m.currentPR()
		if pr != nil && strings.ToLower(pr.State) == valueClosed {
			targets := m.targetPRs()
			if len(targets) == 0 {
				return m, nil
			}
			return m, batchCmd(m.actions, targets, tuiActionReopened,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.reopenPR(owner, repo, pr.Number)
				})
		}
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionClose
		m.confirmYes = true
		m.confirmHasInput = true
		m.confirmInput.SetValue("")
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			m.confirmPrompt = "Close " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmdFn = func(comment string) tea.Cmd {
				return func() tea.Msg {
					owner, repo := prOwnerRepo(t.pr)
					err := actions.closePR(owner, repo, t.pr.Number, comment, false)
					return actionMsg{
						index:  t.index,
						key:    makePRKey(t.pr),
						action: tuiActionClosed,
						err:    err,
					}
				}
			}
		} else {
			m.confirmSubject = fmt.Sprintf("%d PRs", len(targets))
			m.confirmPrompt = fmt.Sprintf("Close %d PRs?", len(targets))
			m.confirmCmdFn = func(comment string) tea.Cmd {
				return func() tea.Msg {
					return runBatchAction(
						actions,
						batch,
						tuiActionClosed,
						func(a *ActionRunner, pr PullRequest) error {
							owner, repo := prOwnerRepo(pr)
							return a.closePR(owner, repo, pr.Number, comment, false)
						},
					)
				}
			}
		}
		return m, m.confirmInput.Focus()

	case tuiKeybindDraftToggle:
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		actions := m.actions
		idx := m.cursor
		prCopy := *pr
		if pr.IsDraft {
			flashPending(&m, statusMarkingReady, pr)
			return m, func() tea.Msg {
				err := actions.markReady(prCopy.NodeID)
				return actionMsg{
					index:  idx,
					key:    makePRKey(prCopy),
					action: tuiActionMarkedReady,
					err:    err,
				}
			}
		}
		flashPending(&m, statusMarkingDraft, pr)
		return m, func() tea.Msg {
			err := actions.markDraft(prCopy.NodeID)
			return actionMsg{
				index:  idx,
				key:    makePRKey(prCopy),
				action: tuiActionMarkedDraft,
				err:    err,
			}
		}

	case tuiKeybindComment:
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		actions := m.actions
		idx := m.cursor
		prCopy := *pr
		m.confirmAction = tuiActionComment
		m.confirmSubject = prCopy.Ref()
		m.confirmURL = prCopy.URL
		m.confirmYes = true
		m.confirmHasInput = true
		m.confirmInput.SetValue("")
		m.confirmPrompt = "Comment on " + styledRef(&prCopy) + "?"
		m.confirmCmdFn = func(comment string) tea.Cmd {
			return func() tea.Msg {
				owner, repo := prOwnerRepo(prCopy)
				err := actions.comment(owner, repo, prCopy.Number, comment)
				return actionMsg{
					index:  idx,
					key:    makePRKey(prCopy),
					action: tuiActionCommented,
					err:    err,
				}
			}
		}
		return m, m.confirmInput.Focus()

	case tuiKeybindReview:
		if !hasClaudeReviewLauncher() {
			m.confirmAction = tuiActionInfo
			m.confirmYes = true
			m.confirmPrompt = tuiClaudeReviewUnsupported
			m.confirmCmd = nil
			return m, nil
		}
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		state := strings.ToLower(pr.State)
		if pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		idx := m.cursor
		prCopy := *pr
		m = m.prepareClaudeReviewConfirm(prCopy, idx)
		return m, m.confirmInput.Focus()

	case tuiKeybindReviewNoConfirm:
		if !hasClaudeReviewLauncher() {
			m.confirmAction = tuiActionInfo
			m.confirmYes = true
			m.confirmPrompt = tuiClaudeReviewUnsupported
			m.confirmCmd = nil
			return m, nil
		}
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		state := strings.ToLower(pr.State)
		if pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		idx := m.cursor
		prCopy := *pr
		prompt := defaultClaudeReviewPrompt(prCopy)
		return m, func() tea.Msg {
			err := launchClaudeReview(prCopy, prompt)
			return claudeReviewMsg{index: idx, key: makePRKey(prCopy), err: err}
		}

	case tuiKeybindSlack:
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		prs := make([]PullRequest, len(targets))
		for i, t := range targets {
			prs[i] = t.pr
		}
		count := len(prs)
		cfg := m.cfg
		cli := m.cli
		m.confirmAction = tuiActionSendSlack
		m.confirmYes = true
		if count == 1 {
			m.confirmSubject = prs[0].Ref()
			m.confirmURL = prs[0].URL
			m.confirmPrompt = "Send " + styledRef(&prs[0]) + " to Slack?"
		} else {
			m.confirmSubject = fmt.Sprintf("%d PRs", count)
			m.confirmPrompt = fmt.Sprintf("Send %d PRs to Slack?", count)
		}
		m.confirmCmd = func() tea.Msg {
			out, err := sendSlack(prs, cli, cfg)
			return slackSentMsg{count: count, output: out, err: err}
		}
		return m, nil

	case tuiKeybindSlackNoConfirm:
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		prs := make([]PullRequest, len(targets))
		for i, t := range targets {
			prs[i] = t.pr
		}
		count := len(prs)
		if count == 1 {
			flashPending(&m, statusSending, &prs[0])
		} else {
			m.statusMsg = m.styles.statusPending.Render(
				fmt.Sprintf("Sending %d PRs", count),
			) + valueEllipsis
			m.statusErr = false
		}
		cfg := m.cfg
		cli := m.cli
		return m, func() tea.Msg {
			out, err := sendSlack(prs, cli, cfg)
			return slackSentMsg{count: count, output: out, err: err}
		}

	case tuiKeybindOpen:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		for _, t := range targets {
			_ = openBrowser(t.pr.URL)
		}
		last := targets[len(targets)-1]
		msg := fmt.Sprintf("%d PRs", len(targets))
		if len(targets) == 1 {
			msg = last.pr.Ref()
		}
		m.selected = make(prKeys)
		return m, flashResult(&m, tuiActionOpened.String(), msg, last.pr.URL, false)

	case tuiKeybindCopyURL:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		urls := make([]string, len(targets))
		for i, t := range targets {
			urls[i] = t.pr.URL
		}
		natsort(urls)
		_ = copyToClipboard(strings.Join(urls, "\n"))
		last := targets[len(targets)-1]
		msg := last.pr.Ref()
		if len(targets) > 1 {
			msg = fmt.Sprintf("%d URLs", len(targets))
		}
		m.selected = make(prKeys)
		return m, flashResult(&m, resultCopied, msg, "", false)

	case tuiKeybindUpdateBranch:
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		setupConfirmBatch(
			&m,
			targets,
			tuiActionUpdateBranch,
			tuiActionBranchUpdated,
			"Update branch for",
			func(a *ActionRunner, pr PullRequest) error {
				owner, repo := prOwnerRepo(pr)
				return a.updateBranch(owner, repo, pr.Number)
			},
		)
		return m, nil

	case tuiKeybindUnassign:
		targets := m.targetOtherActionablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		rest := m.rest
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionUnassign
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			m.confirmPrompt = "Unassign & unsubscribe from " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				login, err := getCurrentLogin(rest)
				if err != nil {
					return actionMsg{
						index:  t.index,
						key:    makePRKey(t.pr),
						action: tuiActionUnsubscribed,
						err:    err,
					}
				}
				owner, repo := prOwnerRepo(t.pr)
				err = actions.removeReviewRequest(owner, repo, t.pr.Number, login, t.pr.NodeID)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionUnsubscribed,
					err:    err,
				}
			}
		} else {
			m.confirmSubject = fmt.Sprintf("%d PRs", len(targets))
			m.confirmPrompt = fmt.Sprintf("Unassign & unsubscribe from %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				login, err := getCurrentLogin(rest)
				if err != nil {
					return batchActionMsg{
						action:   tuiActionUnsubscribed,
						count:    len(batch),
						failed:   len(batch),
						failures: batchResultsForTargets(batch, err),
					}
				}
				return runBatchAction(actions, batch, tuiActionUnsubscribed,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
						return a.removeReviewRequest(owner, repo, pr.Number, login, pr.NodeID)
					})
			}
		}
		return m, nil

	case tuiKeybindUnassignNoConfirm:
		targets := m.targetOtherActionablePRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		rest := m.rest
		if len(targets) == 1 {
			t := targets[0]
			return m, func() tea.Msg {
				login, err := getCurrentLogin(rest)
				if err != nil {
					return actionMsg{
						index:  t.index,
						key:    makePRKey(t.pr),
						action: tuiActionUnsubscribed,
						err:    err,
					}
				}
				owner, repo := prOwnerRepo(t.pr)
				err = actions.removeReviewRequest(owner, repo, t.pr.Number, login, t.pr.NodeID)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionUnsubscribed,
					err:    err,
				}
			}
		}
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		return m, func() tea.Msg {
			login, err := getCurrentLogin(rest)
			if err != nil {
				return batchActionMsg{
					action:   tuiActionUnsubscribed,
					count:    len(batch),
					failed:   len(batch),
					failures: batchResultsForTargets(batch, err),
				}
			}
			return runBatchAction(actions, batch, tuiActionUnsubscribed,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.removeReviewRequest(owner, repo, pr.Number, login, pr.NodeID)
				})
		}

	case tuiKeybindCopilotReview:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		if len(targets) == 1 {
			t := targets[0]
			flashPending(&m, statusCopilotReview, &t.pr)
			return m, func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				err := actions.requestReview(
					owner,
					repo,
					t.pr.Number,
					"copilot-pull-request-reviewer[bot]",
				)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionReviewRequested,
					err:    err,
				}
			}
		}
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		return m, func() tea.Msg {
			return runBatchAction(actions, batch, tuiActionReviewRequested,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.requestReview(
						owner,
						repo,
						pr.Number,
						"copilot-pull-request-reviewer[bot]",
					)
				})
		}

	case tuiKeybindOptions:
		m.showOptions = true
		m.optionsCursor = 0
		m.optionsValues = m.currentFilterValues()
		m.optionsReset = [6]bool{}
		return m, nil

	case tuiKeybindHelp:
		m.showHelp = true
		return m, nil

	case tuiKeybindToggleRefresh:
		m.autoRefresh = !m.autoRefresh
		// Persist to config file in the background.
		enabled := m.autoRefresh
		if err := saveConfigKey(keyTUIAutoRefresh, enabled); err != nil {
			clog.Warn().Err(err).Msg("Failed to persist auto-refresh setting")
		}
		if m.autoRefresh {
			return m, m.rescheduleRefresh()
		}
		m.refreshID++
		return m, nil

	case tuiKeyTab:
		if m.sortColumn != "" {
			return m.toggleSort(m.sortColumn)
		}
		return m, nil

	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		digit := int(msg.String()[0] - '0')
		if m.jumpDigit > 0 {
			// Second digit: combine with first (e.g. 1,0 → row 10).
			target := m.jumpDigit*10 + digit - 1
			m.jumpDigit = 0
			visible := m.visibleIndices()
			if target >= 0 && target < len(visible) {
				m.cursor = visible[target]
				m.offset = m.scrolledOffset()
			}
			return m, nil
		}
		visible := m.visibleIndices()
		if digit*10 > len(visible) {
			target := digit - 1
			if target >= 0 && target < len(visible) {
				m.cursor = visible[target]
				m.offset = m.scrolledOffset()
			}
			return m, nil
		}
		// First digit: wait for possible second digit.
		m.jumpDigit = digit
		m.jumpID++
		id := m.jumpID
		return m, tea.Tick(tuiJumpTimeout, func(time.Time) tea.Msg {
			return jumpTimeoutMsg{id: id}
		})
	}

	return m, nil
}

func (m tuiModel) updateDiffView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmInputPending() {
		return m.updateConfirmOverlay(msg)
	}
	maxScroll := m.diffMaxScroll()
	switch msg.String() {
	case tuiKeybindQuit, tuiKeyEsc, tuiKeybindDiff:
		m.diffQueue = nil
		m.diffHistory = nil
		m.diffQueueTotal = 0
		m.diffAdvanced = false
		m.diffLoading = false
		m.diffKey = ""
		m.view = tuiViewList
		return m, m.rescheduleRefresh()
	case tuiKeybindNext:
		// Skip to next in queue without approving.
		if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
			m.diffHistory = append(m.diffHistory, m.diffKey)
			return m, nextCmd
		}
		return m, nil
	case tuiKeybindPrev:
		// Go back to previous diff in history.
		if len(m.diffHistory) == 0 {
			return m, nil
		}
		prev := m.diffHistory[len(m.diffHistory)-1]
		m.diffHistory = m.diffHistory[:len(m.diffHistory)-1]
		// Push current back onto front of queue.
		m.diffQueue = append([]prKey{m.diffKey}, m.diffQueue...)
		m.diffLoading = true
		idx := m.resolveIndex(prev, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			diff, headSHA, err := actions.fetchDiff(owner, repo, pr.Number)
			return diffFetchedMsg{
				index:   idx,
				key:     makePRKey(pr),
				diff:    diff,
				headSHA: headSHA,
				err:     err,
			}
		}
	case tuiKeybindVimDown, tuiKeyDown:
		if m.diffScroll < maxScroll {
			m.diffScroll++
		}
		return m, nil
	case tuiKeybindVimUp, tuiKeyUp:
		if m.diffScroll > 0 {
			m.diffScroll--
		}
		return m, nil
	case tuiKeyCtrlF, tuiKeySpace:
		m.diffScroll = min(m.diffScroll+m.diffContentViewport(), maxScroll)
		return m, nil
	case tuiKeyCtrlB:
		m.diffScroll = max(m.diffScroll-m.diffContentViewport(), 0)
		return m, nil
	case tuiKeybindTop:
		m.diffScroll = 0
		return m, nil
	case tuiKeybindBottom:
		m.diffScroll = maxScroll
		return m, nil
	case tuiKeybindApprove, tuiKeybindApproveAlias, tuiKeybindApproveNoConfirm:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if m.isCurrentUserPR(pr) || pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		flashPending(&m, statusApproving, &pr)
		actions := m.actions
		approveCmd := func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionApproved, err: err}
		}
		// If there's a next item in queue, prefetch it in parallel with the approve.
		if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
			m.diffHistory = append(m.diffHistory, m.diffKey)
			m.diffAdvanced = true
			return m, tea.Batch(approveCmd, nextCmd)
		}
		// Last item - approve and let actionMsg handler return to list.
		return m, approveCmd
	case tuiKeybindClose:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged {
			return m, nil
		}
		if state == valueClosed {
			actions := m.actions
			flashPending(&m, statusReopening, &pr)
			return m, func() tea.Msg {
				owner, repo := prOwnerRepo(pr)
				err := actions.reopenPR(owner, repo, pr.Number)
				return actionMsg{
					index:  idx,
					key:    makePRKey(pr),
					action: tuiActionReopened,
					err:    err,
				}
			}
		}
		actions := m.actions
		m.confirmAction = tuiActionClose
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmHasInput = true
		m.confirmInput.SetValue("")
		m.confirmPrompt = "Close " + styledRef(&pr) + "?"
		m.confirmCmdFn = func(comment string) tea.Cmd {
			return func() tea.Msg {
				owner, repo := prOwnerRepo(pr)
				err := actions.closePR(owner, repo, pr.Number, comment, false)
				return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionClosed, err: err}
			}
		}
		return m, m.confirmInput.Focus()
	case tuiKeybindDraftToggle:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		actions := m.actions
		if pr.IsDraft {
			flashPending(&m, statusMarkingReady, &pr)
			return m, func() tea.Msg {
				err := actions.markReady(pr.NodeID)
				return actionMsg{
					index:  idx,
					key:    makePRKey(pr),
					action: tuiActionMarkedReady,
					err:    err,
				}
			}
		}
		flashPending(&m, statusMarkingDraft, &pr)
		return m, func() tea.Msg {
			err := actions.markDraft(pr.NodeID)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionMarkedDraft, err: err}
		}
	case tuiKeybindComment:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		m.confirmAction = tuiActionComment
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmHasInput = true
		m.confirmInput.SetValue("")
		m.confirmPrompt = "Comment on " + styledRef(&pr) + "?"
		m.confirmCmdFn = func(comment string) tea.Cmd {
			return func() tea.Msg {
				owner, repo := prOwnerRepo(pr)
				err := actions.comment(owner, repo, pr.Number, comment)
				return actionMsg{
					index:  idx,
					key:    makePRKey(pr),
					action: tuiActionCommented,
					err:    err,
				}
			}
		}
		return m, m.confirmInput.Focus()
	case tuiKeybindUpdateBranch:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		actions := m.actions
		m.confirmAction = tuiActionUpdateBranch
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmPrompt = "Update branch for " + styledRef(&pr) + "?"
		m.confirmCmd = func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.updateBranch(owner, repo, pr.Number)
			return actionMsg{
				index:  idx,
				key:    makePRKey(pr),
				action: tuiActionBranchUpdated,
				err:    err,
			}
		}
		return m, nil
	case tuiKeybindUnassign:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if m.isCurrentUserPR(pr) || state == valueMerged || state == valueClosed {
			return m, nil
		}
		flashPending(&m, statusUnsubscribing, &pr)
		actions := m.actions
		login := m.login
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.removeReviewRequest(owner, repo, pr.Number, login, pr.NodeID)
			return actionMsg{
				index:  idx,
				key:    makePRKey(pr),
				action: tuiActionUnsubscribed,
				err:    err,
			}
		}
	case tuiKeybindMerge:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		flash := statusAutomerging
		if pr.MergeStatus == MergeStatusReady {
			flash = statusMerging
		}
		flashPending(&m, flash, &pr)
		actions := m.actions
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			result, err := actions.mergeOrAutomerge(owner, repo, pr)
			return actionMsg{
				index:  idx,
				key:    makePRKey(pr),
				action: parseMergeResult(result),
				err:    err,
			}
		}
	case tuiKeybindApproveMerge:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if m.isCurrentUserPR(pr) || pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		flashPending(&m, statusApproveMerging, &pr)
		actions := m.actions
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			if err := actions.approve(owner, repo, pr.Number); err != nil {
				return actionMsg{
					index:  idx,
					key:    makePRKey(pr),
					action: tuiActionApproved,
					err:    err,
				}
			}
			result, err := actions.mergeOrAutomerge(owner, repo, pr)
			return actionMsg{
				index:  idx,
				key:    makePRKey(pr),
				action: parseMergeResult(result),
				err:    err,
			}
		}
	case tuiKeybindSlack:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		cfg := m.cfg
		cli := m.cli
		m.confirmAction = tuiActionSendSlack
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmPrompt = "Send " + styledRef(&pr) + " to Slack?"
		m.confirmCmd = func() tea.Msg {
			out, err := sendSlack([]PullRequest{pr}, cli, cfg)
			return slackSentMsg{count: 1, output: out, err: err}
		}
		return m, nil
	case tuiKeybindSlackNoConfirm:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		flashPending(&m, statusSending, &pr)
		cfg := m.cfg
		cli := m.cli
		return m, func() tea.Msg {
			out, err := sendSlack([]PullRequest{pr}, cli, cfg)
			return slackSentMsg{count: 1, output: out, err: err}
		}
	case tuiKeybindOpen:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		_ = openBrowser(pr.URL)
		return m, nil
	case tuiKeybindCopyURL:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		_ = copyToClipboard(pr.URL)
		return m, flashResult(&m, resultCopied, pr.Ref(), "", false)
	case tuiKeybindCopilotReview:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		flashPending(&m, statusCopilotReview, &pr)
		actions := m.actions
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.requestReview(
				owner,
				repo,
				pr.Number,
				"copilot-pull-request-reviewer[bot]",
			)
			return actionMsg{
				index:  idx,
				key:    makePRKey(pr),
				action: tuiActionReviewRequested,
				err:    err,
			}
		}
	}
	return m, nil
}

func (m tuiModel) updateDetailView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmInputPending() {
		return m.updateConfirmOverlay(msg)
	}
	viewport := m.detailViewport()
	switch msg.String() {
	case tuiKeybindQuit, tuiKeyEsc, tuiKeyEnter:
		m.detailKey = ""
		m.view = tuiViewList
		return m, m.rescheduleRefresh()
	case tuiKeybindVimDown, tuiKeyDown:
		if m.detailScroll < len(m.detailLines)-viewport {
			m.detailScroll++
		}
		return m, nil
	case tuiKeybindVimUp, tuiKeyUp:
		if m.detailScroll > 0 {
			m.detailScroll--
		}
		return m, nil
	case tuiKeyCtrlF, tuiKeySpace:
		maxScroll := max(0, len(m.detailLines)-viewport)
		m.detailScroll = min(m.detailScroll+viewport, maxScroll)
		return m, nil
	case tuiKeyCtrlB:
		m.detailScroll = max(m.detailScroll-viewport, 0)
		return m, nil
	case tuiKeybindTop:
		m.detailScroll = 0
		return m, nil
	case tuiKeybindBottom:
		if end := len(m.detailLines) - viewport; end > 0 {
			m.detailScroll = end
		}
		return m, nil
	case tuiKeybindDiff:
		// Jump to diff from detail view.
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		prCopy := pr
		m.diffLoading = true
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			diff, headSHA, err := actions.fetchDiff(owner, repo, prCopy.Number)
			return diffFetchedMsg{
				index:   idx,
				key:     makePRKey(prCopy),
				diff:    diff,
				headSHA: headSHA,
				err:     err,
			}
		}
	case tuiKeybindApprove, tuiKeybindApproveAlias:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if m.isCurrentUserPR(pr) || pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		actions := m.actions
		m.view = tuiViewList
		m.rescheduleRefresh()
		m.confirmAction = tuiActionApprove
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmPrompt = "Approve " + styledRef(&pr) + "?"
		m.confirmCmd = func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionApproved, err: err}
		}
		return m, nil
	case tuiKeybindApproveNoConfirm:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if m.isCurrentUserPR(pr) || pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		flashPending(&m, statusApproving, &pr)
		actions := m.actions
		m.view = tuiViewList
		m.rescheduleRefresh()
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionApproved, err: err}
		}
	case tuiKeybindMerge:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		actions := m.actions
		m.view = tuiViewList
		m.rescheduleRefresh()
		verb := "Automerge "
		if pr.MergeStatus == MergeStatusReady {
			verb = "Merge "
		}
		m.confirmAction = tuiActionMerge
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmPrompt = verb + styledRef(&pr) + "?"
		m.confirmCmd = func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			result, err := actions.mergeOrAutomerge(owner, repo, pr)
			return actionMsg{
				index:  idx,
				key:    makePRKey(pr),
				action: parseMergeResult(result),
				err:    err,
			}
		}
		return m, nil
	case tuiKeybindDraftToggle:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		actions := m.actions
		m.view = tuiViewList
		m.rescheduleRefresh()
		if pr.IsDraft {
			flashPending(&m, statusMarkingReady, &pr)
			return m, func() tea.Msg {
				err := actions.markReady(pr.NodeID)
				return actionMsg{
					index:  idx,
					key:    makePRKey(pr),
					action: tuiActionMarkedReady,
					err:    err,
				}
			}
		}
		flashPending(&m, statusMarkingDraft, &pr)
		return m, func() tea.Msg {
			err := actions.markDraft(pr.NodeID)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionMarkedDraft, err: err}
		}
	case tuiKeybindComment:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		m.view = tuiViewList
		m.rescheduleRefresh()
		m.confirmAction = tuiActionComment
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmHasInput = true
		m.confirmInput.SetValue("")
		m.confirmPrompt = "Comment on " + styledRef(&pr) + "?"
		m.confirmCmdFn = func(comment string) tea.Cmd {
			return func() tea.Msg {
				owner, repo := prOwnerRepo(pr)
				err := actions.comment(owner, repo, pr.Number, comment)
				return actionMsg{
					index:  idx,
					key:    makePRKey(pr),
					action: tuiActionCommented,
					err:    err,
				}
			}
		}
		return m, m.confirmInput.Focus()
	case tuiKeybindSlack:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		cfg := m.cfg
		cli := m.cli
		m.view = tuiViewList
		m.rescheduleRefresh()
		m.confirmAction = tuiActionSendSlack
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmYes = true
		m.confirmPrompt = "Send " + styledRef(&pr) + " to Slack?"
		m.confirmCmd = func() tea.Msg {
			out, err := sendSlack([]PullRequest{pr}, cli, cfg)
			return slackSentMsg{count: 1, output: out, err: err}
		}
		return m, nil
	case tuiKeybindSlackNoConfirm:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		flashPending(&m, statusSending, &pr)
		cfg := m.cfg
		cli := m.cli
		return m, func() tea.Msg {
			out, err := sendSlack([]PullRequest{pr}, cli, cfg)
			return slackSentMsg{count: 1, output: out, err: err}
		}
	case tuiKeybindOpen:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		_ = openBrowser(pr.URL)
		return m, nil
	case tuiKeybindCopyURL:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		_ = copyToClipboard(pr.URL)
		return m, flashResult(&m, resultCopied, pr.Ref(), "", false)
	case tuiKeybindReview:
		if !hasClaudeReviewLauncher() {
			m.view = tuiViewList
			m.confirmAction = tuiActionInfo
			m.confirmYes = true
			m.confirmPrompt = tuiClaudeReviewUnsupported
			m.confirmCmd = nil
			return m, m.rescheduleRefresh()
		}
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if pr.IsDraft || state == valueMerged || state == valueClosed {
			return m, nil
		}
		m.view = tuiViewList
		m = m.prepareClaudeReviewConfirm(pr, idx)
		return m, tea.Batch(m.confirmInput.Focus(), m.rescheduleRefresh())
	}
	return m, nil
}

func (m tuiModel) View() tea.View {
	switch m.view {
	case tuiViewDiff:
		return m.viewDiff()
	case tuiViewDetail:
		return m.viewDetail()
	case tuiViewList:
	}
	v := m.viewList()
	switch {
	case m.showHelp:
		v.Content = overlayCenter(v.Content, m.renderHelpOverlay(), m.width, m.height)
	case m.showOptions:
		v.Content = overlayCenter(v.Content, m.renderOptionsOverlay(), m.width, m.height)
	case m.confirmAction != "":
		v.Content = overlayCenter(v.Content, m.renderConfirmModal(), m.width, m.height)
	case len(m.visibleIndices()) == 0 && !m.dismissedEmpty && m.statusMsg == "":
		v.Content = overlayCenter(v.Content, m.renderEmptyOverlay(), m.width, m.height)
	}
	return v
}

func (m tuiModel) viewList() tea.View {
	var b strings.Builder
	visible := m.visibleIndices()
	viewport := m.listViewport()
	indexPad := strings.Repeat(" ", tuiIndexWidth(len(m.rows)))

	// Column header line.
	if m.header != "" {
		prefix := tuiNonCursorPrefix + indexPad + tuiNonCursorPrefix
		if m.refreshing && len(m.spinner.frames) > 0 {
			frame := m.spinner.frames[m.spinnerTick%len(m.spinner.frames)]
			prefix = frame + " " + indexPad + tuiNonCursorPrefix
		}
		b.WriteString(prefix + m.header + "\n")
	}

	// Determine visible slice based on offset.
	end := min(m.offset+viewport, len(visible))
	start := min(m.offset, len(visible))

	filterVal := m.filterInput.Value()

	for pos, idx := range visible[start:end] {
		index := m.renderTuiIndex(start+pos+1, m.selected[m.rowKeyAt(idx)])
		display := index + tuiNonCursorPrefix + m.rows[idx].Display
		if idx != m.cursor {
			b.WriteString(tuiNonCursorPrefix + display + "\n")
			continue
		}
		line := m.styles.cursor.Render(tuiCursorPrefix) + display
		// Inject background color throughout the line so it persists
		// through existing ANSI codes in the display string.
		// Skip highlight when there's only one visible result.
		if len(visible) > 1 {
			bg := cursorLineBG
			if m.selected[m.rowKeyAt(idx)] {
				bg = cursorLineSelectedBG
			}
			b.WriteString(injectLineBackground(line, m.width, bg))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	// Pad to fill viewport.
	rendered := end - start
	for range viewport - rendered {
		b.WriteString("\n")
	}

	// Filter bar.
	if m.filterInput.Focused() || filterVal != "" {
		b.WriteString(
			lg.NewStyle().
				Foreground(lg.Color("208")).
				Bold(true).
				Render("/") +
				m.filterInput.View() + "\n",
		)
	}

	// Separator line, with active tags embedded inline when present.
	// When filter is focused, place a ┬ junction to connect with the │ in the help line.
	var help string
	if m.filterInput.Focused() {
		b.WriteString(m.renderListSeparator(m.filterHelpPipeCol()))
		b.WriteString("\n")
		help = m.renderFilterHelp()
	} else {
		b.WriteString(m.renderListSeparator())
		b.WriteString("\n")
		help = m.renderListHelp()
	}
	if m.statusMsg != "" {
		b.WriteString(m.appendStatus(help))
	} else {
		status := ""
		total := len(visible)
		if total > viewport {
			pct := scrollPercent(start, total, end-start)
			status = m.styles.statusOK.Render(
				fmt.Sprintf("%d-%d/%d (%d%%)", start+1, end, total, pct),
			)
		}
		b.WriteString(m.appendRightStatus(help, status))
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	m.applyRepaintMarker(&v)
	return v
}

func (m tuiModel) viewDiff() tea.View {
	var b strings.Builder
	viewport := m.diffContentViewport()

	// PR title header.
	headerStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("212"))
	if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
		pr := m.rows[idx].Item.PR
		var headerLine string
		if m.diffQueueTotal > 0 {
			pos := m.diffQueueTotal - len(m.diffQueue)
			headerLine = headerStyle.Render(fmt.Sprintf("[%d/%d]", pos, m.diffQueueTotal)) +
				" "
		}
		ref := fmt.Sprintf("%s#%d", pr.Repository.NameWithOwner, pr.Number)
		titleStyle := lg.NewStyle().Foreground(lg.Color("218"))
		headerLine += xansi.SetHyperlink(pr.URL) +
			headerStyle.Render(
				ref,
			) + lg.NewStyle().Foreground(lg.Color("255")).Render(" » ") +
			titleStyle.Render(normalizeTUIDisplayText(pr.Title)) +
			xansi.ResetHyperlink()
		if m.width > 0 && lg.Width(headerLine) > m.width {
			headerLine = xansi.Truncate(headerLine, m.width-1, valueEllipsis)
		}
		b.WriteString(headerLine)
		b.WriteString("\n")
		if m.width > 0 {
			b.WriteString(m.styles.separator.Render(strings.Repeat(sepHorizontal, m.width)))
		}
		b.WriteString("\n")
	}

	// Diff content.
	end := min(m.diffScroll+viewport, len(m.diffLines))
	for _, line := range m.diffLines[m.diffScroll:end] {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Pad remaining.
	rendered := end - m.diffScroll
	for range viewport - rendered {
		b.WriteString("\n")
	}

	// Separator.
	if m.width > 0 {
		b.WriteString(m.styles.separator.Render(strings.Repeat(sepHorizontal, m.width)))
	}
	b.WriteString("\n")

	// Help line with status on RHS: in-flight action status takes priority over scroll %.
	help := m.renderDiffHelp()
	if m.statusMsg != "" {
		b.WriteString(m.appendStatus(help))
	} else {
		status := ""
		if total := len(m.diffLines); m.diffMaxScroll() > 0 {
			vp := m.diffContentViewport()
			end := min(m.diffScroll+vp, total)
			pct := scrollPercent(m.diffScroll, total, vp)
			status = m.styles.statusOK.Render(
				fmt.Sprintf("%d-%d/%d (%d%%)", m.diffScroll+1, end, total, pct),
			)
		}
		b.WriteString(m.appendRightStatus(help, status))
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	if m.confirmInputPending() {
		v.Content = overlayCenter(v.Content, m.renderConfirmModal(), m.width, m.height)
	}
	m.applyRepaintMarker(&v)
	return v
}

func (m tuiModel) listViewport() int {
	//nolint:mnd // 1 for header + 1 for separator + help lines (variable).
	h := 2 + m.helpLines(m.listHelpPairs())
	if m.filterInput.Value() != "" || m.filterInput.Focused() {
		h++
	}
	if m.height <= h {
		return 1
	}
	return m.height - h
}

const (
	sepHorizontal = "─"
	sepJunction   = "┬"
)

// separatorPad builds a run of ─ characters with an optional ┬ junction at col.
func separatorPad(width, col int) string {
	if col >= 0 && col < width {
		return strings.Repeat(
			sepHorizontal,
			col,
		) + sepJunction + strings.Repeat(
			sepHorizontal,
			width-col-1,
		)
	}
	return strings.Repeat(sepHorizontal, width)
}

// junctionFrom extracts the junction column from a variadic arg, defaulting to -1.
func junctionFrom(junctionCol []int) int {
	if len(junctionCol) > 0 {
		return junctionCol[0]
	}
	return -1
}

// renderListSeparator renders the separator line with an optional scroll status
// centered in the bar. If junctionCol >= 0, a ┬ is placed at that visual column.
// renderListSeparator renders the separator line. If junctionCol >= 0, a ┬ is
// placed at that visual column to connect with a │ in the line below.
func (m tuiModel) renderListSeparator(junctionCol ...int) string {
	if m.width <= 0 {
		return ""
	}
	col := junctionFrom(junctionCol)
	tags := m.activeFilterTags()
	if len(tags) > 0 {
		return m.renderTagSeparator(tags, col)
	}
	return m.styles.separator.Render(separatorPad(m.width, col))
}

func (m tuiModel) renderTagSeparator(tags []string, col int) string {
	filterTagStyle := lg.NewStyle().Foreground(lg.Color("116")).Faint(true)
	filterTagKeyStyle := filterTagStyle.Bold(true)
	renderedTags := make([]string, 0, len(tags))
	for _, tag := range tags {
		if key, value, ok := strings.Cut(tag, ":"); ok {
			renderedTags = append(
				renderedTags,
				filterTagKeyStyle.Render(key)+filterTagStyle.Render(":"+value),
			)
		} else {
			renderedTags = append(renderedTags, filterTagKeyStyle.Render(tag))
		}
	}
	indicator := strings.Join(renderedTags, " ")
	suffixText := strings.Repeat(sepHorizontal, 2) //nolint:mnd // fixed suffix width
	suffix := m.styles.separator.Render(" " + suffixText)
	const indicatorPrefix = " "
	available := m.width - lg.Width(indicatorPrefix) - lg.Width(suffix)
	if available <= 0 {
		return xansi.Truncate(suffix, m.width, "")
	}
	if lg.Width(indicator) > available {
		indicator = xansi.Truncate(indicator, available, valueEllipsis)
	}
	pad := max(m.width-lg.Width(indicatorPrefix)-lg.Width(indicator)-lg.Width(suffix), 0)
	return m.styles.separator.Render(separatorPad(pad, col)) +
		indicatorPrefix +
		indicator +
		suffix
}

func (m tuiModel) viewDetail() tea.View {
	var b strings.Builder
	viewport := m.detailViewport()

	// Content.
	end := min(m.detailScroll+viewport, len(m.detailLines))
	for _, line := range m.detailLines[m.detailScroll:end] {
		b.WriteString(truncateDisplayLine(line, m.width))
		b.WriteString("\n")
	}

	// Pad remaining.
	rendered := end - m.detailScroll
	for range viewport - rendered {
		b.WriteString("\n")
	}

	// Separator.
	if m.width > 0 {
		b.WriteString(m.styles.separator.Render(strings.Repeat(sepHorizontal, m.width)))
	}
	b.WriteString("\n")

	// Help line with status on RHS.
	help := m.renderDetailHelp()
	if m.statusMsg != "" {
		b.WriteString(m.appendStatus(help))
	} else {
		status := ""
		if total := len(m.detailLines); total > viewport {
			end := min(m.detailScroll+viewport, total)
			pct := scrollPercent(m.detailScroll, total, viewport)
			status = m.styles.statusOK.Render(
				fmt.Sprintf("%d-%d/%d (%d%%)", m.detailScroll+1, end, total, pct),
			)
		}
		b.WriteString(m.appendRightStatus(help, status))
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	if m.confirmInputPending() {
		v.Content = overlayCenter(v.Content, m.renderConfirmModal(), m.width, m.height)
	}
	m.applyRepaintMarker(&v)
	return v
}

func (m tuiModel) detailViewport() int {
	h := 1 + m.helpLines(m.detailHelpPairs())
	if m.height <= h {
		return 1
	}
	return m.height - h
}

// renderDetailContent builds the detail view lines from the PR and its detail data.
func (m tuiModel) renderDetailContent() []string {
	idx := m.resolveIndex(m.detailKey, -1)
	if idx < 0 {
		return []string{
			lg.NewStyle().Foreground(lg.Color("240")).Render("Pull request no longer available."),
		}
	}
	pr := m.rows[idx].Item.PR

	headerStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("175"))
	dimStyle := lg.NewStyle().Foreground(lg.Color("240"))

	labelStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("48"))
	valueStyle := lg.NewStyle().Foreground(lg.Color("255"))

	author := m.resolver.Resolve(pr.Author.Login)
	var lines []string
	lines = append(lines, headerStyle.Render("Overview"))
	lines = append(lines, "")
	authorLink := "https://github.com/" + pr.Author.Login
	authorDisplay := "@" + pr.Author.Login
	if author != pr.Author.Login {
		authorDisplay += " (" + author + ")"
	}
	styledAuthor := xansi.SetHyperlink(
		authorLink,
	) + valueStyle.Render(
		authorDisplay,
	) + xansi.ResetHyperlink()
	lines = append(
		lines,
		detailIndent+labelStyle.Render(
			" Title: ",
		)+valueStyle.Render(
			normalizeTUIDisplayText(pr.Title),
		),
	)
	lines = append(lines, detailIndent+labelStyle.Render("Author: ")+styledAuthor)
	styledURL := xansi.SetHyperlink(pr.URL) + valueStyle.Render(pr.URL) + xansi.ResetHyperlink()
	lines = append(lines, detailIndent+labelStyle.Render("   URL: ")+styledURL)
	lines = append(lines, detailIndent+labelStyle.Render("Status: ")+m.renderDetailStatus(pr))
	if m.detail.MergeableState == valueBehind {
		lines = append(lines, detailIndent+labelStyle.Render(" State: ")+
			lg.NewStyle().Foreground(lg.Color("214")).Render("Branch out-of-date"))
	}
	lines = append(lines, "")

	// Reviews.
	if len(m.detail.Reviews) > 0 {
		lines = append(lines, headerStyle.Render("Reviews"))
		lines = append(lines, "")
		for _, r := range m.detail.Reviews {
			icon := iconCommented
			switch r.State {
			case valueReviewApproved:
				icon = iconApproved
			case valueReviewChanges:
				icon = iconRejected
			case valueReviewDismissed:
				icon = iconDismissed
			}
			name := m.resolver.Resolve(r.User)
			lines = append(lines, fmt.Sprintf("%s%s %s", detailIndent, icon, name))
		}
		lines = append(lines, "")
	}

	// Checks.
	if len(m.detail.Checks) > 0 {
		lines = append(lines, headerStyle.Render("Checks"))
		lines = append(lines, "")
		for _, c := range m.detail.Checks {
			var icon string
			switch {
			case c.Status != "completed":
				icon = "🔄"
			case c.Conclusion == ciStatusSuccess:
				icon = iconApproved
			case c.Conclusion == ciStatusFailure:
				icon = iconRejected
			case c.Conclusion == "cancelled":
				icon = iconDismissed
			case c.Conclusion == "skipped":
				icon = "⏭️"
			case c.Conclusion == "timed_out":
				icon = "⏱️"
			case c.Conclusion == "action_required":
				icon = "⚠️"
			case c.Conclusion == "neutral":
				icon = "➖"
			case c.Conclusion == "stale":
				icon = "💤"
			default:
				icon = "❓"
			}
			line := fmt.Sprintf("%s%s %s", detailIndent, icon, c.Name)
			if c.Duration > 0 {
				line += " " + lg.NewStyle().
					Foreground(lg.Color("245")).
					Render(fmt.Sprintf("[%s]", c.Duration.Round(time.Second)))
			}
			lines = append(lines, normalizeTUIDisplayText(line))
		}
		lines = append(lines, "")
	}

	// Body (rendered as markdown via glamour).
	if m.detail.Body != "" {
		lines = append(lines, headerStyle.Render("Description"))
		lines = append(lines, m.renderMarkdown(m.detail.Body)...)
	} else {
		lines = append(lines, dimStyle.Render("No description provided."))
	}

	// Changed files.
	if len(m.detail.Files) > 0 {
		lines = append(lines, "")
		lines = append(lines, headerStyle.Render("Files Changed"))
		lines = append(lines, "")
		addStyle := lg.NewStyle().Foreground(lg.Color("118"))
		delStyle := lg.NewStyle().Foreground(lg.Color("197"))
		modPrefixStyle := lg.NewStyle().Foreground(lg.Color("3")).Bold(true)
		addPrefixStyle := lg.NewStyle().Foreground(lg.Color("2")).Bold(true)
		delPrefixStyle := lg.NewStyle().Foreground(lg.Color("1")).Bold(true)
		renPrefixStyle := lg.NewStyle().Foreground(lg.Color("5")).Bold(true)
		for _, f := range m.detail.Files {
			var prefix string
			switch f.Status {
			case "added":
				prefix = addPrefixStyle.Render("A")
			case "removed":
				prefix = delPrefixStyle.Render("D")
			case "renamed":
				prefix = renPrefixStyle.Render("R")
			default:
				prefix = modPrefixStyle.Render("M")
			}
			stat := addStyle.Render(fmt.Sprintf("+%d", f.Additions)) +
				" " + delStyle.Render(fmt.Sprintf("-%d", f.Deletions))
			lines = append(
				lines,
				fmt.Sprintf("%s%s %s  %s", detailIndent, prefix, f.Filename, stat),
			)
		}
		lines = append(lines, "")
	}

	return lines
}

func (m tuiModel) renderDetailStatus(pr PullRequest) string {
	if pr.IsDraft {
		return lg.NewStyle().Foreground(lg.Color("250")).Render("Draft")
	}
	state := strings.ToLower(pr.State)
	if state == valueMerged {
		return lg.NewStyle().Foreground(lg.Color("141")).Render("Merged")
	}
	if state == "closed" {
		return lg.NewStyle().Foreground(lg.Color("197")).Render("Closed")
	}
	switch pr.MergeStatus {
	case MergeStatusReady:
		return lg.NewStyle().Foreground(lg.Color("2")).Render("Ready to merge")
	case MergeStatusCIPending:
		return lg.NewStyle().Foreground(lg.Color("214")).Render("CI pending")
	case MergeStatusCIFailed:
		return lg.NewStyle().Foreground(lg.Color("197")).Render("CI failed")
	case MergeStatusBlocked:
		return lg.NewStyle().Foreground(lg.Color("214")).Render("Needs review")
	case MergeStatusUnknown:
		return lg.NewStyle().Foreground(lg.Color("240")).Render("Unknown")
	}
	return ""
}

const (
	detailIndent     = "  "
	defaultTermWidth = 80

	iconApproved  = "✅"
	iconRejected  = "❌"
	iconDismissed = "🥀"
	iconCommented = "💬"
)

func (m tuiModel) renderMarkdown(body string) []string {
	width := m.width
	if width <= 0 {
		width = defaultTermWidth
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dracula"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return m.plainBodyLines(body)
	}
	rendered, err := r.Render(body)
	if err != nil {
		return m.plainBodyLines(body)
	}
	var lines []string
	for line := range strings.SplitSeq(strings.TrimRight(rendered, "\n"), "\n") {
		lines = append(lines, line)
	}
	return lines
}

func (m tuiModel) plainBodyLines(body string) []string {
	var lines []string
	for line := range strings.SplitSeq(body, "\n") {
		lines = append(lines, detailIndent+line)
	}
	return lines
}

func (m tuiModel) diffViewport() int {
	// 1 for separator + help lines (variable).
	h := 1 + m.helpLines(m.diffHelpPairs())
	if m.height <= h {
		return 1
	}
	return m.height - h
}

func (m tuiModel) diffContentViewport() int {
	viewport := m.diffViewport()
	if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
		viewport -= 2 // title + separator above the diff body
	}
	return max(0, viewport)
}

func (m tuiModel) diffMaxScroll() int {
	return max(0, len(m.diffLines)-m.diffContentViewport())
}

// scrollPercent returns the scroll position as a percentage in the style of
// less(1): the percentage of the file above and including the bottom of the
// viewport. This means it never shows 0% and reaches 100% at the end.
func scrollPercent(offset, total, viewport int) int {
	const percentMax = 100
	if total <= 0 {
		return percentMax
	}
	return min(percentMax*(offset+viewport)/total, percentMax)
}

// advanceDiffQueue pops the next PR from the diff queue and returns a command
// to fetch its diff. Returns nil if the queue is empty.
func advanceDiffQueue(m *tuiModel) tea.Cmd {
	for len(m.diffQueue) > 0 {
		key := m.diffQueue[0]
		m.diffQueue = m.diffQueue[1:]
		idx := m.resolveIndex(key, -1)
		if idx < 0 {
			continue
		}
		m.diffLoading = true
		pr := m.rows[idx].Item.PR
		actions := m.actions
		return func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			diff, headSHA, err := actions.fetchDiff(owner, repo, pr.Number)
			return diffFetchedMsg{
				index:   idx,
				key:     makePRKey(pr),
				diff:    diff,
				headSHA: headSHA,
				err:     err,
			}
		}
	}
	m.diffQueueTotal = 0
	m.diffKey = ""
	return nil
}

func (m tuiModel) visibleIndices() []int {
	indices := make([]int, 0, len(m.rows))
	f := strings.TrimSpace(m.filterInput.Value())
	if f == "" {
		for i := range m.rows {
			if !m.removed[m.rowKeyAt(i)] {
				indices = append(indices, i)
			}
		}
		return indices
	}

	term := parseFilterTerm(f)
	for i := range m.rows {
		if m.removed[m.rowKeyAt(i)] {
			continue
		}
		text := rowFilterText(m.rows[i])
		if term.prefix || term.suffix {
			text = m.rows[i].Item.PR.Title
		}
		if matchesTerm(text, term) {
			indices = append(indices, i)
		}
	}
	return indices
}

// filterTerm represents a parsed search term with optional modifiers.
type filterTerm struct {
	text          string
	prefix        bool // ^
	suffix        bool // $
	negate        bool // !
	caseSensitive bool
}

// parseFilterTerm parses a filter string into a term.
// Supports: ! (negate), ^ (prefix), $ (suffix).
// Smart case: case-insensitive unless the term contains uppercase.
func parseFilterTerm(f string) filterTerm {
	var t filterTerm

	if rest, ok := strings.CutPrefix(f, "!"); ok {
		t.negate = true
		f = rest
	}
	if rest, ok := strings.CutPrefix(f, "^"); ok {
		t.prefix = true
		f = rest
	}
	if rest, ok := strings.CutSuffix(f, "$"); ok {
		t.suffix = true
		f = rest
	}

	t.caseSensitive = f != strings.ToLower(f)
	t.text = f
	return t
}

func matchesTerm(text string, t filterTerm) bool {
	if t.text == "" {
		return true
	}

	needle := t.text
	if !t.caseSensitive {
		text = strings.ToLower(text)
		needle = strings.ToLower(needle)
	}

	var matched bool
	switch {
	case t.prefix && t.suffix:
		matched = text == needle
	case t.prefix:
		matched = strings.HasPrefix(text, needle)
	case t.suffix:
		matched = strings.HasSuffix(text, needle)
	default:
		matched = strings.Contains(text, needle)
	}

	if t.negate {
		return !matched
	}
	return matched
}

func (m tuiModel) nextVisible(dir int) (int, bool) {
	visible := m.visibleIndices()
	visibleSet := make(map[int]bool, len(visible))
	for _, idx := range visible {
		visibleSet[idx] = true
	}
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if visibleSet[i] {
			return i, true
		}
	}
	return m.cursor, false
}

func (m tuiModel) adjustedCursor() int {
	visible := m.visibleIndices()
	if slices.Contains(visible, m.cursor) {
		return m.cursor
	}
	if next, ok := m.nextVisible(1); ok {
		return next
	}
	if prev, ok := m.nextVisible(-1); ok {
		return prev
	}
	return m.cursor
}

func (m tuiModel) scrolledOffset() int {
	viewport := m.listViewport()
	visible := m.visibleIndices()
	pos := 0
	for i, idx := range visible {
		if idx == m.cursor {
			pos = i
			break
		}
	}
	offset := m.offset
	if pos < offset {
		offset = pos
	} else if pos >= offset+viewport {
		offset = pos - viewport + 1
	}
	// Clamp so we don't leave blank space at the bottom.
	if maxOffset := len(visible) - viewport; maxOffset > 0 && offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

// batchCmd returns a tea.Cmd that runs fn for a single target or as a batch.
func batchCmd(
	actions *ActionRunner,
	targets []targetPR,
	result tuiAction,
	fn func(*ActionRunner, PullRequest) error,
) tea.Cmd {
	if len(targets) == 1 {
		t := targets[0]
		return func() tea.Msg {
			err := fn(actions, t.pr)
			return actionMsg{index: t.index, key: makePRKey(t.pr), action: result, err: err}
		}
	}
	batch := make([]targetPR, len(targets))
	copy(batch, targets)
	return func() tea.Msg {
		return runBatchAction(actions, batch, result, fn)
	}
}

// setupConfirmBatch populates the confirm overlay for a single or batch action.
func setupConfirmBatch(
	m *tuiModel,
	targets []targetPR,
	action string,
	result tuiAction,
	verb string,
	fn func(*ActionRunner, PullRequest) error,
) {
	actions := m.actions
	m.confirmAction = action
	m.confirmYes = true
	if len(targets) == 1 {
		m.confirmSubject = targets[0].pr.Ref()
		m.confirmURL = targets[0].pr.URL
		m.confirmPrompt = verb + " " + styledRef(&targets[0].pr) + "?"
		t := targets[0]
		m.confirmCmd = func() tea.Msg {
			err := fn(actions, t.pr)
			return actionMsg{index: t.index, key: makePRKey(t.pr), action: result, err: err}
		}
	} else {
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmSubject = fmt.Sprintf("%d PRs", len(targets))
		m.confirmPrompt = fmt.Sprintf("%s %d PRs?", verb, len(targets))
		m.confirmCmd = func() tea.Msg {
			return runBatchAction(actions, batch, result, fn)
		}
	}
}

func runBatchAction(
	actions *ActionRunner,
	targets []targetPR,
	action tuiAction,
	fn func(*ActionRunner, PullRequest) error,
) batchActionMsg {
	results := make([]batchResult, len(targets))
	var wg sync.WaitGroup
	wg.Add(len(targets))
	for i, t := range targets {
		go func(i int, t targetPR) {
			defer wg.Done()
			results[i] = batchResult{
				key: makePRKey(t.pr),
				ref: t.pr.Ref(),
				url: t.pr.URL,
				err: fn(actions, t.pr),
			}
		}(i, t)
	}
	wg.Wait()
	var succeeded []prKey
	var failures []batchResult
	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
			failures = append(failures, r)
		} else {
			succeeded = append(succeeded, r.key)
		}
	}
	return batchActionMsg{
		action:   action,
		count:    len(targets),
		failed:   failed,
		keys:     succeeded,
		failures: failures,
	}
}

// flashPending sets a persistent in-progress status (e.g. "Merging foo/bar#421…")
// that remains visible until replaced by the action result.
func flashPending(m *tuiModel, verb string, pr *PullRequest) {
	m.statusMsg = m.styles.statusPending.Render(verb) + " " +
		lg.NewStyle().Foreground(lg.Color("117")).Render(pr.Ref()) + valueEllipsis
	m.statusErr = false
}

func flashResult(m *tuiModel, action, ref, url string, isErr bool) tea.Cmd {
	m.statusID++
	m.statusErr = isErr
	if isErr {
		m.statusMsg = fmt.Sprintf("%s %s", action, ref)
	} else {
		styledRef := lg.NewStyle().Foreground(lg.Color("117")).Render(ref)
		if url != "" {
			styledRef = xansi.SetHyperlink(url) + styledRef + xansi.ResetHyperlink()
		}
		m.statusMsg = m.styles.statusAction.Render(action) + " " + styledRef
	}
	id := m.statusID
	return tea.Tick(tuiStatusFlash, func(time.Time) tea.Msg {
		return clearStatusMsg{id: id}
	})
}

func tuiFlashMessage(m *tuiModel, text string, isErr bool) tea.Cmd {
	m.statusID++
	m.statusErr = isErr
	m.statusMsg = text
	id := m.statusID
	return tea.Tick(tuiStatusFlash, func(time.Time) tea.Msg {
		return clearStatusMsg{id: id}
	})
}

func renderBatchFailurePrompt(msg batchActionMsg) string {
	if len(msg.failures) == 0 {
		return "Some batch actions failed."
	}

	const maxFailures = 5
	var b strings.Builder
	fmt.Fprintf(&b, "%s had failures:\n\n", msg.action.String())
	limit := min(len(msg.failures), maxFailures)
	for i := range limit {
		failure := msg.failures[i]
		if failure.ref != "" {
			ref := lg.NewStyle().Bold(true).Foreground(lg.Color("117")).Render(failure.ref)
			if failure.url != "" {
				ref = xansi.SetHyperlink(failure.url) + ref + xansi.ResetHyperlink()
			}
			fmt.Fprintf(&b, "%s: %v\n", ref, failure.err)
			continue
		}
		fmt.Fprintf(&b, "%v\n", failure.err)
	}
	if remaining := len(msg.failures) - limit; remaining > 0 {
		fmt.Fprintf(&b, "\n…and %d more.", remaining)
	}
	return strings.TrimRight(b.String(), "\n")
}

type targetPR struct {
	index int
	pr    PullRequest
}

func batchResultsForTargets(targets []targetPR, err error) []batchResult {
	failures := make([]batchResult, 0, len(targets))
	for _, t := range targets {
		failures = append(failures, batchResult{
			key: makePRKey(t.pr),
			ref: t.pr.Ref(),
			url: t.pr.URL,
			err: err,
		})
	}
	return failures
}

func (m tuiModel) targetPRs() []targetPR {
	if len(m.selected) > 0 {
		var targets []targetPR
		for _, idx := range m.visibleIndices() {
			if m.selected[m.rowKeyAt(idx)] {
				targets = append(targets, targetPR{idx, m.rows[idx].Item.PR})
			}
		}
		return targets
	}
	if pr := m.currentPR(); pr != nil {
		return []targetPR{{m.cursor, *pr}}
	}
	return nil
}

// prForKey returns a pointer to the PR identified by key, or nil if not found.
func (m tuiModel) prForKey(key prKey) *PullRequest {
	if idx := m.resolveIndex(key, -1); idx >= 0 && idx < len(m.rows) {
		return &m.rows[idx].Item.PR
	}
	return nil
}

// isCurrentUserDiff reports whether the current diff PR was authored by the authenticated user.
// prStateForKey returns the lowercase state of the PR identified by key, or "" if not found.
func (m tuiModel) prStateForKey(key prKey) string {
	if idx := m.resolveIndex(key, -1); idx >= 0 && idx < len(m.rows) {
		return strings.ToLower(m.rows[idx].Item.State)
	}
	return ""
}

// prIsDraftForKey reports whether the PR identified by key is a draft.
func (m tuiModel) prIsDraftForKey(key prKey) bool {
	if idx := m.resolveIndex(key, -1); idx >= 0 && idx < len(m.rows) {
		return m.rows[idx].Item.PR.IsDraft
	}
	return false
}

func (m tuiModel) isCurrentUserDiff() bool {
	if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
		return m.isCurrentUserPR(m.rows[idx].Item.PR)
	}
	return false
}

// isCurrentUserDetail reports whether the current detail PR was authored by the authenticated user.
func (m tuiModel) isCurrentUserDetail() bool {
	if idx := m.resolveIndex(m.detailKey, -1); idx >= 0 && idx < len(m.rows) {
		return m.isCurrentUserPR(m.rows[idx].Item.PR)
	}
	return false
}

// targetActionablePRs returns targetPRs excluding merged and closed PRs.
func (m tuiModel) targetActionablePRs() []targetPR {
	targets := m.targetPRs()
	n := 0
	for _, t := range targets {
		state := strings.ToLower(t.pr.State)
		if state == valueMerged || state == valueClosed {
			continue
		}
		targets[n] = t
		n++
	}
	return targets[:n]
}

// targetOtherActionablePRs returns targetActionablePRs excluding PRs authored by the current user.
func (m tuiModel) targetOtherActionablePRs() []targetPR {
	targets := m.targetActionablePRs()
	n := 0
	for _, t := range targets {
		if m.isCurrentUserPR(t.pr) {
			continue
		}
		targets[n] = t
		n++
	}
	return targets[:n]
}

// targetMergeablePRs returns targetActionablePRs excluding draft PRs.
func (m tuiModel) targetMergeablePRs() []targetPR {
	targets := m.targetActionablePRs()
	n := 0
	for _, t := range targets {
		if t.pr.IsDraft {
			continue
		}
		targets[n] = t
		n++
	}
	return targets[:n]
}

// targetApprovablePRs returns targetPRs excluding PRs authored by the current user,
// draft PRs, and PRs that are merged or closed.
func (m tuiModel) targetApprovablePRs() []targetPR {
	targets := m.targetPRs()
	n := 0
	for _, t := range targets {
		state := strings.ToLower(t.pr.State)
		if m.isCurrentUserPR(t.pr) || t.pr.IsDraft || state == valueMerged || state == valueClosed {
			continue
		}
		targets[n] = t
		n++
	}
	return targets[:n]
}

func (m tuiModel) tableRendererFor(totalRows int) *table.Renderer[PRRowModel] {
	opts := make([]table.Option, 0, 1)
	opts = append(opts, table.WithShowIndex(false))
	if m.sortColumn != "" {
		sortColumn := m.sortColumn
		sortAsc := m.sortAsc
		opts = append(opts, table.WithHeaderRenderer(
			func(name, header string, ctx *table.RenderContext) string {
				rendered := ctx.Theme.RenderBold(header)
				if name != sortColumn || header == "" {
					return rendered
				}
				indicator := " ▼"
				if sortAsc {
					indicator = " ▲"
				}
				return rendered + ctx.Theme.RenderDim(indicator)
			},
		))
	}
	width := max(0, m.width-tuiListPrefixWidth(totalRows))
	return m.p.newTableRenderer(m.cli, m.tty, width, opts...)
}

func (m tuiModel) tableRenderer() *table.Renderer[PRRowModel] {
	return m.tableRendererFor(len(m.rows))
}

func estimateHeaderWidth(name string, compact bool) int {
	if compact {
		if w, ok := columnWidthEstimateCompact[name]; ok {
			return w
		}
	}
	if w, ok := columnWidthEstimate[name]; ok {
		return w
	}
	return 0
}

func renderEstimatedHeader(
	p *prl,
	renderer *table.Renderer[PRRowModel],
	sortColumn string,
	sortAsc bool,
	termWidth int,
) (string, []int) {
	cols := renderer.Columns()
	if len(cols) == 0 {
		return "", nil
	}

	colNames := make([]string, len(cols))
	for i, col := range cols {
		colNames[i] = col.Name
	}
	compact := termWidth > 0 && termWidth < compactTimeThreshold && hasTimeColumns(colNames)

	header := make([]string, len(cols))
	samples := make([]string, len(cols))
	flexCol := -1
	for i, col := range cols {
		cell := p.RenderBold(col.Header)
		if sortColumn != "" && col.Name == sortColumn && col.Header != "" {
			indicator := " ▼"
			if sortAsc {
				indicator = " ▲"
			}
			cell += p.RenderDim(indicator)
		}
		header[i] = cell

		width := max(estimateHeaderWidth(col.Name, compact), lg.Width(cell))
		samples[i] = strings.Repeat(" ", width)
		if col.Flex {
			flexCol = i
		}
	}

	g := table.NewGrid([][]string{header, samples})
	g.TTY = true
	if flexCol >= 0 && termWidth > 0 {
		g.FlexCol = flexCol
		g.MaxWidth = termWidth
	}
	aligned, colWidths := g.AlignColumns()
	return aligned[0], colWidths
}

func renderTUITable(
	p *prl,
	renderer *table.Renderer[PRRowModel],
	items []PRRowModel,
	sortColumn string,
	sortAsc bool,
	termWidth int,
) (string, []TableRow, []int) {
	if len(items) == 0 {
		header, colWidths := renderEstimatedHeader(p, renderer, sortColumn, sortAsc, termWidth)
		return header, nil, colWidths
	}

	rt := renderer.Render(items)
	rows := rt.Rows
	if sortColumn != "" {
		rows = table.SortRows(rows, renderer.Columns(), sortColumn, sortAsc)
	}
	return rt.Header, rows, rt.ColWidths
}

func (m tuiModel) rerender() (string, []TableRow, []int) {
	termWidth := max(0, m.width-tuiListPrefixWidth(len(m.items)))
	renderer := m.tableRendererFor(len(m.items))
	return renderTUITable(m.p, renderer, m.items, m.sortColumn, m.sortAsc, termWidth)
}

func (m tuiModel) currentPR() *PullRequest {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.removed[m.rowKeyAt(m.cursor)] {
		return nil
	}
	pr := m.rows[m.cursor].Item.PR
	return &pr
}

func (m *tuiModel) toggleCurrentSelection() bool {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.removed[m.rowKeyAt(m.cursor)] {
		return false
	}
	key := m.rowKeyAt(m.cursor)
	if m.selected[key] {
		delete(m.selected, key)
	} else {
		m.selected[key] = true
	}
	return true
}

func (m *tuiModel) extendSelectionAndMove(dir int) {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.removed[m.rowKeyAt(m.cursor)] {
		return
	}
	m.selected[m.rowKeyAt(m.cursor)] = true
	next, ok := m.nextVisible(dir)
	if !ok {
		return
	}
	m.cursor = next
	m.offset = m.scrolledOffset()
	m.selected[m.rowKeyAt(m.cursor)] = true
}

// isCurrentUserCursor reports whether the PR under the cursor was authored by the authenticated user.
func (m tuiModel) isCurrentUserCursor() bool {
	if pr := m.currentPR(); pr != nil {
		return m.isCurrentUserPR(*pr)
	}
	return false
}

func (m tuiModel) listHelpPairs() []helpPair {
	pairs := []helpPair{
		{tuiKeyEnter, tuiHelpShow},
		{tuiKeySpace, tuiHelpSelect},
		{tuiKeybindFilter, tuiHelpFilter},
	}
	pr := m.currentPR()
	var state string
	var draft, ownPR bool
	if pr != nil {
		state = strings.ToLower(pr.State)
		draft = pr.IsDraft
		ownPR = m.isCurrentUserCursor()
	}
	actionable := pr != nil && state != valueMerged && state != valueClosed
	if actionable && !ownPR && !draft {
		pairs = append(pairs, helpPair{tuiKeybindApprove, tuiHelpApprove})
	}
	pairs = append(pairs, helpPair{tuiKeybindDiff, tuiHelpDiff})
	if actionable && !draft {
		pairs = append(pairs, helpPair{tuiKeybindMerge, mergeHelpForPR(pr)})
	}
	pairs = append(pairs, helpPair{tuiKeybindComment, tuiHelpComment})
	if state != valueMerged {
		pairs = append(pairs, helpPair{tuiKeybindClose, closeReopenHelp(state)})
	}
	pairs = append(
		pairs,
		helpPair{tuiKeybindOpen, tuiHelpOpen},
		helpPair{tuiKeybindCopyURL, tuiHelpCopy},
	)
	if actionable && !draft && hasClaudeReviewLauncher() {
		pairs = append(pairs, helpPair{tuiKeybindReview, tuiHelpReview})
	}
	if m.autoRefresh {
		pairs = append(pairs, helpPair{tuiKeybindToggleRefresh, "refresh " + styledOn})
	} else {
		pairs = append(pairs, helpPair{tuiKeybindToggleRefresh, "refresh " + styledOff})
	}
	pairs = append(pairs,
		helpPair{tuiKeybindOptions, tuiHelpOptions},
		helpPair{tuiKeybindHelp, tuiHelpHelp},
		helpPair{tuiKeybindQuit, tuiHelpQuit},
	)
	return pairs
}

func (m tuiModel) renderListHelp() string {
	return m.renderHelp(m.listHelpPairs())
}

func (m tuiModel) renderFilterSyntaxHints() string {
	syntaxKey := lg.NewStyle().Foreground(lg.Color("208")).Bold(true)
	syntaxDesc := lg.NewStyle().Foreground(lg.Color("216"))
	syntaxPairs := []struct{ key, desc string }{
		{"^", "start"},
		{"$", "end"},
		{"!", "negate"},
	}
	var parts []string
	for _, p := range syntaxPairs {
		parts = append(parts, syntaxKey.Render(p.key)+" "+syntaxDesc.Render(p.desc))
	}
	return " " + strings.Join(parts, "  ")
}

const filterHelpGap = "  "

// filterHelpPipeCol returns the visual column of the │ separator in the filter help line.
func (m tuiModel) filterHelpPipeCol() int {
	return lg.Width(m.renderFilterSyntaxHints()) + len(filterHelpGap)
}

func (m tuiModel) renderFilterHelp() string {
	pairs := []helpPair{
		{tuiKeyArrows, "prev/next"},
		{tuiKeyEnter, "apply"},
		{tuiKeyEsc, "exit"},
	}
	base := strings.TrimLeft(m.renderHelp(pairs), " ")
	sep := m.styles.separator.Render("│")
	return m.renderFilterSyntaxHints() + filterHelpGap + sep + filterHelpGap + base
}

func (m tuiModel) diffHelpPairs() []helpPair {
	pairs := []helpPair{
		{tuiKeyArrows, tuiHelpScroll},
	}
	pr := m.prForKey(m.diffKey)
	state := m.prStateForKey(m.diffKey)
	draft := m.prIsDraftForKey(m.diffKey)
	ownPR := m.isCurrentUserDiff()
	actionable := state != valueMerged && state != valueClosed
	if actionable && !draft {
		pairs = append(pairs, helpPair{tuiKeybindMerge, mergeHelpForPR(pr)})
	}
	if actionable {
		pairs = append(pairs, helpPair{tuiKeybindDraftToggle, draftToggleHelp(pr)})
	}
	if actionable && !ownPR && !draft {
		pairs = append(
			pairs,
			helpPair{tuiKeybindApprove + "/" + tuiKeybindApproveAlias, tuiHelpApprove},
		)
		pairs = append(pairs, helpPair{tuiKeybindApproveMerge, tuiHelpApproveMerge})
	}
	if actionable && !ownPR {
		pairs = append(pairs, helpPair{tuiKeybindUnassign, tuiHelpUnsubscribe})
	}
	pairs = append(pairs, helpPair{tuiKeybindComment, tuiHelpComment})
	if state != valueMerged {
		pairs = append(pairs, helpPair{tuiKeybindClose, closeReopenHelp(state)})
	}
	pairs = append(pairs, helpPair{tuiKeybindOpen, tuiHelpOpen})
	pairs = append(pairs, helpPair{tuiKeybindCopyURL, tuiHelpCopy})
	if actionable {
		pairs = append(pairs, helpPair{tuiKeybindSlack, tuiHelpSlack})
	}

	if actionable && !draft && hasClaudeReviewLauncher() {
		pairs = append(pairs, helpPair{tuiKeybindReview, tuiHelpReview})
	}
	if actionable && !draft {
		pairs = append(pairs, helpPair{tuiKeybindCopilotReview, tuiHelpCopilotReview})
	}
	if m.diffQueueTotal > 0 {
		if len(m.diffHistory) > 0 {
			pairs = append(pairs, helpPair{tuiKeybindPrev, tuiHelpPrev})
		}
		if len(m.diffQueue) > 0 {
			pairs = append(pairs, helpPair{tuiKeybindNext, tuiHelpNext})
		}
	}
	pairs = append(pairs, helpPair{tuiKeybindDiff, tuiHelpDismiss})
	return pairs
}

func (m tuiModel) renderDiffHelp() string {
	return m.renderHelp(m.diffHelpPairs())
}

func (m tuiModel) detailHelpPairs() []helpPair {
	pairs := []helpPair{
		{tuiKeyArrows, tuiHelpScroll},
		{tuiKeybindDiff, tuiHelpDiff},
	}
	pr := m.prForKey(m.detailKey)
	state := m.prStateForKey(m.detailKey)
	draft := m.prIsDraftForKey(m.detailKey)
	actionable := state != valueMerged && state != valueClosed
	if actionable && !draft {
		pairs = append(pairs, helpPair{tuiKeybindMerge, mergeHelpForPR(pr)})
	}
	if actionable {
		pairs = append(pairs, helpPair{tuiKeybindDraftToggle, draftToggleHelp(pr)})
	}
	if actionable && !draft && !m.isCurrentUserDetail() {
		pairs = append(
			pairs,
			helpPair{tuiKeybindApprove + "/" + tuiKeybindApproveAlias, tuiHelpApprove},
		)
	}
	pairs = append(pairs, helpPair{tuiKeybindComment, tuiHelpComment})
	pairs = append(pairs, helpPair{tuiKeybindOpen, tuiHelpOpen})
	pairs = append(pairs, helpPair{tuiKeybindCopyURL, tuiHelpCopy})
	if actionable {
		pairs = append(pairs, helpPair{tuiKeybindSlack, tuiHelpSlack})
	}
	if actionable && m.detail.MergeableState == valueBehind {
		pairs = append(pairs, helpPair{tuiKeybindUpdateBranch, tuiHelpUpdateBranch})
	}
	if actionable && !draft && hasClaudeReviewLauncher() {
		pairs = append(pairs, helpPair{tuiKeybindReview, tuiHelpReview})
	}
	if actionable && !draft {
		pairs = append(pairs, helpPair{tuiKeybindCopilotReview, tuiHelpCopilotReview})
	}
	pairs = append(pairs, helpPair{tuiKeybindQuit, tuiHelpDismiss})
	return pairs
}

func (m tuiModel) renderDetailHelp() string {
	return m.renderHelp(m.detailHelpPairs())
}

func (m tuiModel) renderHelpOverlay() string {
	pairs := []helpPair{
		{tuiKeyArrows + " · j/k", tuiDescNavigate},
		{tuiKeyJumpFirstLast, tuiDescJumpFirstLast},
		{tuiKeyEnter, tuiDescShow},
		{tuiKeySpace, tuiDescSelect},
		{"shift+" + tuiKeyArrows, tuiDescExtendSelection},
		{tuiKeybindSelectAll, tuiDescSelectAll},
		{tuiKeybindInvertSelection, tuiDescInvertSelection},
		{tuiKeybindFilter, tuiDescFilter},
		{tuiKeybindApprove, tuiDescApprove},
		{tuiKeybindApproveMerge, tuiDescApproveMerge},
		{tuiKeybindApproveNoConfirm, tuiDescApproveNoConfirm},
		{tuiKeybindDiff, tuiDescDiff},
		{tuiKeybindDraftToggle, tuiDescDraftToggle},
		{tuiKeybindMerge, tuiDescMerge},
		{tuiKeybindForceMerge, tuiDescForceMerge},
		{tuiKeybindClose, tuiDescClose},
		{tuiKeybindUpdateBranch, tuiDescUpdateBranch},
		{tuiKeybindUnassign, tuiDescUnassign},
		{tuiKeybindUnassignNoConfirm, tuiDescUnassignNoConf},
		{tuiKeybindOpen, tuiDescOpen},
		{tuiKeybindCopyURL, tuiDescCopy},
		{tuiKeybindSlack, tuiDescSendSlack},
		{tuiKeybindSlackNoConfirm, tuiDescSendSlackNoConf},
		{tuiKeyTab, tuiDescCycleSortOrder},
		{tuiKeybindOptions, tuiDescOptions},
		{tuiKeybindToggleRefresh, tuiDescRefresh},
		{tuiKeybindHelp, tuiDescHelp},
		{tuiKeybindQuit, tuiDescQuit},
	}
	if hasClaudeReviewLauncher() {
		// Insert review before the last two entries (?, q).
		pairs = append(
			pairs[:len(pairs)-2],
			append(
				[]helpPair{
					{tuiKeybindReview, tuiDescReview},
					{tuiKeybindReviewNoConfirm, tuiDescReviewNoConfirm},
					{tuiKeybindCopilotReview, tuiDescCopilotReview},
				},
				pairs[len(pairs)-2:]...)...)
	}

	// Render in two columns.
	rows := (len(pairs) + 1) / 2 //nolint:mnd // ceil division
	keyWidth := 0
	for _, p := range pairs {
		keyWidth = max(keyWidth, lg.Width(p.key))
	}
	renderPair := func(p helpPair) string {
		pad := max(0, keyWidth-lg.Width(p.key))
		key := strings.Repeat(" ", pad) + p.key
		return m.styles.helpKey.Render(key) + "  " + m.styles.helpText.Render(p.desc)
	}

	// Measure column widths for alignment.
	const gutter = 4
	leftWidth := 0
	rightWidth := 0
	for i := range rows {
		if w := lg.Width(renderPair(pairs[i])); w > leftWidth {
			leftWidth = w
		}
		if i+rows < len(pairs) {
			if w := lg.Width(renderPair(pairs[i+rows])); w > rightWidth {
				rightWidth = w
			}
		}
	}
	totalWidth := leftWidth + gutter + rightWidth

	var b strings.Builder
	for i := range rows {
		left := renderPair(pairs[i])
		if i+rows < len(pairs) {
			right := renderPair(pairs[i+rows])
			pad := leftWidth - lg.Width(left) + gutter
			b.WriteString(left + strings.Repeat(" ", pad) + right)
		} else {
			b.WriteString(left)
		}
		b.WriteString("\n")
	}
	dismiss := m.styles.helpText.Bold(true).
		Foreground(lg.Color("210")).
		Render("Press any key to dismiss")
	pad := (totalWidth - lg.Width(dismiss)) / 2 //nolint:mnd // center
	if pad > 0 {
		b.WriteString("\n" + strings.Repeat(" ", pad) + dismiss)
	} else {
		b.WriteString("\n" + dismiss)
	}

	return m.styles.overlayBox.Render(b.String())
}

// renderOptionsOverlay renders the filter options overlay.
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
				line.WriteString(
					lg.NewStyle().Bold(true).Foreground(lg.Color("218")).Render(c.label),
				)
			case isDefault:
				line.WriteString(m.styles.defaultChoice.Render(c.label))
			case locked:
				if selected {
					line.WriteString(
						lg.NewStyle().Bold(true).Foreground(lg.Color("218")).Render(c.label),
					)
				} else {
					line.WriteString(
						lg.NewStyle().Faint(true).Foreground(lg.Color("240")).Render(c.label),
					)
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

// appendStatus appends the status message to the right of the last line of help,
// or returns help unchanged if there's no status or not enough room.
func (m tuiModel) appendStatus(help string) string {
	if m.statusMsg == "" {
		return help
	}
	style := m.styles.statusOK
	if m.statusErr {
		style = m.styles.statusErr
	}
	return m.appendRightStatus(help, style.Render(m.statusMsg))
}

func (m tuiModel) appendRightStatus(help, status string) string {
	if status == "" || m.width <= 0 {
		return help
	}
	lastNL := strings.LastIndex(help, "\n")
	lastLine := help
	if lastNL >= 0 {
		lastLine = help[lastNL+1:]
	}
	pad := m.width - lg.Width(lastLine) - lg.Width(status) - 1
	if pad > 0 {
		return help + strings.Repeat(" ", pad) + status + " "
	}
	return help
}

func (m tuiModel) renderHelp(pairs []helpPair) string {
	const gap = "  "
	var parts []string
	helpText := m.styles.helpText
	for _, p := range pairs {
		var rendered string
		if strings.Contains(p.desc, "\x1b[") {
			// Desc contains pre-styled ANSI - split at the boundary.
			if idx := strings.Index(p.desc, "\x1b["); idx > 0 {
				rendered = helpText.Render(p.desc[:idx]) + p.desc[idx:]
			} else {
				rendered = p.desc
			}
		} else {
			rendered = helpText.Render(p.desc)
		}
		parts = append(parts, m.styles.helpKey.Render(p.key)+" "+rendered)
	}

	if m.width <= 0 {
		return " " + strings.Join(parts, gap)
	}

	// Wrap into multiple lines if needed.
	const indent = " "
	var lines []string
	var line string
	lineWidth := len(indent)
	gapWidth := lg.Width(gap)
	for i, part := range parts {
		partWidth := lg.Width(part)
		switch {
		case i == 0:
			line = indent + part
			lineWidth = len(indent) + partWidth
		case lineWidth+gapWidth+partWidth > m.width:
			lines = append(lines, line)
			line = part
			lineWidth = partWidth
		default:
			line += gap + part
			lineWidth += gapWidth + partWidth
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// helpLines returns the number of lines the help bar occupies at the current width.
func (m tuiModel) helpLines(pairs []helpPair) int {
	return strings.Count(m.renderHelp(pairs), "\n") + 1
}

// confirmActionVerb maps confirm action names to in-progress verbs.
var confirmActionVerb = map[string]string{
	tuiActionApprove:      "Approving",
	tuiActionApproveMerge: "Approving & merging",
	tuiActionClose:        "Closing",
	tuiActionComment:      "Commenting",
	tuiActionForceMerge:   "Force-merging",
	tuiActionMerge:        "Merging",
	tuiActionSendSlack:    "Sending to Slack",
	tuiActionUnassign:     "Unassigning",
}

func (m tuiModel) confirmAccept() (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.confirmHasInput && m.confirmCmdFn != nil {
		cmd = m.confirmCmdFn(strings.TrimSpace(m.confirmInput.Value()))
	} else {
		cmd = m.confirmCmd
	}
	verb := confirmActionVerb[m.confirmAction]
	subject := m.confirmSubject
	url := m.confirmURL
	m = m.clearConfirm()
	if verb != "" {
		if subject != "" {
			styledSubject := lg.NewStyle().Foreground(lg.Color("117")).Render(subject)
			if url != "" {
				styledSubject = xansi.SetHyperlink(url) + styledSubject + xansi.ResetHyperlink()
			}
			m.statusMsg = m.styles.statusPending.Render(
				verb,
			) + " " + styledSubject + valueEllipsis
		} else {
			m.statusMsg = m.styles.statusPending.Render(verb) + valueEllipsis
		}
		m.statusErr = false
	}
	return m, cmd
}

func (m tuiModel) confirmDismiss() (tea.Model, tea.Cmd) {
	m = m.clearConfirm()
	return m, nil
}

// confirmInputPending returns true when a confirm modal with a text input is active.
func (m tuiModel) confirmInputPending() bool {
	return m.confirmHasInput && m.confirmAction != ""
}

func newConfirmInput() textarea.Model {
	ci := textarea.New()
	ci.Prompt = ""
	ci.Placeholder = "Leave blank to close without comment"
	ci.ShowLineNumbers = false
	ci.SetWidth(tuiConfirmInputWidth)
	ci.SetHeight(tuiConfirmInputMinHeight)
	ciStyles := ci.Styles()
	ciStyles.Focused.Text = lg.NewStyle().Foreground(lg.Color("255"))
	ciStyles.Focused.Placeholder = lg.NewStyle().Foreground(lg.Color("242")).Italic(true)
	ciStyles.Focused.CursorLine = lg.NewStyle()
	ciStyles.Blurred.Text = lg.NewStyle().Foreground(lg.Color("242"))
	ciStyles.Blurred.CursorLine = lg.NewStyle()
	ciStyles.Cursor.Color = lg.Color("255")
	ci.SetStyles(ciStyles)
	return ci
}

// resizeConfirmInput adjusts the textarea height to fit the content.
func (m *tuiModel) resizeConfirmInput() {
	lines := strings.Count(
		m.confirmInput.Value(),
		"\n",
	) + 2 //nolint:mnd // +1 for current line, +1 for cursor visibility
	h := max(tuiConfirmInputMinHeight, min(lines, tuiConfirmInputMaxHeight))
	m.confirmInput.SetHeight(h)
}

func (m tuiModel) updateConfirmOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Info-only modal (no confirmCmd) - any key dismisses.
	if m.confirmCmd == nil && m.confirmCmdFn == nil {
		switch msg.String() {
		case tuiKeyEnter, "q", tuiKeyEsc, "y", "n", " ":
			return m.confirmDismiss()
		default:
			return m, nil
		}
	}
	// Modal with text input (e.g. close comment).
	if m.confirmHasInput && m.confirmInput.Focused() {
		switch msg.String() {
		case tuiKeyEsc:
			return m.confirmDismiss()
		case tuiKeybindConfirmSubmit:
			m.confirmYes = true
			return m.confirmAccept()
		default:
			var cmd tea.Cmd
			m.confirmInput, cmd = m.confirmInput.Update(msg)
			m.resizeConfirmInput()
			return m, cmd
		}
	}
	switch msg.String() {
	case tuiKeyLeft, tuiKeyRight, tuiKeybindVimLeft, tuiKeybindVimRight, tuiKeySpace, tuiKeyTab:
		m.confirmYes = !m.confirmYes
		return m, nil
	case tuiKeybindConfirmYes:
		return m.confirmAccept()
	case tuiKeybindConfirmNo, tuiKeybindQuit, tuiKeyEsc:
		return m.confirmDismiss()
	case tuiKeyEnter:
		if m.confirmYes {
			return m.confirmAccept()
		}
		return m.confirmDismiss()
	default:
		return m, nil
	}
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

	// Bump filter generation to discard any in-flight stale refresh results.
	m.filterGen++

	// Trigger background refresh with spinner.
	m.refreshing = true
	m.showRefreshStatus = true
	m.spinnerTick = 0
	m.spinnerID++
	m.statusMsg = lg.NewStyle().Foreground(lg.Color("218")).Bold(true).Render("Applying…")
	m.statusErr = false

	// Recompute cursor/offset since viewport may change (filter indicator line).
	m.cursor = m.adjustedCursor()
	m.offset = m.scrolledOffset()

	filterGen := m.filterGen
	spinCmd := m.scheduleSpinnerTick()
	spinnerID := m.spinnerID
	snapshot := newRefreshSnapshot(m)
	refreshCmd := func() tea.Msg {
		result := snapshot.run()
		result.filterGen = filterGen
		result.spinnerID = spinnerID
		return result
	}
	return m, tea.Batch(spinCmd, refreshCmd)
}

func (m tuiModel) renderConfirmModal() string {
	var buttons string
	if m.confirmCmd == nil && m.confirmCmdFn == nil {
		// Info-only modal - single OK button.
		buttons = lg.NewStyle().
			Background(lg.Color("218")).
			Foreground(lg.Color("#000000")).
			Padding(0, 1).
			Bold(true).
			Render("OK")
	} else {
		var yes, no string
		if m.confirmYes {
			yes = m.styles.confirmYes.Render("Yes")
			no = m.styles.confirmNoDim.Render("No")
		} else {
			yes = m.styles.confirmYesDim.Render("Yes")
			no = m.styles.confirmNo.Render("No")
		}
		buttons = no + "  " + yes
	}

	var b strings.Builder
	b.WriteString(m.confirmPrompt)

	if m.confirmHasInput {
		label := m.confirmInputLabel
		if label == "" {
			label = "Comment"
		}
		b.WriteString("\n\n")
		b.WriteString(
			lg.NewStyle().Foreground(lg.Color("218")).Bold(true).Render(label),
		)
		b.WriteString("\n")
		b.WriteString(m.confirmInput.View())
		b.WriteString("\n\n")
		helpKey := m.styles.helpKey
		helpText := m.styles.helpText
		hint := helpKey.Render(tuiKeybindConfirmSubmit) + " " + helpText.Render("submit") + "  " +
			helpKey.Render("esc") + " " + helpText.Render("cancel")
		b.WriteString(hint)
		// Fix width so the border stays aligned as the textarea grows.
		boxWidth := tuiConfirmInputWidth + tuiConfirmPadX*2 //nolint:mnd // border + padding
		return m.styles.overlayBox.Width(boxWidth).Render(b.String())
	}

	b.WriteString("\n\n")

	content := b.String()
	promptWidth := lg.Width(m.confirmPrompt)
	buttonsWidth := lg.Width(buttons)
	centered := buttons
	if pad := (promptWidth - buttonsWidth) / 2; pad > 0 { //nolint:mnd // center
		centered = strings.Repeat(" ", pad) + buttons
	}
	return m.styles.overlayBox.Render(content + centered)
}

func (m tuiModel) renderEmptyOverlay() string {
	dim := m.styles.helpText
	key := m.styles.helpKey
	box := lg.NewStyle().
		Border(lg.RoundedBorder()).
		BorderForeground(lg.Color("198")).
		Padding(1, tuiConfirmPadX)
	if m.filterInput.Value() != "" {
		filter := lg.NewStyle().Foreground(lg.Color("216"))
		// Truncate the filter value so the overlay doesn't overflow.
		prefix := "No pull requests match \""
		suffix := "\""
		maxQuery := max(1, m.width*4/5-len(prefix)-len(suffix))
		query := m.filterInput.Value()
		if len(query) > maxQuery {
			query = query[:maxQuery-1] + valueEllipsis
		}
		line1 := dim.Render(prefix) +
			filter.Render(query) + dim.Render(suffix)
		line2 := dim.Render("Refine your search, or press ") +
			key.Render("esc") + dim.Render(" to clear the filter")
		return box.Render(line1 + "\n\n" + line2)
	}
	var line1 string
	if len(m.removed) > 0 {
		line1 = dim.Render("All pull requests have been processed")
	} else {
		line1 = dim.Render("No pull requests found for the current query")
	}
	line2 := dim.Render("Press ") + key.Render("q") + dim.Render(" to quit, or ") +
		key.Render("esc") + dim.Render(" to dismiss and wait for new results")
	return box.Render(line1 + "\n\n" + line2)
}

func (m tuiModel) clearConfirm() tuiModel {
	m.confirmAction = ""
	m.confirmPrompt = ""
	m.confirmSubject = ""
	m.confirmURL = ""
	m.confirmCmd = nil
	m.confirmCmdFn = nil
	m.confirmHasInput = false
	m.confirmInputLabel = ""
	m.confirmInput.Blur()
	m.confirmInput.SetValue("")
	m.confirmInput.SetHeight(tuiConfirmInputMinHeight)
	return m
}

func (m tuiModel) prepareClaudeReviewConfirm(pr PullRequest, idx int) tuiModel {
	prCopy := pr
	m.confirmAction = tuiActionReview
	m.confirmYes = true
	m.confirmHasInput = true
	m.confirmInputLabel = "Prompt"
	m.confirmInput.SetValue(defaultClaudeReviewPrompt(pr))
	m.confirmPrompt = "Launch Claude review for " + styledRef(&prCopy) + "?"
	m.confirmCmdFn = func(prompt string) tea.Cmd {
		return func() tea.Msg {
			err := launchClaudeReview(prCopy, prompt)
			return claudeReviewMsg{index: idx, key: makePRKey(prCopy), err: err}
		}
	}
	return m
}

// styledRef returns a bold, hyperlinked PR ref for use in confirm prompts.
func styledRef(pr *PullRequest) string {
	ref := lg.NewStyle().Bold(true).Foreground(lg.Color("117")).Render(pr.Ref())
	return xansi.SetHyperlink(pr.URL) + ref + xansi.ResetHyperlink()
}

// overlayCenter places a box on top of a background string, centered.
func overlayCenter(bg, fg string, width, height int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	// Use WcWidth consistently - it matches terminal rendering for emoji
	// with variation selectors (e.g. ⬆️) where GraphemeWidth disagrees.
	strWidth := xansi.WcWidth.StringWidth

	fgWidth := 0
	for _, line := range fgLines {
		if w := strWidth(line); w > fgWidth {
			fgWidth = w
		}
	}
	fgHeight := len(fgLines)

	startRow := (height - fgHeight) / 2 //nolint:mnd // center
	startCol := (width - fgWidth) / 2   //nolint:mnd // center
	if startRow < 0 {
		startRow = 0
	}
	if startCol < 0 {
		startCol = 0
	}

	// Ensure bg has enough lines.
	for len(bgLines) < height {
		bgLines = append(bgLines, "")
	}

	for i, fgLine := range fgLines {
		row := startRow + i
		if row >= len(bgLines) {
			break
		}
		bgLine := bgLines[row]
		// Pad bg line to startCol with spaces if needed.
		bgVisible := strWidth(bgLine)
		if bgVisible < startCol {
			bgLine += strings.Repeat(" ", startCol-bgVisible)
		}
		// Build: bg left portion + fg line + bg right portion.
		// Use WcWidth-aware truncation to match terminal rendering.
		left := xansi.TruncateWc(bgLine, startCol, "")
		if gap := startCol - strWidth(left); gap > 0 {
			left += strings.Repeat(" ", gap)
		}
		rightStart := startCol + fgWidth
		var right string
		if bgVisible > rightStart {
			right = xansi.TruncateLeftWc(bgLine, rightStart, "")
		}
		bgLines[row] = left + "\033[0m" + fgLine + right
	}

	return strings.Join(bgLines, "\n")
}

// cursorLineBG is the ANSI escape to set the cursor line background color.
const cursorLineBG = "\x1b[48;2;40;10;30m"

// cursorLineSelectedBG is the ANSI escape for selected (checked) row backgrounds.
const cursorLineSelectedBG = "\x1b[48;2;10;30;15m"

// injectLineBackground wraps a line with a background color that persists
// through any embedded ANSI SGR codes. It re-applies the background after
// every SGR sequence (\x1b[...m) so that resets, foreground changes, and
// other styling never clear the line highlight.
func injectLineBackground(line string, width int, bg string) string {
	var b strings.Builder
	b.WriteString(bg)

	i := 0
	for i < len(line) {
		// Look for ESC [ ... m (SGR sequence).
		if line[i] == '\x1b' && i+1 < len(line) && line[i+1] == '[' {
			// Find the end of the SGR sequence.
			j := i + 2 //nolint:mnd // skip ESC [
			for j < len(line) && ((line[j] >= '0' && line[j] <= '9') || line[j] == ';') {
				j++
			}
			if j < len(line) && line[j] == 'm' {
				j++ // include the 'm'
				b.WriteString(line[i:j])
				b.WriteString(bg) // re-apply background after any SGR
				i = j
				continue
			}
		}
		b.WriteByte(line[i])
		i++
	}

	if pad := width - lg.Width(line); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

func truncateDisplayLine(line string, width int) string {
	if width <= 0 || xansi.WcWidth.StringWidth(line) <= width {
		return line
	}
	return xansi.WcWidth.Truncate(line, width, "")
}

func wrapDiffLines(diff string, width int) []string {
	logicalLines := strings.Split(diff, "\n")
	if width <= 0 {
		return logicalLines
	}

	rows := make([]string, 0, len(logicalLines))
	for _, line := range logicalLines {
		rows = append(rows, hardWrapDiffLine(line, width)...)
	}
	return rows
}

func hardWrapDiffLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}

	wrapped := xansi.Hardwrap(line, width, true)
	if !strings.Contains(wrapped, "\n") {
		return []string{line}
	}

	var buf bytes.Buffer
	writer := lg.NewWrapWriter(&buf)
	_, _ = writer.Write([]byte(wrapped))
	_ = writer.Close()
	return strings.Split(buf.String(), "\n")
}

// highlightDiff highlights a unified diff using delta if available,
// falling back to Chroma syntax highlighting.
func highlightDiff(raw, prURL, headSHA string) string {
	if p, err := exec.LookPath("delta"); err == nil {
		if out, err := highlightWithDelta(p, raw, prURL, headSHA); err == nil {
			return out
		}
	}
	return highlightWithChroma(raw)
}

// highlightWithDelta pipes a diff through delta for rich highlighting.
// File hyperlinks point to the blob at the PR's head commit on GitHub.
func highlightWithDelta(deltaBin, raw, prURL, headSHA string) (string, error) {
	// prURL is e.g. "https://github.com/owner/repo/pull/123"
	// Strip "/pull/123" to get the repo URL, then build blob links.
	// Delta resolves {path} to an absolute path based on CWD, so we set
	// CWD to "/" which gives us "/{relative_path}" - no extra slash needed.
	repoURL := prURL[:strings.LastIndex(prURL, "/pull/")]
	linkFmt := repoURL + "/blob/" + headSHA + "{path}?plain=1#L{line}"

	cmd := exec.CommandContext(
		context.Background(),
		deltaBin,
		"--true-color=always",
		"--hyperlinks",
		"--hyperlinks-file-link-format", linkFmt,
	)
	cmd.Dir = "/"
	cmd.Stdin = strings.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// highlightWithChroma applies Chroma syntax highlighting to a unified diff.
func highlightWithChroma(raw string) string {
	lexer := lexers.Get("diff")
	if lexer == nil {
		return raw
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("monokai")
	formatter := formatters.TTY256
	iterator, err := lexer.Tokenise(nil, raw)
	if err != nil {
		return raw
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return raw
	}
	return buf.String()
}

// launchClaudeReview opens a new terminal tab, clones the PR there, and
// launches a Claude session in that tab. Cloning happens in the new tab
// so SSH prompts and progress are visible to the user.
func launchClaudeReview(pr PullRequest, prompt string) error {
	launcher := currentClaudeReviewLauncher()
	if launcher == claudeLauncherNone {
		return fmt.Errorf("unsupported terminal %q", os.Getenv("TERM_PROGRAM"))
	}

	script, err := buildClaudeReviewAppleScript(launcher, buildClaudeReviewCommand(pr, prompt))
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(
		context.Background(),
		"osascript",
		"-e",
		script,
	)
	if output, asErr := cmd.CombinedOutput(); asErr != nil {
		return fmt.Errorf("osascript: %w: %s", asErr, strings.TrimSpace(string(output)))
	}
	return nil
}

func defaultClaudeReviewPrompt(pr PullRequest) string {
	nwo := pr.Repository.NameWithOwner
	return fmt.Sprintf(
		"Perform a comprehensive code review of PR #%d in %s. "+
			"The PR branch is checked out. First read the PR context with: gh pr view %[1]d --repo %[2]s "+
			"Then get the diff with: gh api repos/%[2]s/pulls/%[1]d -H 'Accept: application/vnd.github.v3.diff' "+
			"Focus on: correctness, edge cases, error handling, performance, readability, and style. "+
			"Be thorough but concise.",
		pr.Number, nwo,
	)
}

func buildClaudeReviewCommand(pr PullRequest, prompt string) string {
	nwo := pr.Repository.NameWithOwner

	// Clone repo and checkout the PR ref in the new tab so the user sees
	// progress and any SSH/auth prompts. Fetches refs/pull/N/head which
	// works for open, closed, and fork PRs alike.
	remote := "git@github.com:" + nwo
	// Use a fixed review directory so the user only has to trust it once.
	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		cacheHome = os.Getenv("HOME") + "/.cache"
	}
	reviewDir := fmt.Sprintf("%s/prl/reviews/%s/%d", cacheHome, pr.Repository.Name, pr.Number)
	return fmt.Sprintf(
		"/usr/bin/trash %[1]q 2>/dev/null; /bin/mkdir -p %[1]q && cd %[1]q && git clone --quiet --depth 1 %[2]q . && git fetch origin refs/pull/%[3]d/head:pr-%[3]d --no-tags && git checkout pr-%[3]d && claude --allowedTools 'Bash(gh:*)' --system-prompt %[4]q %[5]q",
		reviewDir,
		remote,
		pr.Number,
		"You are an expert code reviewer. Be thorough, precise, and actionable.",
		prompt,
	)
}

func buildClaudeReviewAppleScript(launcher claudeReviewLauncher, shellCmd string) (string, error) {
	switch launcher {
	case claudeLauncherNone:
		return "", fmt.Errorf("unsupported terminal %q", launcher)
	case claudeLauncherGhostty:
		return fmt.Sprintf(`tell application "Ghostty"
	tell application "System Events" to tell process "Ghostty" to set frontmost to true
	set cfg to new surface configuration
	set initial input of cfg to %q
	new tab in front window with configuration cfg
end tell`, shellCmd), nil
	case claudeLauncherITerm2:
		return fmt.Sprintf(`tell application "iTerm2"
	activate
	tell current window
		set newTab to (create tab with default profile)
		tell current session of newTab
			write text " " & %q
		end tell
	end tell
end tell`, shellCmd), nil
	}
	return "", fmt.Errorf("unsupported terminal %q", launcher)
}

type (
	prKey  string
	prKeys map[prKey]bool
)

// makePRKey returns a unique identifier for a PR (repo + number).
func makePRKey(pr PullRequest) prKey {
	return prKey(fmt.Sprintf("%s#%d", pr.Repository.NameWithOwner, pr.Number))
}

// resolveIndex resolves a PR key to its current index in the model's row list.
// If the key matches the hint index, it returns the hint directly (fast path).
// Returns -1 if the PR is no longer in the list.
func (m tuiModel) resolveIndex(key prKey, hint int) int {
	if key == "" {
		return -1
	}
	if hint >= 0 && hint < len(m.rows) && makePRKey(m.rows[hint].Item.PR) == key {
		return hint
	}
	for i, row := range m.rows {
		if makePRKey(row.Item.PR) == key {
			return i
		}
	}
	return -1
}

func (m tuiModel) rowKeyAt(idx int) prKey {
	if idx < 0 || idx >= len(m.rows) {
		return ""
	}
	return makePRKey(m.rows[idx].Item.PR)
}

// mergeRefresh replaces the model's data with fresh results while preserving
// cursor position, selections, and removed state by matching on PR identity.
func (m tuiModel) mergeRefresh(newRows []TableRow, newItems []PRRowModel) tuiModel {
	oldRows := m.rows

	// Try to keep cursor on the same PR.
	cursorKey := prKey("")
	if m.cursor >= 0 && m.cursor < len(oldRows) {
		cursorKey = makePRKey(oldRows[m.cursor].Item.PR)
	}
	newCursor := 0
	for i, row := range newRows {
		if makePRKey(row.Item.PR) == cursorKey {
			newCursor = i
			break
		}
	}

	m.items = newItems
	m.rows = newRows
	m.cursor = newCursor
	m.offset = m.scrolledOffset()
	return m
}

// reindexRows remaps the cursor from oldRows order to newRows order using PR identity.
func (m tuiModel) reindexRows(oldRows, newRows []TableRow) tuiModel {
	if m.cursor >= 0 && m.cursor < len(oldRows) {
		key := makePRKey(oldRows[m.cursor].Item.PR)
		for i, row := range newRows {
			if makePRKey(row.Item.PR) == key {
				m.cursor = i
				break
			}
		}
	}
	m.offset = m.scrolledOffset()
	return m
}

// rowFilterText returns the semantic text for a row, used for filtering.
func rowFilterText(row TableRow) string {
	parts := make([]string, 0, len(row.Cells))
	for _, cell := range row.Cells {
		if cell.Plain != "" {
			parts = append(parts, cell.Plain)
		}
	}
	return strings.Join(parts, " ")
}

// headerColumnAt maps a click X coordinate to a column name by walking colWidths.
func (m tuiModel) headerColumnAt(x int) string {
	x -= m.listPrefixWidth()
	if x < 0 {
		return ""
	}
	renderer := m.tableRenderer()
	cols := renderer.Columns()
	const colGap = 2 // matches grid.defaultColumnPadding
	pos := 0
	for i, w := range m.colWidths {
		end := pos + w
		if x >= pos && x < end+colGap {
			if i >= 0 && i < len(cols) {
				return cols[i].Name
			}
		}
		pos = end + colGap
	}
	return ""
}

// toggleSort cycles a column through: default direction → reverse → off.
// Clicking a different column activates it with its default direction.
func (m tuiModel) toggleSort(col string) (tea.Model, tea.Cmd) {
	sortable := m.sortableColumns()
	if !slices.Contains(sortable, col) {
		return m, nil
	}

	oldRows := m.rows
	if m.sortColumn == col {
		// Already sorted by this column: cycle direction → off.
		if m.sortAsc == defaultSortAsc(col) {
			// Currently default direction → reverse.
			m.sortAsc = !m.sortAsc
		} else {
			// Currently reversed → turn off.
			m.sortColumn = ""
			m.sortAsc = false
		}
	} else {
		m.sortColumn = col
		m.sortAsc = defaultSortAsc(col)
	}
	header, newRows, colWidths := m.rerender()
	m = m.reindexRows(oldRows, newRows)
	m.header = header
	m.rows = newRows
	m.colWidths = colWidths

	// Persist sort settings to config file in the background.
	if err := saveConfigKey(keyTUISortKey, m.sortColumn); err != nil {
		clog.Warn().Err(err).Msg("Failed to persist sort key")
	}
	order := ""
	if m.sortColumn != "" {
		order = "desc"
		if m.sortAsc {
			order = "asc"
		}
	}
	if err := saveConfigKey(keyTUISortOrder, order); err != nil {
		clog.Warn().Err(err).Msg("Failed to persist sort order")
	}

	return m, nil
}

// sortableColumns returns visible column names that have sortable cells.
func (m tuiModel) sortableColumns() []string {
	renderer := m.tableRenderer()
	var result []string
	for _, col := range renderer.Columns() {
		if col.Name != "" {
			result = append(result, col.Name)
		}
	}
	return result
}

// defaultSortAsc returns the natural sort direction for a column.
// Time columns default to descending (newest first); strings to ascending (A-Z).
func defaultSortAsc(column string) bool {
	switch column {
	case "updated", "created":
		return false
	default:
		return true
	}
}

// applyTUIFilterDefaults overrides non-explicit CLI filter fields with persisted
// tui.filters.* config values. Returns true if any field was changed.
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

// runTui launches the interactive TUI.
func runTui(
	p *prl,
	rest *api.RESTClient,
	cli *CLI,
	cfg *Config,
	tty bool,
	params *SearchParams,
	s spinner,
) error {
	// Apply persisted TUI filter defaults before the initial fetch.
	if applyTUIFilterDefaults(cli, cfg) {
		var err error
		params, err = buildSearchQuery(cli, cfg)
		if err != nil {
			return err
		}
	}

	resolver := NewAuthorResolver(cfg)

	type fetchResult struct {
		rows      []TableRow
		items     []PRRowModel
		header    string
		colWidths []int
		err       error
	}
	r := withSpinner(tty && !cli.Debug, s, func(func()) fetchResult {
		prs, err := executeSearch(rest, params)
		if err != nil {
			return fetchResult{err: err}
		}
		prs, err = applyFilters(cli, prs)
		if err != nil {
			return fetchResult{err: err}
		}
		// Post-fetch filters: --closed-by, --merged-by
		prs = applyTimelineFilters(rest, cli, prs)

		// Determine if post-enrichment filters require GraphQL data.
		needsEnrich := cli.PRState() == StateReady || cli.CIStatus() != CINone

		if len(prs) > 0 && (!cli.Quick || needsEnrich) {
			if gql, gqlErr := newGraphQLClient(withDebug(cli.Debug)); gqlErr == nil {
				enrichMergeStatus(gql, prs)
			}
		} else if len(prs) > 0 {
			for i := range prs {
				if prs[i].State == valueOpen {
					prs[i].MergeStatus = MergeStatusBlocked
				}
			}
		}

		// Post-enrichment filters.
		if cli.PRState() == StateReady {
			prs = filterReady(prs)
		}
		if ci := cli.CIStatus(); ci != CINone {
			prs = filterByCI(prs, ci)
		}

		orgFilter := singleOrg(cli.Organization.Values)
		items := buildPRRowModels(prs, orgFilter, resolver)

		initWidth := max(0, term.Width(os.Stdout)-tuiListPrefixWidth(len(items)))
		renderer := p.newTableRenderer(cli, tty, initWidth, table.WithShowIndex(false))
		header, rows, colWidths := renderTUITable(p, renderer, items, "", false, initWidth)
		return fetchResult{rows: rows, items: items, header: header, colWidths: colWidths}
	})

	if r.err != nil {
		return r.err
	}

	// Create ActionRunner with GraphQL (always needed for merge/automerge).
	gql, err := newGraphQLClient(withDebug(cli.Debug))
	if err != nil {
		return fmt.Errorf("creating GraphQL client: %w", err)
	}
	actions := NewActionRunner(rest, gql)

	fi := textinput.New()
	fi.Prompt = ""
	filterStyle := lg.NewStyle().Foreground(lg.Color("216"))
	fiStyles := fi.Styles()
	fiStyles.Focused.Text = filterStyle
	fiStyles.Blurred.Text = filterStyle
	fiStyles.Cursor.Color = lg.Color("216")
	fi.SetStyles(fiStyles)

	ci := newConfirmInput()

	// Resolve current user login eagerly so it's cached for View calls.
	login, _ := getCurrentLogin(rest)

	model := tuiModel{
		items:           r.items,
		rows:            r.rows,
		header:          r.header,
		colWidths:       r.colWidths,
		actions:         actions,
		login:           login,
		autoRefresh:     cfg.TUI.AutoRefresh.Enabled,
		lastInteraction: time.Now(),
		spinner:         buildSpinner(cfg.Spinner),
		styles:          newTuiStyles(),
		removed:         make(prKeys),
		selected:        make(prKeys),
		filterInput:     fi,
		confirmInput:    ci,
		p:               p,
		cli:             cli,
		cfg:             cfg,
		tty:             tty,
		resolver:        resolver,
		rest:            rest,
		params:          params,
	}

	// Apply persisted sort settings.
	if cfg.TUI.Sort.Key != "" {
		model.sortColumn = cfg.TUI.Sort.Key
		model.sortAsc = cfg.TUI.Sort.Order == "asc"
		model.header, model.rows, model.colWidths = model.rerender()
	}

	_, err = tea.NewProgram(model).Run()
	if err != nil {
		return fmt.Errorf("interactive TUI: %w", err)
	}
	return nil
}
