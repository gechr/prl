package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
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

// clearStatusMsg clears the status bar after a timeout.
type clearStatusMsg struct{ id int }

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

type batchedWheelMsg struct {
	target wheelTarget
	delta  int
}

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
	statusMsg         string
	statusErr         bool
	statusID          int
	actions           *ActionRunner
	width             int
	height            int
	styles            tuiStyles
	removed           prKeys
	selected          prKeys

	// Filter options overlay.
	showOptions   bool
	optionsCursor filterRow
	optionsValues [6]int // index into choices for each filterOptionDef
	optionsReset  [6]bool

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
	confirmYes        bool              // true = Yes selected, false = No selected
	confirmHasInput   bool              // true when modal includes a text input
	confirmInputLabel string            // label above the textarea (default: "Comment")
	confirmInput      textarea.Model    // optional text input (e.g. close comment)
	confirmOptions    []filterOptionDef // optional selectable rows shown in confirm modal
	confirmOptValues  []int             // selected choice per confirm option row
	confirmOptCursor  int               // focused confirm option row
	confirmOptFocus   bool              // true when confirm option rows have focus
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
		if gql, gqlErr := newGraphQLClient(withDebug(r.cli.Debug)); gqlErr == nil {
			actors, hydrateErr := hydrateListMetadata(gql, prs, listMetadataRequest{
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
	if row < len(m.confirmOptValues) {
		idx = min(max(m.confirmOptValues[row], 0), len(choices)-1)
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
		m.statusMsg = m.styles.statusPending.Render("Applying" + valueEllipsis)
		m.statusErr = false
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
		return m, nil

	case tea.MouseMotionMsg:
		m.touchInteraction()
		if m.handleScrollbarMotion(msg.Mouse()) {
			return m, nil
		}
		return m, nil

	case tea.MouseReleaseMsg:
		m.touchInteraction()
		m.scrollDrag = scrollbarDragState{}
		return m, nil

	case batchedWheelMsg:
		m.touchInteraction()
		m.applyWheelScroll(msg.target, msg.delta)
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
		m.diffLines = wrapDiffLines(m.diff, m.width-tuiScrollbarWidth)
		m.syncDiffView()
		m.diffView.GotoTop()
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
		m.refreshTerminalSize()
		m.detailKey = msg.key
		m.detail = msg.detail
		m.detailLines = m.renderDetailContent()
		m.syncDetailView()
		m.detailView.GotoTop()
		m.view = tuiViewDetail
		m.statusMsg = ""
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
			m.statusMsg = ""
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
		case tuiKeyEnter:
			m.filterInput.Blur()
			return m, nil
		case tuiKeyEsc, tuiKeyCtrlC, tuiKeyCtrlD:
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.resyncCursorAndOffset()
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

	// Try action keybinds first.
	if result, cmd, handled := m.updateListActions(msg); handled {
		return result, cmd
	}

	switch msg.String() {
	case tuiKeyEsc:
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

	case tuiKeyEnter:
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		m.refreshTerminalSize()
		idx := m.cursor
		actions := m.actions
		prCopy := *pr
		m.statusMsg = m.styles.statusPending.Render("Fetching") + " " +
			styleRef.Render(prCopy.Ref()) + valueEllipsis
		m.statusErr = false
		m.detailLoading = true
		key := makePRKey(prCopy)
		fetchCmd := func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			detail, err := actions.fetchPRDetail(owner, repo, prCopy.Number, prCopy.NodeID)
			return detailFetchedMsg{index: idx, key: key, detail: detail, err: err}
		}
		return m, tea.Batch(requestWindowSizeCmd(), fetchCmd)

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
		m.invalidateRefresh()
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
			b.WriteString(injectLineBackground(line, m.width, bg))
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

	v := tea.NewView(expandTabs(b.String()))
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
	return m.styles.separator.Render(separatorPad(m.width, col))
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
	var b strings.Builder

	if header != "" {
		b.WriteString(header)
		b.WriteString(nl)
		if m.width > 0 {
			b.WriteString(m.styles.separator.Render(strings.Repeat(sepHorizontal, m.width)))
		}
		b.WriteString(nl)
	}

	totalLines := vp.TotalLineCount()
	vpHeight := vp.Height()
	switch {
	case vpHeight <= 0:
		b.WriteString(nl)
	case totalLines > vpHeight:
		b.WriteString(m.renderViewportContent(renderLines, vp, true))
	default:
		b.WriteString(m.renderViewportContent(renderLines, vp, false))
	}
	b.WriteString(nl)

	if m.width > 0 {
		b.WriteString(m.styles.separator.Render(strings.Repeat(sepHorizontal, m.width)))
	}
	b.WriteString(nl)

	if m.statusMsg != "" {
		b.WriteString(m.appendStatus(help))
	} else {
		status := ""
		if totalLines > vpHeight {
			offset := vp.YOffset()
			end := min(offset+vpHeight, totalLines)
			pct := int(math.Round(vp.ScrollPercent() * 100)) //nolint:mnd // percent
			status = m.styles.statusOK.Render(
				fmt.Sprintf("%d-%d/%d (%d%%)", offset+1, end, totalLines, pct),
			)
		}
		b.WriteString(m.appendRightStatus(help, status))
	}

	v := tea.NewView(m.fillViewToTerminal(b.String()))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if m.confirmAction != "" {
		v.Content = overlayCenter(v.Content, m.renderConfirmModal(), m.width, m.height)
	}
	m.applyRepaintMarker(&v)
	return v
}

func (m tuiModel) fillViewToTerminal(output string) string {
	output = expandTabs(output)
	if m.width <= 0 || m.height <= 0 {
		return output
	}

	// Extend short views to the terminal height so the next fullscreen render
	// has blank rows available below the content.
	lines := strings.Split(output, nl)
	blank := strings.Repeat(" ", m.width)
	for len(lines) < m.height {
		lines = append(lines, blank)
	}
	return strings.Join(lines, nl)
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
		m.diffLines = wrapDiffLines(m.diff, m.width-tuiScrollbarWidth)
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
	for line := range strings.SplitSeq(strings.TrimRight(rendered, nl), nl) {
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

func (m tuiModel) listHelpPairs() []helpPair {
	pairs := []helpPair{
		{tuiKeyEnter, tuiHelpShow},
		{tuiKeySpace, tuiHelpSelect},
		{tuiKeybindFilter, tuiHelpFilter},
	}
	ctx, ok := m.actionContextForCursor()
	actionable := ok && ctx.actionable
	if actionable && !ctx.ownPR && !ctx.draft {
		pairs = append(pairs, helpPair{tuiKeybindApprove, tuiHelpApprove})
	}
	pairs = append(pairs, helpPair{tuiKeybindDiff, tuiHelpDiff})
	if actionable && !ctx.draft {
		pairs = append(pairs, helpPair{tuiKeybindMerge, mergeHelpForPR(&ctx.pr)})
	}
	pairs = append(pairs, helpPair{tuiKeybindComment, tuiHelpComment})
	if ctx.state != valueMerged {
		pairs = append(pairs, helpPair{tuiKeybindClose, closeReopenHelp(ctx.state)})
	}
	pairs = append(
		pairs,
		helpPair{tuiKeybindOpen, tuiHelpOpen},
		helpPair{tuiKeybindCopyURL, tuiHelpCopy},
	)
	if actionable && !ctx.draft && hasAIReviewLauncher() {
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

func (m tuiModel) renderFilterSyntaxHints() string {
	syntaxPairs := []struct{ key, desc string }{
		{"^", "start"},
		{"$", "end"},
		{"!", "negate"},
	}
	var parts []string
	for _, p := range syntaxPairs {
		parts = append(parts, styleHeading.Bold(true).Render(p.key)+" "+styleFilter.Render(p.desc))
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
		{tuiKeysArrows, "prev/next"},
		{tuiKeyEnter, "apply"},
		{tuiKeyEsc, "exit"},
	}
	base := strings.TrimLeft(m.renderHelp(pairs), " ")
	sep := m.styles.separator.Render("│")
	return m.renderFilterSyntaxHints() + filterHelpGap + sep + filterHelpGap + base
}

func (m tuiModel) diffHelpPairs() []helpPair {
	pairs := []helpPair{
		{tuiKeysArrows, tuiHelpScroll},
	}
	ctx, _ := m.actionContextForKey(m.diffKey)
	actionable := ctx.actionable
	if actionable && !ctx.draft {
		pairs = append(pairs, helpPair{tuiKeybindMerge, mergeHelpForPR(&ctx.pr)})
	}
	if actionable {
		pairs = append(pairs, helpPair{tuiKeybindDraftToggle, draftToggleHelp(&ctx.pr)})
	}
	if actionable && !ctx.ownPR && !ctx.draft {
		pairs = append(
			pairs,
			helpPair{tuiKeybindApprove, tuiHelpApprove},
		)
		pairs = append(pairs, helpPair{tuiKeybindApproveMerge, tuiHelpApproveMerge})
	}
	if actionable && !ctx.ownPR {
		pairs = append(pairs, helpPair{tuiKeybindUnassign, tuiHelpUnsubscribe})
	}
	pairs = append(pairs, helpPair{tuiKeybindComment, tuiHelpComment})
	if ctx.state != valueMerged {
		pairs = append(pairs, helpPair{tuiKeybindClose, closeReopenHelp(ctx.state)})
	}
	pairs = append(pairs, helpPair{tuiKeybindOpen, tuiHelpOpen})
	pairs = append(pairs, helpPair{tuiKeybindCopyURL, tuiHelpCopy})
	if actionable {
		pairs = append(pairs, helpPair{tuiKeybindSlack, tuiHelpSlack})
	}

	if actionable && !ctx.draft && hasAIReviewLauncher() {
		pairs = append(pairs, helpPair{tuiKeybindReview, tuiHelpReview})
	}
	if actionable && !ctx.draft {
		pairs = append(pairs, helpPair{tuiKeybindCopilotReview, tuiHelpCopilot})
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

func (m tuiModel) detailHelpPairs() []helpPair {
	pairs := []helpPair{
		{tuiKeysArrows, tuiHelpScroll},
		{tuiKeybindDiff, tuiHelpDiff},
	}
	ctx, _ := m.actionContextForKey(m.detailKey)
	actionable := ctx.actionable
	if actionable && !ctx.draft {
		pairs = append(pairs, helpPair{tuiKeybindMerge, mergeHelpForPR(&ctx.pr)})
	}
	if actionable {
		pairs = append(pairs, helpPair{tuiKeybindDraftToggle, draftToggleHelp(&ctx.pr)})
	}
	if actionable && !ctx.draft && !ctx.ownPR {
		pairs = append(
			pairs,
			helpPair{tuiKeybindApprove, tuiHelpApprove},
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
	if actionable && !ctx.draft && hasAIReviewLauncher() {
		pairs = append(pairs, helpPair{tuiKeybindReview, tuiHelpReview})
	}
	if actionable && !ctx.draft {
		pairs = append(pairs, helpPair{tuiKeybindCopilotReview, tuiHelpCopilot})
	}
	pairs = append(pairs, helpPair{tuiKeybindQuit, tuiHelpDismiss})
	return pairs
}

func (m tuiModel) renderHelpOverlay() string {
	pairs := []helpPair{
		{tuiKeysVimUpDown, tuiDescNavigate},
		{tuiKeysJumpFirstLast, tuiDescJumpFirstLast},
		{tuiKeyEnter, tuiDescShow},
		{tuiKeySpace, tuiDescSelect},
		{tuiKeysArrowsUpDown, tuiDescExtendSelection},
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
	if hasAIReviewLauncher() {
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
		b.WriteString(nl)
	}
	dismiss := styleDismiss.Bold(true).Render("Press any key to dismiss")
	pad := (totalWidth - lg.Width(dismiss)) / 2 //nolint:mnd // center
	if pad > 0 {
		b.WriteString(nl + strings.Repeat(" ", pad) + dismiss)
	} else {
		b.WriteString(nl + dismiss)
	}

	return m.styles.overlayBox.Render(b.String())
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
	usableWidth := max(1, m.width-1)
	lastNL := strings.LastIndex(help, nl)
	prefix := ""
	lastLine := help
	if lastNL >= 0 {
		prefix = help[:lastNL+1]
		lastLine = help[lastNL+1:]
	}
	sw := lg.Width(status)
	// Drop help pairs from the right until status fits.
	const helpGap = "  "
	for {
		pad := usableWidth - lg.Width(lastLine) - sw
		if pad > 0 {
			return prefix + lastLine + strings.Repeat(" ", pad) + status
		}
		// Remove the rightmost help pair.
		idx := strings.LastIndex(lastLine, helpGap)
		if idx < 0 {
			break
		}
		lastLine = lastLine[:idx]
	}

	// No pairs left. Keep status on a single line so transient footer updates
	// don't change the overall view height.
	if sw < usableWidth {
		return prefix + strings.Repeat(" ", usableWidth-sw) + status
	}
	return prefix + xansi.Truncate(status, usableWidth, valueEllipsis)
}

// inlineHelpKey tries to embed a single-letter key into its description.
// It supports plain keys ("a" + "approve" -> "approve" with 'a' key-styled)
// and modified keys ("alt+c" + "copy" -> "alt+copy" with the prefix and
// leading 'c' key-styled). Returns false if the key doesn't end in a single
// ASCII letter or that letter doesn't appear in the description.
func (m tuiModel) inlineHelpKey(p helpPair, helpText lg.Style) (string, bool) {
	keyPrefix, keyLetter, ok := splitInlineHelpKey(p.key)
	if !ok {
		return "", false
	}
	idx := strings.Index(strings.ToLower(p.desc), strings.ToLower(keyLetter))
	if idx < 0 {
		return "", false
	}
	before := p.desc[:idx]
	after := p.desc[idx+1:]
	var part string
	if keyPrefix != "" {
		part = m.styles.helpKey.Render(keyPrefix)
	}
	if before != "" {
		part += helpText.Render(before)
	}
	part += m.styles.helpKey.Render(keyLetter)
	if after != "" {
		part += helpText.Render(after)
	}
	return part, true
}

func splitInlineHelpKey(key string) (string, string, bool) {
	if len(key) == 1 {
		ch := key[0] | 0x20 //nolint:mnd // ASCII lowercase
		if ch < 'a' || ch > 'z' {
			return "", "", false
		}
		return "", key, true
	}

	idx := strings.LastIndex(key, "+")
	if idx <= 0 || idx == len(key)-1 {
		return "", "", false
	}
	letter := key[idx+1:]
	if len(letter) != 1 {
		return "", "", false
	}
	ch := letter[0] | 0x20 //nolint:mnd // ASCII lowercase
	if ch < 'a' || ch > 'z' {
		return "", "", false
	}
	return key[:idx+1], letter, true
}

func (m tuiModel) renderHelp(pairs []helpPair) string {
	const gap = "  "
	var parts []string
	helpText := m.styles.helpText
	for _, p := range pairs {
		// For single-letter keys whose letter appears in the description,
		// embed the key into the word to save space (e.g. "a approve" → "approve"
		// with the 'a' key-styled inline).
		if inlined, ok := m.inlineHelpKey(p, helpText); ok {
			parts = append(parts, inlined)
			continue
		}

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
	return strings.Join(lines, nl)
}

// helpLines returns the number of lines the help bar occupies at the current width.
func (m tuiModel) helpLines(pairs []helpPair) int {
	return strings.Count(m.renderHelp(pairs), nl) + 1
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
	m.confirmOptValues = nil
	m.confirmOptCursor = 0
	m.confirmOptFocus = false
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
func overlayCenter(bg, fg string, width, height int) string {
	bgLines := strings.Split(bg, nl)
	fgLines := strings.Split(fg, nl)

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

	return strings.Join(bgLines, nl)
}

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

func expandTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "    ")
}

func wrapDiffLines(diff string, width int) []string {
	logicalLines := strings.Split(expandTabs(diff), nl)
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
	if !strings.Contains(wrapped, nl) {
		return []string{line}
	}

	var buf bytes.Buffer
	writer := lg.NewWrapWriter(&buf)
	_, _ = writer.Write([]byte(wrapped))
	_ = writer.Close()
	return strings.Split(buf.String(), nl)
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
		snapshot := refreshSnapshot{
			cli:      cli,
			cfg:      cfg,
			p:        p,
			tty:      tty,
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

	// Create ActionRunner with GraphQL (always needed for merge/automerge).
	gql, err := newGraphQLClient(withDebug(cli.Debug))
	if err != nil {
		return fmt.Errorf("creating GraphQL client: %w", err)
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
	wheelFilter := newTUIWheelFilter(tuiWheelBatchDelay, func(msg tea.Msg) {
		if program != nil {
			program.Send(msg)
		}
	})
	defer wheelFilter.Stop()

	program = tea.NewProgram(
		model,
		tea.WithFPS(tuiRenderFPS),
		tea.WithFilter(wheelFilter.filter),
	)
	_, err = program.Run()
	if err != nil {
		return fmt.Errorf("interactive TUI: %w", err)
	}
	return nil
}
