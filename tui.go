package main

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"bloomfx.dev/bloom"
	"bloomfx.dev/bloom/anim"
	"bloomfx.dev/bloom/fx"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	cansi "github.com/charmbracelet/x/ansi"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	"github.com/gechr/primer/filter"
	"github.com/gechr/primer/flash"
	"github.com/gechr/primer/helpbar"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/render"
	"github.com/gechr/primer/scrollbar"
	"github.com/gechr/primer/scrollwheel"
	"github.com/gechr/primer/table"
	xansi "github.com/gechr/x/ansi"
	"github.com/gechr/x/human"
	xterminal "github.com/gechr/x/terminal"
)

type confirmSubmission struct {
	Input   string
	Options map[string]string
}

type confirmDraftKey struct {
	action string
	url    string
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
		confirmYes: pillButtonStyle(lg.NewStyle().
			Background(colorOK).
			Foreground(colorBlack).
			Bold(true).
			Padding(0, 1), colorOK, false),
		confirmYesDim: pillButtonStyle(lg.NewStyle().
			Foreground(colorOK).
			Padding(0, 1), colorOK, true),
		confirmNo: pillButtonStyle(lg.NewStyle().
			Background(colorDanger).
			Foreground(colorBlack).
			Bold(true).
			Padding(0, 1), colorDanger, false),
		confirmNoDim: pillButtonStyle(lg.NewStyle().
			Foreground(colorDanger).
			Padding(0, 1), colorDanger, true),
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

func (m tuiModel) indexGap() string {
	if m.stacksEnabled() {
		return " "
	}
	return tuiNonCursorPrefix
}

func (m tuiModel) tablePrefixWidth(total int) int {
	width := tuiListPrefixWidth(total) + m.stackExtraWidth()
	return width + len(m.indexGap()) - len(tuiNonCursorPrefix)
}

func (m tuiModel) listPrefixWidth() int {
	return m.tablePrefixWidth(len(m.rows))
}

func (m tuiModel) renderTuiIndexLabel(label string, selected bool) string {
	return m.renderTuiIndexWidth(label, selected, m.listIndexWidth())
}

func (m tuiModel) renderTuiIndexWidth(label string, selected bool, width int) string {
	text := fmt.Sprintf("%-*s", width, label)
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
	tuiActionUnapproved
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
	case tuiActionUnapproved:
		return resultUnapproved
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
	case tuiActionUnapproved:
		return "Unapprove"
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
	ref    string
	url    string
	action tuiAction
	// outcome is set for approvals.
	outcome   approveOutcome
	approvals []approvalResult // approval legs of approve-and-merge
	// navigated is true when navigation is handled at dispatch, so a
	// background completion must preserve the current view.
	navigated bool
	err       error
}

// detailFetchedMsg is sent when PR detail has been fetched.
type detailFetchedMsg struct {
	reviewStamp

	key    prKey
	detail PRDetail
	err    error
}

// diffFetchedMsg is sent when a diff has been fetched.
type diffFetchedMsg struct {
	index            int
	key              prKey // stable lookup after refresh
	diff             string
	headSHA          string
	visited          bool
	fetchedAt        time.Time
	requestedHeadSHA string // head known when the fetch started
	baseRef          string
	headGen          int
	updatedAt        time.Time
	err              error
}

// batchActionMsg is sent when a batch action (multi-select) completes.
type batchActionMsg struct {
	action    tuiAction
	count     int
	failed    int
	keys      []prKey
	failures  []batchResult
	actions   map[prKey]tuiAction      // individual merge results
	outcomes  map[prKey]approveOutcome // set for approvals
	approvals []approvalResult         // approval legs of approve-and-merge
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
	params   *SearchParams
	rows     []TableRow
	items    []PRRowModel
	err      error
	queryGen int // refresh request generation; stale completions are discarded
}

type headValidationMsg struct {
	changed  bool
	err      error
	queryGen int
}

// tuiModel is the Bubble Tea model for the interactive TUI.
//
//nolint:recvcheck // selection helpers use pointer receivers to mutate maps/fields in-place
type tuiModel struct {
	session              *tuiSession
	quitting             bool
	contentRevision      int
	resumeTimeCutoff     *time.Time
	resumeRefreshPending bool
	items                []PRRowModel // canonical data for rerender on resize/refresh
	rows                 []TableRow   // current rendered order; row.Item is the action target
	header               string
	colWidths            []int // visible column widths for header click hit-testing
	sortColumn           string
	sortAsc              bool
	cursor               int
	offset               int
	screen               *tuiScreen
	slide                prSlide
	view                 tuiView
	diff                 string
	diffKey              prKey
	diffView             *reviewViewport
	detail               PRDetail
	detailLines          []string
	detailKey            prKey
	detailView           *reviewViewport
	detailRefreshID      int
	detailLoading        bool
	detailAwaitKey       prKey
	detailAwaitFrom      fx.Direction
	detailCache          map[prKey]detailFetchedMsg
	detailInFlight       prKeys
	flash                flash.State
	actions              *ActionRunner
	width                int
	height               int
	styles               tuiStyles
	selected             prKeys

	// Filter options overlay.
	showOptions     bool
	optionsPicker   *filterPicker
	presetMenu      presetMenuState
	showPresetHover bool // Keyboard and resize hide hover until the next pointer motion.
	collapsedStacks prKeys
	// filterHidden holds rows hidden client-side because they no longer match
	// the search filters just applied via the overlay, or because an approval
	// took them out of the filter. It keeps the list responsive while the
	// authoritative refresh runs, and is rebuilt once it lands. The CI filter
	// and presets are separate: they always apply when rows are shown.
	filterHidden prKeys

	// Confirmed actions retained while the search index catches up.
	pendingActions map[prKey]pendingAction

	// Successful mutations awaiting a targeted read when review ends.
	changedPRs             prKeys
	prChangeGen            map[prKey]int
	prEventGen             map[prKey]int
	changedRefreshInFlight bool

	// Optimistic approvals: sent but not yet confirmed. approvalGen bumps on
	// every begin and settlement so stale status responses are ignored.
	pendingApprovals map[prKey]pendingApproval
	approvalGen      map[prKey]int

	// Tracked actions sent but not yet returned (see trackAction).
	actionsInFlight int

	// Background reads tracked independently of mutations and refresh scheduling.
	readsInFlight int

	// Diff queue for sequential multi-PR review.
	diffQueue      []prKey // remaining PR keys to diff through
	diffHistory    []prKey // previously viewed PR keys (for going back)
	diffQueueTotal int     // total PRs in the queue (for counter display)
	diffQueuePos   int     // position of the displayed diff, including while the next PR loads
	diffLoading    bool
	diffAwaitKey   prKey                    // diff the view is waiting to show
	diffAwaitFrom  fx.Direction             // slide direction for that request
	diffCache      map[prKey]diffFetchedMsg // completed foreground and prefetched diffs
	diffHeadGen    map[prKey]int
	diffStatus     map[prKey]reviewStamp
	diffInFlight   prKeys // diff fetches not yet returned

	// Empty overlay dismissed (esc to dismiss, then esc again to quit).
	dismissedEmpty bool

	// Filter mode.
	filterInput textinput.Model

	// Pending digit jump (e.g. "1" waiting for second digit).
	jumpDigit int // first digit (1-9), 0 = no pending jump
	jumpID    int // timeout generation

	// Pending confirmation (e.g. close/merge).
	confirmAction           string  // "close", "merge", "diff"
	confirmPrompt           string  // prompt text for modal
	confirmSubject          string  // target description for progress (e.g. "data-team#8", "3 PRs")
	confirmURL              string  // optional URL for hyperlinking the subject in progress
	confirmCmd              tea.Cmd // command to run on confirmation
	confirmCmdFn            func(confirmSubmission) tea.Cmd
	confirmOnAccept         func(*tuiModel)   // runs when the dialog is accepted
	confirmOptionValues     []int             // initially selected value for each option row
	confirmHasInput         bool              // true when the dialog includes a text input
	confirmInputLabel       string            // label above the textarea (default: "Comment")
	confirmInputValue       string            // initial text input value
	confirmInputPlaceholder string            // empty text input hint
	confirmOptions          []filterOptionDef // optional selectable rows shown in the dialog
	confirmReviewPR         *PullRequest      // selected PR when the dialog is for AI review
	confirmDrafts           map[confirmDraftKey]string

	// Background auto-refresh.
	eventStreams           *eventStreams
	eventTargets           eventTargets
	eventAttachGen         int
	pendingEvents          prEvents
	nextEventListRead      time.Time
	eventListReadScheduled bool
	streamFailed           bool
	autoRefresh            bool
	refreshing             bool      // true while a background refresh is in-flight
	lastInteraction        time.Time // tracks last user keypress for idle-based refresh decay
	lastRefreshAt          time.Time // last accepted membership check
	confirmed              confirmedPRList
	// showRefreshStatus is true when the in-flight refresh was triggered by
	// applying options and should show a temporary "Refreshing..." status.
	showRefreshStatus  bool
	refreshCancellable bool    // only periodic reads may yield to targeted refreshes
	refreshID          int     // generation counter to discard stale refresh ticks
	spinner            spinner // spinner animation frames
	spinnerTick        int     // current spinner frame index
	queryGen           int     // generation counter to discard stale ticks and results
	repaintTick        bool    // toggled each heartbeat to invalidate the renderer cache
	help               *helpSheet

	// Cached GitHub login of the authenticated user (resolved lazily).
	login string

	// Retained for re-rendering the table on resize and background refresh.
	p             *prl
	cli           *CLI
	cfg           *Config
	tty           bool
	resolver      *AuthorResolver
	gql           *api.GraphQLClient
	rest          *api.RESTClient
	params        *SearchParams
	metadataCache *listMetadataCache
	initCmd       tea.Cmd
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
	cache    *listMetadataCache
	policy   *listRefreshPolicy
}

func newRefreshSnapshot(m tuiModel) refreshSnapshot {
	return refreshSnapshot{
		cli:      cloneCLI(m.cli),
		cfg:      m.cfg,
		p:        m.p,
		tty:      m.tty,
		gql:      m.gql,
		resolver: m.resolver,
		rest:     m.rest,
		params:   cloneSearchParams(m.params),
		width:    m.width,
		cache:    m.metadataCache,
	}
}

// fetchAndBuild runs the search, filter, enrich, and build pipeline.
// It returns the built row models, or an error.
func (r refreshSnapshot) fetchAndBuild() ([]PRRowModel, error) {
	loader := listLoader{rest: r.rest, graphQL: func() (*api.GraphQLClient, error) {
		if r.gql == nil {
			return nil, fmt.Errorf("GraphQL client unavailable")
		}
		return r.gql, nil
	}, cache: r.cache, policy: r.policy, browse: true}
	result, err := loader.load(r.cli, r.params)
	if err != nil {
		return nil, err
	}
	return buildPRRowModels(result.prs, singleOwner(r.cli.Owner.Values), r.resolver), nil
}

// run fetches and renders fresh results. The list-result disk cache is saved
// by the Update handler once the result is accepted as current, so a stale
// refresh can never overwrite a newer snapshot.
func (r refreshSnapshot) run() refreshResultMsg {
	// A resumed session can paint its cached view before resolving teams or topics.
	if r.params == nil {
		params, err := buildSearchQuery(r.cli, r.cfg)
		if err != nil {
			return refreshResultMsg{err: err}
		}
		r.params = params
	}
	items, err := r.fetchAndBuild()
	if err != nil {
		return refreshResultMsg{err: err}
	}
	termWidth := max(0, r.width-tuiListPrefixWidth(len(items)))
	renderer := r.p.newTableRenderer(r.cli, r.tty, termWidth, table.WithShowIndex(false))
	_, rows, _ := renderTUITable(renderer, items, "", false, termWidth)
	return refreshResultMsg{rows: rows, items: items, params: r.params}
}

func choiceIndex(choices []filterChoice, value string) int {
	for i, c := range choices {
		if c.value == value {
			return i
		}
	}
	return 0
}

func (m tuiModel) selectedConfirmOptionValue(row int) string {
	if row < 0 || row >= len(m.confirmOptions) {
		return ""
	}
	choices := m.confirmOptions[row].choices
	if len(choices) == 0 {
		return ""
	}
	idx := 0
	if row < len(m.confirmOptionValues) {
		idx = min(max(m.confirmOptionValues[row], 0), len(choices)-1)
	}
	return choices[idx].value
}

func (m tuiModel) Init() tea.Cmd {
	reportProgramStatus(m.programStatus())
	var cmds []tea.Cmd
	if m.screen != nil {
		cmds = append(cmds, m.screen.root.Init())
	}
	if m.eventStreams != nil {
		cmds = append(cmds, m.eventStreams.wait)
	}
	if m.initCmd != nil {
		cmds = append(cmds, m.initCmd)
	}
	if m.cfg != nil && m.cfg.TUI.ScreenRepair {
		cmds = append(cmds, m.scheduleScreenCheck())
	}
	if m.autoRefresh {
		cmds = append(
			cmds,
			scheduleRefresh(
				len(m.items),
				m.refreshID,
				time.Since(m.lastInteraction),
				m.refreshIntervalOverride(),
			),
		)
	}
	return tea.Batch(cmds...)
}

func interactiveRefreshBaseDelay(n int, override *time.Duration) time.Duration {
	d := watchInterval(n)
	if override != nil && *override > d {
		return *override
	}
	return d
}

func (m tuiModel) refreshIntervalOverride() *time.Duration {
	if m.cli == nil {
		return nil
	}
	return m.cli.Interval
}

// refreshDelay returns the next list refresh delay using the adaptive watch-mode
// cadence plus idle-based slowdown.
func refreshDelay(n int, idle time.Duration, override *time.Duration) time.Duration {
	d := interactiveRefreshBaseDelay(n, override)
	if d < watchIdleMax && idle > 0 && idle < watchIdleDecay {
		// Linearly blend from the base interval toward watchIdleMax.
		frac := float64(idle) / float64(watchIdleDecay)
		d = time.Duration(float64(d)*(1-frac) + float64(watchIdleMax)*frac)
	} else if d < watchIdleMax && idle >= watchIdleDecay {
		d = watchIdleMax
	}
	return refreshCooldownDelay(d)
}

// scheduleRefresh returns a tea.Cmd that fires a refreshTickMsg after a delay
// scaled by the number of results (reusing watch-mode intervals) and further
// slowed by user inactivity.
func scheduleRefresh(n, id int, idle time.Duration, override *time.Duration) tea.Cmd {
	d := refreshDelay(n, idle, override)
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshTickMsg{id: id} })
}

func scheduleTick(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

func (m tuiModel) scheduleSpinnerTick() tea.Cmd {
	return scheduleTick(m.spinner.interval, spinnerTickMsg{id: m.queryGen})
}

func prItems(items []PRRowModel) []PullRequest {
	prs := make([]PullRequest, len(items))
	for i, item := range items {
		prs[i] = item.PR
	}
	return prs
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
	return tea.Tick(refreshCooldownDelay(detailCheckInterval), func(time.Time) tea.Msg {
		return detailRefreshTickMsg{id: id, key: key}
	})
}

func (m tuiModel) isStaleDetailRefresh(id int, key prKey) bool {
	return id != m.detailRefreshID || key != m.detailKey || m.view != tuiViewDetail || m.diffLoading
}

func (m *tuiModel) stopDetailRefresh(clearKey bool) {
	m.detailRefreshID++
	m.detailAwaitKey, m.detailAwaitFrom = "", 0
	if clearKey {
		m.detailLoading = false
		m.detailKey = ""
	}
}

// clearDiffState ends navigation. In-flight reads can still populate the cache.
func (m *tuiModel) clearDiffState() {
	m.diffQueue = nil
	m.diffHistory = nil
	m.diffAwaitFrom = 0
	m.diffQueueTotal = 0
	m.diffQueuePos = 0
	m.diffLoading = false
	m.diffKey = ""
	m.diffAwaitKey = ""
}

func (m *tuiModel) startRefresh(showStatus bool) tea.Cmd {
	return m.startRefreshWithPolicy(showStatus, nil)
}

func (m *tuiModel) startRefreshWithPolicy(showStatus bool, policy *listRefreshPolicy) tea.Cmd {
	m.refreshCancellable = false
	m.refreshing = true
	m.showRefreshStatus = showStatus
	m.spinnerTick = 0
	m.queryGen++
	if showStatus {
		m.flash.Msg = m.styles.statusPending.Render("Applying" + valueEllipsis)
		m.flash.Err = false
	}
	snapshot := newRefreshSnapshot(*m)
	snapshot.policy = policy
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

func (m *tuiModel) startHeadValidation(items []PRRowModel) tea.Cmd {
	if m.gql == nil || m.metadataCache == nil || len(items) == 0 {
		return nil
	}

	prs := prItems(items)
	queryGen := m.queryGen
	gql := m.gql
	cache := m.metadataCache
	return m.trackRead(func() tea.Msg {
		changed, err := validateCachedHeads(gql, prs, cache)
		return headValidationMsg{changed: changed, err: err, queryGen: queryGen}
	})
}

func (m *tuiModel) invalidateRefresh() { m.refreshID++ }

// rescheduleRefresh invalidates older refresh ticks and schedules a new one.
func (m *tuiModel) rescheduleRefresh() tea.Cmd {
	if m.autoRefresh {
		m.invalidateRefresh()
		return scheduleRefresh(
			len(m.items),
			m.refreshID,
			time.Since(m.lastInteraction),
			m.refreshIntervalOverride(),
		)
	}
	return nil
}

func (m *tuiModel) refreshOrReschedule() tea.Cmd {
	if len(m.changedPRs) > 0 || m.changedRefreshInFlight || m.actionsInFlight > 0 {
		return m.rescheduleRefresh()
	}
	if !m.autoRefresh {
		return nil
	}
	if m.refreshing {
		return nil
	}
	if time.Since(
		m.lastRefreshAt,
	) >= refreshDelay(
		len(m.items),
		time.Since(m.lastInteraction),
		m.refreshIntervalOverride(),
	) {
		m.invalidateRefresh()
		return m.startPeriodicRefresh()
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

// Update runs the real update, then reconciles the dialog stack so a dialog
// set up (or cleared) anywhere opens or closes before the next View.
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch e := msg.(type) {
	case anim.FrameMsg:
		cmd := m.slide.progress.Update(msg)
		if !m.slide.progress.Animating() {
			m.slide = prSlide{}
		}
		return m, cmd
	case tea.MouseMotionMsg:
		if e.Button != tea.MouseNone {
			m.slide = prSlide{}
		}
	case tea.KeyMsg, tea.MouseMsg, scrollwheel.Msg[wheelTarget], tea.WindowSizeMsg, tea.BlurMsg:
		m.slide = prSlide{}
	}
	previousState, previousKind := m.programStatus()
	reader := m.activeReviewViewport()
	var inputCmd tea.Cmd
	switch {
	case reader != nil:
		inputCmd = reader.pointer.update(reader.root, msg)
		if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == key.CtrlC {
			if copyCmd := reader.root.CopySelection(); copyCmd != nil {
				return m, tea.Batch(inputCmd, copyCmd)
			}
		}
	case m.view == tuiViewList || m.confirmUsesDialog():
		s := m.ensureScreen()
		s.listOffset = -1
		inputCmd = s.pointer.update(s.root, msg)
		if s.listOffset >= 0 && m.listScrollbarActive() {
			m.setListScrollOffset(s.listOffset)
		}
	}
	m.recordChangedPRs(msg)
	model, cmd := m.update(msg)
	tm, ok := model.(tuiModel)
	if !ok {
		return model, cmd
	}
	if tm.view == tuiViewList && tm.filterInput.Focused() && tm.flash.Msg != m.flash.Msg {
		// A flash can take a row from the list while the filter is open.
		tm.offset = tm.scrolledOffset()
	}
	cmd = tea.Batch(
		cmd, inputCmd,
		tm.syncVisibleEventStreams(msg),
		tm.refreshChangedPRs(),
		tm.refreshFromEvents(),
	)
	if tm.resumeRefreshPending && tm.view == tuiViewList && !tm.quitting {
		tm.resumeRefreshPending = false
		if !tm.refreshing {
			cmd = tea.Batch(cmd, tm.startRefresh(false))
		}
	}
	tm.syncConfirmDialog()
	if m.showOptions && !tm.showOptions && tm.screen != nil &&
		tm.screen.pointer.last.Button == tea.MouseLeft {
		tm.screen.resetRoot(tm.reviewWidthMethod())
	}
	if tm.view == tuiViewList && m.view != tuiViewList && tm.screen != nil &&
		tm.screen.pointer.last.Button == tea.MouseLeft {
		// A release in the review viewport never reached the list scrollbar.
		tm.screen.resetRoot(tm.reviewWidthMethod())
	}
	if reader != nil && reader != tm.activeReviewViewport() {
		reader.resetInput()
	}
	if tm.view != m.view || tm.confirmUsesDialog() {
		tm.slide = prSlide{}
	}
	if tm.presetMenu.open {
		if tm.canKeepPresetMenuOpen() {
			tm.movePresetMenu(0)
		} else {
			tm.presetMenu.open = false
		}
	}
	if !tm.listScrollbarActive() || tm.detailLoading || tm.diffLoading {
		tm.showPresetHover = false
	}
	state, kind := tm.programStatus()
	if state != previousState || kind != previousKind {
		reportProgramStatus(state, kind)
	}
	tm.saveSession()
	return tm, cmd
}

func (m tuiModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(readDoneMsg); ok {
		m.readsInFlight = max(m.readsInFlight-1, 0)
		if done.msg == nil {
			return m, nil
		}
		return m.update(done.msg)
	}
	if done, ok := msg.(actionDoneMsg); ok {
		m.actionsInFlight = max(m.actionsInFlight-1, 0)
		if done.msg == nil {
			return m, nil
		}
		return m.update(done.msg)
	}
	switch msg.(type) {
	case tea.KeyReleaseMsg:
		// A key released after dismissing a modal must not activate a list action.
		return m, nil
	case tea.KeyPressMsg, tea.WindowSizeMsg:
		m.showPresetHover = false
	}
	if keyMsg, ok := msg.(tea.KeyMsg); ok &&
		(keyMsg.String() == key.CtrlC || keyMsg.String() == key.CtrlD) &&
		!m.filterInput.Focused() {
		return m.quit()
	}

	if keypress, ok := msg.(tea.KeyPressMsg); ok && m.help != nil {
		return m.updateHelp(keypress), nil
	}

	// Handle filter options overlay keys.
	if key, ok := msg.(tea.KeyMsg); ok && m.showOptions {
		return m.updateOptionsOverlay(key)
	}
	if key, ok := msg.(tea.KeyMsg); ok && m.presetMenu.open {
		m.touchInteraction()
		return m.updatePresetMenuKey(key)
	}

	switch msg := msg.(type) {
	case tea.EnvMsg:
		if !m.quitting && m.session == nil && msg.Getenv(cansi.ProgramResumeTokenEnv) != "" {
			if root, err := sessionRoot(); err == nil {
				if m.cli.ResumeSession == "" {
					pruneSessions(root)
				}
				m.session = newTUISession(root, os.Args[1:], m.cli.ResumeSession)
			}
		}
		return m, nil

	case bloom.CopyMsg:
		if msg.Err != nil {
			cmd := tuiFlashMessage(
				&m,
				"System clipboard unavailable (sent to terminal clipboard)",
				true,
			)
			return m, cmd
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.applyWindowSize(msg.Width, msg.Height)
		return m, nil

	case tea.BackgroundColorMsg:
		s := m.ensureScreen()
		s.bg = msg.Color
		s.root.SetColors(s.fg, s.bg)
		return m, nil

	case tea.ForegroundColorMsg:
		s := m.ensureScreen()
		s.fg = msg.Color
		s.root.SetColors(s.fg, s.bg)
		return m, nil

	case tea.ModeReportMsg:
		return m.applyModeReport(msg), nil

	case tea.MouseClickMsg:
		m.touchInteraction()
		if m.help != nil {
			return m, nil
		}
		if m.showOptions {
			return m.clickFilterBackdrop(msg)
		}
		if m.confirmUsesDialog() {
			return m.updateConfirmDialog(msg)
		}
		if m.presetMenu.open {
			return m.updatePresetMenuClick(msg)
		}
		if msg.Button == tea.MouseLeft {
			if binding, ok := m.footerKeyAt(msg.X, msg.Y); ok {
				return m.update(key.Replay(binding))
			}
		}
		if msg.Button == tea.MouseLeft && m.canOpenPresetMenu(msg.Y) {
			switch hitID(m.screen.root, msg.X, msg.Y) {
			case filterTagsID:
				m.openOptions()
				return m, nil
			case timePresetID:
				m.openFilterPresetMenu(true)
				return m, nil
			case prPresetID:
				m.openPresetMenu()
				return m, nil
			}
		}
		if m.listScrollbarActive() && m.screen.contains(scrollbarID, msg.X, msg.Y) {
			return m, nil
		}
		if m.view == tuiViewList && msg.Button == tea.MouseLeft && msg.Y == 0 && m.header != "" {
			col := m.headerColumnAt(msg.X)
			if col != "" {
				return m.toggleSort(col)
			}
		}
		if m.view == tuiViewList && msg.Button == tea.MouseLeft {
			if stack := m.stackGlyphAt(msg.X, msg.Y); stack != "" {
				m.toggleStack(stack)
				return m, nil
			}
			if idx, ok := m.rowIndexAt(msg.Y); ok {
				m.cursor = idx
			}
		}
		return m, nil

	case tea.MouseMotionMsg:
		if msg.Button != tea.MouseNone {
			m.touchInteraction()
		}
		m.showPresetHover = true
		if m.presetMenu.open {
			if idx, ok := m.presetMenuIndexAt(msg.X, msg.Y); ok {
				m.presetMenu.cursor = idx
			}
			return m, nil
		}
		return m, nil

	case tea.MouseReleaseMsg:
		m.touchInteraction()
		if m.confirmUsesDialog() && !m.confirmHasInput {
			return m.updateConfirmDialog(msg)
		}
		return m, nil

	case scrollwheel.Msg[wheelTarget]:
		m.touchInteraction()
		m.applyWheelScroll(msg.Target, msg.Delta)
		return m, nil

	case actionMsg:
		// Settle first: the row may already have left the list, and a failed
		// merge may still have submitted its approval successfully.
		var settleCmd tea.Cmd
		if len(msg.approvals) > 0 {
			settleCmd = m.settleApprovals(msg.approvals)
		} else if msg.action == tuiActionApproved {
			settleCmd = m.settleApprovals([]approvalResult{
				{key: msg.key, outcome: msg.outcome, err: msg.err},
			})
		}
		if msg.action == tuiActionApproved || msg.action == tuiActionUnapproved {
			if msg.navigated || m.resolveIndex(msg.key, msg.index) < 0 {
				return m, tea.Batch(m.approvalFlash(msg), settleCmd)
			}
		}
		model, cmd := m.handleActionResult(msg)
		return model, tea.Batch(cmd, settleCmd)

	case batchActionMsg:
		var settleCmd tea.Cmd
		if len(msg.approvals) > 0 {
			settleCmd = m.settleApprovals(msg.approvals)
		} else if msg.action == tuiActionApproved {
			results := make([]approvalResult, 0, msg.count)
			for _, key := range msg.keys {
				results = append(results, approvalResult{key: key, outcome: msg.outcomes[key]})
			}
			for _, f := range msg.failures {
				results = append(results, approvalResult{key: f.key, err: f.err})
			}
			settleCmd = m.settleApprovals(results)
		}
		for _, key := range msg.keys {
			action := msg.action
			if result, ok := msg.actions[key]; ok {
				action = result
			}
			m.applyActionState(key, action)
		}
		m.resyncCursorAndOffset()
		m.selected = make(prKeys)
		status := fmt.Sprintf("%d/%d", msg.count-msg.failed, msg.count)
		if msg.failed > 0 {
			m.showInfo(renderBatchFailurePrompt(msg))
			return m, tea.Batch(settleCmd, flashResult(
				&m,
				msg.action.String(),
				status+" ("+fmt.Sprintf("%d failed", msg.failed)+")",
				"",
				true,
			))
		}
		return m, tea.Batch(
			settleCmd,
			flashResult(&m, msg.action.String(), status+" PRs", "", false),
		)

	case flash.ClearMsg:
		m.flash.Clear(msg)
		return m, nil

	case diffFetchedMsg:
		return m.handleDiffFetched(msg)

	case prStatusMsg:
		m.applyPRStatus(msg)
		if m.view == tuiViewDiff && msg.key == m.diffKey && !m.diffLoading && msg.err == nil {
			cmd := m.refreshCachedDiff(msg.key)
			return m, cmd
		}
		return m, nil

	case detailFetchedMsg:
		cmd := m.handleDetailFetched(msg)
		return m, cmd

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
		cmd := m.trackRead(func() tea.Msg {
			checks, err := actions.fetchChecksGraphQL(nodeID)
			return detailChecksRefreshedMsg{id: id, key: key, checks: checks, err: err}
		})
		return m, cmd

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
		if cached, ok := m.detailCache[msg.key]; ok {
			cached.detail.Checks = msg.checks
			m.detailCache[msg.key] = cached
		}
		m.contentRevision++
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
		status := human.Pluralize(msg.count, "PR", "PRs")
		return m, flashResult(&m, "Slacked", status, "", false)

	case jumpTimeoutMsg:
		if msg.id == m.jumpID && m.jumpDigit > 0 {
			visible := m.jumpIndices()
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
		return m, tea.Batch(tea.Raw(xansi.RequestExtendedCursorPosition), m.scheduleScreenCheck())

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
			return m, tea.Sequence(tea.ClearScreen, tea.Raw(xansi.RequestExtendedCursorPosition))
		}
		return m, nil

	case eventListReadTickMsg:
		m.eventListReadScheduled = false
		return m, nil

	case prEventsMsg:
		cmd := m.queueReviewEvents()
		return m, tea.Batch(cmd, m.eventStreams.wait)

	case eventStreamFailureMsg:
		m.streamFailed = true
		if m.autoRefresh {
			return m, m.eventStreams.wait
		}
		return m, tea.Batch(m.eventStreams.wait, tuiFlashMessage(&m,
			"Event stream unavailable for a PR (press R for periodic refresh)", true))

	case changedPRsMsg:
		cmd := m.applyChangedPRs(msg)
		return m, tea.Batch(cmd, m.refreshOpenReview())

	case tea.FocusMsg:
		m.touchInteraction()
		if m.view == tuiViewList {
			return m, m.refreshOrReschedule()
		}
		return m, nil

	case refreshTickMsg:
		if !m.autoRefresh || m.view != tuiViewList || msg.id != m.refreshID || m.refreshing {
			return m, nil
		}
		if len(m.changedPRs) > 0 || m.changedRefreshInFlight {
			return m, m.rescheduleRefresh()
		}
		return m, m.startPeriodicRefresh()

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
		m.resumeTimeCutoff = nil
		m.dismissedEmpty = false
		// Saved here, once the result is accepted as current, so writes stay
		// in order and a superseded refresh never overwrites a newer one.
		// Fresh results are authoritative; drop the optimistic client-side
		// filtering, except for approvals still in flight, before recomputing
		// cursor/offset against the new rows.
		// Render with the current stack/sort settings; a refresh may have
		// captured settings from before the options menu was applied.
		if msg.params != nil {
			m.params = msg.params
		}
		m.replaceListItems(msg.items)
		m.applyPendingActions()
		m.saveSnapshot()
		m.applyPendingApprovals()
		header, rows, widths := m.rerender()
		m = m.mergeRefresh(rows)
		m.header, m.colWidths = header, widths
		return m, tea.Batch(
			m.rescheduleRefresh(),
			m.startHeadValidation(msg.items),
			m.refreshOpenReview(),
		)

	case headValidationMsg:
		if msg.queryGen != m.queryGen {
			return m, nil
		}
		if msg.err != nil {
			clog.Debug().Err(msg.err).Msg("Head validation failed")
			return m, nil
		}
		if msg.changed && !m.refreshing && m.view == tuiViewList {
			m.invalidateRefresh()
			return m, m.startRefresh(false)
		}
		return m, nil

	case tea.PasteMsg:
		m.touchInteraction()
		if m.help != nil || m.showOptions || m.presetMenu.open {
			return m, nil
		}
		return m.doPaste(msg)

	case tea.KeyMsg:
		m.touchInteraction()
		if m.confirmUsesDialog() {
			return m.updateConfirmDialog(msg)
		}
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

// handleActionResult reports a finished single-PR action and moves the view
// on: to the next queued diff, back to the list, or by refreshing the detail.
func (m tuiModel) handleActionResult(msg actionMsg) (tea.Model, tea.Cmd) {
	idx := m.resolveIndex(msg.key, msg.index)
	if idx < 0 {
		return m, nil // PR no longer in list (removed by refresh)
	}
	pr := m.rows[idx].Item.PR
	if msg.ref == "" {
		msg.ref, msg.url = pr.Ref(), pr.URL
	}
	// HintError: action succeeded but has a follow-up hint for the user.
	hint, isHint := errors.AsType[*HintError](msg.err)
	if isHint {
		msg.err = nil // treat as success
	}
	if msg.err == nil {
		m.applyActionState(msg.key, msg.action)
		m.resyncCursorAndOffset()
	}
	flashCmd := m.actionFlash(msg)
	if hint != nil {
		cmd := styleAccent.Bold(true).Render(hint.Hint)
		m.showInfo("To also mute notifications, run:\n\n" + cmd)
	}
	// A completed action may belong to a PR the user has already navigated away from.
	if m.view == tuiViewDiff && !m.diffLoading && m.diffKey == msg.key {
		nextCmd := m.advanceOrExitDiff()
		return m, tea.Batch(flashCmd, nextCmd)
	}
	if msg.err == nil && m.view == tuiViewDetail && m.detailKey == msg.key {
		return m, tea.Batch(flashCmd, m.refetchDetail())
	}
	return m, flashCmd
}

// actionFlash renders an action's result for the status line.
func (m *tuiModel) actionFlash(msg actionMsg) tea.Cmd {
	if msg.action == tuiActionApproved || msg.action == tuiActionUnapproved {
		return m.approvalFlash(msg)
	}
	if msg.err != nil {
		return flashResult(
			m,
			msg.action.String()+" failed:",
			fmt.Sprintf("%v", withGitHubErrorDetail(msg.err)),
			"",
			true,
		)
	}
	return flashResult(m, msg.action.String(), msg.ref, msg.url, false)
}

// applyDetailMergeStatus replaces list enrichment for a single PR with the
// uncapped status fetched specifically for its detail view.
func (m *tuiModel) applyDetailMergeStatus(key prKey, status *MergeStatus) {
	if status == nil {
		return
	}

	m.updateConfirmed(key, func(pr *PullRequest) { pr.MergeStatus = *status })
	m.rerenderList()
}

func (m tuiModel) doPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.confirmUsesDialog() {
		return m.updateConfirmDialog(msg)
	}
	if m.filterInput.Focused() {
		prev := m.filterInput.Value()
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		if m.filterInput.Value() != prev {
			if vis := m.visibleIndices(); len(vis) > 0 {
				m.cursor = vis[0]
			}
			m.offset = 0
		}
		return m, cmd
	}
	return m, nil
}

func (m tuiModel) updateListView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle filter input mode.
	if m.filterInput.Focused() {
		switch msg.String() {
		case key.Enter:
			m.filterInput.Blur()
			m.offset = m.scrolledOffset()
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

	// Freeze interactions while a view is loading in the background.
	if m.detailLoading || m.diffLoading {
		switch msg.String() {
		case key.Esc, tuiKeybindQuit:
			return m.quit()
		default:
			return m, nil
		}
	}

	if stack := m.cursorStackKey(); stack != "" {
		switch msg.String() {
		case key.Left, tuiKeybindVimLeft:
			if !m.collapsedStacks[stack] {
				m.toggleStack(stack)
			}
			return m, nil
		case key.Right, tuiKeybindVimRight, key.Enter:
			if m.collapsedStacks[stack] {
				m.toggleStack(stack)
				return m, nil
			}
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
		return m.quit()

	case tuiKeybindQuit:
		return m.quit()

	case tuiKeybindFilter:
		cmd := m.filterInput.Focus()
		m.offset = m.scrolledOffset()
		return m, cmd

	case key.Enter:
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		m.refreshTerminalSize()
		cmd := m.openDetail(m.cursor, 0)
		return m, tea.Batch(requestWindowSizeCmd(), cmd)

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
			if m.isCollapsedRow(idx) {
				continue
			}
			if !m.selected[m.rowKeyAt(idx)] {
				allSelected = false
				break
			}
		}
		if allSelected {
			m.selected = make(prKeys)
		} else {
			for _, idx := range visible {
				if !m.isCollapsedRow(idx) {
					m.selected[m.rowKeyAt(idx)] = true
				}
			}
		}
		return m, nil

	case tuiKeybindInvertSelection:
		for _, idx := range m.visibleIndices() {
			if m.isCollapsedRow(idx) {
				continue
			}
			key := m.rowKeyAt(idx)
			if m.selected[key] {
				delete(m.selected, key)
			} else {
				m.selected[key] = true
			}
		}
		return m, nil

	case tuiKeybindOptions:
		m.openOptions()
		return m, nil

	case tuiKeybindHelp:
		m.help = &helpSheet{}
		return m, nil

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

	case tuiKeybindToggleRefresh:
		m.autoRefresh = !m.autoRefresh
		// Mirror into config and persist the snapshot to the state file.
		m.cfg.TUI.AutoRefresh.Enabled = m.autoRefresh
		if err := saveTUIState(m.cfg); err != nil {
			warnStateSaveErr(err, "auto-refresh setting")
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

	case key.Zero,
		key.One,
		key.Two,
		key.Three,
		key.Four,
		key.Five,
		key.Six,
		key.Seven,
		key.Eight,
		key.Nine:
		digit := int(msg.String()[0] - '0')
		if m.jumpDigit > 0 {
			// Second digit: combine with first (e.g. 1,0 -> row 10).
			target := m.jumpDigit*10 + digit - 1
			m.jumpDigit = 0
			visible := m.jumpIndices()
			if target >= 0 && target < len(visible) {
				m.cursor = visible[target]
				m.offset = m.scrolledOffset()
			}
			return m, nil
		}
		visible := m.jumpIndices()
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

func (m tuiModel) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if m.session != nil {
		_ = m.session.lock.Close()
		_ = os.RemoveAll(m.session.dir)
		m.session = nil
	}
	return m, tea.Quit
}

func (m tuiModel) View() tea.View {
	v := m.renderView()
	v.ReportFocus = true
	if m.session != nil && m.session.lastPosition != nil {
		v.ProgramResume = m.session.command
	}
	return v
}

func (m tuiModel) renderView() tea.View {
	switch m.view {
	case tuiViewDiff:
		return m.viewDiff()
	case tuiViewDetail:
		return m.viewDetail()
	case tuiViewList:
	}
	return m.viewList()
}

func (m tuiModel) viewList() tea.View {
	screen := m.ensureScreen()
	visible := m.listLines()
	var viewport int
	var footer bloom.Child
	junction := -1
	if m.filterInput.Focused() {
		footer, viewport, junction = m.filterFooter(visible)
	} else {
		viewport = m.listViewport()
		footer = m.shortcutFooter(m.listHelpPairs(), m.listStatus(visible, viewport))
	}
	rowWidth := m.width
	if len(visible) > viewport {
		rowWidth = max(0, rowWidth-tuiScrollbarWidth)
	}
	indexWidth := m.listIndexWidth()
	indexPad := strings.Repeat(" ", indexWidth)
	indexGap := m.indexGap()
	rowNode := func(line string) bloom.TextNode {
		if rowWidth < m.width {
			line = m.reviewWidthMethod().Truncate(line, rowWidth, valueEllipsis)
		}
		return bloom.Text(layout.ExpandTabs(line)).NoWrap()
	}

	var rows []bloom.Child
	if m.header != "" {
		prefix := tuiNonCursorPrefix + indexPad + indexGap
		if m.refreshing && len(m.spinner.frames) > 0 {
			frame := m.spinner.frames[m.spinnerTick%len(m.spinner.frames)]
			prefix = frame + " " + indexPad + indexGap
		}
		rows = append(rows, bloom.Fixed(1, rowNode(prefix+m.header)))
	}

	rowHover := lg.NewStyle().Transform(func(line string) string {
		return layout.PreserveBackgroundWidth(line, hoverBG, rowWidth)
	})
	listHeight := viewport + m.listHeaderAndFilterRows()
	hoverEnabled := m.canOpenPresetMenu(listHeight) && !m.presetMenu.open
	listRow := func(line string, idx, pos int) bloom.Child {
		node := rowNode(line)
		if hoverEnabled && idx >= 0 && idx != m.cursor {
			// Hover belongs to the screen row when scrolling changes the PR underneath it.
			node = node.ID(bloom.ID("pr-row/" + strconv.Itoa(pos-m.offset))).HoverStyle(rowHover)
		}
		return node
	}
	var body bloom.Child = bloom.List(len(visible), func(pos int) bloom.Child {
		entry := visible[pos]
		idx := entry.row
		index := m.renderTuiIndexWidth(entry.label(), m.selected[m.rowKeyAt(idx)], indexWidth)
		if entry.header != "" {
			folded := m.collapsedStacks[entry.stackKey]
			arrowStyle := styleStackArrow
			if folded {
				arrowStyle = styleHeading
			}
			headerStyle := styleStackHeader.Inherit(arrowStyle).Italic(folded)
			line := tuiNonCursorPrefix + index + indexGap + arrowStyle.Render(
				entry.tree,
			) + headerStyle.Render(
				entry.header,
			)
			if rowWidth > 0 {
				line = xansi.Truncate(line, rowWidth, valueEllipsis)
			}
			if idx == m.cursor {
				line = m.styles.cursor.Render(
					tuiCursorPrefix,
				) + strings.TrimPrefix(
					line,
					tuiNonCursorPrefix,
				)
				line = layout.PreserveBackgroundWidth(line, cursorLineBG, rowWidth)
			}
			return listRow(line, idx, pos)
		}
		tree := entry.tree
		if m.p != nil && m.p.theme != nil && m.p.theme.MarkdownText != nil {
			tree = m.p.theme.MarkdownText.Render(tree)
		}
		display := index + indexGap + tree + m.rows[idx].Display
		if idx != m.cursor {
			return listRow(m.rowGutter(idx)+display, idx, pos)
		}
		line := m.rowCursor(idx) + display
		// Inject background color throughout the line so it persists
		// through existing ANSI codes in the display string.
		// Skip highlight when there's only one visible result.
		if len(visible) > 1 {
			bg := cursorLineBG
			if m.selected[m.rowKeyAt(idx)] {
				bg = cursorLineSelectedBG
			}
			line = layout.PreserveBackgroundWidth(line, bg, rowWidth)
		}
		return rowNode(line)
	}).Offset(m.offset).ID(prListID)
	if len(visible) > viewport {
		body = bloom.Row(
			bloom.Fill(body),
			bloom.Fixed(tuiScrollbarWidth, screen.listScrollbar()),
		)
	}
	if len(visible) == 0 && !m.dismissedEmpty && m.flash.Msg == "" && m.help == nil &&
		!m.showOptions && !m.confirmUsesDialog() {
		body = bloom.Overlay(body, m.emptyLayer(viewport))
	}
	rows = append(rows, bloom.Fixed(viewport, body))
	filterVal := m.filterInput.Value()
	if m.filterInput.Focused() || filterVal != "" {
		input := styleHeading.Bold(true).Render("/") + m.filterInput.View()
		rows = append(rows, bloom.Fixed(1, bloom.Text(input).NoWrap()))
	}
	var list bloom.Child = bloom.Column(rows...)

	presets := m.presetControls()
	var page bloom.Child = bloom.Column(
		bloom.Fixed(listHeight, list),
		bloom.Fixed(1, m.listSeparator(junction, presets, hoverEnabled && m.showPresetHover)),
		bloom.Fill(footer),
	)
	switch {
	case m.help != nil:
		page = bloom.Overlay(page, m.helpLayer())
	case m.showOptions:
		page = bloom.Overlay(page, m.filterLayer())
	case m.confirmUsesDialog() && screen.dialog != nil:
		page = bloom.Overlay(page, m.confirmLayer())
	case m.presetMenu.open:
		page = bloom.Overlay(page, m.presetMenuLayer(presets))
	}
	v := tea.NewView(screen.root.Render(m.width, m.height, page))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	m.applyRepaintMarker(&v)
	return v
}

// rowCursor renders the cursor prefix for row idx, in the approval check's
// green when the viewer has approved it.
func (m tuiModel) rowCursor(idx int) string {
	if isViewerApproved(m.rows[idx].Item.PR) {
		return m.styles.statusOK.Bold(true).Render(tuiCursorPrefix)
	}
	return m.styles.cursor.Render(tuiCursorPrefix)
}

// rowGutter returns the prefix for a non-cursor row: a check mark when the
// viewer has approved the PR, otherwise blank.
func (m tuiModel) rowGutter(idx int) string {
	if !isViewerApproved(m.rows[idx].Item.PR) {
		return tuiNonCursorPrefix
	}
	mark := activeIcons.Check
	pad := max(0, lg.Width(tuiNonCursorPrefix)-lg.Width(mark))
	return m.styles.statusOK.Render(mark) + strings.Repeat(" ", pad)
}

func (m tuiModel) viewDiff() tea.View {
	var header bloom.Child
	headerStyle := styleLabel.Bold(true)
	if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
		pr := m.rows[idx].Item.PR
		var headerLine string
		if m.diffQueueTotal > 0 {
			headerLine = headerStyle.Render(
				fmt.Sprintf("[%d/%d]", m.diffQueuePos, m.diffQueueTotal),
			) +
				" "
		}
		ref := fmt.Sprintf("%s#%d", pr.Repository.NameWithOwner, pr.Number)
		headerLine += xansi.Force().Hyperlink(pr.URL,
			headerStyle.Render(ref)+styleText.Render(" » ")+
				styleTitle.Render(m.p.displayText(pr.Title)))
		author := m.rows[idx].Item.Author
		if author.Display != "" {
			headerLine += styleDim.Render(dotSeparator) + xansi.Force().Hyperlink(author.URL,
				m.p.authorStyle(author).Render(author.Display))
		}
		status := m.renderPRStatusLine(pr)
		if m.reviewOutdated() {
			status = "Diff outdated"
		}
		header = diffHeader(headerLine, status)
	}
	return m.renderFullScreenView(
		header,
		m.diffView,
		m.diffHelpPairs(),
	)
}

// diffHeaderMinTitle is the narrowest the title may be truncated to before the
// right-aligned status is dropped.
const diffHeaderMinTitle = 20

func diffHeader(title, status string) bloom.Child {
	const gap = 2
	label := bloom.Text(title).Truncate()
	return bloom.LayoutFunc(func(ctx bloom.Context) bloom.Child {
		if status == "" ||
			ctx.Width() < diffHeaderMinTitle+gap+ctx.WidthMethod().StringWidth(status) {
			return label
		}
		return bloom.Row(bloom.Fill(label), bloom.Text(status).NoWrap()).Gap(gap)
	})
}

// renderPRStatusLine summarises a PR's CI, review and viewer-approval state
// as pills for the diff header.
func (m tuiModel) renderPRStatusLine(pr PullRequest) string {
	open := strings.EqualFold(pr.State, valueOpen)
	// "Needs your review" already implies both "Needs review" and a required
	// review, so those pills are dropped alongside it.
	needsViewer := needsReview(pr) && !m.isCurrentUserPR(pr) &&
		pr.viewerApprovalLoaded && !pr.viewerApproved
	var parts []string
	if !needsViewer || resolvePRStatus(pr) != resolvedBlocked {
		if badge := m.renderDetailStatus(pr); badge != "" {
			parts = append(parts, badge)
		}
	}
	if open {
		if badge := renderReviewBadge(pr, needsViewer); badge != "" {
			parts = append(parts, badge)
		}
		switch {
		case m.isCurrentUserPR(pr):
			parts = append(parts, statusBadge("Your PR", "", colorGitHubDraft, styleDim))
		case isViewerApproved(pr):
			parts = append(parts, statusBadge(
				"Approved by you", activeIcons.Check, colorGitHubOpen, styleGreen,
			))
		case needsViewer:
			// Blue rather than amber, so it stands apart from the status pills.
			parts = append(parts, statusBadge(
				"Needs your review", "", colorGitHubAccent,
				lg.NewStyle().Foreground(colorGitHubAccent),
			))
		}
	}
	// Pills are self-delimiting; plain-text badges need a separator.
	sep := " "
	if activeIcons.PillLeft == "" {
		sep = styleDim.Render(dotSeparator)
	}
	return strings.Join(parts, sep)
}

// renderReviewBadge renders the PR's review decision, unless another pill
// already says the same thing.
func renderReviewBadge(pr PullRequest, needsViewer bool) string {
	if !pr.reviewDecisionLoaded {
		return ""
	}
	status := resolvePRStatus(pr)
	switch pr.ReviewDecision {
	case valueReviewRequired:
		if needsViewer || status == resolvedBlocked {
			return ""
		}
		return statusBadge("Review required", "", colorGitHubAttention, styleWarning)
	case valueReviewApproved:
		if status == resolvedReady {
			return ""
		}
		return statusBadge("Approved", "", colorGitHubOpen, styleGreen)
	case valueReviewChanges:
		if status == resolvedBlocked {
			return ""
		}
		return statusBadge("Changes requested", "", colorGitHubClosed, styleClosed)
	}
	return ""
}

// The rule fills the space to the left of the clickable controls. A focused
// filter joins it to the help line below at junction.
func (m tuiModel) listSeparator(junction int, presets presetControls, hover bool) bloom.Child {
	separator := m.styles.separator
	method := m.reviewWidthMethod()
	if m.cli == nil {
		return bloom.Text(separator.Render(layout.Separator(max(0, m.width), junction))).NoWrap()
	}
	room := m.width - method.StringWidth(presetIndicatorPrefix+presetIndicatorSuffix)
	if room <= 0 {
		return bloom.Text(separator.Render(method.Truncate(presetIndicatorSuffix, max(0, m.width), ""))).
			NoWrap()
	}
	rule := bloom.LayoutFunc(func(ctx bloom.Context) bloom.Child {
		return bloom.Text(separator.Render(layout.Separator(ctx.Width(), junction))).NoWrap()
	})
	parts := []bloom.Child{bloom.Fill(rule), bloom.Text(separator.Render(presetIndicatorPrefix))}
	gap := bloom.Text(separator.Render(timePresetSeparator))
	gapWidth := method.StringWidth(timePresetSeparator)
	room -= method.StringWidth(presets.pr + presets.time)
	if presets.time != "" && presets.pr != "" {
		room -= gapWidth
	}
	if presets.pr != "" && room > gapWidth {
		if tags := m.filterTagText(room - gapWidth); tags != "" {
			parts = append(parts, footerButton(tags, filterTagsID, hover), gap)
		}
	}
	if presets.time != "" {
		parts = append(parts, footerButton(presets.time, timePresetID, hover))
		if presets.pr != "" {
			parts = append(parts, gap)
		}
	}
	if presets.pr != "" {
		parts = append(parts, footerButton(presets.pr, prPresetID, hover))
	}
	parts = append(parts, bloom.Text(separator.Render(presetIndicatorSuffix)))
	return bloom.Row(parts...)
}

func (m tuiModel) filterTagText(width int) string {
	style := styleFilterTag.Faint(false)
	var labels []string
	for _, tag := range m.activeFilterTags() {
		if key, value, ok := strings.Cut(tag, ":"); ok {
			labels = append(labels, style.Bold(true).Render(key)+style.Render(":"+value))
		} else {
			labels = append(labels, style.Bold(true).Render(tag))
		}
	}
	if len(labels) == 0 {
		return ""
	}
	method := m.reviewWidthMethod()
	return method.Truncate(
		strings.Join(labels, style.Faint(true).Render(dotSeparator)),
		width,
		valueEllipsis,
	)
}

func (m tuiModel) viewDetail() tea.View {
	return m.renderFullScreenView(
		nil,
		m.detailView,
		m.detailHelpPairs(),
	)
}

func (m tuiModel) renderFullScreenView(
	header bloom.Child,
	vp *reviewViewport,
	hints []key.Hint,
) tea.View {
	if vp == nil {
		vp = m.reviewViewport(nil, "", "", bloom.WrapNone, 0)
	}
	chrome := 1 + m.helpLines(hints) // separator and footer
	if header != nil {
		chrome += 2 // header and separator
	}
	vp.height = max(0, m.height-chrome)
	geo := vp.geometry()
	status := ""
	if m.flash.Msg != "" {
		status = m.renderFlashStatus()
	} else if geo.TotalRows > geo.Rows {
		end := min(geo.Row+geo.Rows, geo.TotalRows)
		pct := int(math.Round(geo.Percent() * 100)) //nolint:mnd // percent
		status = m.styles.statusOK.Render(
			fmt.Sprintf("%d-%d/%d (%d%%)", geo.Row+1, end, geo.TotalRows, pct),
		)
	}

	var body bloom.Child = vp.node()
	if m.slide.progress.Animating() && vp.width > 0 && vp.height > 0 {
		body = m.slide.layout(vp.width, vp.height, body)
	}
	bar := bloom.ScrollbarOf(reviewBodyID).Style(reviewBarStyle())
	separator := bloom.Fixed(
		1,
		bloom.Text(m.styles.separator.Render(layout.Separator(max(0, m.width), -1))).NoWrap(),
	)
	var rows []bloom.Child
	if header != nil {
		rows = append(rows, bloom.Fixed(1, header), separator)
	}
	rows = append(rows,
		bloom.Fixed(vp.height, bloom.Row(bloom.Fill(body), bloom.Fixed(tuiScrollbarWidth, bar))),
		separator, bloom.Fill(m.shortcutFooter(hints, status)),
	)
	var page bloom.Child = bloom.Column(rows...)
	root := vp.root
	if m.confirmUsesDialog() && m.screen != nil && m.screen.dialog != nil {
		page = bloom.Overlay(page, m.confirmLayer())
		root = m.screen.root
	}
	v := tea.NewView(root.Render(m.width, m.height, page))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	m.applyRepaintMarker(&v)
	return v
}

func requestWindowSizeCmd() tea.Cmd {
	return tea.RequestWindowSize
}

func (m *tuiModel) refreshTerminalSize() {
	width, height := xterminal.Size(os.Stdout)
	if width <= 0 || height <= 0 {
		return
	}
	if width == m.width && height == m.height {
		return
	}
	m.applyWindowSize(width, height)
}

// applyModeReport follows bubbletea's own reaction to a mode 2027 report: once
// the terminal answers, bubbletea measures its cells in grapheme clusters, so
// prl has to pad rows the same way or every VS16 emoji shifts its row by a
// cell. The values accepted here mirror bubbletea's.
func (m tuiModel) applyModeReport(msg tea.ModeReportMsg) tuiModel {
	if msg.Mode != cansi.ModeUnicodeCore || m.p.grapheme {
		return m
	}
	if msg.Value != cansi.ModeReset &&
		msg.Value != cansi.ModeSet &&
		msg.Value != cansi.ModePermanentlySet {
		return m
	}
	m.p = m.p.withGraphemeWidth()
	if m.screen != nil {
		m.screen.resetRoot(m.reviewWidthMethod())
	}
	m.applyWindowSize(m.width, m.height)
	return m
}

func (m *tuiModel) applyWindowSize(width, height int) {
	m.width = width
	m.height = height
	m.header, m.rows, m.colWidths = m.rerender()
	if m.view == tuiViewList {
		m.offset = m.scrolledOffset()
	}
	if m.view == tuiViewDiff {
		m.syncDiffView()
	}
	if m.view == tuiViewDetail && len(m.detailLines) > 0 {
		m.detailLines = m.renderDetailContent()
		m.syncDetailView()
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
	styledAuthor := xansi.Force().Hyperlink(authorLink, styleText.Render(authorDisplay))
	lines = append(
		lines,
		detailIndent+labelStyle.Render(
			"  Title: ",
		)+xansi.Force().Hyperlink(pr.URL, styleText.Render(
			m.p.displayText(pr.Title),
		)),
	)
	lines = append(lines, detailIndent+labelStyle.Render(" Author: ")+styledAuthor)
	styledURL := xansi.Force().Hyperlink(pr.URL, styleText.Render(pr.URL))
	lines = append(lines, detailIndent+labelStyle.Render("    URL: ")+styledURL)
	if len(m.detail.Reviews) > 0 {
		var parts []string
		for _, r := range m.detail.Reviews {
			name := m.resolver.Resolve(r.User)
			var link string
			if isAuthorBot(r.User) {
				link = "https://github.com/apps/" + strings.TrimSuffix(r.User, "[bot]")
			} else {
				link = "https://github.com/" + r.User
			}
			styled := xansi.Force().Hyperlink(link, styleText.Render(name))
			parts = append(parts, renderReviewIcon(r)+" "+styled)
		}
		lines = append(lines, detailIndent+labelStyle.Render("Reviews: ")+
			strings.Join(parts, dotSeparator))
	}
	lines = append(lines, detailIndent+labelStyle.Render(" Status: ")+m.renderDetailStatus(pr))
	// The Outdated badge already says so.
	if m.detail.MergeableState == valueBehind && resolvePRStatus(pr) != resolvedBehind {
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
			var iconStyle lg.Style
			switch {
			case c.Status != ciStatusCompleted:
				icon = activeIcons.CIInProgress
				iconStyle = styleWarning
			case c.Conclusion == ciStatusSuccess:
				icon = activeIcons.Approved
				iconStyle = styleOK
			case c.Conclusion == ciStatusFailure:
				icon = activeIcons.Rejected
				iconStyle = styleDanger
			case c.Conclusion == ciConclusionCancelled:
				icon = activeIcons.Dismissed
				iconStyle = styleDismiss
			case c.Conclusion == ciConclusionSkipped:
				icon = activeIcons.CISkipped
				iconStyle = styleDim
			case c.Conclusion == ciConclusionTimedOut:
				icon = activeIcons.CITimedOut
				iconStyle = styleDanger
			case c.Conclusion == ciConclusionActionRequired:
				icon = activeIcons.CIActionRequired
				iconStyle = styleWarning
			case c.Conclusion == ciConclusionNeutral:
				icon = activeIcons.CINeutral
				iconStyle = styleDim
			case c.Conclusion == ciConclusionStale:
				icon = activeIcons.CIStale
				iconStyle = styleDim
			default:
				icon = activeIcons.CIUnknown
				iconStyle = styleDim
			}
			icon = iconStyle.Render(icon)
			line := fmt.Sprintf("%s%s %s", detailIndent, icon, c.Name)
			if c.Duration > 0 {
				line += " " + styleCheckDur.Render(
					fmt.Sprintf("[%s]", c.Duration.Round(time.Second)),
				)
			}
			lines = append(lines, m.p.displayText(line))
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
			case valueAdded:
				prefix = styleGreen.Bold(true).Render("A")
			case valueRemoved:
				prefix = styleRed.Bold(true).Render("D")
			case valueRenamed:
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

func renderReviewIcon(review PRReview) string {
	icon := activeIcons.Commented
	switch review.State {
	case valueReviewApproved:
		if activeIcons == iconsNerd {
			return styleOK.Render(activeIcons.Approved)
		}
		return activeIcons.Approved
	case valueReviewChanges:
		icon = activeIcons.Rejected
	case valueReviewDismissed:
		icon = activeIcons.Dismissed
	}
	if isAuthorBot(review.User) && icon == activeIcons.Commented {
		return activeIcons.Copilot
	}
	return icon
}

func (m tuiModel) renderDetailStatus(pr PullRequest) string {
	switch resolvePRStatus(pr) {
	case resolvedDraft, resolvedDraftCIFail:
		return statusBadge("Draft", activeIcons.StatusDraft, colorGitHubDraft, styleDraftLbl)
	case resolvedMerged:
		return statusBadge("Merged", activeIcons.StatusMerged, colorGitHubDone, styleMerged)
	case resolvedClosed:
		return statusBadge("Closed", activeIcons.StatusClosed, colorGitHubClosed, styleClosed)
	case resolvedReady:
		return statusBadge("Ready to merge", activeIcons.StatusReady, colorGitHubOpen, styleGreen)
	case resolvedCIPending:
		return statusBadge(
			"CI pending",
			activeIcons.StatusCIPending,
			colorGitHubAttention,
			styleWarning,
		)
	case resolvedCIFailed:
		return statusBadge("CI failed", activeIcons.StatusCIFailed, colorGitHubClosed, styleClosed)
	case resolvedCIError:
		return statusBadge("CI error", activeIcons.StatusCIFailed, colorGitHubClosed, styleClosed)
	case resolvedBlocked:
		if pr.reviewDecisionLoaded && pr.ReviewDecision == valueReviewChanges {
			return statusBadge("Changes requested", "", colorGitHubClosed, styleClosed)
		}
		return statusBadge(
			"Needs review",
			activeIcons.StatusNeedsReview,
			colorGitHubAttention,
			styleWarning,
		)
	case resolvedConflict:
		return statusBadge(
			"Merge conflicts",
			activeIcons.StatusConflict,
			colorGitHubClosed,
			styleHeading,
		)
	case resolvedBehind:
		return statusBadge(
			"Outdated",
			activeIcons.StatusConflict,
			colorGitHubAttention,
			styleWarning,
		)
	case resolvedUnknown:
		return statusBadge("Unknown", activeIcons.StatusUnknown, colorGitHubDraft, styleDim)
	}
	return ""
}

func statusBadge(label, glyph string, background color.Color, fallback lg.Style) string {
	text := statusPrefix(glyph) + label
	if activeIcons.PillLeft == "" {
		return fallback.Render(text)
	}
	body := lg.NewStyle().
		Foreground(colorGitHubOnEmphasis).
		Background(background).
		Bold(true).
		Render(text)
	return pillWrap(body, background, false)
}

func statusPrefix(glyph string) string {
	if glyph == "" {
		return ""
	}
	return glyph + " "
}

const (
	detailIndent     = "  "
	defaultTermWidth = 80
)

func (m tuiModel) renderMarkdown(body string) []string {
	width := m.width
	if width <= 0 {
		width = defaultTermWidth
	}
	rendered := render.Markdown(body, width, markdownStyle())
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

// isRowHidden reports whether the row at idx is excluded from the list:
// hidden while a refresh catches up, or outside the active filters.
func (m tuiModel) isRowHidden(idx int) bool {
	return m.rowHiddenBy(m.displayFilter(), idx)
}

func (m tuiModel) rowHiddenBy(df displayFilter, idx int) bool {
	key := m.rowKeyAt(idx)
	return m.filterHidden[key] || !m.shows(df, m.rows[idx].Item)
}

func (m tuiModel) visibleIndices() []int {
	indices := make([]int, 0, len(m.rows))
	f := strings.TrimSpace(m.filterInput.Value())
	df := m.displayFilter()
	if f == "" {
		for i := range m.rows {
			if !m.rowHiddenBy(df, i) {
				indices = append(indices, i)
			}
		}
		return m.visibleStackIndices(indices)
	}

	term := filter.Parse(f)
	for i := range m.rows {
		if m.rowHiddenBy(df, i) {
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
	return m.visibleStackIndices(indices)
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
	visible := m.listLines()
	viewport := m.listViewportFor(visible)
	pos := m.cursorLinePosition(visible)
	top := pos
	if pos > 0 && viewport > 1 && visible[pos-1].row < 0 {
		top-- // keep a stack header visible with its first member
	}
	offset := m.offset
	if top < offset {
		offset = top
	} else if pos >= offset+viewport {
		offset = pos - viewport + 1
	}
	// Clamp so we don't leave blank space at the bottom.
	offset = min(offset, max(0, len(visible)-viewport))
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
				indicator := " " + activeIcons.SortDescending
				if sortAsc {
					indicator = " " + activeIcons.SortAscending
				}
				if activeIcons == iconsNerd {
					return rendered + styleStackArrow.Render(indicator)
				}
				return rendered + ctx.Theme.RenderDim(indicator)
			},
		))
	}
	width := max(0, m.width-m.tablePrefixWidth(totalRows))
	return m.p.newTableRenderer(m.cli, m.tty, width, opts...)
}

func (m tuiModel) tableRenderer() *table.Renderer[PRRowModel] {
	return m.tableRendererFor(len(m.rows))
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
		sampleWidths[i] = estimatedColumnWidth(col.Name, compact)
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
	termWidth := max(0, m.width-m.tablePrefixWidth(len(m.items)))
	renderer := m.tableRendererFor(len(m.items))
	if !m.stacksEnabled() {
		return renderTUITable(renderer, m.items, m.sortColumn, m.sortAsc, termWidth)
	}
	header, rows, widths := renderTUITable(renderer, m.items, "", false, termWidth)
	rows = groupStackRows(rows, renderer.Columns(), m.sortColumn, m.sortAsc)
	if m.stackExtraWidth() > 0 {
		header = strings.Repeat(" ", stackTreeWidth) + header
	}
	return header, rows, widths
}

func (m tuiModel) currentPR() *PullRequest {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.isRowHidden(m.cursor) ||
		m.isCollapsedRow(m.cursor) {
		return nil
	}
	pr := m.rows[m.cursor].Item.PR
	return &pr
}

func (m *tuiModel) toggleCurrentSelection() bool {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.isRowHidden(m.cursor) ||
		m.isCollapsedRow(m.cursor) {
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
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.isRowHidden(m.cursor) {
		return
	}
	if !m.isCollapsedRow(m.cursor) {
		m.selected[m.rowKeyAt(m.cursor)] = true
	}
	next, ok := m.nextVisible(dir)
	if !ok {
		return
	}
	m.cursor = next
	m.offset = m.scrolledOffset()
	if !m.isCollapsedRow(m.cursor) {
		m.selected[m.rowKeyAt(m.cursor)] = true
	}
}

func (m tuiModel) listHelpPairs() []key.Hint {
	pairs := []key.Hint{
		{Key: key.Enter, Desc: tuiHelpShow},
		{Key: key.Space, Desc: tuiHelpSelect},
		{Key: tuiKeybindFilter, Desc: tuiHelpFilter},
	}
	ctx, ok := m.actionContextForCursor()
	actionable := ok && ctx.actionable
	if targets, _, remove := m.approvalToggleTargets(); len(targets) > 0 {
		pairs = append(pairs, key.Hint{Key: tuiKeybindApprove, Desc: approvalHelp(remove)})
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
	if actionable && hasAIReviewLauncher() {
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

func (m tuiModel) diffHelpPairs() []key.Hint {
	pairs := []key.Hint{
		{Key: key.ArrowsVertical, Desc: tuiHelpScroll},
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
		reason := m.approvalBlockReason(ctx.key, ctx.pr)
		if reason != resultApprovalPending {
			pairs = append(
				pairs,
				key.Hint{Key: tuiKeybindApprove, Desc: approvalHelp(isViewerApproved(ctx.pr))},
			)
			pairs = append(pairs, key.Hint{Key: tuiKeybindApproveMerge, Desc: tuiHelpApproveMerge})
		}
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

	if actionable && hasAIReviewLauncher() {
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
		{Key: key.ArrowsVertical, Desc: tuiHelpScroll},
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
	if actionable && !ctx.draft && !ctx.ownPR &&
		m.approvalBlockReason(ctx.key, ctx.pr) != resultApprovalPending {
		pairs = append(
			pairs,
			key.Hint{Key: tuiKeybindApprove, Desc: approvalHelp(isViewerApproved(ctx.pr))},
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
	if actionable && hasAIReviewLauncher() {
		pairs = append(pairs, key.Hint{Key: tuiKeybindReview, Desc: tuiHelpReview})
	}
	if actionable && !ctx.draft {
		pairs = append(pairs, key.Hint{Key: tuiKeybindCopilotReview, Desc: tuiHelpCopilot})
	}
	prev, next := m.detailNeighbors()
	if prev >= 0 {
		pairs = append(pairs, key.Hint{Key: tuiKeybindPrev, Desc: tuiHelpPrev})
	}
	if next >= 0 {
		pairs = append(pairs, key.Hint{Key: tuiKeybindNext, Desc: tuiHelpNext})
	}
	pairs = append(pairs, key.Hint{Key: tuiKeybindQuit, Desc: tuiHelpDismiss})
	return pairs
}

var flashLineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", nl, " ")

// The footer reserves one row for status, including multiline API errors.
func (m tuiModel) renderFlashStatus() string {
	if m.flash.Msg == "" {
		return ""
	}
	style := m.styles.statusOK
	if m.flash.Err {
		style = m.styles.statusErr
	}
	return style.Render(flashLineBreaks.Replace(m.flash.Msg))
}

// listStatus is the list footer's right-aligned status: a flash message, or
// the range of displayed group numbers when the list overflows the viewport.
func (m tuiModel) listStatus(lines []tuiListLine, viewport int) string {
	if m.flash.Msg != "" {
		return m.renderFlashStatus()
	}
	total := len(lines)
	if total <= viewport {
		return ""
	}
	start := min(m.offset, total-viewport)
	end := start + viewport - 1
	position := scrollbar.Position(lines[start].group-1, lines[end].group, lines[total-1].group)
	return m.styles.statusOK.Render(position)
}

func (m tuiModel) helpBar(pairs []key.Hint) helpbar.Model {
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
	}
}

// helpLines returns the number of lines the help bar occupies at the current width.
func (m tuiModel) helpLines(pairs []key.Hint) int {
	return m.helpBar(pairs).Lines()
}

// hasFilteredRows reports whether the query returned any loaded PRs.
func (m tuiModel) hasFilteredRows() bool {
	return len(m.rows) > 0
}

func (m tuiModel) clearConfirm() tuiModel {
	m.confirmAction = ""
	m.confirmPrompt = ""
	m.confirmSubject = ""
	m.confirmURL = ""
	m.confirmCmd = nil
	m.confirmCmdFn = nil
	m.confirmOnAccept = nil
	m.confirmHasInput = false
	m.confirmInputLabel = ""
	m.confirmOptions = nil
	m.confirmOptionValues = nil
	m.confirmReviewPR = nil
	m.confirmInputValue = ""
	m.confirmInputPlaceholder = ""
	return m
}

// styledRef returns a bold, hyperlinked PR ref for use in confirm prompts.
func styledRef(pr *PullRequest) string {
	ref := styleRef.Bold(true).Render(pr.Ref())
	return xansi.Force().Hyperlink(pr.URL, ref)
}

// overlayCenter places a box on top of a background string, centered.

// highlightDiff highlights a unified diff using delta if available,
// falling back to Chroma syntax highlighting.
func highlightDiff(raw, prURL, headSHA string) string {
	repoURL, _, ok := strings.Cut(prURL, "/pull/")
	if !ok {
		return render.DiffStyled(raw, render.DiffOptions{Dark: !paletteIsLight})
	}
	return render.DiffStyled(raw, render.DiffOptions{
		RepoURL:   repoURL,
		CommitSHA: headSHA,
		Dark:      !paletteIsLight,
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
// cursor position and selections by matching on PR identity.
func (m tuiModel) mergeRefresh(newRows []TableRow) tuiModel {
	oldRows := m.rows

	// Try to keep cursor on the same PR.
	cursorKey := prKey("")
	if m.cursor >= 0 && m.cursor < len(oldRows) {
		cursorKey = makePRKey(oldRows[m.cursor].Item.PR)
	}
	screenLine := m.cursorLinePosition(m.listLines()) - m.offset
	newCursor := min(m.cursor, max(0, len(newRows)-1))
	for i, row := range newRows {
		if makePRKey(row.Item.PR) == cursorKey {
			newCursor = i
			break
		}
	}

	m.rows = newRows
	m.cursor = newCursor
	m.restoreFoldedCursor()
	m.cursor = m.adjustedCursor()
	if m.offset > 0 {
		m.offset = max(0, m.cursorLinePosition(m.listLines())-screenLine)
	}
	m.offset = m.scrolledOffset()
	return m
}

// reindexRows remaps the cursor from oldRows order to newRows order using PR identity.
func (m tuiModel) reindexRows(oldRows, newRows []TableRow) tuiModel {
	screenLine := m.cursorLinePosition(m.listLines()) - m.offset
	if m.cursor >= 0 && m.cursor < len(oldRows) {
		key := makePRKey(oldRows[m.cursor].Item.PR)
		for i, row := range newRows {
			if makePRKey(row.Item.PR) == key {
				m.cursor = i
				break
			}
		}
	}
	m.cursor = min(m.cursor, max(0, len(newRows)-1))
	m.rows = newRows
	m.restoreFoldedCursor()
	m.cursor = m.adjustedCursor()
	if m.offset > 0 {
		m.offset = max(0, m.cursorLinePosition(m.listLines())-screenLine)
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
// Returns the index into m.rows and ok=true if y is on a visible row. Lines
// outside the viewport (header, filter bar, separator, help) map to no row,
// even when rows are scrolled out of view above or below them.
func (m tuiModel) listEntryAt(y int) (tuiListLine, bool) {
	if m.screen == nil {
		return tuiListLine{}, false
	}
	frame := m.screen.root.Frame()
	rect, _ := frame.Rect(prListID)
	list, ok := frame.List(prListID)
	local := y - rect.Min.Y
	if !ok || local < 0 || local >= list.Visible || y >= rect.Max.Y {
		return tuiListLine{}, false
	}
	lines := m.listLines()
	pos := list.First + local
	if pos >= len(lines) {
		return tuiListLine{}, false
	}
	return lines[pos], true
}

func (m tuiModel) rowIndexAt(y int) (int, bool) {
	entry, ok := m.listEntryAt(y)
	return entry.row, ok && entry.row >= 0
}

// toggleSort cycles a column through: default direction -> reverse -> off.
// Clicking a different column activates it with its default direction.
func (m tuiModel) toggleSort(col string) (tea.Model, tea.Cmd) {
	sortable := m.sortableColumns()
	if !slices.Contains(sortable, col) {
		return m, nil
	}

	oldRows := m.rows
	if m.sortColumn == col {
		// Already sorted by this column: cycle direction -> off.
		if m.sortAsc == defaultSortAsc(col) {
			// Currently default direction -> reverse.
			m.sortAsc = !m.sortAsc
		} else {
			// Currently reversed -> turn off.
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

	// Mirror sort settings into config and persist the snapshot to state.
	order := ""
	if m.sortColumn != "" {
		order = valueDesc
		if m.sortAsc {
			order = valueAsc
		}
	}
	m.cfg.TUI.Sort.Key = m.sortColumn
	m.cfg.TUI.Sort.Order = order
	if err := saveTUIState(m.cfg); err != nil {
		warnStateSaveErr(err, "sort settings")
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
	case valueUpdated, valueCreated:
		return false
	default:
		return true
	}
}

// runTui launches the interactive TUI.
func runTui(
	p *prl,
	clients *githubClients,
	cli *CLI,
	cfg *Config,
	tty bool,
	terminalEnv []string,
	s spinner,
) error {
	// Overlay persisted TUI state (sort, filters, auto-refresh) onto config so
	// the user's last interactive choices take precedence over config defaults.
	if st, err := loadTUIState(); err != nil {
		clog.Warn().Err(err).Msg("Failed to load TUI state")
	} else {
		applyTUIState(cfg, st)
	}

	// Apply persisted TUI filter defaults before the initial fetch.
	var saved *savedSession
	if cli.ResumeSession != "" {
		if root, rootErr := sessionRoot(); rootErr == nil {
			saved = loadSession(root, cli.ResumeSession)
		}
	}
	var params *SearchParams
	if saved != nil {
		saved.applySettings(cli, cfg)
	} else {
		applyTUIFilterDefaults(cli, cfg)
		var err error
		params, err = buildSearchQuery(cli, cfg)
		if err != nil {
			return err
		}
	}

	resolver := NewAuthorResolver(cfg)
	gql, err := clients.graphQL()
	if err != nil {
		return err
	}

	type fetchResult struct {
		rows      []TableRow
		items     []PRRowModel
		header    string
		colWidths []int
		cache     *listMetadataCache
		err       error
	}

	buildInitialResult := func(prs []PullRequest, cache *listMetadataCache) fetchResult {
		items := buildPRRowModels(prs, singleOwner(cli.Owner.Values), resolver)
		initWidth := max(0, xterminal.Width(os.Stdout)-tuiListPrefixWidth(len(items)))
		renderer := p.newTableRenderer(cli, tty, initWidth, table.WithShowIndex(false))
		header, rows, colWidths := renderTUITable(renderer, items, "", false, initWidth)
		return fetchResult{
			rows: rows, items: items, header: header, colWidths: colWidths, cache: cache,
		}
	}

	cache := newListMetadataCache()
	// Old results are misleading during the background refresh (for example,
	// already-closed PRs remain visible), so only seed the TUI from a recent
	// snapshot.
	var cachedPRs []PullRequest
	var cachedAt time.Time
	if saved != nil {
		cachedAt = saved.LastRefresh
		for _, pr := range saved.PRs {
			cachedPRs = append(cachedPRs, pr.pullRequest())
		}
	} else {
		var cachedErr error
		cachedPRs, cachedAt, cachedErr = loadListResultSnapshot(
			cli,
			params,
			tuiListResultCacheMaxAge,
		)
		if cachedErr != nil {
			clog.Debug().Err(cachedErr).Msg("list cache load failed")
		}
	}
	cachedOK := saved != nil || !cachedAt.IsZero()

	var r fetchResult
	if cachedOK {
		r = buildInitialResult(cachedPRs, cache)
	} else {
		r = withSpinner(tty && !cli.Debug, s, func(func()) fetchResult {
			snapshot := refreshSnapshot{
				cli:      cli,
				cfg:      cfg,
				p:        p,
				tty:      tty,
				gql:      gql,
				resolver: resolver,
				rest:     clients.rest,
				params:   params,
				width:    xterminal.Width(os.Stdout),
				cache:    cache,
			}
			items, searchErr := snapshot.fetchAndBuild()
			if searchErr != nil {
				return fetchResult{err: searchErr, cache: cache}
			}
			if saveErr := saveListResultCache(cli, params, prItems(items)); saveErr != nil {
				clog.Debug().Err(saveErr).Msg("list cache save failed")
			}
			initWidth := max(0, snapshot.width-tuiListPrefixWidth(len(items)))
			renderer := p.newTableRenderer(cli, tty, initWidth, table.WithShowIndex(false))
			header, rows, colWidths := renderTUITable(renderer, items, "", false, initWidth)
			return fetchResult{
				rows: rows, items: items, header: header, colWidths: colWidths, cache: cache,
			}
		})
	}

	if r.err != nil {
		return r.err
	}

	actions := NewActionRunner(clients.rest, gql)

	fi := textinput.New()
	fi.Prompt = ""
	fiStyles := fi.Styles()
	fiStyles.Focused.Text = styleFilter
	fiStyles.Blurred.Text = styleFilter
	fiStyles.Cursor.Color = colorFilter
	fi.SetStyles(fiStyles)

	// Resolve current user login eagerly so it's cached for View calls.
	var login string
	if saved != nil {
		login = saved.Login
	} else {
		login, _ = getCurrentLogin(clients.rest)
	}

	model := tuiModel{
		screen:          newTUIScreen(p.widthMethod()),
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
		selected:        make(prKeys),
		collapsedStacks: foldedStackKeys(cfg.TUI.FoldedStacks),
		filterInput:     fi,
		p:               p,
		cli:             cli,
		cfg:             cfg,
		tty:             tty,
		resolver:        resolver,
		rest:            clients.rest,
		gql:             gql,
		params:          params,
		metadataCache:   r.cache,
	}

	model.replaceListItems(r.items)
	model.eventStreams = newEventStreams(cfg.Features.EventStreams, clients.events)
	defer model.eventStreams.close()

	// Apply persisted sort and stack settings before showing the first frame.
	model.sortColumn = cfg.TUI.Sort.Key
	model.sortAsc = cfg.TUI.Sort.Order == valueAsc
	model.header, model.rows, model.colWidths = model.rerender()
	if visible := model.visibleIndices(); len(visible) > 0 {
		model.cursor = visible[0]
	}
	if cachedOK {
		model.lastRefreshAt = cachedAt
		if saved != nil {
			saved.restore(&model)
		} else {
			model.initCmd = model.startRefresh(false)
		}
	}
	var program *tea.Program
	sw := scrollwheel.New(resolveWheelTarget, func(msg tea.Msg) {
		if program != nil {
			program.Send(msg)
		}
	})
	defer sw.Stop()

	program = tea.NewProgram(
		model,
		tea.WithEnvironment(terminalEnv),
		tea.WithFPS(tuiRenderFPS),
		tea.WithFilter(sw.Filter),
	)
	_, err = program.Run()
	if err != nil {
		return fmt.Errorf("interactive TUI: %w", err)
	}
	return errCanceled
}
