package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/x/ansi"
)

// targetPR pairs a list index with a copy of the PR at that index.
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

// actionContext bundles the commonly-needed PR eligibility state
// that action handlers and help builders repeatedly derive.
type actionContext struct {
	idx        int
	pr         PullRequest
	key        prKey
	state      string // lowercase State
	draft      bool
	ownPR      bool
	actionable bool // not merged/closed
}

// actionContextForKey resolves a PR key to an actionContext.
// Returns false if the key no longer exists in the current row set.
func (m tuiModel) actionContextForKey(key prKey) (actionContext, bool) {
	idx := m.resolveIndex(key, -1)
	if idx < 0 || idx >= len(m.rows) {
		return actionContext{}, false
	}
	pr := m.rows[idx].Item.PR
	state := strings.ToLower(pr.State)
	return actionContext{
		idx:        idx,
		pr:         pr,
		key:        key,
		state:      state,
		draft:      pr.IsDraft,
		ownPR:      m.isCurrentUserPR(pr),
		actionable: state != valueMerged && state != valueClosed,
	}, true
}

// actionContextForCursor resolves the cursor PR to an actionContext.
func (m tuiModel) actionContextForCursor() (actionContext, bool) {
	pr := m.currentPR()
	if pr == nil {
		return actionContext{}, false
	}
	state := strings.ToLower(pr.State)
	return actionContext{
		idx:        m.cursor,
		pr:         *pr,
		key:        makePRKey(*pr),
		state:      state,
		draft:      pr.IsDraft,
		ownPR:      m.isCurrentUserPR(*pr),
		actionable: state != valueMerged && state != valueClosed,
	}, true
}

func filterTargetPRs(targets []targetPR, exclude func(targetPR) bool) []targetPR {
	n := 0
	for _, t := range targets {
		if !exclude(t) {
			targets[n] = t
			n++
		}
	}
	return targets[:n]
}

func (m tuiModel) targetActionablePRs() []targetPR {
	return filterTargetPRs(m.targetPRs(), func(t targetPR) bool {
		state := strings.ToLower(t.pr.State)
		return state == valueMerged || state == valueClosed
	})
}

func (m tuiModel) targetOtherActionablePRs() []targetPR {
	return filterTargetPRs(m.targetActionablePRs(), func(t targetPR) bool {
		return m.isCurrentUserPR(t.pr)
	})
}

func (m tuiModel) targetMergeablePRs() []targetPR {
	return filterTargetPRs(m.targetActionablePRs(), func(t targetPR) bool {
		return t.pr.IsDraft
	})
}

func (m tuiModel) targetApprovablePRs() []targetPR {
	return filterTargetPRs(m.targetPRs(), func(t targetPR) bool {
		state := strings.ToLower(t.pr.State)
		return m.isCurrentUserPR(t.pr) || t.pr.IsDraft || state == valueMerged ||
			state == valueClosed
	})
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
	m.confirmState.Yes = true
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

// flashPending sets a persistent in-progress status (e.g. "Merging foo/bar#421...")
// that remains visible until replaced by the action result.
func flashPending(m *tuiModel, verb string, pr *PullRequest) {
	m.flash.Msg = m.styles.statusPending.Render(verb) + " " +
		styleRef.Render(pr.Ref()) + valueEllipsis
	m.flash.Err = false
}

func flashResult(m *tuiModel, action, ref, url string, isErr bool) tea.Cmd {
	var msg string
	if isErr {
		msg = fmt.Sprintf("%s %s", action, ref)
	} else {
		styledRef := styleRef.Render(ref)
		if url != "" {
			styledRef = ansi.Force().Hyperlink(url, styledRef)
		}
		msg = m.styles.statusAction.Render(action) + " " + styledRef
	}
	clearMsg := m.flash.Set(msg, isErr)
	return tea.Tick(tuiStatusFlash, func(time.Time) tea.Msg { return clearMsg })
}

func tuiFlashMessage(m *tuiModel, text string, isErr bool) tea.Cmd {
	clearMsg := m.flash.Set(text, isErr)
	return tea.Tick(tuiStatusFlash, func(time.Time) tea.Msg { return clearMsg })
}

func renderBatchFailurePrompt(msg batchActionMsg) string {
	if len(msg.failures) == 0 {
		return "Some batch actions failed."
	}

	const maxFailures = 5
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", styleDanger.Bold(true).Render(msg.action.Verb()+" failed:"))
	limit := min(len(msg.failures), maxFailures)
	for i := range limit {
		failure := msg.failures[i]
		if failure.ref != "" {
			ref := styleRef.Bold(true).Render(failure.ref)
			if failure.url != "" {
				ref = ansi.Force().Hyperlink(failure.url, ref)
			}
			fmt.Fprintf(&b, "%s: %v\n", ref, failure.err)
			continue
		}
		fmt.Fprintf(&b, "%v\n", failure.err)
	}
	if remaining := len(msg.failures) - limit; remaining > 0 {
		fmt.Fprintf(&b, "\n...and %d more.", remaining)
	}
	return strings.TrimRight(b.String(), nl)
}

// updateListActions handles action keybinds in the list view.
// Returns (model, cmd, true) if the key was handled, or (model, nil, false) otherwise.
func (m tuiModel) updateListActions(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case tuiKeybindApprove:
		targets := m.targetApprovablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		setupConfirmBatch(&m, targets, tuiActionApprove, tuiActionApproved, "Approve",
			func(a *ActionRunner, pr PullRequest) error {
				return a.approvePR(pr)
			})
		return m, nil, true

	case tuiKeybindApproveNoConfirm:
		targets := m.targetApprovablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		return m, batchCmd(m.actions, targets, tuiActionApproved,
			func(a *ActionRunner, pr PullRequest) error {
				return a.approvePR(pr)
			}), true

	case tuiKeybindDiff:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil, true
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
		m.refreshTerminalSize()
		m.diffLoading = true
		flashPending(&m, statusDiffing, &first.pr)
		fetchCmd := func() tea.Msg {
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
		return m, tea.Batch(requestWindowSizeCmd(), fetchCmd), true

	case tuiKeybindMerge:
		targets := m.targetMergeablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionMerge
		m.confirmState.Yes = true
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
		return m, nil, true

	case tuiKeybindApproveMerge:
		targets := m.targetApprovablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionApproveMerge
		m.confirmState.Yes = true
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			m.confirmPrompt = "Approve & merge " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				owner, repo := prOwnerRepo(t.pr)
				if err := actions.approvePR(t.pr); err != nil {
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
						if err := a.approvePR(pr); err != nil {
							return err
						}
						_, err := a.mergeOrAutomerge(owner, repo, pr)
						return err
					},
				)
			}
		}
		return m, nil, true

	case tuiKeybindForceMerge:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionForceMerge
		m.confirmState.Yes = true
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			m.confirmPrompt = "Force-merge " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmd = func() tea.Msg {
				err := actions.retryForceMergePR(context.Background(), t.pr.NodeID)
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
						return a.retryForceMergePR(context.Background(), pr.NodeID)
					},
				)
			}
		}
		return m, nil, true

	case tuiKeybindClose:
		// Dynamic close/reopen: if the current PR is closed, reopen; otherwise close.
		pr := m.currentPR()
		if pr != nil && strings.ToLower(pr.State) == valueClosed {
			targets := m.targetPRs()
			if len(targets) == 0 {
				return m, nil, true
			}
			return m, batchCmd(m.actions, targets, tuiActionReopened,
				func(a *ActionRunner, pr PullRequest) error {
					owner, repo := prOwnerRepo(pr)
					return a.reopenPR(owner, repo, pr.Number)
				}), true
		}
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		actions := m.actions
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionClose
		m.confirmState.Yes = true
		m.confirmHasInput = true
		m = m.prepareConfirmInput()
		m.confirmInput.SetValue("")
		if len(targets) == 1 {
			m.confirmSubject = targets[0].pr.Ref()
			m.confirmURL = targets[0].pr.URL
			m.confirmPrompt = "Close " + styledRef(&targets[0].pr) + "?"
			t := targets[0]
			m.confirmCmdFn = func(submission confirmSubmission) tea.Cmd {
				comment := submission.Input
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
			m.confirmCmdFn = func(submission confirmSubmission) tea.Cmd {
				comment := submission.Input
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
		return m, m.confirmInput.Focus(), true

	case tuiKeybindDraftToggle:
		pr := m.currentPR()
		if pr == nil {
			return m, nil, true
		}
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil, true
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
			}, true
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
		}, true

	case tuiKeybindComment:
		pr := m.currentPR()
		if pr == nil {
			return m, nil, true
		}
		actions := m.actions
		idx := m.cursor
		prCopy := *pr
		m.confirmAction = tuiActionComment
		m.confirmSubject = prCopy.Ref()
		m.confirmURL = prCopy.URL
		m.confirmState.Yes = true
		m.confirmHasInput = true
		m = m.prepareConfirmInput()
		m = m.setConfirmInputPlaceholder("Leave blank to close without comment")
		m.confirmInput.SetValue("")
		m.confirmPrompt = "Comment on " + styledRef(&prCopy) + "?"
		m.confirmCmdFn = func(submission confirmSubmission) tea.Cmd {
			comment := submission.Input
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
		return m, m.confirmInput.Focus(), true

	case tuiKeybindReview:
		if !hasAIReviewLauncher() {
			m.confirmAction = tuiActionInfo
			m.confirmState.Yes = true
			m.confirmPrompt = tuiAIReviewUnsupported
			m.confirmCmd = nil
			return m, nil, true
		}
		pr := m.currentPR()
		if pr == nil {
			return m, nil, true
		}
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, flashResult(&m, "Cannot review:", "PR is "+state, "", true), true
		}
		idx := m.cursor
		prCopy := *pr
		m = m.prepareAIReviewConfirm(prCopy, idx)
		return m, nil, true

	case tuiKeybindReviewNoConfirm:
		if !hasAIReviewLauncher() {
			m.confirmAction = tuiActionInfo
			m.confirmState.Yes = true
			m.confirmPrompt = tuiAIReviewUnsupported
			m.confirmCmd = nil
			return m, nil, true
		}
		pr := m.currentPR()
		if pr == nil {
			return m, nil, true
		}
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil, true
		}
		idx := m.cursor
		prCopy := *pr
		provider := configuredReviewProvider(m.cfg)
		model := configuredReviewModel(m.cfg, provider)
		effort := configuredReviewEffort(m.cfg, provider, model)
		prompt := reviewPrompt(prCopy, m.cfg, provider)
		return m, func() tea.Msg {
			err := launchAIReview(prCopy, prompt, m.cfg, provider, model, effort)
			return aiReviewMsg{index: idx, key: makePRKey(prCopy), err: err}
		}, true

	case tuiKeybindSlack:
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		prs := make([]PullRequest, len(targets))
		for i, t := range targets {
			prs[i] = t.pr
		}
		count := len(prs)
		cfg := m.cfg
		cli := m.cli
		m.confirmAction = tuiActionSendSlack
		m.confirmState.Yes = true
		if count == 1 {
			m.confirmSubject = prs[0].Ref()
			m.confirmURL = prs[0].URL
			m.confirmPrompt = "Send " + styledRef(&prs[0]) + " to Slack?"
		} else {
			m.confirmSubject = fmt.Sprintf("%d PRs", count)
			m.confirmPrompt = fmt.Sprintf("Send %d PRs to Slack?", count)
		}
		m.confirmCmd = func() tea.Msg {
			err := pluginSlackSend(cfg, cli.SendTo, prs)
			return slackSentMsg{count: count, err: err}
		}
		return m, nil, true

	case tuiKeybindSlackNoConfirm:
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		prs := make([]PullRequest, len(targets))
		for i, t := range targets {
			prs[i] = t.pr
		}
		count := len(prs)
		if count == 1 {
			flashPending(&m, statusSlacking, &prs[0])
		} else {
			m.flash.Msg = m.styles.statusPending.Render(
				fmt.Sprintf("Sending %d PRs", count),
			) + valueEllipsis
			m.flash.Err = false
		}
		cfg := m.cfg
		cli := m.cli
		return m, func() tea.Msg {
			err := pluginSlackSend(cfg, cli.SendTo, prs)
			return slackSentMsg{count: count, err: err}
		}, true

	case tuiKeybindOpen:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil, true
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
		return m, flashResult(&m, tuiActionOpened.String(), msg, last.pr.URL, false), true

	case tuiKeybindCopyURL:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		urls := make([]string, len(targets))
		for i, t := range targets {
			urls[i] = t.pr.URL
		}
		natsort(urls)
		_ = copyToClipboard(strings.Join(urls, nl))
		last := targets[len(targets)-1]
		msg := last.pr.Ref()
		if len(targets) > 1 {
			msg = fmt.Sprintf("%d URLs", len(targets))
		}
		m.selected = make(prKeys)
		return m, flashResult(&m, resultCopied, msg, "", false), true

	case tuiKeybindUpdateBranch:
		targets := m.targetActionablePRs()
		if len(targets) == 0 {
			return m, nil, true
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
		return m, nil, true

	case tuiKeybindUnassign:
		targets := m.targetOtherActionablePRs()
		if len(targets) == 0 {
			return m, nil, true
		}
		actions := m.actions
		rest := m.rest
		batch := make([]targetPR, len(targets))
		copy(batch, targets)
		m.confirmAction = tuiActionUnassign
		m.confirmState.Yes = true
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
		return m, nil, true

	case tuiKeybindUnassignNoConfirm:
		targets := m.targetOtherActionablePRs()
		if len(targets) == 0 {
			return m, nil, true
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
			}, true
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
		}, true

	case tuiKeybindCopilotReview:
		targets := m.targetPRs()
		if len(targets) == 0 {
			return m, nil, true
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
					copilotReviewer,
				)
				return actionMsg{
					index:  t.index,
					key:    makePRKey(t.pr),
					action: tuiActionReviewRequested,
					err:    err,
				}
			}, true
		}
		setupConfirmBatch(
			&m,
			targets,
			tuiActionCopilotReview,
			tuiActionReviewRequested,
			"Request Copilot review for",
			func(a *ActionRunner, pr PullRequest) error {
				owner, repo := prOwnerRepo(pr)
				return a.requestReview(
					owner,
					repo,
					pr.Number,
					copilotReviewer,
				)
			},
		)
		return m, nil, true
	}

	return m, nil, false
}

// handleViewAction handles keybinds shared between diff and detail views.
// It resolves the PR using the view-appropriate key and, for detail view,
// exits back to the list before actions that show a confirm overlay.
func (m tuiModel) handleViewAction(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	var key prKey
	switch m.view {
	case tuiViewDiff:
		key = m.diffKey
	case tuiViewDetail:
		key = m.detailKey
	case tuiViewList:
		return m, nil, false
	}

	ctx, ok := m.actionContextForKey(key)
	if !ok {
		// PR no longer in list - consume the key but do nothing.
		switch msg.String() {
		case tuiKeybindOpen, tuiKeybindCopyURL, tuiKeybindSlackNoConfirm,
			tuiKeybindUpdateBranch, tuiKeybindDraftToggle, tuiKeybindComment,
			tuiKeybindSlack:
			return m, nil, true
		}
		return m, nil, false
	}

	switch msg.String() {
	case tuiKeybindOpen:
		_ = openBrowser(ctx.pr.URL)
		return m, nil, true
	case tuiKeybindCopyURL:
		_ = copyToClipboard(ctx.pr.URL)
		return m, flashResult(&m, resultCopied, ctx.pr.Ref(), "", false), true
	case tuiKeybindSlackNoConfirm:
		if !ctx.actionable {
			return m, nil, true
		}
		pr := ctx.pr
		flashPending(&m, statusSlacking, &pr)
		cfg := m.cfg
		cli := m.cli
		return m, func() tea.Msg {
			err := pluginSlackSend(cfg, cli.SendTo, []PullRequest{pr})
			return slackSentMsg{count: 1, err: err}
		}, true
	case tuiKeybindUpdateBranch:
		if !ctx.actionable {
			return m, nil, true
		}
		pr := ctx.pr
		actions := m.actions
		m.confirmAction = tuiActionUpdateBranch
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmState.Yes = true
		m.confirmPrompt = "Update branch for " + styledRef(&pr) + "?"
		m.confirmCmd = func() tea.Msg {
			owner, repo := prOwnerRepo(pr)
			err := actions.updateBranch(owner, repo, pr.Number)
			return actionMsg{
				index:  ctx.idx,
				key:    ctx.key,
				action: tuiActionBranchUpdated,
				err:    err,
			}
		}
		return m, nil, true
	case tuiKeybindDraftToggle:
		if !ctx.actionable {
			return m, nil, true
		}
		pr := ctx.pr
		actions := m.actions
		var exitCmd tea.Cmd
		if m.view == tuiViewDetail {
			exitCmd = m.exitDetailView()
		}
		if pr.IsDraft {
			flashPending(&m, statusMarkingReady, &pr)
			return m, tea.Batch(exitCmd, func() tea.Msg {
				err := actions.markReady(pr.NodeID)
				return actionMsg{
					index:  ctx.idx,
					key:    ctx.key,
					action: tuiActionMarkedReady,
					err:    err,
				}
			}), true
		}
		flashPending(&m, statusMarkingDraft, &pr)
		return m, tea.Batch(exitCmd, func() tea.Msg {
			err := actions.markDraft(pr.NodeID)
			return actionMsg{index: ctx.idx, key: ctx.key, action: tuiActionMarkedDraft, err: err}
		}), true
	case tuiKeybindComment:
		pr := ctx.pr
		actions := m.actions
		var exitCmd tea.Cmd
		if m.view == tuiViewDetail {
			exitCmd = m.exitDetailView()
		}
		m.confirmAction = tuiActionComment
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmState.Yes = true
		m.confirmHasInput = true
		m = m.prepareConfirmInput()
		m = m.setConfirmInputPlaceholder("Leave blank to close without comment")
		m.confirmInput.SetValue("")
		m.confirmPrompt = "Comment on " + styledRef(&pr) + "?"
		m.confirmCmdFn = func(submission confirmSubmission) tea.Cmd {
			comment := submission.Input
			return func() tea.Msg {
				owner, repo := prOwnerRepo(pr)
				err := actions.comment(owner, repo, pr.Number, comment)
				return actionMsg{
					index:  ctx.idx,
					key:    ctx.key,
					action: tuiActionCommented,
					err:    err,
				}
			}
		}
		return m, tea.Batch(m.confirmInput.Focus(), exitCmd), true
	case tuiKeybindSlack:
		if !ctx.actionable {
			return m, nil, true
		}
		pr := ctx.pr
		cfg := m.cfg
		cli := m.cli
		var exitCmd tea.Cmd
		if m.view == tuiViewDetail {
			exitCmd = m.exitDetailView()
		}
		m.confirmAction = tuiActionSendSlack
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmState.Yes = true
		m.confirmPrompt = "Send " + styledRef(&pr) + " to Slack?"
		m.confirmCmd = func() tea.Msg {
			err := pluginSlackSend(cfg, cli.SendTo, []PullRequest{pr})
			return slackSentMsg{count: 1, err: err}
		}
		return m, exitCmd, true
	}
	return m, nil, false
}

func (m tuiModel) updateDiffView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmAction != "" {
		return m.updateConfirmOverlay(msg)
	}
	if result, cmd, handled := m.handleViewAction(msg); handled {
		return result, cmd
	}
	switch msg.String() {
	case tuiKeybindQuit, key.Esc, tuiKeybindDiff:
		return m, m.exitDiffView()
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
	case tuiKeybindVimDown, key.Down:
		m.diffView.ScrollDown(1)
		return m, nil
	case tuiKeybindVimUp, key.Up:
		m.diffView.ScrollUp(1)
		return m, nil
	case key.CtrlF, key.Space:
		m.diffView.PageDown()
		return m, nil
	case key.CtrlB:
		m.diffView.PageUp()
		return m, nil
	case tuiKeybindTop:
		m.diffView.GotoTop()
		return m, nil
	case tuiKeybindBottom:
		m.diffView.GotoBottom()
		return m, nil
	case tuiKeybindApprove, tuiKeybindApproveNoConfirm:
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
			err := actions.approvePR(pr)
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
		m.confirmState.Yes = true
		m.confirmHasInput = true
		m = m.prepareConfirmInput()
		m = m.setConfirmInputPlaceholder("Leave blank to close without comment")
		m.confirmInput.SetValue("")
		m.confirmPrompt = "Close " + styledRef(&pr) + "?"
		m.confirmCmdFn = func(submission confirmSubmission) tea.Cmd {
			comment := submission.Input
			return func() tea.Msg {
				owner, repo := prOwnerRepo(pr)
				err := actions.closePR(owner, repo, pr.Number, comment, false)
				return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionClosed, err: err}
			}
		}
		return m, m.confirmInput.Focus()
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
			if err := actions.approvePR(pr); err != nil {
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
				copilotReviewer,
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
	if m.confirmAction != "" {
		return m.updateConfirmOverlay(msg)
	}
	if result, cmd, handled := m.handleViewAction(msg); handled {
		return result, cmd
	}
	switch msg.String() {
	case tuiKeybindQuit, key.Esc, key.Enter:
		return m, m.exitDetailView()
	case tuiKeybindVimDown, key.Down:
		m.detailView.ScrollDown(1)
		return m, nil
	case tuiKeybindVimUp, key.Up:
		m.detailView.ScrollUp(1)
		return m, nil
	case key.CtrlF, key.Space:
		m.detailView.PageDown()
		return m, nil
	case key.CtrlB:
		m.detailView.PageUp()
		return m, nil
	case tuiKeybindTop:
		m.detailView.GotoTop()
		return m, nil
	case tuiKeybindBottom:
		m.detailView.GotoBottom()
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
		m.refreshTerminalSize()
		m.diffLoading = true
		fetchCmd := func() tea.Msg {
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
		return m, tea.Batch(requestWindowSizeCmd(), fetchCmd)
	case tuiKeybindApprove:
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
		refreshCmd := m.exitDetailView()
		m.confirmAction = tuiActionApprove
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmState.Yes = true
		m.confirmPrompt = "Approve " + styledRef(&pr) + "?"
		m.confirmCmd = func() tea.Msg {
			err := actions.approvePR(pr)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionApproved, err: err}
		}
		return m, refreshCmd
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
		refreshCmd := m.exitDetailView()
		return m, tea.Batch(refreshCmd, func() tea.Msg {
			err := actions.approvePR(pr)
			return actionMsg{index: idx, key: makePRKey(pr), action: tuiActionApproved, err: err}
		})
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
		refreshCmd := m.exitDetailView()
		verb := "Automerge "
		if pr.MergeStatus == MergeStatusReady {
			verb = "Merge "
		}
		m.confirmAction = tuiActionMerge
		m.confirmSubject = pr.Ref()
		m.confirmURL = pr.URL
		m.confirmState.Yes = true
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
		return m, refreshCmd
	case tuiKeybindReview:
		if !hasAIReviewLauncher() {
			refreshCmd := m.exitDetailView()
			m.confirmAction = tuiActionInfo
			m.confirmState.Yes = true
			m.confirmPrompt = tuiAIReviewUnsupported
			m.confirmCmd = nil
			return m, refreshCmd
		}
		idx := m.resolveIndex(m.detailKey, -1)
		if idx < 0 {
			return m, nil
		}
		pr := m.rows[idx].Item.PR
		state := strings.ToLower(pr.State)
		if state == valueMerged || state == valueClosed {
			return m, nil
		}
		refreshCmd := m.exitDetailView()
		m = m.prepareAIReviewConfirm(pr, idx)
		return m, refreshCmd
	}
	return m, nil
}
