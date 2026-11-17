package main

import tea "charm.land/bubbletea/v2"

const defaultUnapproveMessage = "Approval withdrawn by reviewer."

func approvalHelp(remove bool) string {
	if remove {
		return tuiHelpUnapprove
	}
	return tuiHelpApprove
}

// approvalToggleTargets approves a mixed selection, or removes approvals when
// every eligible target is already approved. Pending targets are always skipped.
func (m tuiModel) approvalToggleTargets() ([]targetPR, []targetPR, bool) {
	approve, skipped := m.approvableTargets()
	if len(approve) > 0 {
		return approve, skipped, false
	}
	var remove, pending []targetPR
	for _, target := range skipped {
		if _, ok := m.pendingApprovals[makePRKey(target.pr)]; ok {
			pending = append(pending, target)
		} else {
			remove = append(remove, target)
		}
	}
	return remove, pending, true
}

func approvalChangeCmd(actions *ActionRunner, targets []targetPR, remove bool) tea.Cmd {
	if !remove {
		return approveTargetsCmd(actions, targets)
	}
	return unapproveTargetsCmd(actions, targets, defaultUnapproveMessage)
}

func unapproveTargetsCmd(actions *ActionRunner, targets []targetPR, message string) tea.Cmd {
	cmd := batchCmd(
		actions,
		targets,
		tuiActionUnapproved,
		func(a *ActionRunner, pr PullRequest) error {
			return a.unapprovePR(pr, message)
		},
	)
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case actionMsg:
			msg.ref, msg.url = targets[0].pr.Ref(), targets[0].pr.URL
			msg.navigated = true
			msg.approvals = []approvalResult{{key: msg.key, remove: true, err: msg.err}}
			return msg
		case batchActionMsg:
			for _, key := range msg.keys {
				msg.approvals = append(msg.approvals, approvalResult{key: key, remove: true})
			}
			for _, failure := range msg.failures {
				msg.approvals = append(
					msg.approvals,
					approvalResult{key: failure.key, remove: true, err: failure.err},
				)
			}
			return msg
		default:
			return msg
		}
	}
}

func (m *tuiModel) confirmApprovalChange(targets []targetPR, remove bool, cmd tea.Cmd) {
	action, verb := tuiActionApprove, "Approve"
	if remove {
		action, verb = tuiActionUnapprove, "Unapprove"
	}
	setupConfirmPrompt(m, targets, action, verb)
	m.confirmCmd = cmd
	if remove {
		m.confirmHasInput = true
		m.confirmInputLabel = "Reason"
		m.confirmInputValue = defaultUnapproveMessage
		actions := m.actions
		batch := append([]targetPR(nil), targets...)
		m.confirmCmdFn = func(submission confirmSubmission) tea.Cmd {
			return unapproveTargetsCmd(actions, batch, submission.Input)
		}
	}
	keys := targetKeys(targets)
	m.confirmOnAccept = func(m *tuiModel) { m.beginApprovalChange(keys, remove) }
}
