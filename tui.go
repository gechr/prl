package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
	"github.com/gechr/clib/terminal"
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

// browseView tracks which view is active in the browse TUI.
type browseView int

const (
	browseViewList browseView = iota
	browseViewDiff
	browseViewDetail
)

// browseStyles holds all styles for the browse TUI.
type browseStyles struct {
	cursor         lg.Style
	selectedPrefix lg.Style
	statusOK       lg.Style
	statusErr      lg.Style
	statusAction   lg.Style
	helpText       lg.Style
	helpKey        lg.Style
	separator      lg.Style
	diffHead       lg.Style
	confirmBox     lg.Style
	confirmYes     lg.Style
	confirmYesDim  lg.Style
	confirmNo      lg.Style
	confirmNoDim   lg.Style
}

func newBrowseStyles() browseStyles {
	return browseStyles{
		cursor:         lg.NewStyle().Foreground(lg.Color("198")).Bold(true),
		selectedPrefix: lg.NewStyle().Foreground(lg.Color("48")),
		statusOK:       lg.NewStyle().Foreground(lg.Color("48")),
		statusErr:      lg.NewStyle().Foreground(lg.Color("196")),
		statusAction:   lg.NewStyle().Bold(true),
		helpText:       lg.NewStyle().Foreground(lg.Color("175")),
		helpKey:        lg.NewStyle().Foreground(lg.Color("198")).Bold(true),
		separator:      lg.NewStyle().Foreground(lg.Color("198")).Faint(true),
		diffHead:       lg.NewStyle().Foreground(lg.Color("208")).Bold(true),
		confirmBox: lg.NewStyle().
			Border(lg.RoundedBorder()).
			BorderForeground(lg.Color("198")).
			Padding(browseConfirmPadY, browseConfirmPadX),
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

// browseAction identifies the type of action performed on a PR.
type browseAction int

const (
	browseActionApproved browseAction = iota
	browseActionClosed
	browseActionMerged
	browseActionAutoMerged
	browseActionForceMerged
	browseActionOpened
	browseActionReopened
	browseActionUnassigned
)

func (a browseAction) String() string {
	switch a {
	case browseActionApproved:
		return "Approved"
	case browseActionClosed:
		return "Closed"
	case browseActionMerged:
		return "Merged"
	case browseActionAutoMerged:
		return resultAutoMerged
	case browseActionForceMerged:
		return "Force-merged"
	case browseActionOpened:
		return "Opened"
	case browseActionReopened:
		return "Reopened"
	case browseActionUnassigned:
		return "Unassigned"
	default:
		return "Unknown"
	}
}

// removes returns true if this action removes a PR from the list.
func (a browseAction) removes() bool {
	switch a {
	case browseActionClosed, browseActionMerged, browseActionAutoMerged, browseActionForceMerged,
		browseActionUnassigned:
		return true
	case browseActionApproved, browseActionOpened, browseActionReopened:
		return false
	}
	return false
}

// parseMergeResult converts a mergeOrAutoMerge result string to a browseAction.
func parseMergeResult(result string) browseAction {
	if result == resultAutoMerged {
		return browseActionAutoMerged
	}
	return browseActionMerged
}

// actionMsg is sent when an async action completes.
type actionMsg struct {
	index  int
	key    string // prKey for stable lookup after refresh
	action browseAction
	err    error
}

// detailFetchedMsg is sent when PR detail has been fetched.
type detailFetchedMsg struct {
	index  int
	key    string // prKey for stable lookup after refresh
	detail PRDetail
	err    error
}

// diffFetchedMsg is sent when a diff has been fetched.
type diffFetchedMsg struct {
	index int
	key   string // prKey for stable lookup after refresh
	diff  string
	err   error
}

// batchActionMsg is sent when a batch action (multi-select) completes.
type batchActionMsg struct {
	action  browseAction
	count   int
	failed  int
	indices []int // indices of successfully acted PRs
}

// clearStatusMsg clears the status bar after a timeout.
type clearStatusMsg struct{ id int }

// claudeReviewMsg is sent when the Claude review clone+launch completes.
type claudeReviewMsg struct {
	index int
	key   string // prKey for stable lookup after refresh
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
type refreshTickMsg struct{}

// refreshResultMsg carries the result of a background data refresh.
type refreshResultMsg struct {
	rows []TableRow
	prs  []PullRequest
	err  error
}

// browseModel is the Bubble Tea model for the browse TUI.
type browseModel struct {
	rows         []TableRow
	prs          []PullRequest
	cursor       int
	offset       int
	view         browseView
	diff         string
	diffLines    []string
	diffIndex    int
	diffScroll   int
	detail       PRDetail
	detailLines  []string
	detailIndex  int
	detailScroll int
	statusMsg    string
	statusErr    bool
	statusID     int
	actions      *ActionRunner
	width        int
	height       int
	styles       browseStyles
	removed      map[int]bool
	selected     map[int]bool

	// Diff queue for sequential multi-PR review.
	diffQueue      []int // remaining PR indices to diff through
	diffHistory    []int // previously viewed PR indices (for going back)
	diffQueueTotal int   // total PRs in the queue (for counter display)
	diffAdvanced   bool  // true when queue was advanced from diff view (skip actionMsg view switch)
	diffExpected   bool  // true when a diffFetchedMsg is expected (cleared on dismiss)

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

func (m browseModel) Init() tea.Cmd {
	if m.autoRefresh {
		return scheduleRefresh(len(m.prs))
	}
	return nil
}

// scheduleRefresh returns a tea.Cmd that fires a refreshTickMsg after a delay
// scaled by the number of results (reusing watch-mode intervals).
func scheduleRefresh(n int) tea.Cmd {
	d := watchInterval(n)
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

// resumeRefresh reschedules a refresh tick if auto-refresh is enabled.
func (m browseModel) resumeRefresh() tea.Cmd {
	if m.autoRefresh {
		return scheduleRefresh(len(m.prs))
	}
	return nil
}

func (m browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok &&
		(key.String() == browseKeyCtrlC || key.String() == browseKeyCtrlD) &&
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
		m.rows = m.rerenderedRows()
		if m.view == browseViewDetail && len(m.detailLines) > 0 {
			m.detailLines = m.renderDetailContent()
		}
		return m, nil

	case actionMsg:
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil // PR no longer in list (removed by refresh)
		}
		pr := m.prs[idx]
		if msg.err != nil {
			flashCmd := browseFlashStatus(
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
				m.diffHistory = append(m.diffHistory, idx)
				return m, tea.Batch(flashCmd, nextCmd)
			}
			if m.view == browseViewDiff {
				m.view = browseViewList
				m.diffHistory = nil
				m.diffQueueTotal = 0
			}
			return m, flashCmd
		}
		if msg.action.removes() {
			m.removed[idx] = true
			m.cursor = m.adjustedCursor()
		}
		flashCmd := browseFlashStatus(&m, msg.action.String(), pr.Ref(), pr.URL, false)
		// Queue already advanced from diff view - just flash, stay in diff.
		if m.diffAdvanced {
			m.diffAdvanced = false
			return m, flashCmd
		}
		if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
			m.diffHistory = append(m.diffHistory, idx)
			return m, tea.Batch(flashCmd, nextCmd)
		}
		if m.view == browseViewDiff {
			m.view = browseViewList
			m.diffHistory = nil
			m.diffQueueTotal = 0
		}
		return m, flashCmd

	case batchActionMsg:
		if msg.action.removes() {
			for _, idx := range msg.indices {
				m.removed[idx] = true
				delete(m.selected, idx)
			}
			m.cursor = m.adjustedCursor()
		}
		m.selected = make(map[int]bool)
		status := fmt.Sprintf("%d/%d", msg.count-msg.failed, msg.count)
		if msg.failed > 0 {
			return m, browseFlashStatus(
				&m,
				msg.action.String(),
				status+" ("+fmt.Sprintf("%d failed", msg.failed)+")",
				"",
				true,
			)
		}
		return m, browseFlashStatus(&m, msg.action.String(), status+" PRs", "", false)

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
			flashCmd := browseFlashStatus(&m, "Diff failed:", fmt.Sprintf("%v", msg.err), "", true)
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
		m.diffIndex = idx
		m.diff = highlightDiff(msg.diff)
		m.diffLines = strings.Split(m.diff, "\n")
		m.diffScroll = 0
		m.view = browseViewDiff
		return m, nil

	case detailFetchedMsg:
		if msg.err != nil {
			return m, browseFlashStatus(&m, "Detail failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil // PR no longer in list
		}
		m.detailIndex = idx
		m.detail = msg.detail
		m.detailLines = m.renderDetailContent()
		m.detailScroll = 0
		m.view = browseViewDetail
		m.statusMsg = ""
		return m, nil

	case claudeReviewMsg:
		if msg.err != nil {
			return m, browseFlashStatus(&m, "Claude failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		idx := m.resolveIndex(msg.key, msg.index)
		if idx < 0 {
			return m, nil
		}
		return m, browseFlashStatus(
			&m,
			"Claude review launched",
			m.prs[idx].Ref(),
			m.prs[idx].URL,
			false,
		)

	case slackSentMsg:
		if msg.err != nil {
			return m, browseFlashStatus(&m, "Slack failed:", fmt.Sprintf("%v", msg.err), "", true)
		}
		status := fmt.Sprintf("%d PRs", msg.count)
		if msg.count == 1 {
			status = "1 PR"
		}
		return m, browseFlashStatus(&m, "Sent to Slack", status, "", false)

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
		if !m.autoRefresh || m.view != browseViewList {
			return m, nil
		}
		model := m
		return m, func() tea.Msg {
			return model.backgroundRefresh()
		}

	case refreshResultMsg:
		if msg.err == nil && len(msg.prs) > 0 {
			m = m.mergeRefresh(msg.rows, msg.prs)
		}
		if m.autoRefresh {
			return m, scheduleRefresh(len(m.prs))
		}
		return m, nil

	case tea.KeyMsg:
		switch m.view {
		case browseViewDiff:
			return m.updateDiffView(msg)
		case browseViewDetail:
			return m.updateDetailView(msg)
		case browseViewList:
			return m.updateListView(msg)
		}
	}

	return m, nil
}

func (m browseModel) updateListView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle filter input mode.
	if m.filterInput.Focused() {
		switch msg.String() {
		case browseKeyEnter:
			m.filterInput.Blur()
			return m, nil
		case browseKeyEsc, browseKeyCtrlC, browseKeyCtrlD:
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			m.cursor = m.adjustedCursor()
			m.offset = m.scrolledOffset()
			return m, nil
		case browseKeyUp, browseKeyDown:
			dir := 1
			if msg.String() == browseKeyUp {
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
			if m.filterInput.Value() == "" && prev != "" {
				// Backspaced to empty - exit filter mode.
				m.filterInput.Blur()
			}
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
			case browseKeyEnter, "q", browseKeyEsc, "y", "n", " ":
				return m.confirmDismiss()
			default:
				return m, nil
			}
		}
		switch msg.String() {
		case browseKeyLeft, browseKeyRight, "h", "l":
			m.confirmYes = !m.confirmYes
			return m, nil
		case "y":
			return m.confirmAccept()
		case "n", "q", browseKeyEsc:
			return m.confirmDismiss()
		case browseKeyEnter:
			if m.confirmYes {
				return m.confirmAccept()
			}
			return m.confirmDismiss()
		default:
			return m, nil
		}
	}

	switch msg.String() {
	case browseKeyEsc:
		if m.filterInput.Value() != "" {
			m.filterInput.SetValue("")
			m.cursor = m.adjustedCursor()
			m.offset = m.scrolledOffset()
			return m, nil
		}
		return m, tea.Quit

	case "q":
		return m, tea.Quit

	case "/":
		return m, m.filterInput.Focus()

	case browseKeyEnter:
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
		key := prKey(prCopy)
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			detail, err := actions.fetchPRDetail(owner, repo, prCopy.Number)
			return detailFetchedMsg{index: idx, key: key, detail: detail, err: err}
		}

	case "j", browseKeyDown:
		if next, ok := m.nextVisible(1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case "k", browseKeyUp:
		if next, ok := m.nextVisible(-1); ok {
			m.cursor = next
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case browseKeyLeft:
		visible := m.visibleIndices()
		if len(visible) > 0 {
			m.cursor = visible[0]
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case browseKeyRight:
		visible := m.visibleIndices()
		if len(visible) > 0 {
			m.cursor = visible[len(visible)-1]
			m.offset = m.scrolledOffset()
		}
		return m, nil

	case "space":
		if !m.removed[m.cursor] && m.cursor >= 0 && m.cursor < len(m.prs) {
			if m.selected[m.cursor] {
				delete(m.selected, m.cursor)
			} else {
				m.selected[m.cursor] = true
			}
		}
		return m, nil

	case "ctrl+a":
		if len(m.selected) > 0 {
			m.selected = make(map[int]bool)
		} else {
			for _, idx := range m.visibleIndices() {
				m.selected[idx] = true
			}
		}
		return m, nil

	case "i":
		for _, idx := range m.visibleIndices() {
			if m.selected[idx] {
				delete(m.selected, idx)
			} else {
				m.selected[idx] = true
			}
		}
		return m, nil

	case "a", "y":
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = browseActionApprove
		m.confirmYes = true
		if len(targets) == 1 {
			m.confirmPrompt = "Approve " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				err := actions.approve(owner, repo, t.pr.Number)
				return actionMsg{
					index:  t.index,
					key:    prKey(t.pr),
					action: browseActionApproved,
					err:    err,
				}
			}
		} else {
			m.confirmPrompt = fmt.Sprintf("Approve %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					browseActionApproved,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
						return a.approve(owner, repo, pr.Number)
					},
				)
			}
		}
		return m, nil

	case browseKeyAltA:
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
					key:    prKey(t.pr),
					action: browseActionApproved,
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
				browseActionApproved,
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
			queue := make([]int, 0, len(targets)-1)
			for _, t := range targets[1:] {
				queue = append(queue, t.index)
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
			return diffFetchedMsg{index: first.index, key: prKey(first.pr), diff: diff, err: err}
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
					key:    prKey(t.pr),
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
					browseActionMerged,
					func(a *ActionRunner, pr PullRequest) error {
						owner, repo := prOwnerRepo(pr)
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
					key:    prKey(t.pr),
					action: browseActionForceMerged,
					err:    err,
				}
			}
		} else {
			m.confirmPrompt = fmt.Sprintf("Force-merge %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					browseActionForceMerged,
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
					key:    prKey(t.pr),
					action: browseActionClosed,
					err:    err,
				}
			}
		} else {
			m.confirmPrompt = fmt.Sprintf("Close %d PRs?", len(targets))
			m.confirmCmd = func() tea.Msg {
				return runBatchAction(
					actions,
					batch,
					browseActionClosed,
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
			m.confirmAction = browseActionInfo
			m.confirmYes = true
			m.confirmPrompt = browseClaudeReviewUnsupported
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
			m.confirmAction = browseActionInfo
			m.confirmYes = true
			m.confirmPrompt = browseClaudeReviewUnsupported
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
			return claudeReviewMsg{index: idx, key: prKey(prCopy), err: err}
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
		m.selected = make(map[int]bool)
		return m, browseFlashStatus(&m, browseActionOpened.String(), msg, last.pr.URL, false)

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
					key:    prKey(t.pr),
					action: browseActionReopened,
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
				browseActionReopened,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.reopenPR(owner, repo, pr.Number)
				},
			)
		}

	case "U":
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
						key:    prKey(t.pr),
						action: browseActionUnassigned,
						err:    err,
					}
				}
				owner, repo := prOwnerRepo(t.pr)
				err = actions.removeReviewRequest(owner, repo, t.pr.Number, login)
				return actionMsg{
					index:  t.index,
					key:    prKey(t.pr),
					action: browseActionUnassigned,
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
					action: browseActionUnassigned,
					count:  len(batch),
					failed: len(batch),
				}
			}
			return runBatchAction(
				actions,
				batch,
				browseActionUnassigned,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.removeReviewRequest(owner, repo, pr.Number, login)
				},
			)
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
			return m, scheduleRefresh(len(m.prs))
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
		return m, tea.Tick(browseJumpTimeout, func(time.Time) tea.Msg {
			return jumpTimeoutMsg{id: id}
		})
	}

	return m, nil
}

func (m browseModel) updateDiffView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	viewport := m.diffViewport()
	switch msg.String() {
	case "q", browseKeyEsc, "d":
		m.diffQueue = nil
		m.diffHistory = nil
		m.diffQueueTotal = 0
		m.diffAdvanced = false
		m.diffExpected = false
		m.view = browseViewList
		return m, m.resumeRefresh()
	case "n":
		// Skip to next in queue without approving.
		if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
			m.diffHistory = append(m.diffHistory, m.diffIndex)
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
		m.diffQueue = append([]int{m.diffIndex}, m.diffQueue...)
		m.diffExpected = true
		pr := m.prs[prev]
		actions := m.actions
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			diff, err := actions.fetchDiff(owner, repo, pr.Number)
			return diffFetchedMsg{index: prev, key: prKey(pr), diff: diff, err: err}
		}
	case "j", browseKeyDown:
		if m.diffScroll < len(m.diffLines)-viewport {
			m.diffScroll++
		}
		return m, nil
	case "k", browseKeyUp:
		if m.diffScroll > 0 {
			m.diffScroll--
		}
		return m, nil
	case "g", browseKeyLeft:
		m.diffScroll = 0
		return m, nil
	case "G", browseKeyRight:
		if end := len(m.diffLines) - viewport; end > 0 {
			m.diffScroll = end
		}
		return m, nil
	case "a", "y", browseKeyAltA:
		idx := m.diffIndex
		pr := m.prs[idx]
		actions := m.actions
		approveCmd := func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: prKey(pr), action: browseActionApproved, err: err}
		}
		// If there's a next item in queue, prefetch it in parallel with the approve.
		if nextCmd := advanceDiffQueue(&m); nextCmd != nil {
			m.diffHistory = append(m.diffHistory, idx)
			m.diffAdvanced = true
			return m, tea.Batch(approveCmd, nextCmd)
		}
		// Last item - approve and let actionMsg handler return to list.
		return m, approveCmd
	case "C":
		idx := m.diffIndex
		pr := m.prs[idx]
		actions := m.actions
		if strings.ToLower(pr.State) == valueClosed {
			return m, func() tea.Msg {
				owner, repo := prOwnerRepo(pr)
				err := actions.reopenPR(owner, repo, pr.Number)
				return actionMsg{index: idx, key: prKey(pr), action: browseActionReopened, err: err}
			}
		}
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.closePR(owner, repo, pr.Number, "", false)
			return actionMsg{index: idx, key: prKey(pr), action: browseActionClosed, err: err}
		}
	case "U":
		idx := m.diffIndex
		pr := m.prs[idx]
		actions := m.actions
		rest := m.rest
		return m, func() tea.Msg {
			login, err := getCurrentLogin(rest)
			if err != nil {
				return actionMsg{
					index:  idx,
					key:    prKey(pr),
					action: browseActionUnassigned,
					err:    err,
				}
			}
			owner, repo := prOwnerRepo(pr)
			err = actions.removeReviewRequest(owner, repo, pr.Number, login)
			return actionMsg{index: idx, key: prKey(pr), action: browseActionUnassigned, err: err}
		}
	}
	return m, nil
}

func (m browseModel) updateDetailView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	viewport := m.detailViewport()
	switch msg.String() {
	case "q", browseKeyEsc, browseKeyEnter:
		m.view = browseViewList
		return m, m.resumeRefresh()
	case "j", browseKeyDown:
		if m.detailScroll < len(m.detailLines)-viewport {
			m.detailScroll++
		}
		return m, nil
	case "k", browseKeyUp:
		if m.detailScroll > 0 {
			m.detailScroll--
		}
		return m, nil
	case "g", browseKeyLeft:
		m.detailScroll = 0
		return m, nil
	case "G", browseKeyRight:
		if end := len(m.detailLines) - viewport; end > 0 {
			m.detailScroll = end
		}
		return m, nil
	case "d":
		// Jump to diff from detail view.
		idx := m.detailIndex
		pr := m.prs[idx]
		actions := m.actions
		prCopy := pr
		m.diffExpected = true
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(prCopy)
			diff, err := actions.fetchDiff(owner, repo, prCopy.Number)
			return diffFetchedMsg{index: idx, key: prKey(prCopy), diff: diff, err: err}
		}
	case "a", "y":
		idx := m.detailIndex
		pr := m.prs[idx]
		actions := m.actions
		m.view = browseViewList
		m.confirmAction = browseActionApprove
		m.confirmYes = true
		m.confirmPrompt = "Approve " + styledRef(&pr) + "?"
		m.confirmCmd = func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: prKey(pr), action: browseActionApproved, err: err}
		}
		return m, nil
	case browseKeyAltA:
		idx := m.detailIndex
		pr := m.prs[idx]
		actions := m.actions
		m.view = browseViewList
		return m, func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.approve(owner, repo, pr.Number)
			return actionMsg{index: idx, key: prKey(pr), action: browseActionApproved, err: err}
		}
	case "o":
		pr := m.prs[m.detailIndex]
		_ = openBrowser(pr.URL)
		return m, nil
	case "r":
		if !hasClaudeReviewLauncher() {
			m.view = browseViewList
			m.confirmAction = browseActionInfo
			m.confirmYes = true
			m.confirmPrompt = browseClaudeReviewUnsupported
			return m, nil
		}
		idx := m.detailIndex
		pr := m.prs[idx]
		m.view = browseViewList
		m = m.prepareClaudeReviewConfirm(pr, idx)
		return m, nil
	}
	return m, nil
}

func (m browseModel) View() tea.View {
	switch m.view {
	case browseViewDiff:
		return m.viewDiff()
	case browseViewDetail:
		return m.viewDetail()
	case browseViewList:
	}
	v := m.viewList()
	if m.showHelp {
		v.Content = overlayCenter(v.Content, m.renderHelpOverlay(), m.width, m.height)
	} else if m.confirmAction != "" {
		v.Content = overlayCenter(v.Content, m.renderConfirmModal(), m.width, m.height)
	}
	return v
}

func (m browseModel) viewList() tea.View {
	var b strings.Builder
	visible := m.visibleIndices()
	viewport := m.listViewport()

	// Determine visible slice based on offset.
	end := min(m.offset+viewport, len(visible))
	start := min(m.offset, len(visible))

	filterVal := m.filterInput.Value()
	if len(visible) == 0 && filterVal != "" {
		b.WriteString(
			"\n" + lg.NewStyle().Foreground(lg.Color("210")).Render("  no results") + "\n",
		)
	}

	for _, idx := range visible[start:end] {
		sel := browseNonCursorPrefix
		if m.selected[idx] {
			sel = m.styles.selectedPrefix.Render("● ")
		}
		display := m.rows[idx].Display
		if idx == m.cursor {
			line := m.styles.cursor.Render(browseCursorPrefix) + sel + display
			// Inject background color throughout the line so it persists
			// through existing ANSI codes in the display string.
			// Skip highlight when there's only one visible result.
			if len(visible) > 1 {
				b.WriteString(injectLineBackground(line, m.width))
			} else {
				b.WriteString(line)
			}
		} else {
			b.WriteString(browseNonCursorPrefix + sel)
			b.WriteString(display)
		}
		b.WriteString("\n")
	}

	// Pad to fill viewport.
	rendered := end - start
	if len(visible) == 0 && filterVal != "" {
		rendered = 1 // "no results" line
	}
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
	return v
}

func (m browseModel) viewDiff() tea.View {
	var b strings.Builder
	viewport := m.diffViewport()

	// PR title header.
	titleStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("218"))
	repoStyle := lg.NewStyle().Foreground(lg.Color("118"))
	if m.diffIndex >= 0 && m.diffIndex < len(m.prs) {
		pr := m.prs[m.diffIndex]
		var repoLine string
		if m.diffQueueTotal > 0 {
			pos := m.diffQueueTotal - len(m.diffQueue)
			repoLine = repoStyle.Bold(true).
				Render(fmt.Sprintf("[%d/%d]", pos, m.diffQueueTotal)) +
				" "
		}
		repoLine += repoStyle.Render(pr.Repository.NameWithOwner)
		b.WriteString(repoLine)
		b.WriteString("\n")
		b.WriteString(titleStyle.Render(pr.Title))
		b.WriteString("\n")
		viewport -= 2 // account for repo + title lines
	}

	// Diff content.
	end := min(m.diffScroll+viewport, len(m.diffLines))
	for _, line := range m.diffLines[m.diffScroll:end] {
		if m.width > 0 && len(line) > m.width {
			line = line[:m.width]
		}
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
		b.WriteString(m.styles.separator.Render(strings.Repeat("─", m.width)))
	}
	b.WriteString("\n")

	// Help line with scroll percentage on RHS.
	help := m.renderDiffHelp()
	if m.width > 0 && len(m.diffLines) > viewport {
		const percent = 100
		pct := min(percent*(m.diffScroll+viewport)/len(m.diffLines), percent)
		status := m.styles.statusOK.Render(fmt.Sprintf("%d%%", pct))
		lastNL := strings.LastIndex(help, "\n")
		lastLine := help
		if lastNL >= 0 {
			lastLine = help[lastNL+1:]
		}
		pad := m.width - lg.Width(lastLine) - lg.Width(status)
		if pad > 0 {
			b.WriteString(help)
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(status)
		} else {
			b.WriteString(help)
		}
	} else {
		b.WriteString(help)
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m browseModel) listViewport() int {
	// 1 for separator + help lines (variable).
	h := 1 + m.helpLines(m.listHelpPairs())
	if m.filterInput.Value() != "" || m.filterInput.Focused() {
		h++
	}
	if m.height <= h {
		return 1
	}
	return m.height - h
}

func (m browseModel) viewDetail() tea.View {
	var b strings.Builder
	viewport := m.detailViewport()

	// Content.
	end := min(m.detailScroll+viewport, len(m.detailLines))
	for _, line := range m.detailLines[m.detailScroll:end] {
		if m.width > 0 && lg.Width(line) > m.width {
			line = line[:m.width]
		}
		b.WriteString(line)
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
	if m.width > 0 && len(m.detailLines) > viewport {
		const percent = 100
		pct := min(percent*(m.detailScroll+viewport)/len(m.detailLines), percent)
		status := m.styles.statusOK.Render(fmt.Sprintf("%d%%", pct))
		lastNL := strings.LastIndex(help, "\n")
		lastLine := help
		if lastNL >= 0 {
			lastLine = help[lastNL+1:]
		}
		pad := m.width - lg.Width(lastLine) - lg.Width(status)
		if pad > 0 {
			b.WriteString(help)
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(status)
		} else {
			b.WriteString(help)
		}
	} else {
		b.WriteString(help)
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m browseModel) detailViewport() int {
	h := 1 + m.helpLines(m.detailHelpPairs())
	if m.height <= h {
		return 1
	}
	return m.height - h
}

// renderDetailContent builds the detail view lines from the PR and its detail data.
func (m browseModel) renderDetailContent() []string {
	pr := m.prs[m.detailIndex]

	titleStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("218"))
	headerStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("175"))
	dimStyle := lg.NewStyle().Foreground(lg.Color("240"))

	labelStyle := lg.NewStyle().Bold(true).Foreground(lg.Color("48"))
	valueStyle := lg.NewStyle().Foreground(lg.Color("255"))

	author := m.resolver.Resolve(pr.Author.Login)
	var lines []string
	lines = append(lines, titleStyle.Render(pr.Title))
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

func (m browseModel) renderDetailStatus(pr PullRequest) string {
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

func (m browseModel) renderMarkdown(body string) []string {
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

func (m browseModel) plainBodyLines(body string) []string {
	var lines []string
	for line := range strings.SplitSeq(body, "\n") {
		lines = append(lines, detailIndent+line)
	}
	return lines
}

func (m browseModel) diffViewport() int {
	// 1 for separator + help lines (variable).
	h := 1 + m.helpLines(m.diffHelpPairs())
	if m.height <= h {
		return 1
	}
	return m.height - h
}

// advanceDiffQueue pops the next PR from the diff queue and returns a command
// to fetch its diff. Returns nil if the queue is empty.
func advanceDiffQueue(m *browseModel) tea.Cmd {
	if len(m.diffQueue) == 0 {
		m.diffQueueTotal = 0
		return nil
	}
	idx := m.diffQueue[0]
	m.diffQueue = m.diffQueue[1:]
	m.diffExpected = true
	pr := m.prs[idx]
	actions := m.actions
	return func() tea.Msg {
		owner, repo := prOwnerRepo(pr)
		diff, err := actions.fetchDiff(owner, repo, pr.Number)
		return diffFetchedMsg{index: idx, key: prKey(pr), diff: diff, err: err}
	}
}

func (m browseModel) visibleIndices() []int {
	indices := make([]int, 0, len(m.rows))
	f := strings.TrimSpace(m.filterInput.Value())
	if f == "" {
		for i := range m.rows {
			if !m.removed[i] {
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
		if m.removed[i] {
			continue
		}
		if re.MatchString(xansi.Strip(m.rows[i].Display)) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m browseModel) nextVisible(dir int) (int, bool) {
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

func (m browseModel) adjustedCursor() int {
	if next, ok := m.nextVisible(1); ok {
		return next
	}
	if prev, ok := m.nextVisible(-1); ok {
		return prev
	}
	return m.cursor
}

func (m browseModel) scrolledOffset() int {
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
	action browseAction,
	fn func(*ActionRunner, PullRequest) error,
) batchActionMsg {
	var succeeded []int
	failed := 0
	for _, t := range targets {
		if err := fn(actions, t.pr); err != nil {
			failed++
		} else {
			succeeded = append(succeeded, t.index)
		}
	}
	return batchActionMsg{
		action:  action,
		count:   len(targets),
		failed:  failed,
		indices: succeeded,
	}
}

func browseFlashStatus(m *browseModel, action, ref, url string, isErr bool) tea.Cmd {
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
	return tea.Tick(browseStatusFlash, func(time.Time) tea.Msg {
		return clearStatusMsg{id: id}
	})
}

type targetPR struct {
	index int
	pr    PullRequest
}

func (m browseModel) targetPRs() []targetPR {
	if len(m.selected) > 0 {
		var targets []targetPR
		for _, idx := range m.visibleIndices() {
			if m.selected[idx] {
				targets = append(targets, targetPR{idx, m.prs[idx]})
			}
		}
		return targets
	}
	if pr := m.currentPR(); pr != nil {
		return []targetPR{{m.cursor, *pr}}
	}
	return nil
}

func (m browseModel) tableWidth() int {
	// Subtract cursor prefix + selection marker widths.
	return m.width - 2*lg.Width(browseNonCursorPrefix)
}

func (m browseModel) rerenderedRows() []TableRow {
	renderer := m.p.newTableRenderer(m.cli, m.tty, m.resolver, m.tableWidth())
	_, rows := renderer.Render(m.prs)
	return rows
}

func (m browseModel) currentPR() *PullRequest {
	if m.cursor < 0 || m.cursor >= len(m.prs) || m.removed[m.cursor] {
		return nil
	}
	return &m.prs[m.cursor]
}

func (m browseModel) listHelpPairs() []struct{ key, desc string } {
	pairs := []struct{ key, desc string }{
		{browseKeyEnter, "show"},
		{"←/→", "first/last"},
		{"space", "select"},
		{"/", "filter"},
		{"a/y", "approve"},
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

func (m browseModel) renderListHelp() string {
	return m.renderHelp(m.listHelpPairs())
}

func (m browseModel) renderFilterHelp() string {
	pairs := []struct{ key, desc string }{
		{"↑/↓", "prev/next"},
		{browseKeyEnter, "apply"},
		{browseKeyEsc, "exit"},
	}
	return m.renderHelp(pairs)
}

func (m browseModel) diffHelpPairs() []struct{ key, desc string } {
	pairs := []struct{ key, desc string }{
		{"↑/↓", "scroll"},
		{"←/→", "top/bottom"},
		{"a/y", "approve"},
	}
	if m.diffIndex >= 0 && m.diffIndex < len(m.prs) {
		if strings.ToLower(m.prs[m.diffIndex].State) == valueClosed {
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

func (m browseModel) renderDiffHelp() string {
	return m.renderHelp(m.diffHelpPairs())
}

func (m browseModel) detailHelpPairs() []struct{ key, desc string } {
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

func (m browseModel) renderDetailHelp() string {
	return m.renderHelp(m.detailHelpPairs())
}

func (m browseModel) renderHelpOverlay() string {
	pairs := []struct{ key, desc string }{
		{"↑/↓ j/k", "Navigate up/down"},
		{"←/→ g/G", "Jump to first/last"},
		{"enter", "Show PR detail"},
		{"space", "Toggle selection"},
		{"ctrl+a", "Select all/none"},
		{"i", "Invert selection"},
		{"/", "Filter"},
		{"a/y", "Approve PRs"},
		{browseKeyAltA, "Approve PRs (no confirm)"},
		{"d", "View diff"},
		{"m", "Merge PRs"},
		{"M", "Force-merge PRs"},
		{"C", "Close PRs"},
		{"O", "Reopen PRs"},
		{"U", "Unassign as reviewer"},
		{"o", "Open in browser"},
		{"s", "Send to Slack"},
		{"alt+s", "Send to Slack (no confirm)"},
		{"R", "Toggle auto-refresh"},
		{"?", "This help"},
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
				},
				pairs[len(pairs)-2:]...)...)
	}

	// Render in two columns.
	rows := (len(pairs) + 1) / 2 //nolint:mnd // ceil division
	renderPair := func(p struct{ key, desc string }) string {
		return m.styles.helpKey.Render(
			fmt.Sprintf("%8s", p.key),
		) + "  " + m.styles.helpText.Render(
			p.desc,
		)
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

	return m.styles.confirmBox.Render(b.String())
}

// appendStatus appends the status message to the right of the last line of help,
// or returns help unchanged if there's no status or not enough room.
func (m browseModel) appendStatus(help string) string {
	if m.statusMsg == "" || m.width <= 0 {
		return help
	}
	style := m.styles.statusOK
	if m.statusErr {
		style = m.styles.statusErr
	}
	status := style.Render(m.statusMsg)
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

func (m browseModel) renderHelp(pairs []struct{ key, desc string }) string {
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
func (m browseModel) helpLines(pairs []struct{ key, desc string }) int {
	return strings.Count(m.renderHelp(pairs), "\n") + 1
}

func (m browseModel) confirmAccept() (tea.Model, tea.Cmd) {
	cmd := m.confirmCmd
	m = m.clearConfirm()
	return m, cmd
}

func (m browseModel) confirmDismiss() (tea.Model, tea.Cmd) {
	m = m.clearConfirm()
	return m, nil
}

func (m browseModel) renderConfirmModal() string {
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
	return m.styles.confirmBox.Render(m.confirmPrompt + "\n\n" + centered)
}

func (m browseModel) clearConfirm() browseModel {
	m.confirmAction = ""
	m.confirmPrompt = ""
	m.confirmCmd = nil
	return m
}

func (m browseModel) prepareClaudeReviewConfirm(pr PullRequest, idx int) browseModel {
	prCopy := pr
	m.confirmAction = "review"
	m.confirmYes = true
	m.confirmPrompt = "Launch Claude review for " + styledRef(
		&prCopy,
	) + "?\n\nThis will clone the repo and open a new terminal tab."
	m.confirmCmd = func() tea.Msg {
		err := launchClaudeReview(prCopy)
		return claudeReviewMsg{index: idx, key: prKey(prCopy), err: err}
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
// Dim version of cursor color 208 (orange #ff8700).
const cursorLineBG = "\x1b[48;2;20;3;14m"

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
func (m browseModel) backgroundRefresh() refreshResultMsg {
	prs, err := executeSearch(m.rest, m.params)
	if err != nil {
		return refreshResultMsg{err: err}
	}
	prs, err = applyFilters(m.cli, prs)
	if err != nil {
		return refreshResultMsg{err: err}
	}
	if len(prs) == 0 {
		return refreshResultMsg{}
	}

	if !m.cli.Quick {
		if gql, gqlErr := newGraphQLClient(withDebug(m.cli.Debug)); gqlErr == nil {
			enrichMergeStatus(gql, prs)
		}
	} else {
		for i := range prs {
			if prs[i].State == valueOpen {
				prs[i].MergeStatus = MergeStatusBlocked
			}
		}
	}

	renderer := m.p.newTableRenderer(m.cli, m.tty, m.resolver, m.tableWidth())
	_, rows := renderer.Render(prs)
	return refreshResultMsg{rows: rows, prs: prs}
}

// prKey returns a unique identifier for a PR (repo + number).
func prKey(pr PullRequest) string {
	return fmt.Sprintf("%s#%d", pr.Repository.NameWithOwner, pr.Number)
}

// resolveIndex resolves a PR key to its current index in the model's PR list.
// If the key matches the hint index, it returns the hint directly (fast path).
// Returns -1 if the PR is no longer in the list.
func (m browseModel) resolveIndex(key string, hint int) int {
	if hint >= 0 && hint < len(m.prs) && prKey(m.prs[hint]) == key {
		return hint
	}
	for i, pr := range m.prs {
		if prKey(pr) == key {
			return i
		}
	}
	return -1
}

// mergeRefresh replaces the model's data with fresh results while preserving
// cursor position, selections, and removed state by matching on PR identity.
func (m browseModel) mergeRefresh(rows []TableRow, prs []PullRequest) browseModel {
	// Build lookup: old index -> PR key.
	oldKeys := make(map[string]int, len(m.prs))
	for i, pr := range m.prs {
		oldKeys[prKey(pr)] = i
	}

	// Transfer removed/selected state to new indices.
	newRemoved := make(map[int]bool)
	newSelected := make(map[int]bool)
	for i, pr := range prs {
		if oldIdx, ok := oldKeys[prKey(pr)]; ok {
			if m.removed[oldIdx] {
				newRemoved[i] = true
			}
			if m.selected[oldIdx] {
				newSelected[i] = true
			}
		}
	}

	// Try to keep cursor on the same PR.
	cursorKey := ""
	if m.cursor >= 0 && m.cursor < len(m.prs) {
		cursorKey = prKey(m.prs[m.cursor])
	}
	newCursor := 0
	for i, pr := range prs {
		if prKey(pr) == cursorKey {
			newCursor = i
			break
		}
	}

	// Build new-index lookup for remapping diff/detail/queue indices.
	newKeyIdx := make(map[string]int, len(prs))
	for i, pr := range prs {
		newKeyIdx[prKey(pr)] = i
	}
	oldPRs := m.prs // capture before overwrite

	m.rows = rows
	m.prs = prs
	m.removed = newRemoved
	m.selected = newSelected
	m.cursor = newCursor
	m.offset = m.scrolledOffset()

	// Remap diff/detail indices so in-progress views stay correct.
	m.diffIndex = remapIndex(m.diffIndex, oldPRs, newKeyIdx)
	m.detailIndex = remapIndex(m.detailIndex, oldPRs, newKeyIdx)
	m.diffQueue = remapIndices(m.diffQueue, oldPRs, newKeyIdx)
	m.diffHistory = remapIndices(m.diffHistory, oldPRs, newKeyIdx)
	return m
}

// remapIndex maps an old PR index to its new position after a refresh.
func remapIndex(oldIdx int, oldPRs []PullRequest, newKeyIdx map[string]int) int {
	if oldIdx < 0 || oldIdx >= len(oldPRs) {
		return oldIdx
	}
	if newIdx, ok := newKeyIdx[prKey(oldPRs[oldIdx])]; ok {
		return newIdx
	}
	return oldIdx
}

// remapIndices maps a slice of old PR indices to their new positions,
// dropping any that no longer exist in the refreshed list.
func remapIndices(oldIndices []int, oldPRs []PullRequest, newKeyIdx map[string]int) []int {
	if len(oldIndices) == 0 {
		return oldIndices
	}
	result := make([]int, 0, len(oldIndices))
	for _, oldIdx := range oldIndices {
		if oldIdx < 0 || oldIdx >= len(oldPRs) {
			continue
		}
		if newIdx, ok := newKeyIdx[prKey(oldPRs[oldIdx])]; ok {
			result = append(result, newIdx)
		}
	}
	return result
}

// runBrowse launches the interactive browse TUI.
func runBrowse(
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
		rows []TableRow
		prs  []PullRequest
		err  error
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
		if len(prs) == 0 {
			return fetchResult{}
		}

		// Enrich merge status for table coloring.
		if !cli.Quick {
			if gql, gqlErr := newGraphQLClient(withDebug(cli.Debug)); gqlErr == nil {
				enrichMergeStatus(gql, prs)
			}
		} else {
			for i := range prs {
				if prs[i].State == valueOpen {
					prs[i].MergeStatus = MergeStatusBlocked
				}
			}
		}

		// Subtract cursor prefix + selection marker widths from terminal width.
		initWidth := terminal.Width(
			os.Stdout,
		) - 2*lg.Width( //nolint:mnd // cursor + selection prefix
			browseNonCursorPrefix,
		)
		renderer := p.newTableRenderer(cli, tty, resolver, initWidth)
		_, rows := renderer.Render(prs)
		return fetchResult{rows: rows, prs: prs}
	})

	if r.err != nil {
		return r.err
	}
	if len(r.prs) == 0 {
		return nil
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
	fi.SetStyles(fiStyles)

	model := browseModel{
		rows:        r.rows,
		prs:         r.prs,
		actions:     actions,
		autoRefresh: cfg.TUI.AutoRefresh.Enabled,
		styles:      newBrowseStyles(),
		removed:     make(map[int]bool),
		selected:    make(map[int]bool),
		filterInput: fi,
		p:           p,
		cli:         cli,
		cfg:         cfg,
		tty:         tty,
		resolver:    resolver,
		rest:        rest,
		params:      params,
	}

	_, err = tea.NewProgram(model).Run()
	if err != nil {
		return fmt.Errorf("browse TUI: %w", err)
	}
	return nil
}
