package main

import (
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/clog"
)

// pendingApproval records an approval change that has been sent but not confirmed.
type pendingApproval struct {
	remove bool
}

// approvalResult is the outcome of one approval change.
type approvalResult struct {
	key     prKey
	remove  bool
	outcome approveOutcome
	err     error
}

// prStatusMsg carries a single PR's freshly fetched status. gen is the key's
// approval generation when the request was sent.
type prStatusMsg struct {
	key       prKey
	gen       int
	changeGen int
	pr        PullRequest
	err       error
}

// isViewerApproved reports whether the viewer is known to have approved pr.
func isViewerApproved(pr PullRequest) bool {
	return pr.viewerApprovalLoaded && pr.viewerApproved
}

// approveCmd approves a single PR in the background.
func approveCmd(actions *ActionRunner, t targetPR) tea.Cmd {
	return func() tea.Msg {
		outcome, err := actions.approvePR(t.pr)
		return actionMsg{
			index:     t.index,
			key:       makePRKey(t.pr),
			ref:       t.pr.Ref(),
			url:       t.pr.URL,
			action:    tuiActionApproved,
			outcome:   outcome,
			navigated: true,
			err:       err,
		}
	}
}

// approveTargetsCmd approves one PR or a batch in the background.
func approveTargetsCmd(actions *ActionRunner, targets []targetPR) tea.Cmd {
	if len(targets) == 1 {
		return approveCmd(actions, targets[0])
	}
	batch := make([]targetPR, len(targets))
	copy(batch, targets)
	return func() tea.Msg {
		var mu sync.Mutex
		outcomes := make(map[prKey]approveOutcome, len(batch))
		msg := runBatchAction(actions, batch, tuiActionApproved,
			func(a *ActionRunner, pr PullRequest) error {
				outcome, err := a.approvePR(pr)
				mu.Lock()
				outcomes[makePRKey(pr)] = outcome
				mu.Unlock()
				return err
			})
		msg.outcomes = outcomes
		return msg
	}
}

// approveAndMerge keeps the approval outcome separate from the merge outcome.
func approveAndMerge(actions *ActionRunner, pr PullRequest) (string, approvalResult, error) {
	outcome, err := actions.approvePR(pr)
	approval := approvalResult{key: makePRKey(pr), outcome: outcome, err: err}
	if err != nil {
		return "", approval, err
	}
	owner, repo := prOwnerRepo(pr)
	result, err := actions.mergeOrAutomerge(owner, repo, pr)
	return result, approval, err
}

func approveMergeTargetsCmd(actions *ActionRunner, targets []targetPR) tea.Cmd {
	if len(targets) == 1 {
		t := targets[0]
		return func() tea.Msg {
			result, approval, err := approveAndMerge(actions, t.pr)
			action := parseMergeResult(result)
			if approval.err != nil {
				action = tuiActionApproved
			}
			return actionMsg{
				index:     t.index,
				key:       makePRKey(t.pr),
				ref:       t.pr.Ref(),
				url:       t.pr.URL,
				action:    action,
				outcome:   approval.outcome,
				approvals: []approvalResult{approval},
				err:       err,
			}
		}
	}
	batch := append([]targetPR(nil), targets...)
	return func() tea.Msg {
		return runMergeBatch(actions, batch, true)
	}
}

func targetKeys(targets []targetPR) []prKey {
	keys := make([]prKey, len(targets))
	for i, t := range targets {
		keys[i] = makePRKey(t.pr)
	}
	return keys
}

func (m *tuiModel) itemForKey(key prKey) *PRRowModel {
	for i := range m.items {
		if makePRKey(m.items[i].PR) == key {
			return &m.items[i]
		}
	}
	return nil
}

// reconcileVisibility hides key if its row no longer matches the active
// filters, or unhides it if it matches again.
func (m *tuiModel) reconcileVisibility(key prKey) {
	item := m.itemForKey(key)
	if item == nil || m.cli == nil {
		return
	}
	if m.matchesLocalFilters(*item) {
		delete(m.filterHidden, key)
		return
	}
	if m.filterHidden == nil {
		m.filterHidden = make(prKeys)
	}
	m.filterHidden[key] = true
	delete(m.selected, key)
}

// beginApproval optimistically marks PRs as approved by the viewer, hiding
// rows that no longer match the filters. Callers capture the PRs for the
// approve command before calling this.
func (m *tuiModel) beginApproval(keys []prKey) {
	m.beginApprovalChange(keys, false)
}

func (m *tuiModel) beginApprovalChange(keys []prKey, remove bool) {
	if m.pendingApprovals == nil {
		m.pendingApprovals = make(map[prKey]pendingApproval)
	}
	if m.approvalGen == nil {
		m.approvalGen = make(map[prKey]int)
	}
	for _, key := range keys {
		pr := m.prForKey(key)
		if pr == nil {
			continue
		}
		m.pendingApprovals[key] = pendingApproval{remove: remove}
		m.approvalGen[key]++
		m.projectPR(key)
		m.reconcileVisibility(key)
	}
	m.resyncCursorAndOffset()
}

// settleApprovals finalises approvals once their requests return. Successful
// approvals keep the optimistic state; failures and aggregate skips roll it
// back. It returns a command that refreshes the list when cached data is now
// stale or a failed PR has left the list.
func (m *tuiModel) settleApprovals(results []approvalResult) tea.Cmd {
	if m.approvalGen == nil {
		m.approvalGen = make(map[prKey]int)
	}
	updated, missing := false, false
	changed := make(prKeys)
	for _, r := range results {
		_, wasPending := m.pendingApprovals[r.key]
		delete(m.pendingApprovals, r.key)
		m.approvalGen[r.key]++
		if r.err == nil && r.outcome != approveSkippedAggregate {
			updated = true
			changed[r.key] = true
			m.updateConfirmed(
				r.key,
				func(pr *PullRequest) { pr.viewerApproved = !r.remove; pr.viewerApprovalLoaded = true },
			)
			m.reconcileVisibility(r.key)
			continue
		}
		if m.itemForKey(r.key) == nil {
			missing = missing || r.err != nil
			continue
		}
		if wasPending {
			m.projectPR(r.key)
		}
		m.reconcileVisibility(r.key)
	}
	m.resyncCursorAndOffset()

	// Invalidate confirmed approval changes while retaining unrelated cached rows.
	// In-flight hydrations keep writing to the old cache. Restart any refresh
	// that started before the approval landed.
	if updated {
		m.invalidatePRMetadata(changed)
		if m.refreshing {
			m.invalidateRefresh()
			return m.startRefresh(m.showRefreshStatus)
		}
	}
	if missing && !m.refreshing {
		m.invalidateRefresh()
		return m.startRefresh(false)
	}
	return nil
}

// applyPendingApprovals re-applies optimistic approvals to freshly loaded
// items and rebuilds the hidden set for them, so a refresh that lands while
// an approval is in flight doesn't flash the row back.
func (m *tuiModel) applyPendingApprovals() {
	for key := range m.pendingApprovals {
		m.projectPR(key)
		m.reconcileVisibility(key)
	}
}

// approvalFlash renders the result of a single approval for the status line.
func (m *tuiModel) approvalFlash(msg actionMsg) tea.Cmd {
	if msg.ref == "" {
		msg.ref = string(msg.key)
	}
	if msg.err != nil {
		return flashResult(m, msg.action.Verb()+" failed:",
			fmt.Sprintf("%s: %v", msg.ref, withGitHubErrorDetail(msg.err)), "", true)
	}
	action := msg.action.String()
	if msg.action == tuiActionApproved && msg.outcome != approvePosted {
		action = resultAlreadyApproved
	}
	return flashResult(m, action, msg.ref, msg.url, false)
}

// statusCmd re-fetches the status of the PR shown in the diff view.
func (m *tuiModel) statusCmd(key prKey) tea.Cmd {
	pr := m.prForKey(key)
	if pr == nil || m.gql == nil {
		return nil
	}
	gql := m.gql
	prCopy := *pr
	gen := m.approvalGen[key]
	changeGen := m.prChangeGen[key]
	return m.trackRead(func() tea.Msg {
		fresh, err := fetchPRStatus(gql, prCopy)
		return prStatusMsg{key: key, gen: gen, changeGen: changeGen, pr: fresh, err: err}
	})
}

// applyPRStatus merges a fetched status into the row. Viewer approval is only
// taken from responses requested after the key's last approval change.
func (m *tuiModel) applyPRStatus(msg prStatusMsg) {
	if msg.changeGen != m.prChangeGen[msg.key] {
		return
	}
	if msg.err != nil {
		clog.Debug().Err(msg.err).Str("pr", string(msg.key)).Msg("PR status refresh failed")
		return
	}
	if m.itemForKey(msg.key) == nil {
		return
	}
	_, pending := m.pendingApprovals[msg.key]
	viewerCurrent := !pending && msg.gen == m.approvalGen[msg.key]
	fresh := msg.pr
	m.updateConfirmed(msg.key, func(pr *PullRequest) {
		if fresh.reviewDecisionLoaded {
			pr.statusFetchedAt = fresh.statusFetchedAt
			pr.MergeStatus = fresh.MergeStatus
			pr.behind = fresh.behind
			pr.ReviewDecision = fresh.ReviewDecision
			pr.reviewDecisionLoaded = true
			if fresh.HeadSHA != "" {
				pr.HeadSHA = fresh.HeadSHA
			}
		}
		if fresh.viewerApprovalLoaded && viewerCurrent {
			pr.viewerApproved = fresh.viewerApproved
			pr.viewerApprovalLoaded = true
			pr.viewerIsAuthor = fresh.viewerIsAuthor
		}
	})
	m.reconcileVisibility(msg.key)
	m.rerenderList()
	m.resyncCursorAndOffset()
}

// approvalBlockReason explains why a PR can't be approved again, or returns ""
// if it can.
func (m tuiModel) approvalBlockReason(key prKey, pr PullRequest) string {
	if _, ok := m.pendingApprovals[key]; ok {
		return resultApprovalPending
	}
	if isViewerApproved(pr) {
		return resultAlreadyApproved
	}
	return ""
}

// flashApprovalBlocked reports targets that were skipped because they are
// already approved or awaiting approval.
func flashApprovalBlocked(m *tuiModel, reason string, skipped []targetPR) tea.Cmd {
	if len(skipped) == 1 {
		return flashResult(m, reason, skipped[0].pr.Ref(), skipped[0].pr.URL, false)
	}
	return flashResult(m, reason, fmt.Sprintf("%d PRs", len(skipped)), "", false)
}

// approvalSkipReason picks the flash text for a set of skipped targets.
func (m tuiModel) approvalSkipReason(skipped []targetPR) string {
	for _, t := range skipped {
		if m.approvalBlockReason(makePRKey(t.pr), t.pr) == resultApprovalPending {
			return resultApprovalPending
		}
	}
	return resultAlreadyApproved
}

// isApprovable reports whether the viewer may approve pr at all (ignoring
// whether they already have).
func (m tuiModel) isApprovable(pr PullRequest) bool {
	state := strings.ToLower(pr.State)
	return !m.isCurrentUserPR(pr) && !pr.IsDraft && state != valueMerged && state != valueClosed
}
