package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	"github.com/gechr/primer/filter"
	"github.com/gechr/primer/flash"
	"github.com/gechr/primer/helpbar"
	"github.com/gechr/primer/helpsheet"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/overlay"
	"github.com/gechr/primer/picker"
	"github.com/gechr/primer/prompt"
	"github.com/gechr/primer/render"
	"github.com/gechr/primer/scrollbar"
	"github.com/gechr/primer/scrollwheel"
	"github.com/gechr/primer/table"
	"github.com/gechr/primer/term"
	"github.com/gechr/primer/view"
)

type confirmSubmission struct {
	Input   string
	Options map[string]string
}

func (s confirmSubmission) Option(label string) string {
	if s.Options == nil {
		return ""
	}
	return s.Options[label]
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
		cursor:        styleAccent.Bold(true),
		defaultChoice: styleDefault.Faint(true),
		selectedIndex: styleHighlight.Bold(true),
		statusOK:      styleOK,
		statusErr:     styleDanger,
		statusAction:  styleGreen.Bold(true),
		statusPending: styleWarning.Bold(true),
		helpText:      styleHelp,
		helpKey:       styleAccent.Bold(true),
		separator:     styleAccent.Faint(true),
		diffHead:      styleHeading.Bold(true),
		overlayBox: lg.NewStyle().
			Border(lg.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(tuiConfirmPadY, tuiConfirmPadX),
		confirmYes: lg.NewStyle().
			Background(colorOK).
			Foreground(colorBlack).
			Bold(true).
			Padding(0, 1),
		confirmYesDim: lg.NewStyle().
			Foreground(colorOK).
			Padding(0, 1),
		confirmNo: lg.NewStyle().
			Background(colorDanger).
			Foreground(colorBlack).
			Bold(true).
			Padding(0, 1),
		confirmNoDim: lg.NewStyle().
			Foreground(colorDanger).
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
	return styleDim.Render(text)
}

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

// Verb returns the imperative form of the action (e.g. "Force-merge").
func (a tuiAction) Verb() string {
	switch a {
	case tuiActionApproved:
		return "Approve"
	case tuiActionAutomerged:
		return "Automerge"
	case tuiActionBranchUpdated:
		return "Update branch"
	case tuiActionClosed:
		return "Close"
	case tuiActionCommented:
		return "Comment"
	case tuiActionEnqueued:
		return "Enqueue"
	case tuiActionForceMerged:
		return "Force-merge"
	case tuiActionMarkedDraft:
		return "Mark draft"
	case tuiActionMarkedReady:
		return "Mark ready"
	case tuiActionMerged:
		return "Merge"
	case tuiActionOpened:
		return "Open"
	case tuiActionReopened:
		return "Reopen"
	case tuiActionReviewRequested:
		return "Request review"
	case tuiActionUnsubscribed:
		return "Unsubscribe"
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

// aiReviewMsg is sent when the AI review clone+launch completes.
type aiReviewMsg struct {
	index int
	key   prKey // stable lookup after refresh
	err   error
}

// slackSentMsg is sent when a Slack send completes.
type slackSentMsg struct {
	count int
	err   error
}

// jumpTimeoutMsg fires when the digit-input window expires.
type jumpTimeoutMsg struct{ id int }

// refreshTickMsg fires when it's time to start a background refresh.
type refreshTickMsg struct{ id int }

type detailRefreshTickMsg struct {
	id  int
	key prKey
}

type detailChecksRefreshedMsg struct {
	id     int
	key    prKey
	checks []PRCheck
	err    error
}

// spinnerTickMsg fires to advance the spinner animation frame.
type spinnerTickMsg struct{ id int }

// screenCheckMsg fires periodically to probe the cursor position and detect
// external screen clears (e.g. Cmd+K in iTerm2).
type screenCheckMsg struct{}

// refreshResultMsg carries the result of a background data refresh.
type refreshResultMsg struct {
	rows     []TableRow
	items    []PRRowModel
	err      error
	queryGen int // refresh request generation; stale completions are discarded
}

// tuiModel is the Bubble Tea model for the interactive TUI.
//
//nolint:recvcheck // selection helpers use pointer receivers to mutate maps/fields in-place
type tuiModel struct {
	items             []PRRowModel // canonical data for rerender on resize/refresh
	rows              []TableRow   // current rendered order; row.Item is the action target
	header            string
	colWidths         []int // visible column widths for header click hit-testing
	sortColumn        string
	sortAsc           bool
	cursor            int
	offset            int
	view              tuiView
	diff              string
	diffLines         []string
	diffRenderLines   []string
	diffKey           prKey
	diffView          viewport.Model
	detail            PRDetail
	detailLines       []string
	detailRenderLines []string
	detailKey         prKey
	detailView        viewport.Model
	detailRefreshID   int
	detailLoading     bool
	flash             flash.State
	actions           *ActionRunner
	width             int
	height            int
	styles            tuiStyles
	removed           prKeys
	selected          prKeys

	// Filter options overlay.
	showOptions   bool
	optionsPicker picker.Model

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
	confirmAction     string  // "close", "merge", "diff"
	confirmPrompt     string  // prompt text for modal
	confirmSubject    string  // target description for progress (e.g. "data-team#8", "3 PRs")
	confirmURL        string  // optional URL for hyperlinking the subject in progress
	confirmCmd        tea.Cmd // command to run on confirmation
	confirmCmdFn      func(confirmSubmission) tea.Cmd
	confirmState      prompt.State      // generic prompt interaction state (yes/no, option focus/cursor/values)
	confirmHasInput   bool              // true when modal includes a text input
	confirmInputLabel string            // label above the textarea (default: "Comment")
	confirmInput      textarea.Model    // optional text input (e.g. close comment)
	confirmOptions    []filterOptionDef // optional selectable rows shown in confirm modal
	confirmReviewPR   *PullRequest      // selected PR when the confirm modal is for AI review
	confirmView       viewport.Model    // scrollable viewport for overflowing confirm modals
	scrollDrag        scrollbarDragState

	// Background auto-refresh.
	autoRefresh     bool
	refreshing      bool      // true while a background refresh is in-flight
	lastInteraction time.Time // tracks last user keypress for idle-based refresh decay
	lastRefreshAt   time.Time // last successful list refresh
	// showRefreshStatus is true when the in-flight refresh was triggered by
	// applying options and should show a temporary "Refreshing..." status.
	showRefreshStatus bool
	refreshID         int     // generation counter to discard stale refresh ticks
	spinner           spinner // spinner animation frames
	spinnerTick       int     // current spinner frame index
	queryGen          int     // generation counter to discard stale ticks and results
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
	dst.Owner = cloneCSVFlag(src.Owner)
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
	gql      *api.GraphQLClient
	resolver *AuthorResolver
	rest     *api.RESTClient
	params   *SearchParams
	width    int
}

func newRefreshSnapshot(m tuiModel) refreshSnapshot {
	var gql *api.GraphQLClient
	if m.actions != nil {
		gql = m.actions.gql
	}
	return refreshSnapshot{
		cli:      cloneCLI(m.cli),
		cfg:      m.cfg,
		p:        m.p,
		tty:      m.tty,
		gql:      gql,
		resolver: m.resolver,
		rest:     m.rest,
		params:   cloneSearchParams(m.params),
		width:    m.width,
	}
}

// fetchAndBuild runs the search, filter, enrich, and build pipeline.
// It returns the built row models, or an error.
func (r refreshSnapshot) fetchAndBuild() ([]PRRowModel, error) {
	prs, err := executeSearch(r.rest, r.params)
	if err != nil {
		return nil, err
	}
	prs, err = applyFilters(r.cli, prs)
	if err != nil {
		return nil, err
	}

	// Determine if post-enrichment filters require GraphQL data.
	needsEnrich := r.cli.PRState() == StateReady || r.cli.CIStatus() != CINone
	closedAllowed, err := resolveTimelineLogins(r.rest, r.cli.ClosedBy.Values)
	if err != nil {
		clog.Debug().Err(err).Msg("timeline filters failed")
		closedAllowed = map[string]bool{}
	}
	mergedAllowed, err := resolveTimelineLogins(r.rest, r.cli.MergedBy.Values)
	if err != nil {
		clog.Debug().Err(err).Msg("timeline filters failed")
		mergedAllowed = map[string]bool{}
	}
	needTimeline := len(closedAllowed) > 0 || len(mergedAllowed) > 0
	needMergeStatus := len(prs) > 0 && (!r.cli.Quick || needsEnrich)

	if len(prs) > 0 && (needTimeline || needMergeStatus) {
		if r.gql != nil {
			actors, hydrateErr := hydrateListMetadata(r.gql, prs, listMetadataRequest{
				mergeStatus:    needMergeStatus,
				timelineClosed: len(closedAllowed) > 0,
				timelineMerged: len(mergedAllowed) > 0,
			})
			if hydrateErr != nil {
				clog.Debug().Err(hydrateErr).Msg("list metadata hydration failed")
			} else if needTimeline {
				prs = filterByTimelineActorsLoaded(prs, closedAllowed, mergedAllowed, actors)
			}
		}
	}
	if len(prs) > 0 && !needMergeStatus {
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

	ownerFilter := singleOwner(r.cli.Owner.Values)
	return buildPRRowModels(prs, ownerFilter, r.resolver), nil
}

func (r refreshSnapshot) run() refreshResultMsg {
	items, err := r.fetchAndBuild()
	if err != nil {
		return refreshResultMsg{err: err}
	}
	termWidth := max(0, r.width-tuiListPrefixWidth(len(items)))
	renderer := r.p.newTableRenderer(r.cli, r.tty, termWidth, table.WithShowIndex(false))
	_, rows, _ := renderTUITable(renderer, items, "", false, termWidth)
	return refreshResultMsg{rows: rows, items: items}
}

func choiceIndex(choices []filterChoice, value string) int {
	for i, c := range choices {
		if c.value == value {
			return i
		}
	}
	return 0
}

func (m tuiModel) hasConfirmOptions() bool { return len(m.confirmOptions) > 0 }

func (m tuiModel) selectedConfirmOptionValue(row int) string {
	if row < 0 || row >= len(m.confirmOptions) {
		return ""
	}
	choices := m.confirmOptions[row].choices
	if len(choices) == 0 {
		return ""
	}
	idx := 0
	if row < len(m.confirmState.OptValues) {
		idx = min(max(m.confirmState.OptValues[row], 0), len(choices)-1)
	}
	return choices[idx].value
}

func (m tuiModel) buildConfirmSubmission() confirmSubmission {
	submission := confirmSubmission{
		Input: strings.TrimSpace(m.confirmInput.Value()),
	}
	if !m.hasConfirmOptions() {
		return submission
	}

	submission.Options = make(map[string]string, len(m.confirmOptions))
	for i, def := range m.confirmOptions {
		submission.Options[def.label] = m.selectedConfirmOptionValue(i)
	}
	return submission
}

func (m tuiModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.cfg != nil && m.cfg.TUI.ScreenRepair {
		cmds = append(cmds, m.scheduleScreenCheck())
	}
	if m.autoRefresh {
		cmds = append(
			cmds,
			scheduleRefresh(len(m.items), m.refreshID, time.Since(m.lastInteraction)),
		)
	}
	return tea.Batch(cmds...)
}

// refreshDelay returns the next list refresh delay using the adaptive watch-mode
// cadence plus idle-based slowdown.
func refreshDelay(n int, idle time.Duration) time.Duration {
	d := watchInterval(n)
	if idle > 0 && idle < watchIdleDecay {
		// Linearly blend from the base interval toward watchIdleMax.
		frac := float64(idle) / float64(watchIdleDecay)
		d = time.Duration(float64(d)*(1-frac) + float64(watchIdleMax)*frac)
	} else if idle >= watchIdleDecay {
		d = watchIdleMax
	}
	return d
}

// scheduleRefresh returns a tea.Cmd that fires a refreshTickMsg after a delay
// scaled by the number of results (reusing watch-mode intervals) and further
// slowed by user inactivity.
func scheduleRefresh(n, id int, idle time.Duration) tea.Cmd {
	d := refreshDelay(n, idle)
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshTickMsg{id: id} })
}

func scheduleTick(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

func (m tuiModel) scheduleSpinnerTick() tea.Cmd {
	return scheduleTick(m.spinner.interval, spinnerTickMsg{id: m.queryGen})
}

func (m tuiModel) scheduleScreenCheck() tea.Cmd {
	return scheduleTick(tuiScreenCheckInt, screenCheckMsg{})
}

// applyRepaintMarker toggles the WindowTitle between two values so that
// the renderer's viewEquals check sees a changed View on every heartbeat tick.
// This forces a periodic full repaint, recovering from external screen clears
// (e.g. Cmd+K in iTerm2) that the differential renderer cannot detect.
func (m tuiModel) applyRepaintMarker(v *tea.View) {
	if m.cfg == nil || !m.cfg.TUI.ScreenRepair {
		return
	}
	if m.repaintTick {
		v.WindowTitle = " "
	}
}

// touchInteraction records that the user interacted with the TUI,
// resetting the idle decay for auto-refresh scheduling.
func (m *tuiModel) touchInteraction() { m.lastInteraction = time.Now() }

func (m tuiModel) detailHasPendingChecks() bool {
	for _, check := range m.detail.Checks {
		if check.Status != ciStatusCompleted {
			return true
		}
	}
	return false
}

func (m tuiModel) scheduleDetailRefresh() tea.Cmd {
	if m.view != tuiViewDetail || m.diffLoading || m.detailKey == "" ||
		!m.detailHasPendingChecks() {
		return nil
	}
	id := m.detailRefreshID
	key := m.detailKey
	return tea.Tick(detailCheckInterval, func(time.Time) tea.Msg {
		return detailRefreshTickMsg{id: id, key: key}
	})
}

// refetchDetail re-fetches the currently displayed detail view (e.g. after
// an action like update-branch changes PR state).
func (m tuiModel) refetchDetail() tea.Cmd {
	pr := m.prForKey(m.detailKey)
	if pr == nil {
		return nil
	}
	actions := m.actions
	key := m.detailKey
	idx := m.resolveIndex(key, -1)
	prCopy := *pr
	return func() tea.Msg {
		owner, repo := prOwnerRepo(prCopy)
		detail, err := actions.fetchPRDetail(owner, repo, prCopy.Number, prCopy.NodeID)
		return detailFetchedMsg{index: idx, key: key, detail: detail, err: err}
	}
}

func (m tuiModel) isStaleDetailRefresh(id int, key prKey) bool {
	return id != m.detailRefreshID || key != m.detailKey || m.view != tuiViewDetail || m.diffLoading
}

func (m *tuiModel) stopDetailRefresh(clearKey bool) {
	m.detailRefreshID++
	if clearKey {
		m.detailKey = ""
	}
}

func (m *tuiModel) clearDiffState() {
	m.diffQueue = nil
	m.diffHistory = nil
	m.diffQueueTotal = 0
	m.diffAdvanced = false
	m.diffLoading = false
	m.diffKey = ""
}

func (m *tuiModel) startRefresh(showStatus bool) tea.Cmd {
	m.refreshing = true
	m.showRefreshStatus = showStatus
	m.spinnerTick = 0
	m.queryGen++
	if showStatus {
		m.flash.Msg = m.styles.statusPending.Render("Applying" + valueEllipsis)
		m.flash.Err = false
	}
	snapshot := newRefreshSnapshot(*m)
	queryGen := m.queryGen
	return tea.Batch(
		m.scheduleSpinnerTick(),
		func() tea.Msg {
			result := snapshot.run()
			result.queryGen = queryGen
			return result
		},
	)
}

func (m *tuiModel) invalidateRefresh() { m.refreshID++ }

// rescheduleRefresh invalidates older refresh ticks and schedules a new one.
func (m *tuiModel) rescheduleRefresh() tea.Cmd {
	if m.autoRefresh {
		m.invalidateRefresh()
		return scheduleRefresh(len(m.items), m.refreshID, time.Since(m.lastInteraction))
	}
	return nil
}

func (m *tuiModel) refreshOrReschedule() tea.Cmd {
	if !m.autoRefresh {
		return nil
	}
	if m.refreshing {
		return nil
	}
	if time.Since(m.lastRefreshAt) >= refreshDelay(len(m.items), time.Since(m.lastInteraction)) {
		m.invalidateRefresh()
		return m.startRefresh(false)
	}
	return m.rescheduleRefresh()
}

func (m *tuiModel) exitDetailView() tea.Cmd {
	m.stopDetailRefresh(true)
	m.view = tuiViewList
	return m.refreshOrReschedule()
}

func (m *tuiModel) exitDiffView() tea.Cmd {
	m.clearDiffState()
	m.view = tuiViewList
	return m.refreshOrReschedule()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok &&
		(keyMsg.String() == key.CtrlC || keyMsg.String() == key.CtrlD) &&
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
		m.applyWindowSize(msg.Width, msg.Height)
		return m, nil

	case tea.MouseClickMsg:
		m.touchInteraction()
		if msg.Button == tea.MouseLeft && m.handleScrollbarPress(msg.Mouse()) {
			return m, nil
		}
		if m.view == tuiViewList && msg.Button == tea.MouseLeft && msg.Y == 0 {
			col := m.headerColumnAt(msg.X)
			if col != "" {
				return m.toggleSort(col)
			}
		}
		if m.view == tuiViewList && msg.Button == tea.MouseLeft {
			if idx, ok := m.rowIndexAt(msg.Y); ok {
				m.cursor = idx
			}
		}
		return m, nil

	case tea.MouseMotionMsg:
		m.touchInteraction()
		if m.handleScrollbarMotion(msg.Mouse()) {
			return m, nil
		}
		return m, nil

	case tea.MouseReleaseMsg:
		m.touchInteraction()
		m.scrollDrag.Release()
		return m, nil

	case scrollwheel.Msg[wheelTarget]:
		m.touchInteraction()
		m.applyWheelScroll(msg.Target, msg.Delta)
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
				return m, tea.Batch(flashCmd, m.exitDiffView())
			}
			return m, flashCmd
		}
		if msg.action.removes() {
			m.removed[msg.key] = true
			m.resyncCursorAndOffset()
		}
		flashCmd := flashResult(&m, msg.action.String(), pr.Ref(), pr.URL, false)
		if hint != nil {
			cmd := styleAccent.Bold(true).Render(hint.Hint)
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
			return m, tea.Batch(flashCmd, m.exitDiffView())
		}
		if m.view == tuiViewDetail && m.detailKey != "" {
			return m, tea.Batch(flashCmd, m.refetchDetail())
		}
		return m, flashCmd

	case batchActionMsg:
		if msg.action.removes() {
			for _, key := range msg.keys {
				m.removed[key] = true
				delete(m.selected, key)
			}
			m.resyncCursorAndOffset()
		}
		m.selected = make(prKeys)
		status := fmt.Sprintf("%d/%d", msg.count-msg.failed, msg.count)
		if msg.failed > 0 {
			m.confirmAction = tuiActionInfo
			m.confirmState.Yes = true
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

	case flash.ClearMsg:
		m.flash.Clear(msg)
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
			if m.view == tuiViewDetail {
				return m, tea.Batch(flashCmd, m.scheduleDetailRefresh())
			}
			return m, flashCmd
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil // PR no longer in list
		}
		if m.view == tuiViewDetail {
			m.stopDetailRefresh(true)
		}
		m.refreshTerminalSize()
		m.diffKey = msg.key
		pr := m.rows[idx].Item.PR
		m.diff = highlightDiff(msg.diff, pr.URL, msg.headSHA)
		m.diffLines = layout.WrapLines(m.diff, m.width-tuiScrollbarWidth)
		m.syncDiffView()
		m.diffView.GotoTop()
		m.view = tuiViewDiff
		m.flash.Msg = ""
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
		m.refreshTerminalSize()
		m.detailKey = msg.key
		m.detail = msg.detail
		m.detailLines = m.renderDetailContent()
		m.syncDetailView()
		m.detailView.GotoTop()
		m.view = tuiViewDetail
		m.flash.Msg = ""
		return m, m.scheduleDetailRefresh()

	case detailRefreshTickMsg:
		if m.isStaleDetailRefresh(msg.id, msg.key) {
			return m, nil
		}
		pr := m.prForKey(msg.key)
		if pr == nil || pr.NodeID == "" {
			return m, nil
		}
		actions := m.actions
		key := msg.key
		id := msg.id
		nodeID := pr.NodeID
		return m, func() tea.Msg {
			checks, err := actions.fetchChecksGraphQL(nodeID)
			return detailChecksRefreshedMsg{id: id, key: key, checks: checks, err: err}
		}

	case detailChecksRefreshedMsg:
		if m.isStaleDetailRefresh(msg.id, msg.key) {
			return m, nil
		}
		if msg.err != nil {
			clog.Debug().
				Err(msg.err).
				Str("detail_key", string(msg.key)).
				Msg("Detail check refresh failed")
			return m, m.scheduleDetailRefresh()
		}
		m.detail.Checks = msg.checks
		m.detailLines = m.renderDetailContent()
		m.syncDetailView()
		return m, m.scheduleDetailRefresh()

	case aiReviewMsg:
		if msg.err != nil {
			return m, flashResult(&m, "AI review failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		return m, flashResult(
			&m,
			"AI review launched",
			pr.Ref(),
			pr.URL,
			false,
		)

	case slackSentMsg:
		if msg.err != nil {
			return m, flashResult(&m, "Slack failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		status := fmt.Sprintf("%d PRs", msg.count)
		if msg.count == 1 {
			status = "1 PR"
		}
		return m, flashResult(&m, "Slacked", status, "", false)

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
		if m.cfg == nil || !m.cfg.TUI.ScreenRepair {
			return m, nil
		}
		return m, tea.Batch(tea.Raw(ansiDECXCPR), m.scheduleScreenCheck())

	case tea.CursorPositionMsg:
		if m.cfg == nil || !m.cfg.TUI.ScreenRepair {
			return m, nil
		}
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
		return m, m.startRefresh(false)

	case spinnerTickMsg:
		if !m.refreshing || msg.id != m.queryGen {
			return m, nil
		}
		m.spinnerTick++
		return m, m.scheduleSpinnerTick()

	case refreshResultMsg:
		if msg.queryGen != m.queryGen {
			return m, nil
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
			m.flash.Msg = ""
		}
		m.lastRefreshAt = time.Now()
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
		case key.Enter:
			m.filterInput.Blur()
			return m, nil
		case key.Esc, key.CtrlC, key.CtrlD:
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.resyncCursorAndOffset()
			return m, nil
		case key.Up, key.Down:
			dir := 1
			if msg.String() == key.Up {
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
		case key.Esc, "q":
			return m, tea.Quit
		default:
			return m, nil
		}
	}

	// Try action keybinds first.
	if result, cmd, handled := m.updateListActions(msg); handled {
		return result, cmd
	}

	switch msg.String() {
	case key.Esc:
		if m.filterInput.Value() != "" {
			m.filterInput.SetValue("")
			m.resyncCursorAndOffset()
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

	case key.Enter:
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		m.refreshTerminalSize()
		idx := m.cursor
		actions := m.actions
		prCopy := *pr
		m.flash.Msg = m.styles.statusPending.Render("Fetching") + " " +
			styleRef.Render(prCopy.Ref()) + valueEllipsis
		m.flash.Err = false
		m.detailLoading = true
		key := makePRKey(prCopy)
		fetchCmd := func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			detail, err := actions.fetchPRDetail(owner, repo, prCopy.Number, prCopy.NodeID)
			return detailFetchedMsg{index: idx, key: key, detail: detail, err: err}
		}
		return m, tea.Batch(requestWindowSizeCmd(), fetchCmd)

	case tuiKeybindVimDown, key.Down:
		if next, ok := m.nextVisible(1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case tuiKeybindVimUp, key.Up:
		if next, ok := m.nextVisible(-1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case key.CtrlF:
		viewport := m.listViewport()
		for range viewport {
			if next, ok := m.nextVisible(1); ok {
				m.cursor = next
			}
		}
		m.offset = m.scrolledOffset()
		return m, nil

	case key.CtrlB:
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

	case key.Space:
		m.toggleCurrentSelection()
		return m, nil

	case key.ShiftDown:
		m.extendSelectionAndMove(1)
		return m, nil

	case key.ShiftUp:
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

	case tuiKeybindOptions:
		m.showOptions = true
		m.optionsPicker = m.newFilterPicker()
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
		m.invalidateRefresh()
		return m, nil

	case key.Tab:
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
		v.Content = overlay.Place(
			v.Content,
			m.renderHelpOverlay(),
			m.width,
			m.height,
			overlay.Center,
		)
	case m.showOptions:
		v.Content = overlay.Place(
			v.Content,
			m.renderOptionsOverlay(),
			m.width,
			m.height,
			overlay.Center,
		)
	case m.confirmAction != "":
		v.Content = overlay.Place(
			v.Content,
			m.renderConfirmModal(),
			m.width,
			m.height,
			overlay.Center,
		)
	case len(m.visibleIndices()) == 0 && !m.dismissedEmpty && m.flash.Msg == "":
		v.Content = overlay.Place(
			v.Content,
			m.renderEmptyOverlay(),
			m.width,
			m.height,
			overlay.Center,
		)
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
		b.WriteString(prefix + m.header + nl)
	}

	// Determine visible slice based on offset.
	end := min(m.offset+viewport, len(visible))
	start := min(m.offset, len(visible))

	filterVal := m.filterInput.Value()

	for pos, idx := range visible[start:end] {
		index := m.renderTuiIndex(start+pos+1, m.selected[m.rowKeyAt(idx)])
		display := index + tuiNonCursorPrefix + m.rows[idx].Display
		if idx != m.cursor {
			b.WriteString(tuiNonCursorPrefix + display + nl)
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
			b.WriteString(layout.PreserveBackgroundWidth(line, bg, m.width))
		} else {
			b.WriteString(line)
		}
		b.WriteString(nl)
	}

	// Pad to fill viewport.
	rendered := end - start
	for range viewport - rendered {
		b.WriteString(nl)
	}

	// Filter bar.
	if m.filterInput.Focused() || filterVal != "" {
		b.WriteString(styleHeading.Bold(true).Render("/") + m.filterInput.View() + nl)
	}

	// Separator line, with active tags embedded inline when present.
	// When filter is focused, place a ┬ junction to connect with the │ in the help line.
	var help string
	if m.filterInput.Focused() {
		b.WriteString(m.renderListSeparator(m.filterHelpPipeCol()))
		b.WriteString(nl)
		help = m.renderFilterHelp()
	} else {
		b.WriteString(m.renderListSeparator())
		b.WriteString(nl)
		help = m.renderHelp(m.listHelpPairs())
	}
	if m.flash.Msg != "" {
		b.WriteString(m.appendStatus(help))
	} else {
		status := ""
		total := len(visible)
		if total > viewport {
			status = m.styles.statusOK.Render(scrollbar.Position(start, end, total))
		}
		b.WriteString(m.appendRightStatus(help, status))
	}

	v := tea.NewView(layout.ExpandTabs(b.String()))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	m.applyRepaintMarker(&v)
	return v
}

func (m tuiModel) viewDiff() tea.View {
	var header string
	headerStyle := styleLabel.Bold(true)
	if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
		pr := m.rows[idx].Item.PR
		var headerLine string
		if m.diffQueueTotal > 0 {
			pos := m.diffQueueTotal - len(m.diffQueue)
			headerLine = headerStyle.Render(fmt.Sprintf("[%d/%d]", pos, m.diffQueueTotal)) +
				" "
		}
		ref := fmt.Sprintf("%s#%d", pr.Repository.NameWithOwner, pr.Number)
		headerLine += xansi.SetHyperlink(pr.URL) +
			headerStyle.Render(ref) + styleText.Render(" » ") +
			styleTitle.Render(normalizeTUIDisplayText(pr.Title)) +
			xansi.ResetHyperlink()
		if m.width > 0 && lg.Width(headerLine) > m.width {
			headerLine = xansi.Truncate(headerLine, m.width-1, valueEllipsis)
		}
		header = headerLine
	}
	return m.renderFullScreenView(
		header,
		m.diffView,
		m.diffRenderLines,
		m.renderHelp(m.diffHelpPairs()),
	)
}

// renderListSeparator renders the separator line. If junctionCol >= 0, a ┬ is
// placed at that visual column to connect with a │ in the line below.
func (m tuiModel) renderListSeparator(junctionCol ...int) string {
	if m.width <= 0 {
		return ""
	}
	col := -1
	if len(junctionCol) > 0 {
		col = junctionCol[0]
	}
	tags := m.activeFilterTags()
	if len(tags) > 0 {
		return m.renderTagSeparator(tags, col)
	}
	return m.styles.separator.Render(layout.Separator(m.width, col))
}

func (m tuiModel) renderTagSeparator(tags []string, col int) string {
	filterTagStyle := styleFilterTag.Faint(true)
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
	suffixText := strings.Repeat("─", 2) //nolint:mnd // fixed suffix width
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
	return m.styles.separator.Render(layout.Separator(pad, col)) +
		indicatorPrefix +
		indicator +
		suffix
}

func (m tuiModel) viewDetail() tea.View {
	return m.renderFullScreenView(
		"",
		m.detailView,
		m.detailRenderLines,
		m.renderHelp(m.detailHelpPairs()),
	)
}

func (m tuiModel) renderFullScreenView(
	header string,
	vp viewport.Model,
	renderLines []string,
	help string,
) tea.View {
	totalLines := vp.TotalLineCount()
	vpHeight := vp.Height()
	status := ""
	if m.flash.Msg != "" {
		status = m.renderFlashStatus()
	} else if totalLines > vpHeight {
		offset := vp.YOffset()
		end := min(offset+vpHeight, totalLines)
		pct := int(math.Round(vp.ScrollPercent() * 100)) //nolint:mnd // percent
		status = m.styles.statusOK.Render(
			fmt.Sprintf("%d-%d/%d (%d%%)", offset+1, end, totalLines, pct),
		)
	}

	v := tea.NewView(view.RenderFrame(view.FrameModel{
		Header: header,
		Footer: m.footerComponents(help, status),
		Lines:  renderLines,
		View:   vp,
		Width:  m.width,
		Height: m.height,
		Styles: view.FrameStyles{
			Separator: m.styles.separator,
			Scrollbar: scrollbar.Styles{
				Thumb: lg.NewStyle().Foreground(colorAccent),
				Track: lg.NewStyle().Foreground(colorAccent).Faint(true),
			},
		},
	}))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if m.confirmAction != "" {
		v.Content = overlay.Place(
			v.Content,
			m.renderConfirmModal(),
			m.width,
			m.height,
			overlay.Center,
		)
	}
	m.applyRepaintMarker(&v)
	return v
}

func (m tuiModel) renderViewportContent(
	lines []string,
	vp viewport.Model,
	withScrollbar bool,
) string {
	return view.RenderContent(lines, vp, withScrollbar, scrollbar.Styles{
		Thumb: lg.NewStyle().Foreground(colorAccent),
		Track: lg.NewStyle().Foreground(colorAccent).Faint(true),
	})
}

func requestWindowSizeCmd() tea.Cmd {
	return tea.RequestWindowSize
}

func (m *tuiModel) refreshTerminalSize() {
	width, height := term.Size(os.Stdout)
	if width <= 0 || height <= 0 {
		return
	}
	if width == m.width && height == m.height {
		return
	}
	m.applyWindowSize(width, height)
}

func (m *tuiModel) applyWindowSize(width, height int) {
	m.width = width
	m.height = height
	m.header, m.rows, m.colWidths = m.rerender()
	if m.view == tuiViewDiff {
		m.diffLines = layout.WrapLines(m.diff, m.width-tuiScrollbarWidth)
		m.syncDiffView()
	}
	if m.view == tuiViewDetail && len(m.detailLines) > 0 {
		m.detailLines = m.renderDetailContent()
		m.syncDetailView()
	}
	if m.confirmAction != "" {
		m.confirmInput.SetWidth(m.confirmInputWidth())
		m.confirmInput.MaxHeight = m.confirmTextareaMaxHeight()
	}
}

// renderDetailContent builds the detail view lines from the PR and its detail data.
func (m tuiModel) renderDetailContent() []string {
	idx := m.resolveIndex(m.detailKey, -1)
	if idx < 0 {
		return []string{
			styleDim.Render("Pull request no longer available."),
		}
	}
	pr := m.rows[idx].Item.PR

	headerStyle := styleHelp.Bold(true)
	labelStyle := styleOK.Bold(true)
	styleText := styleText

	author := m.resolver.Resolve(pr.Author.Login)
	var lines []string
	lines = append(lines, headerStyle.Render("Overview"))
	lines = append(lines, "")
	authorLink := "https://github.com/" + pr.Author.Login
	authorDisplay := "@" + pr.Author.Login
	if author != pr.Author.Login {
		authorDisplay += " (" + author + ")"
	}
	styledAuthor := xansi.SetHyperlink(authorLink) +
		styleText.Render(authorDisplay) + xansi.ResetHyperlink()
	lines = append(
		lines,
		detailIndent+labelStyle.Render(
			"  Title: ",
		)+xansi.SetHyperlink(pr.URL)+styleText.Render(
			normalizeTUIDisplayText(pr.Title),
		)+xansi.ResetHyperlink(),
	)
	lines = append(lines, detailIndent+labelStyle.Render(" Author: ")+styledAuthor)
	styledURL := xansi.SetHyperlink(pr.URL) + styleText.Render(pr.URL) + xansi.ResetHyperlink()
	lines = append(lines, detailIndent+labelStyle.Render("    URL: ")+styledURL)
	if len(m.detail.Reviews) > 0 {
		var parts []string
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
			if isAuthorBot(r.User) && icon == iconCommented {
				icon = iconCopilot
			}
			name := m.resolver.Resolve(r.User)
			var link string
			if isAuthorBot(r.User) {
				link = "https://github.com/apps/" + strings.TrimSuffix(r.User, "[bot]")
			} else {
				link = "https://github.com/" + r.User
			}
			styled := xansi.SetHyperlink(link) +
				styleText.Render(name) + xansi.ResetHyperlink()
			parts = append(parts, icon+" "+styled)
		}
		lines = append(lines, detailIndent+labelStyle.Render("Reviews: ")+
			strings.Join(parts, " · "))
	}
	lines = append(lines, detailIndent+labelStyle.Render(" Status: ")+m.renderDetailStatus(pr))
	if m.detail.MergeableState == valueBehind {
		lines = append(lines, detailIndent+labelStyle.Render(" State: ")+
			styleWarning.Render("Branch out-of-date"))
	}
	lines = append(lines, "")

	// Checks.
	if len(m.detail.Checks) > 0 {
		lines = append(lines, headerStyle.Render("Checks"))
		lines = append(lines, "")
		for _, c := range m.detail.Checks {
			var icon string
			switch {
			case c.Status != ciStatusCompleted:
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
				line += " " + styleCheckDur.Render(
					fmt.Sprintf("[%s]", c.Duration.Round(time.Second)),
				)
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
		lines = append(lines, styleDim.Render("No description provided."))
	}

	// Changed files.
	if len(m.detail.Files) > 0 {
		lines = append(lines, "")
		lines = append(lines, headerStyle.Render("Files Changed"))
		lines = append(lines, "")
		for _, f := range m.detail.Files {
			var prefix string
			switch f.Status {
			case "added":
				prefix = styleGreen.Bold(true).Render("A")
			case "removed":
				prefix = styleRed.Bold(true).Render("D")
			case "renamed":
				prefix = styleMagenta.Bold(true).Render("R")
			default:
				prefix = styleYellow.Bold(true).Render("M")
			}
			stat := styleAdd.Render(fmt.Sprintf("+%d", f.Additions)) +
				" " + styleDelete.Render(fmt.Sprintf("-%d", f.Deletions))
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
	switch resolvePRStatus(pr) {
	case resolvedDraft, resolvedDraftCIFail:
		return styleDraftLbl.Render("Draft")
	case resolvedMerged:
		return styleMerged.Render("Merged")
	case resolvedClosed:
		return styleClosed.Render("Closed")
	case resolvedReady:
		return styleGreen.Render("Ready to merge")
	case resolvedCIPending:
		return styleWarning.Render("CI pending")
	case resolvedCIFailed:
		return styleClosed.Render("CI failed")
	case resolvedBlocked:
		return styleWarning.Render("Needs review")
	case resolvedUnknown:
		return styleDim.Render("Unknown")
	}
	return ""
}

const (
	detailIndent     = "  "
	defaultTermWidth = 80

	iconApproved  = "✅"
	iconCopilot   = "🤖"
	iconRejected  = "❌"
	iconDismissed = "🥀"
	iconCommented = "💬"
)

func (m tuiModel) renderMarkdown(body string) []string {
	width := m.width
	if width <= 0 {
		width = defaultTermWidth
	}
	rendered := render.Markdown(body, width, "dracula")
	if rendered == "" {
		return m.plainBodyLines(body)
	}
	var lines []string
	for line := range strings.SplitSeq(rendered, nl) {
		lines = append(lines, line)
	}
	return lines
}

func (m tuiModel) plainBodyLines(body string) []string {
	var lines []string
	for line := range strings.SplitSeq(body, nl) {
		lines = append(lines, detailIndent+line)
	}
	return lines
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

	term := filter.Parse(f)
	for i := range m.rows {
		if m.removed[m.rowKeyAt(i)] {
			continue
		}
		text := rowFilterText(m.rows[i])
		if term.Prefix || term.Suffix {
			text = m.rows[i].Item.PR.Title
		}
		if term.Match(text) {
			indices = append(indices, i)
		}
	}
	return indices
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

func (m *tuiModel) resyncCursorAndOffset() {
	m.cursor = m.adjustedCursor()
	m.offset = m.scrolledOffset()
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
	renderer *table.Renderer[PRRowModel],
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
	sampleWidths := make([]int, len(cols))
	for i, col := range cols {
		sampleWidths[i] = estimateHeaderWidth(col.Name, compact)
	}
	return renderer.RenderHeaderOnly(sampleWidths)
}

func renderTUITable(
	renderer *table.Renderer[PRRowModel],
	items []PRRowModel,
	sortColumn string,
	sortAsc bool,
	termWidth int,
) (string, []TableRow, []int) {
	if len(items) == 0 {
		header, colWidths := renderEstimatedHeader(renderer, termWidth)
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
	return renderTUITable(renderer, m.items, m.sortColumn, m.sortAsc, termWidth)
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

func (m tuiModel) listHelpPairs() []key.Hint {
	pairs := []key.Hint{
		{Key: key.Enter, Desc: tuiHelpShow},
		{Key: key.Space, Desc: tuiHelpSelect},
		{Key: tuiKeybindFilter, Desc: tuiHelpFilter},
	}
	ctx, ok := m.actionContextForCursor()
	actionable := ok && ctx.actionable
	if actionable && !ctx.ownPR && !ctx.draft {
		pairs = append(pairs, key.Hint{Key: tuiKeybindApprove, Desc: tuiHelpApprove})
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindDiff, Desc: tuiHelpDiff})
	if actionable && !ctx.draft {
		pairs = append(pairs, key.Hint{Key: tuiKeybindMerge, Desc: mergeHelpForPR(&ctx.pr)})
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindComment, Desc: tuiHelpComment})
	if ctx.state != valueMerged {
		pairs = append(pairs, key.Hint{Key: tuiKeybindClose, Desc: closeReopenHelp(ctx.state)})
	}
	pairs = append(
		pairs,
		key.Hint{Key: tuiKeybindOpen, Desc: tuiHelpOpen},
		key.Hint{Key: tuiKeybindCopyURL, Desc: tuiHelpCopy},
	)
	if actionable && !ctx.draft && hasAIReviewLauncher() {
		pairs = append(pairs, key.Hint{Key: tuiKeybindReview, Desc: tuiHelpReview})
	}
	if m.autoRefresh {
		pairs = append(
			pairs,
			key.Hint{Key: tuiKeybindToggleRefresh, Desc: "refresh " + styledOn},
		)
	} else {
		pairs = append(
			pairs,
			key.Hint{Key: tuiKeybindToggleRefresh, Desc: "refresh " + styledOff},
		)
	}
	pairs = append(pairs,
		key.Hint{Key: tuiKeybindOptions, Desc: tuiHelpOptions},
		key.Hint{Key: tuiKeybindHelp, Desc: tuiHelpHelp},
		key.Hint{Key: tuiKeybindQuit, Desc: tuiHelpQuit},
	)
	return pairs
}

func (m tuiModel) renderFilterSyntaxHints() string {
	return key.Renderer{
		Styles: key.Styles{
			Key:  styleHeading.Bold(true),
			Text: styleFilter,
		},
		Gap: "  ",
	}.Render([]key.Hint{
		{Key: "^", Desc: "start"},
		{Key: "$", Desc: "end"},
		{Key: "!", Desc: "negate"},
	})
}

const filterHelpGap = "  "

// filterHelpPipeCol returns the visual column of the │ separator in the filter help line.
func (m tuiModel) filterHelpPipeCol() int {
	return lg.Width(m.renderFilterSyntaxHints()) + len(filterHelpGap)
}

func (m tuiModel) renderFilterHelp() string {
	pairs := []key.Hint{
		{Key: key.ArrowsUpDown, Desc: "prev/next"},
		{Key: key.Enter, Desc: "apply"},
		{Key: key.Esc, Desc: "exit"},
	}
	base := key.Renderer{
		Styles: key.Styles{
			Key:  m.styles.helpKey,
			Text: m.styles.helpText,
		},
		Gap:    helpGap,
		Prefix: new(""),
		Inline: true,
	}.Render(pairs)
	sep := m.styles.separator.Render("│")
	return m.renderFilterSyntaxHints() + filterHelpGap + sep + filterHelpGap + base
}

func (m tuiModel) diffHelpPairs() []key.Hint {
	pairs := []key.Hint{
		{Key: key.ArrowsUpDown, Desc: tuiHelpScroll},
	}
	ctx, _ := m.actionContextForKey(m.diffKey)
	actionable := ctx.actionable
	if actionable && !ctx.draft {
		pairs = append(pairs, key.Hint{Key: tuiKeybindMerge, Desc: mergeHelpForPR(&ctx.pr)})
	}
	if actionable {
		pairs = append(
			pairs,
			key.Hint{Key: tuiKeybindDraftToggle, Desc: draftToggleHelp(&ctx.pr)},
		)
	}
	if actionable && !ctx.ownPR && !ctx.draft {
		pairs = append(
			pairs,
			key.Hint{Key: tuiKeybindApprove, Desc: tuiHelpApprove},
		)
		pairs = append(pairs, key.Hint{Key: tuiKeybindApproveMerge, Desc: tuiHelpApproveMerge})
	}
	if actionable && !ctx.ownPR {
		pairs = append(pairs, key.Hint{Key: tuiKeybindUnassign, Desc: tuiHelpUnsubscribe})
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindComment, Desc: tuiHelpComment})
	if ctx.state != valueMerged {
		pairs = append(pairs, key.Hint{Key: tuiKeybindClose, Desc: closeReopenHelp(ctx.state)})
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindOpen, Desc: tuiHelpOpen})
	pairs = append(pairs, key.Hint{Key: tuiKeybindCopyURL, Desc: tuiHelpCopy})
	if actionable {
		pairs = append(pairs, key.Hint{Key: tuiKeybindSlack, Desc: tuiHelpSlack})
	}

	if actionable && !ctx.draft && hasAIReviewLauncher() {
		pairs = append(pairs, key.Hint{Key: tuiKeybindReview, Desc: tuiHelpReview})
	}
	if actionable && !ctx.draft {
		pairs = append(pairs, key.Hint{Key: tuiKeybindCopilotReview, Desc: tuiHelpCopilot})
	}
	if m.diffQueueTotal > 0 {
		if len(m.diffHistory) > 0 {
			pairs = append(pairs, key.Hint{Key: tuiKeybindPrev, Desc: tuiHelpPrev})
		}
		if len(m.diffQueue) > 0 {
			pairs = append(pairs, key.Hint{Key: tuiKeybindNext, Desc: tuiHelpNext})
		}
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindDiff, Desc: tuiHelpDismiss})
	return pairs
}

func (m tuiModel) detailHelpPairs() []key.Hint {
	pairs := []key.Hint{
		{Key: key.ArrowsUpDown, Desc: tuiHelpScroll},
		{Key: tuiKeybindDiff, Desc: tuiHelpDiff},
	}
	ctx, _ := m.actionContextForKey(m.detailKey)
	actionable := ctx.actionable
	if actionable && !ctx.draft {
		pairs = append(pairs, key.Hint{Key: tuiKeybindMerge, Desc: mergeHelpForPR(&ctx.pr)})
	}
	if actionable {
		pairs = append(
			pairs,
			key.Hint{Key: tuiKeybindDraftToggle, Desc: draftToggleHelp(&ctx.pr)},
		)
	}
	if actionable && !ctx.draft && !ctx.ownPR {
		pairs = append(
			pairs,
			key.Hint{Key: tuiKeybindApprove, Desc: tuiHelpApprove},
		)
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindComment, Desc: tuiHelpComment})
	pairs = append(pairs, key.Hint{Key: tuiKeybindOpen, Desc: tuiHelpOpen})
	pairs = append(pairs, key.Hint{Key: tuiKeybindCopyURL, Desc: tuiHelpCopy})
	if actionable {
		pairs = append(pairs, key.Hint{Key: tuiKeybindSlack, Desc: tuiHelpSlack})
	}
	if actionable && m.detail.MergeableState == valueBehind {
		pairs = append(pairs, key.Hint{Key: tuiKeybindUpdateBranch, Desc: tuiHelpUpdateBranch})
	}
	if actionable && !ctx.draft && hasAIReviewLauncher() {
		pairs = append(pairs, key.Hint{Key: tuiKeybindReview, Desc: tuiHelpReview})
	}
	if actionable && !ctx.draft {
		pairs = append(pairs, key.Hint{Key: tuiKeybindCopilotReview, Desc: tuiHelpCopilot})
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindQuit, Desc: tuiHelpDismiss})
	return pairs
}

func (m tuiModel) renderHelpOverlay() string {
	pairs := []key.Hint{
		{Key: tuiKeysVimUpDown, Desc: tuiDescNavigate},
		{Key: tuiKeysJumpFirstLast, Desc: tuiDescJumpFirstLast},
		{Key: key.Enter, Desc: tuiDescShow},
		{Key: key.Space, Desc: tuiDescSelect},
		{Key: key.ShiftArrowsUpDown, Desc: tuiDescExtendSelection},
		{Key: tuiKeybindSelectAll, Desc: tuiDescSelectAll},
		{Key: tuiKeybindInvertSelection, Desc: tuiDescInvertSelection},
		{Key: tuiKeybindFilter, Desc: tuiDescFilter},
		{Key: tuiKeybindApprove, Desc: tuiDescApprove},
		{Key: tuiKeybindApproveMerge, Desc: tuiDescApproveMerge},
		{Key: tuiKeybindApproveNoConfirm, Desc: tuiDescApproveNoConfirm},
		{Key: tuiKeybindDiff, Desc: tuiDescDiff},
		{Key: tuiKeybindDraftToggle, Desc: tuiDescDraftToggle},
		{Key: tuiKeybindMerge, Desc: tuiDescMerge},
		{Key: tuiKeybindForceMerge, Desc: tuiDescForceMerge},
		{Key: tuiKeybindClose, Desc: tuiDescClose},
		{Key: tuiKeybindUpdateBranch, Desc: tuiDescUpdateBranch},
		{Key: tuiKeybindUnassign, Desc: tuiDescUnassign},
		{Key: tuiKeybindUnassignNoConfirm, Desc: tuiDescUnassignNoConf},
		{Key: tuiKeybindOpen, Desc: tuiDescOpen},
		{Key: tuiKeybindCopyURL, Desc: tuiDescCopy},
		{Key: tuiKeybindSlack, Desc: tuiDescSendSlack},
		{Key: tuiKeybindSlackNoConfirm, Desc: tuiDescSendSlackNoConf},
		{Key: key.Tab, Desc: tuiDescCycleSortOrder},
		{Key: tuiKeybindOptions, Desc: tuiDescOptions},
		{Key: tuiKeybindToggleRefresh, Desc: tuiDescRefresh},
		{Key: tuiKeybindHelp, Desc: tuiDescHelp},
		{Key: tuiKeybindQuit, Desc: tuiDescQuit},
	}
	if hasAIReviewLauncher() {
		// Insert review before the last two entries (?, q).
		pairs = append(
			pairs[:len(pairs)-2],
			append(
				[]key.Hint{
					{Key: tuiKeybindReview, Desc: tuiDescReview},
					{Key: tuiKeybindReviewNoConfirm, Desc: tuiDescReviewNoConfirm},
					{Key: tuiKeybindCopilotReview, Desc: tuiDescCopilotReview},
				},
				pairs[len(pairs)-2:]...)...)
	}

	sheetPairs := make([]helpsheet.Pair, len(pairs))
	for i, p := range pairs {
		sheetPairs[i] = helpsheet.Pair{Key: p.Key, Desc: p.Desc}
	}
	return helpsheet.Model{
		Pairs:   sheetPairs,
		Dismiss: "Press any key to dismiss",
		Styles: helpsheet.Styles{
			Key:     m.styles.helpKey,
			Text:    m.styles.helpText,
			Dismiss: styleDismiss.Bold(true),
			Box:     m.styles.overlayBox,
		},
	}.Render()
}

// appendStatus appends the status message to the right of the last line of help,
// or returns help unchanged if there's no status or not enough room.
func (m tuiModel) appendStatus(help string) string {
	status := m.renderFlashStatus()
	if status == "" {
		return help
	}
	return m.appendRightStatus(help, status)
}

func (m tuiModel) renderFlashStatus() string {
	if m.flash.Msg == "" {
		return ""
	}
	style := m.styles.statusOK
	if m.flash.Err {
		style = m.styles.statusErr
	}
	return style.Render(m.flash.Msg)
}

func (m tuiModel) footerComponents(help, status string) []view.FooterComponent {
	//nolint:mnd // this helper builds at most help and status
	components := make(
		[]view.FooterComponent,
		0,
		2,
	)
	if help != "" {
		components = append(components, view.FooterComponent{Content: help})
	}
	if status != "" {
		components = append(components, view.FooterComponent{
			Align:   view.FooterAlignRight,
			Content: status,
		})
	}
	return components
}

func (m tuiModel) appendRightStatus(help, status string) string {
	if status == "" || m.width <= 0 {
		return help
	}
	usableWidth := max(1, m.width-1)
	lastNL := strings.LastIndex(help, "\n")
	prefix := ""
	lastLine := help
	if lastNL >= 0 {
		prefix = help[:lastNL+1]
		lastLine = help[lastNL+1:]
	}
	statusWidth := lg.Width(status)

	for {
		pad := usableWidth - lg.Width(lastLine) - statusWidth
		if pad > 0 {
			return prefix + lastLine + strings.Repeat(" ", pad) + status
		}
		idx := strings.LastIndex(lastLine, helpGap)
		if idx < 0 {
			break
		}
		lastLine = lastLine[:idx]
	}

	if statusWidth < usableWidth {
		return prefix + strings.Repeat(" ", usableWidth-statusWidth) + status
	}
	return prefix + xansi.Truncate(status, usableWidth, valueEllipsis)
}

func (m tuiModel) renderHelp(pairs []key.Hint) string {
	return helpbar.Model{
		Hints: pairs,
		Renderer: key.Renderer{
			Styles: key.Styles{
				Key:  m.styles.helpKey,
				Text: m.styles.helpText,
			},
			Gap:    helpGap,
			Width:  m.width,
			Inline: true,
		},
	}.Render()
}

// helpLines returns the number of lines the help bar occupies at the current width.
func (m tuiModel) helpLines(pairs []key.Hint) int {
	return helpbar.Model{
		Hints: pairs,
		Renderer: key.Renderer{
			Styles: key.Styles{
				Key:  m.styles.helpKey,
				Text: m.styles.helpText,
			},
			Gap:    helpGap,
			Width:  m.width,
			Inline: true,
		},
	}.Lines()
}

func (m tuiModel) renderEmptyOverlay() string {
	dim := m.styles.helpText
	key := m.styles.helpKey
	box := lg.NewStyle().
		Border(lg.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, tuiConfirmPadX)
	if m.filterInput.Value() != "" {
		// Truncate the filter value so the overlay doesn't overflow.
		prefix := "No pull requests match \""
		suffix := "\""
		maxQuery := max(1, m.width*4/5-len(prefix)-len(suffix))
		query := m.filterInput.Value()
		if len(query) > maxQuery {
			query = query[:maxQuery-1] + valueEllipsis
		}
		line1 := dim.Render(prefix) +
			styleFilter.Render(query) + dim.Render(suffix)
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
	m.confirmOptions = nil
	m.confirmState = prompt.State{}
	m.confirmReviewPR = nil
	m.confirmView.GotoTop()
	m.confirmInput.SetWidth(tuiConfirmInputWidth)
	m.confirmInput.MaxHeight = tuiConfirmInputMaxHeight
	m.confirmInput.Blur()
	m.confirmInput.SetValue("")
	return m
}

// styledRef returns a bold, hyperlinked PR ref for use in confirm prompts.
func styledRef(pr *PullRequest) string {
	ref := styleRef.Bold(true).Render(pr.Ref())
	return xansi.SetHyperlink(pr.URL) + ref + xansi.ResetHyperlink()
}

// overlayCenter places a box on top of a background string, centered.

// highlightDiff highlights a unified diff using delta if available,
// falling back to Chroma syntax highlighting.
func highlightDiff(raw, prURL, headSHA string) string {
	repoURL, _, ok := strings.Cut(prURL, "/pull/")
	if !ok {
		return render.DiffStyled(raw, render.DiffOptions{})
	}
	return render.DiffStyled(raw, render.DiffOptions{
		RepoURL:   repoURL,
		CommitSHA: headSHA,
	})
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

// rowIndexAt maps a screen Y coordinate to a row index.
// Returns the index into m.rows and ok=true if the click landed on a visible row.
func (m tuiModel) rowIndexAt(y int) (int, bool) {
	const headerLines = 1 // header is always rendered on Y=0
	row := y - headerLines + m.offset
	visible := m.visibleIndices()
	if row < 0 || row >= len(visible) {
		return 0, false
	}
	return visible[row], true
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
	gql, err := newGraphQLClient(withDebug(cli.Debug))
	if err != nil {
		return fmt.Errorf("creating GraphQL client: %w", err)
	}

	type fetchResult struct {
		rows      []TableRow
		items     []PRRowModel
		header    string
		colWidths []int
		err       error
	}
	r := withSpinner(tty && !cli.Debug, s, func(func()) fetchResult {
		snapshot := refreshSnapshot{
			cli:      cli,
			cfg:      cfg,
			p:        p,
			tty:      tty,
			gql:      gql,
			resolver: resolver,
			rest:     rest,
			params:   params,
			width:    term.Width(os.Stdout),
		}
		items, searchErr := snapshot.fetchAndBuild()
		if searchErr != nil {
			return fetchResult{err: searchErr}
		}
		initWidth := max(0, snapshot.width-tuiListPrefixWidth(len(items)))
		renderer := p.newTableRenderer(cli, tty, initWidth, table.WithShowIndex(false))
		header, rows, colWidths := renderTUITable(renderer, items, "", false, initWidth)
		return fetchResult{rows: rows, items: items, header: header, colWidths: colWidths}
	})

	if r.err != nil {
		return r.err
	}

	actions := NewActionRunner(rest, gql)

	fi := textinput.New()
	fi.Prompt = ""
	fiStyles := fi.Styles()
	fiStyles.Focused.Text = styleFilter
	fiStyles.Blurred.Text = styleFilter
	fiStyles.Cursor.Color = colorFilter
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
		lastRefreshAt:   time.Now(),
		spinner:         buildSpinner(cfg.Spinner),
		styles:          newTuiStyles(),
		removed:         make(prKeys),
		selected:        make(prKeys),
		filterInput:     fi,
		confirmInput:    ci,
		diffView:        newScrollView(),
		detailView:      newScrollView(),
		confirmView:     newScrollViewSoftWrap(),
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

	var program *tea.Program
	resolve := func(m tea.Model) (wheelTarget, bool) {
		tui, ok := m.(tuiModel)
		if !ok {
			return wheelTargetNone, false
		}
		return tui.wheelScrollTarget()
	}
	sw := scrollwheel.New(resolve, func(msg tea.Msg) {
		if program != nil {
			program.Send(msg)
		}
	})
	defer sw.Stop()

	program = tea.NewProgram(
		model,
		tea.WithFPS(tuiRenderFPS),
		tea.WithFilter(sw.Filter),
	)
	_, err = program.Run()
	if err != nil {
		return fmt.Errorf("interactive TUI: %w", err)
	}
	return nil
}
