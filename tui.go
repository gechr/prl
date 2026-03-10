package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	diffHead      lg.Style
	helpKey       lg.Style
	helpText      lg.Style
	overlayBox    lg.Style
	selectedIndex lg.Style
	separator     lg.Style
	statusAction  lg.Style
	statusErr     lg.Style
	statusOK      lg.Style
}

func newTuiStyles() tuiStyles {
	return tuiStyles{
		cursor:        lg.NewStyle().Foreground(lg.Color("198")).Bold(true),
		selectedIndex: lg.NewStyle().Foreground(lg.Color("118")).Bold(true),
		statusOK:      lg.NewStyle().Foreground(lg.Color("48")),
		statusErr:     lg.NewStyle().Foreground(lg.Color("196")),
		statusAction:  lg.NewStyle().Bold(true),
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

// tuiAction identifies the type of action performed on a PR.
type tuiAction int

const (
	tuiActionApproved tuiAction = iota
	tuiActionClosed
	tuiActionMerged
	tuiActionAutoMerged
	tuiActionForceMerged
	tuiActionOpened
	tuiActionReopened
	tuiActionUnsubscribed
	tuiActionReviewRequested
)

func (a tuiAction) String() string {
	switch a {
	case tuiActionApproved:
		return "Approved"
	case tuiActionClosed:
		return "Closed"
	case tuiActionMerged:
		return "Merged"
	case tuiActionAutoMerged:
		return resultAutoMerged
	case tuiActionForceMerged:
		return "Force-merged"
	case tuiActionOpened:
		return "Opened"
	case tuiActionReopened:
		return "Reopened"
	case tuiActionUnsubscribed:
		return "Unsubscribed"
	case tuiActionReviewRequested:
		return "Copilot review requested"
	default:
		return "Unknown"
	}
}

// removes returns true if this action removes a PR from the list.
func (a tuiAction) removes() bool {
	switch a {
	case tuiActionClosed, tuiActionMerged, tuiActionAutoMerged, tuiActionForceMerged,
		tuiActionUnsubscribed:
		return true
	case tuiActionApproved,
		tuiActionOpened,
		tuiActionReopened,
		tuiActionReviewRequested:
		return false
	}
	return false
}

// parseMergeResult converts a mergeOrAutoMerge result string to a tuiAction.
func parseMergeResult(result string) tuiAction {
	if result == resultAutoMerged {
		return tuiActionAutoMerged
	}
	return tuiActionMerged
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
	index int
	key   prKey // stable lookup after refresh
	diff  string
	err   error
}

// batchActionMsg is sent when a batch action (multi-select) completes.
type batchActionMsg struct {
	action   tuiAction
	count    int
	failed   int
	keys     []prKey
	failures []batchFailure
}

type batchFailure struct {
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
	count int
	err   error
}

// jumpTimeoutMsg fires when the digit-input window expires.
type jumpTimeoutMsg struct{ id int }

// refreshTickMsg fires when it's time to start a background refresh.
type refreshTickMsg struct{ id int }

// spinnerTickMsg fires to advance the spinner animation frame.
type spinnerTickMsg struct{ id int }

// refreshResultMsg carries the result of a background data refresh.
type refreshResultMsg struct {
	rows  []TableRow
	items []PRRowModel
	err   error
}

// tuiModel is the Bubble Tea model for the interactive TUI.
//
//nolint:recvcheck // selection helpers use pointer receivers to mutate maps/fields in-place
type tuiModel struct {
	items        []PRRowModel // canonical data for rerender on resize/refresh
	rows         []TableRow   // current rendered order; row.Item is the action target
	header       string
	colWidths    []int // visible column widths for header click hit-testing
	sortColumn   string
	sortAsc      bool
	cursor       int
	offset       int
	view         tuiView
	diff         string
	diffLines    []string
	diffKey      prKey
	diffScroll   int
	detail       PRDetail
	detailLines  []string
	detailKey    prKey
	detailScroll int
	statusMsg    string
	statusErr    bool
	statusID     int
	actions      *ActionRunner
	width        int
	height       int
	styles       tuiStyles
	removed      prKeys
	selected     prKeys

	// Diff queue for sequential multi-PR review.
	diffQueue      []prKey // remaining PR keys to diff through
	diffHistory    []prKey // previously viewed PR keys (for going back)
	diffQueueTotal int     // total PRs in the queue (for counter display)
	diffAdvanced   bool    // true when queue was advanced from diff view (skip actionMsg view switch)
	diffExpected   bool    // true when a diffFetchedMsg is expected (cleared on dismiss)

	// Empty overlay dismissed (esc to dismiss, then esc again to quit).
	dismissedEmpty bool

	// Filter mode.
	filterInput textinput.Model

	// Pending digit jump (e.g. "1" waiting for second digit).
	jumpDigit int // first digit (1-9), 0 = no pending jump
	jumpID    int // timeout generation

	// Pending confirmation (e.g. close/merge).
	confirmAction string  // "close", "merge", "diff"
	confirmPrompt string  // prompt text for modal
	confirmCmd    tea.Cmd // command to run on confirmation
	confirmYes    bool    // true = Yes selected, false = No selected

	// Background auto-refresh.
	autoRefresh bool
	refreshing  bool    // true while a background refresh is in-flight
	refreshID   int     // generation counter to discard stale refresh ticks
	spinner     spinner // spinner animation frames
	spinnerTick int     // current spinner frame index
	spinnerID   int     // generation counter to discard stale ticks
	showHelp    bool

	// Retained for re-rendering the table on resize and background refresh.
	p        *prl
	cli      *CLI
	cfg      *Config
	tty      bool
	resolver *AuthorResolver
	rest     *api.RESTClient
	params   *SearchParams
}

func (m tuiModel) Init() tea.Cmd {
	if m.autoRefresh {
		return scheduleRefresh(len(m.items), m.refreshID)
	}
	return nil
}

// scheduleRefresh returns a tea.Cmd that fires a refreshTickMsg after a delay
// scaled by the number of results (reusing watch-mode intervals).
func scheduleRefresh(n, id int) tea.Cmd {
	d := watchInterval(n)
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshTickMsg{id: id} })
}

// scheduleSpinnerTick returns a tea.Cmd that fires a spinnerTickMsg after the
// spinner's interval, scoped to the current generation (spinnerID).
func (m tuiModel) scheduleSpinnerTick() tea.Cmd {
	id := m.spinnerID
	d := m.spinner.interval
	return tea.Tick(d, func(time.Time) tea.Msg { return spinnerTickMsg{id: id} })
}

// rescheduleRefresh invalidates older refresh ticks and schedules a new one.
func (m *tuiModel) rescheduleRefresh() tea.Cmd {
	if m.autoRefresh {
		m.refreshID++
		return scheduleRefresh(len(m.items), m.refreshID)
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

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.header, m.rows, m.colWidths = m.rerender()
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
			flashCmd := tuiFlashStatus(
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
			}
			return m, flashCmd
		}
		if msg.action.removes() {
			m.removed[msg.key] = true
			m.cursor = m.adjustedCursor()
			m.offset = m.scrolledOffset()
		}
		flashCmd := tuiFlashStatus(&m, msg.action.String(), pr.Ref(), pr.URL, false)
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
			return m, tuiFlashStatus(
				&m,
				msg.action.String(),
				status+" ("+fmt.Sprintf("%d failed", msg.failed)+")",
				"",
				true,
			)
		}
		return m, tuiFlashStatus(&m, msg.action.String(), status+" PRs", "", false)

	case clearStatusMsg:
		if msg.id == m.statusID {
			m.statusMsg = ""
		}
		return m, nil

	case diffFetchedMsg:
		if !m.diffExpected {
			return m, nil // stale fetch from a dismissed diff view
		}
		m.diffExpected = false
		if msg.err != nil {
			flashCmd := tuiFlashStatus(&m, "Diff failed:", fmt.Sprintf("%v", msg.err), "", true)
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
		m.diff = highlightDiff(msg.diff)
		m.diffLines = strings.Split(m.diff, "\n")
		m.diffScroll = 0
		m.view = tuiViewDiff
		return m, nil

	case detailFetchedMsg:
		if msg.err != nil {
			return m, tuiFlashStatus(&m, "Detail failed:", fmt.Sprintf("%v", msg.err), "", true)
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
			return m, tuiFlashStatus(&m, "Claude failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		return m, tuiFlashStatus(
			&m,
			"Claude review launched",
			pr.Ref(),
			pr.URL,
			false,
		)

	case slackSentMsg:
		if msg.err != nil {
			return m, tuiFlashStatus(&m, "Slack failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		status := fmt.Sprintf("%d PRs", msg.count)
		if msg.count == 1 {
			status = "1 PR"
		}
		return m, tuiFlashStatus(&m, "Sent to Slack", status, "", false)

	case jumpTimeoutMsg:
		if msg.id == m.jumpID && m.jumpDigit > 0 {
			visible := m.visibleIndices()
			target := m.jumpDigit - 1
			if target < len(visible) {
				m.cursor = visible[target]
				m.offset = m.scrolledOffset()
			}
			m.jumpDigit = 0
		}
		return m, nil

	case refreshTickMsg:
		if !m.autoRefresh || m.view != tuiViewList || msg.id != m.refreshID || m.refreshing {
			return m, nil
		}
		m.refreshing = true
		m.spinnerTick = 0
		m.spinnerID++
		model := m
		return m, tea.Batch(
			m.scheduleSpinnerTick(),
			func() tea.Msg { return model.backgroundRefresh() },
		)

	case spinnerTickMsg:
		if !m.refreshing || msg.id != m.spinnerID {
			return m, nil
		}
		m.spinnerTick++
		return m, m.scheduleSpinnerTick()

	case refreshResultMsg:
		m.refreshing = false
		if msg.err == nil {
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
		}
		return m, m.rescheduleRefresh()

	case tea.KeyMsg:
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
			if m.filterInput.Value() != prev {
				m.cursor = m.adjustedCursor()
				m.offset = m.scrolledOffset()
			}
			return m, cmd
		}
	}

	// Handle pending confirmation.
	if m.confirmAction != "" {
		// Info-only modal (no confirmCmd) - any key dismisses.
		if m.confirmCmd == nil {
			switch msg.String() {
			case tuiKeyEnter, "q", tuiKeyEsc, "y", "n", " ":
				return m.confirmDismiss()
			default:
				return m, nil
			}
		}
		switch msg.String() {
		case tuiKeyLeft, tuiKeyRight, "h", "l":
			m.confirmYes = !m.confirmYes
			return m, nil
		case "y":
			return m.confirmAccept()
		case "n", "q", tuiKeyEsc:
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

	case "q":
		return m, tea.Quit

	case "/":
		return m, m.filterInput.Focus()

	case tuiKeyEnter:
		pr := m.currentPR()
		if pr == nil {
			return m, nil
		}
		idx := m.cursor
		actions := m.actions
		prCopy := *pr
		m.statusMsg = m.styles.statusAction.Render("Fetching") + " " +
			lg.NewStyle().Foreground(lg.Color("117")).Render(prCopy.Ref())
		m.statusErr = false
		key := makePRKey(prCopy)
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			detail, err := actions.fetchPRDetail(owner, repo, prCopy.Number)
			return detailFetchedMsg{index: idx, key: key, detail: detail, err: err}
		}

	case "j", tuiKeyDown:
		if next, ok := m.nextVisible(1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case "k", tuiKeyUp:
		if next, ok := m.nextVisible(-1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case tuiKeyLeft:
		visible := m.visibleIndices()
		if len(visible) > 0 {
			m.cursor = visible[0]
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case tuiKeyRight:
		visible := m.visibleIndices()
		if len(visible) > 0 {
			m.cursor = visible[len(visible)-1]
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case "space":
		m.toggleCurrentSelection()
		return m, nil

	case tuiKeyShiftDown:
		m.extendSelectionAndMove(1)
		return m, nil

	case tuiKeyShiftUp:
		m.extendSelectionAndMove(-1)
		return m, nil

	case "ctrl+a":
		if len(m.selected) > 0 {
			m.selected = make(prKeys)
		} else {
			for _, idx := range m.visibleIndices() {
				m.selected[m.rowKeyAt(idx)] = true
			}
		}
		return m, nil

	case "i":
		for _, idx := range m.visibleIndices() {
			key := m.rowKeyAt(idx)
			if m.selected[key] {
				delete(m.selected, key)
			} else {
				m.selected[key] = true
			}
		}
		return m, nil

	case "a":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionApprove
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmPrompt = "Approve " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				err := actions.approve(owner, repo, t.pr.Number)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionApproved,
					err:    err,
				}
			}
		} else {
			m.confirmPrompt = fmt.Sprintf("Approve %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					tuiActionApproved,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
						return a.approve(owner, repo, pr.Number)
					},
				)
			}
		}
		return m, nil

	case tuiKeyAltA:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		if len(targets) == 1 {
			t := targets[0]
			actions := m.actions
			return m, func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				err := actions.approve(owner, repo, t.pr.Number)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionApproved,
					err:    err,
				}
			}
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		return m, func() tea.Msg {
			return runBatchAction(
				actions,
				batch,
				tuiActionApproved,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.approve(owner, repo, pr.Number)
				},
			)
		}

	case "d":
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
		m.diffExpected = true
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(first.pr)
			diff, err := actions.fetchDiff(owner, repo, first.pr.Number)
			return diffFetchedMsg{
				index: first.index,
				key:   makePRKey(first.pr),
				diff:  diff,
				err:   err,
			}
		}

	case "m":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = "merge"
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmPrompt = "Merge " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				result, err := actions.mergeOrAutoMerge(owner, repo, t.pr)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: parseMergeResult(result),
					err:    err,
				}
			}
		} else {
			m.confirmPrompt = fmt.Sprintf("Merge %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					tuiActionMerged,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
						_, err := a.mergeOrAutoMerge(owner, repo, pr)
						return err
					},
				)
			}
		}
		return m, nil

	case "A":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = "approve+merge"
		m.confirmYes = true
		if len(targets) == 1 {
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
				result, err := actions.mergeOrAutoMerge(owner, repo, t.pr)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: parseMergeResult(result),
					err:    err,
				}
			}
		} else {
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
						_, err := a.mergeOrAutoMerge(owner, repo, pr)
						return err
					},
				)
			}
		}
		return m, nil

	case "M":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = "force-merge"
		m.confirmYes = true
		if len(targets) == 1 {
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

	case "C":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = "close"
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmPrompt = "Close " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				err := actions.closePR(owner, repo, t.pr.Number, "", false)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionClosed,
					err:    err,
				}
			}
		} else {
			m.confirmPrompt = fmt.Sprintf("Close %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					tuiActionClosed,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
						return a.closePR(owner, repo, pr.Number, "", false)
					},
				)
			}
		}
		return m, nil

	case "r":
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
		idx := m.cursor
		prCopy := *pr
		m = m.prepareClaudeReviewConfirm(prCopy, idx)
		return m, nil

	case "alt+r":
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
		idx := m.cursor
		prCopy := *pr
		return m, func() tea.Msg {
			err := launchClaudeReview(prCopy)
			return claudeReviewMsg{index: idx, key: makePRKey(prCopy), err: err}
		}

	case "s":
		targets := m.targetPRs()
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
		m.confirmAction = "send-slack"
		m.confirmYes = true
		if count == 1 {
			m.confirmPrompt = "Send " + styledRef(&prs[0]) + " to Slack?"
		} else {
			m.confirmPrompt = fmt.Sprintf("Send %d PRs to Slack?", count)
		}
		m.confirmCmd = func() tea.Msg {
			err := sendSlack(prs, cli, cfg)
			return slackSentMsg{count: count, err: err}
		}
		return m, nil

	case "alt+s":
		targets := m.targetPRs()
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
		return m, func() tea.Msg {
			err := sendSlack(prs, cli, cfg)
			return slackSentMsg{count: count, err: err}
		}

	case "o":
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
		return m, tuiFlashStatus(&m, tuiActionOpened.String(), msg, last.pr.URL, false)

	case "O":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		if len(targets) == 1 {
			t := targets[0]
			actions := m.actions
			return m, func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				err := actions.reopenPR(owner, repo, t.pr.Number)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionReopened,
					err:    err,
				}
			}
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		return m, func() tea.Msg {
			return runBatchAction(
				actions,
				batch,
				tuiActionReopened,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.reopenPR(owner, repo, pr.Number)
				},
			)
		}

	case "u":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		rest := m.rest
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = "unassign"
		m.confirmYes = true
		if len(targets) == 1 {
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
			m.confirmPrompt = fmt.Sprintf("Unassign & unsubscribe from %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				login, err := getCurrentLogin(rest)
				if err != nil {
					return batchActionMsg{
						action:   tuiActionUnsubscribed,
						count:    len(batch),
						failed:   len(batch),
						failures: batchFailuresForTargets(batch, err),
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

	case "alt+u":
		targets := m.targetPRs()
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
					failures: batchFailuresForTargets(batch, err),
				}
			}
			return runBatchAction(actions, batch, tuiActionUnsubscribed,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.removeReviewRequest(owner, repo, pr.Number, login, pr.NodeID)
				})
		}

	case "ctrl+r":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		if len(targets) == 1 {
			t := targets[0]
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

	case "?":
		m.showHelp = true
		return m, nil

	case "R":
		m.autoRefresh = !m.autoRefresh
		// Persist to config file in the background.
		enabled := m.autoRefresh
		_ = saveConfigKey(keyTUIAutoRefresh, enabled)
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
	maxScroll := m.diffMaxScroll()
	switch msg.String() {
	case "q", tuiKeyEsc, "d":
		m.diffQueue = nil
		m.diffHistory = nil
		m.diffQueueTotal = 0
		m.diffAdvanced = false
		m.diffExpected = false
		m.diffKey = ""
		m.view = tuiViewList
		return m, m.rescheduleRefresh()
	case "n":
		// Skip to next in queue without approving.
		if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
			m.diffHistory = append(m.diffHistory, m.diffKey)
			return m, nextCmd
		}
		return m, nil
	case "p":
		// Go back to previous diff in history.
		if len(m.diffHistory) == 0 {
			return m, nil
		}
		prev := m.diffHistory[len(m.diffHistory)-1]
		m.diffHistory = m.diffHistory[:len(m.diffHistory)-1]
		// Push current back onto front of queue.
		m.diffQueue = append([]prKey{m.diffKey}, m.diffQueue...)
		m.diffExpected = true
		idx := m.resolveIndex(prev, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			diff, err := actions.fetchDiff(owner, repo, pr.Number)
			return diffFetchedMsg{index: idx, key: makePRKey(pr), diff: diff, err: err}
		}
	case "j", tuiKeyDown:
		if m.diffScroll < maxScroll {
			m.diffScroll++
		}
		return m, nil
	case "k", tuiKeyUp:
		if m.diffScroll > 0 {
			m.diffScroll--
		}
		return m, nil
	case "g", tuiKeyLeft:
		m.diffScroll = 0
		return m, nil
	case "G", tuiKeyRight:
		m.diffScroll = maxScroll
		return m, nil
	case "a", "y", tuiKeyAltA:
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
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
	case "C":
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		if strings.ToLower(pr.State) == valueClosed {
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
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.closePR(owner, repo, pr.Number, "", false)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionClosed, err: err}
		}
	case "u":
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		rest := m.rest
		return m, func() tea.Msg {
			login, err := getCurrentLogin(rest)
			if err != nil {
				return actionMsg{
					index:  idx,
					key:    makePRKey(pr),
					action: tuiActionUnsubscribed,
					err:    err,
				}
			}
			owner, repo := prOwnerRepo(pr)
			err = actions.removeReviewRequest(owner, repo, pr.Number, login, pr.NodeID)
			return actionMsg{
				index:  idx,
				key:    makePRKey(pr),
				action: tuiActionUnsubscribed,
				err:    err,
			}
		}
	case "ctrl+r":
		idx := m.resolveIndex(m.diffKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
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
	viewport := m.detailViewport()
	switch msg.String() {
	case "q", tuiKeyEsc, tuiKeyEnter:
		m.detailKey = ""
		m.view = tuiViewList
		return m, m.rescheduleRefresh()
	case "j", tuiKeyDown:
		if m.detailScroll < len(m.detailLines)-viewport {
			m.detailScroll++
		}
		return m, nil
	case "k", tuiKeyUp:
		if m.detailScroll > 0 {
			m.detailScroll--
		}
		return m, nil
	case "g", tuiKeyLeft:
		m.detailScroll = 0
		return m, nil
	case "G", tuiKeyRight:
		if end := len(m.detailLines) - viewport; end > 0 {
			m.detailScroll = end
		}
		return m, nil
	case "d":
		// Jump to diff from detail view.
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		prCopy := pr
		m.diffExpected = true
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			diff, err := actions.fetchDiff(owner, repo, prCopy.Number)
			return diffFetchedMsg{index: idx, key: makePRKey(prCopy), diff: diff, err: err}
		}
	case "a", "y":
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		m.view = tuiViewList
		m.confirmAction = tuiActionApprove
		m.confirmYes = true
		m.confirmPrompt = "Approve " + styledRef(&pr) + "?"
		m.confirmCmd = func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionApproved, err: err}
		}
		return m, nil
	case tuiKeyAltA:
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		actions := m.actions
		m.view = tuiViewList
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionApproved, err: err}
		}
	case "o":
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		_ = openBrowser(pr.URL)
		return m, nil
	case "r":
		if !hasClaudeReviewLauncher() {
			m.view = tuiViewList
			m.confirmAction = tuiActionInfo
			m.confirmYes = true
			m.confirmPrompt = tuiClaudeReviewUnsupported
			m.confirmCmd = nil
			return m, nil
		}
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		m.view = tuiViewList
		m = m.prepareClaudeReviewConfirm(pr, idx)
		return m, nil
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
		if idx == m.cursor {
			line := m.styles.cursor.Render(tuiCursorPrefix) + display
			// Inject background color throughout the line so it persists
			// through existing ANSI codes in the display string.
			// Skip highlight when there's only one visible result.
			if len(visible) > 1 {
				b.WriteString(injectLineBackground(line, m.width))
			} else {
				b.WriteString(line)
			}
		} else {
			b.WriteString(tuiNonCursorPrefix)
			b.WriteString(display)
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

	// Separator.
	if m.width > 0 {
		b.WriteString(m.styles.separator.Render(strings.Repeat("─", m.width)))
	}
	b.WriteString("\n")

	// Help line with status on RHS.
	var help string
	if m.filterInput.Focused() {
		help = m.renderFilterHelp()
	} else {
		help = m.renderListHelp()
	}
	b.WriteString(m.appendStatus(help))

	v := tea.NewView(b.String())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
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
			headerLine = xansi.Truncate(headerLine, m.width-1, "…")
		}
		b.WriteString(headerLine)
		b.WriteString("\n")
		if m.width > 0 {
			b.WriteString(m.styles.separator.Render(strings.Repeat("─", m.width)))
		}
		b.WriteString("\n")
	}

	// Diff content.
	end := min(m.diffScroll+viewport, len(m.diffLines))
	for _, line := range m.diffLines[m.diffScroll:end] {
		b.WriteString(truncateDisplayLine(line, m.width))
		b.WriteString("\n")
	}

	// Pad remaining.
	rendered := end - m.diffScroll
	for range viewport - rendered {
		b.WriteString("\n")
	}

	// Separator.
	if m.width > 0 {
		b.WriteString(m.styles.separator.Render(strings.Repeat("─", m.width)))
	}
	b.WriteString("\n")

	// Help line with scroll percentage on RHS.
	help := m.renderDiffHelp()
	status := ""
	if maxScroll := m.diffMaxScroll(); maxScroll > 0 {
		pct := scrollPercent(m.diffScroll, maxScroll)
		status = m.styles.statusOK.Render(fmt.Sprintf("%d%%", pct))
	}
	b.WriteString(m.appendRightStatus(help, status))

	v := tea.NewView(b.String())
	v.AltScreen = true
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
		b.WriteString(m.styles.separator.Render(strings.Repeat("─", m.width)))
	}
	b.WriteString("\n")

	// Help line with scroll percentage on RHS.
	help := m.renderDetailHelp()
	status := ""
	if maxScroll := max(0, len(m.detailLines)-viewport); maxScroll > 0 {
		pct := scrollPercent(m.detailScroll, maxScroll)
		status = m.styles.statusOK.Render(fmt.Sprintf("%d%%", pct))
	}
	b.WriteString(m.appendRightStatus(help, status))

	v := tea.NewView(b.String())
	v.AltScreen = true
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

	titleStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("218"))
	headerStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("175"))
	dimStyle := lg.NewStyle().Foreground(lg.Color("240"))

	labelStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("48"))
	valueStyle := lg.NewStyle().Foreground(lg.Color("255"))

	author := m.resolver.Resolve(pr.Author.Login)
	var lines []string
	lines = append(lines, titleStyle.Render(normalizeTUIDisplayText(pr.Title)))
	lines = append(lines, "")
	authorValue := pr.Author.Login
	if author != pr.Author.Login {
		authorValue = "@" + pr.Author.Login + " (" + author + ")"
	}
	lines = append(lines, detailIndent+labelStyle.Render("Author: ")+valueStyle.Render(authorValue))
	styledURL := xansi.SetHyperlink(pr.URL) + valueStyle.Render(pr.URL) + xansi.ResetHyperlink()
	lines = append(lines, detailIndent+labelStyle.Render("   URL: ")+styledURL)
	lines = append(lines, detailIndent+labelStyle.Render("Status: ")+m.renderDetailStatus(pr))
	lines = append(lines, "")

	// Reviews.
	if len(m.detail.Reviews) > 0 {
		lines = append(lines, headerStyle.Render("Reviews"))
		lines = append(lines, "")
		for _, r := range m.detail.Reviews {
			icon := "💬"
			switch r.State {
			case "APPROVED":
				icon = "✅"
			case "CHANGES_REQUESTED":
				icon = "❌"
			case "DISMISSED":
				icon = "🚫"
			}
			name := m.resolver.Resolve(r.User)
			lines = append(lines, fmt.Sprintf("%s%s %s", detailIndent, icon, name))
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
		for _, f := range m.detail.Files {
			prefix := "M"
			switch f.Status {
			case "added":
				prefix = "A"
			case "removed":
				prefix = "D"
			case "renamed":
				prefix = "R"
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
		return lg.NewStyle().Foreground(lg.Color("240")).Render("Draft")
	}
	state := strings.ToLower(pr.State)
	if state == "merged" {
		return lg.NewStyle().Foreground(lg.Color("141")).Render("Merged")
	}
	if state == "closed" {
		return lg.NewStyle().Foreground(lg.Color("197")).Render("Closed")
	}
	switch pr.MergeStatus {
	case MergeStatusReady:
		return lg.NewStyle().Foreground(lg.Color("48")).Render("Ready to merge")
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

func scrollPercent(offset, maxScroll int) int {
	const percentMax = 100
	if maxScroll <= 0 {
		return percentMax
	}
	return min(percentMax*offset/maxScroll, percentMax)
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
		m.diffExpected = true
		pr := m.rows[idx].Item.PR
		actions := m.actions
		return func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			diff, err := actions.fetchDiff(owner, repo, pr.Number)
			return diffFetchedMsg{index: idx, key: makePRKey(pr), diff: diff, err: err}
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

	// Smart case: case-insensitive unless filter contains uppercase.
	flags := "(?i)"
	if f != strings.ToLower(f) {
		flags = ""
	}
	re, err := regexp.Compile(flags + f)
	if err != nil {
		// Invalid regex - fall back to literal match.
		re = regexp.MustCompile(flags + regexp.QuoteMeta(f))
	}
	for i := range m.rows {
		if m.removed[m.rowKeyAt(i)] {
			continue
		}
		if re.MatchString(rowFilterText(m.rows[i])) {
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

func (m tuiModel) adjustedCursor() int {
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
	return offset
}

func runBatchAction(
	actions *ActionRunner,
	targets []targetPR,
	action tuiAction,
	fn func(*ActionRunner, PullRequest) error,
) batchActionMsg {
	var succeeded []prKey
	var failures []batchFailure
	failed := 0
	for _, t := range targets {
		if err := fn(actions, t.pr); err != nil {
			failed++
			failures = append(failures, batchFailure{
				key: makePRKey(t.pr),
				ref: t.pr.Ref(),
				url: t.pr.URL,
				err: err,
			})
		} else {
			succeeded = append(succeeded, makePRKey(t.pr))
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

func tuiFlashStatus(m *tuiModel, action, ref, url string, isErr bool) tea.Cmd {
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

func batchFailuresForTargets(targets []targetPR, err error) []batchFailure {
	failures := make([]batchFailure, 0, len(targets))
	for _, t := range targets {
		failures = append(failures, batchFailure{
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

func (m tuiModel) listHelpPairs() []struct{ key, desc string } {
	pairs := []struct{ key, desc string }{
		{tuiKeyEnter, "show"},
		{"←/→", "first/last"},
		{"space", "select"},
		{"/", "filter"},
		{"a", "approve"},
		{"d", "diff"},
		{"m", "merge"},
		{"C", "close"},
		{"o", "open"},
	}
	if hasClaudeReviewLauncher() {
		pairs = append(pairs, struct{ key, desc string }{"r", "review"})
	}
	if m.autoRefresh {
		pairs = append(pairs, struct{ key, desc string }{"R", "refresh " + styledOn})
	} else {
		pairs = append(pairs, struct{ key, desc string }{"R", "refresh " + styledOff})
	}
	pairs = append(pairs, struct{ key, desc string }{"?", "help"})
	pairs = append(pairs, struct{ key, desc string }{"q", "quit"})
	return pairs
}

func (m tuiModel) renderListHelp() string {
	return m.renderHelp(m.listHelpPairs())
}

func (m tuiModel) renderFilterHelp() string {
	pairs := []struct{ key, desc string }{
		{"↑/↓", "prev/next"},
		{tuiKeyEnter, "apply"},
		{tuiKeyEsc, "exit"},
	}
	return m.renderHelp(pairs)
}

func (m tuiModel) diffHelpPairs() []struct{ key, desc string } {
	pairs := []struct{ key, desc string }{
		{"↑/↓", "scroll"},
		{"←/→", "top/bottom"},
		{"a/y", "approve"},
	}
	if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
		if strings.ToLower(m.rows[idx].Item.State) == valueClosed {
			pairs = append(pairs, struct{ key, desc string }{"C", "reopen"})
		} else {
			pairs = append(pairs, struct{ key, desc string }{"C", "close"})
		}
	}
	if m.diffQueueTotal > 0 {
		if len(m.diffHistory) > 0 {
			pairs = append(pairs, struct{ key, desc string }{"p", "prev"})
		}
		if len(m.diffQueue) > 0 {
			pairs = append(pairs, struct{ key, desc string }{"n", "next"})
		}
	}
	pairs = append(pairs, struct{ key, desc string }{"d/q", "dismiss"})
	return pairs
}

func (m tuiModel) renderDiffHelp() string {
	return m.renderHelp(m.diffHelpPairs())
}

func (m tuiModel) detailHelpPairs() []struct{ key, desc string } {
	pairs := []struct{ key, desc string }{
		{"↑/↓", "scroll"},
		{"←/→", "top/bottom"},
		{"d", "diff"},
		{"a/y", "approve"},
		{"o", "open"},
	}
	if hasClaudeReviewLauncher() {
		pairs = append(pairs, struct{ key, desc string }{"r", "review"})
	}
	pairs = append(pairs, struct{ key, desc string }{"q", "dismiss"})
	return pairs
}

func (m tuiModel) renderDetailHelp() string {
	return m.renderHelp(m.detailHelpPairs())
}

func (m tuiModel) renderHelpOverlay() string {
	pairs := []struct{ key, desc string }{
		{"↑/↓ j/k", "Navigate up/down"},
		{"←/→ g/G", "Jump to first/last"},
		{"enter", "Show PR detail"},
		{"space", "Toggle selection"},
		{"shift+↑/↓", "Extend selection"},
		{"ctrl+a", "Select all/none"},
		{"i", "Invert selection"},
		{"/", "Filter"},
		{"a", "Approve PRs"},
		{"A", "Approve/Merge PRs"},
		{tuiKeyAltA, "Approve PRs (no confirm)"},
		{"d", "View diff"},
		{"m", "Merge PRs"},
		{"M", "Force-merge PRs"},
		{"C", "Close PRs"},
		{"O", "Reopen PRs"},
		{"u", "Unassign/unsubscribe"},
		{"alt+u", "Unassign (no confirm)"},
		{"o", "Open in browser"},
		{"s", "Send to Slack"},
		{"alt+s", "Send to Slack (no confirm)"},
		{tuiKeyTab, "Cycle sort order"},
		{"R", "Toggle auto-refresh"},
		{"?", "Toggle this help"},
		{"q", "Quit"},
	}
	if hasClaudeReviewLauncher() {
		// Insert review before the last two entries (?, q).
		pairs = append(
			pairs[:len(pairs)-2],
			append(
				[]struct{ key, desc string }{
					{"r", "Launch Claude review"},
					{"alt+r", "Launch Claude review (no confirm)"},
					{"ctrl+r", "Request Copilot review"},
				},
				pairs[len(pairs)-2:]...)...)
	}

	// Render in two columns.
	rows := (len(pairs) + 1) / 2 //nolint:mnd // ceil division
	keyWidth := 0
	for _, p := range pairs {
		keyWidth = max(keyWidth, lg.Width(p.key))
	}
	renderPair := func(p struct{ key, desc string }) string {
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
		Foreground(lg.Color("198")).
		Render("Press any key to dismiss")
	pad := (totalWidth - lg.Width(dismiss)) / 2 //nolint:mnd // center
	if pad > 0 {
		b.WriteString("\n" + strings.Repeat(" ", pad) + dismiss)
	} else {
		b.WriteString("\n" + dismiss)
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
	lastNL := strings.LastIndex(help, "\n")
	lastLine := help
	if lastNL >= 0 {
		lastLine = help[lastNL+1:]
	}
	pad := m.width - lg.Width(lastLine) - lg.Width(status)
	if pad > 0 {
		return help + strings.Repeat(" ", pad) + status
	}
	return help
}

func (m tuiModel) renderHelp(pairs []struct{ key, desc string }) string {
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
		return strings.Join(parts, gap)
	}

	// Wrap into multiple lines if needed.
	var lines []string
	var line string
	lineWidth := 0
	gapWidth := lg.Width(gap)
	for i, part := range parts {
		partWidth := lg.Width(part)
		switch {
		case i == 0:
			line = part
			lineWidth = partWidth
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
func (m tuiModel) helpLines(pairs []struct{ key, desc string }) int {
	return strings.Count(m.renderHelp(pairs), "\n") + 1
}

func (m tuiModel) confirmAccept() (tea.Model, tea.Cmd) {
	cmd := m.confirmCmd
	m = m.clearConfirm()
	return m, cmd
}

func (m tuiModel) confirmDismiss() (tea.Model, tea.Cmd) {
	m = m.clearConfirm()
	return m, nil
}

func (m tuiModel) renderConfirmModal() string {
	var buttons string
	if m.confirmCmd == nil {
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
	promptWidth := lg.Width(m.confirmPrompt)
	buttonsWidth := lg.Width(buttons)
	centered := buttons
	if pad := (promptWidth - buttonsWidth) / 2; pad > 0 { //nolint:mnd // center
		centered = strings.Repeat(" ", pad) + buttons
	}
	return m.styles.overlayBox.Render(m.confirmPrompt + "\n\n" + centered)
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
			query = query[:maxQuery-1] + "…"
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
	m.confirmCmd = nil
	return m
}

func (m tuiModel) prepareClaudeReviewConfirm(pr PullRequest, idx int) tuiModel {
	prCopy := pr
	m.confirmAction = "review"
	m.confirmYes = true
	m.confirmPrompt = "Launch Claude review for " + styledRef(
		&prCopy,
	) + "?\n\nThis will clone the repo and open a new terminal tab."
	m.confirmCmd = func() tea.Msg {
		err := launchClaudeReview(prCopy)
		return claudeReviewMsg{index: idx, key: makePRKey(prCopy), err: err}
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

	fgWidth := 0
	for _, line := range fgLines {
		if w := lg.Width(line); w > fgWidth {
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
		bgVisible := lg.Width(bgLine)
		if bgVisible < startCol {
			bgLine += strings.Repeat(" ", startCol-bgVisible)
		}
		// Build: bg left portion + fg line + bg right portion.
		// Use ANSI-aware truncation for the left portion.
		left := xansi.Truncate(bgLine, startCol, "")
		rightStart := startCol + fgWidth
		var right string
		if bgVisible > rightStart {
			right = xansi.TruncateLeft(bgLine, rightStart, "")
		}
		bgLines[row] = left + fgLine + right
	}

	return strings.Join(bgLines, "\n")
}

// cursorLineBG is the ANSI escape to set the cursor line background color.
const cursorLineBG = "\x1b[48;2;40;10;30m"

// injectLineBackground wraps a line with a background color that persists
// through any embedded ANSI SGR codes. It re-applies the background after
// every SGR sequence (\x1b[...m) so that resets, foreground changes, and
// other styling never clear the line highlight.
func injectLineBackground(line string, width int) string {
	var b strings.Builder
	b.WriteString(cursorLineBG)

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
				b.WriteString(cursorLineBG) // re-apply background after any SGR
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

// highlightDiff applies Chroma syntax highlighting to a unified diff.
func highlightDiff(raw string) string {
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
func launchClaudeReview(pr PullRequest) error {
	launcher := currentClaudeReviewLauncher()
	if launcher == claudeLauncherNone {
		return fmt.Errorf("unsupported terminal %q", os.Getenv("TERM_PROGRAM"))
	}

	script, err := buildClaudeReviewAppleScript(launcher, buildClaudeReviewCommand(pr))
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

func buildClaudeReviewCommand(pr PullRequest) string {
	nwo := pr.Repository.NameWithOwner

	// Clone repo and checkout the PR ref in the new tab so the user sees
	// progress and any SSH/auth prompts. Fetches refs/pull/N/head which
	// works for open, closed, and fork PRs alike.
	remote := "git@github.com:" + nwo
	prompt := fmt.Sprintf(
		"Perform a comprehensive code review of PR #%d in %s. "+
			"The PR branch is checked out. First read the PR context with: gh pr view %[1]d --repo %[2]s "+
			"Then get the diff with: gh api repos/%[2]s/pulls/%[1]d -H 'Accept: application/vnd.github.v3.diff' "+
			"Focus on: correctness, edge cases, error handling, performance, readability, and style. "+
			"Be thorough but concise.",
		pr.Number, nwo,
	)
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

// backgroundRefresh re-executes the search and returns a refreshResultMsg.
func (m tuiModel) backgroundRefresh() refreshResultMsg {
	prs, err := executeSearch(m.rest, m.params)
	if err != nil {
		return refreshResultMsg{err: err}
	}
	prs, err = applyFilters(m.cli, prs)
	if err != nil {
		return refreshResultMsg{err: err}
	}
	if len(prs) > 0 && !m.cli.Quick {
		if gql, gqlErr := newGraphQLClient(withDebug(m.cli.Debug)); gqlErr == nil {
			enrichMergeStatus(gql, prs)
		}
	} else if len(prs) > 0 {
		for i := range prs {
			if prs[i].State == valueOpen {
				prs[i].MergeStatus = MergeStatusBlocked
			}
		}
	}

	orgFilter := singleOrg(m.cli.Organization.Values)
	items := buildPRRowModels(prs, orgFilter, m.resolver)
	termWidth := max(0, m.width-tuiListPrefixWidth(len(items)))
	renderer := m.tableRendererFor(len(items))
	_, rows, _ := renderTUITable(m.p, renderer, items, "", false, termWidth)
	return refreshResultMsg{rows: rows, items: items}
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
	_ = saveConfigKey(keyTUISortKey, m.sortColumn)
	order := ""
	if m.sortColumn != "" {
		order = "desc"
		if m.sortAsc {
			order = "asc"
		}
	}
	_ = saveConfigKey(keyTUISortOrder, order)

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
	resolver := NewAuthorResolver(cfg)

	type fetchResult struct {
		rows      []TableRow
		items     []PRRowModel
		header    string
		colWidths []int
		err       error
	}
	r := withSpinner(tty, s, func(func()) fetchResult {
		prs, err := executeSearch(rest, params)
		if err != nil {
			return fetchResult{err: err}
		}
		prs, err = applyFilters(cli, prs)
		if err != nil {
			return fetchResult{err: err}
		}
		// Enrich merge status for table coloring.
		if len(prs) > 0 && !cli.Quick {
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

	model := tuiModel{
		items:       r.items,
		rows:        r.rows,
		header:      r.header,
		colWidths:   r.colWidths,
		actions:     actions,
		autoRefresh: cfg.TUI.AutoRefresh.Enabled,
		spinner:     buildSpinner(cfg.Spinner),
		styles:      newTuiStyles(),
		removed:     make(prKeys),
		selected:    make(prKeys),
		filterInput: fi,
		p:           p,
		cli:         cli,
		cfg:         cfg,
		tty:         tty,
		resolver:    resolver,
		rest:        rest,
		params:      params,
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
