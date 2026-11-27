package main

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/x/ansi"
)

type wheelTarget uint8

const (
	wheelTargetNone wheelTarget = iota
	wheelTargetList
	wheelTargetDiff
	wheelTargetDetail
	wheelTargetConfirm
	wheelTargetPresetMenu
	wheelTargetHelp
)

func resolveWheelTarget(model tea.Model) (wheelTarget, bool) {
	tui, ok := model.(tuiModel)
	if !ok {
		return wheelTargetNone, false
	}
	return tui.wheelScrollTarget()
}

// wheelScrollTarget returns the scroll target for the current view state.
// Used as the resolver callback for [scrollwheel.Coalescer].
func (m tuiModel) wheelScrollTarget() (wheelTarget, bool) {
	if m.help != nil {
		return wheelTargetHelp, true
	}
	if m.showOptions {
		return wheelTargetNone, false
	}
	if m.presetMenu.open {
		return wheelTargetPresetMenu, true
	}
	if m.confirmAction != "" {
		return wheelTargetConfirm, true
	}

	switch m.view {
	case tuiViewList:
		return wheelTargetList, true
	case tuiViewDiff:
		return wheelTargetDiff, true
	case tuiViewDetail:
		return wheelTargetDetail, true
	default:
		return wheelTargetNone, false
	}
}

func (m *tuiModel) applyWheelScroll(target wheelTarget, delta int) {
	if delta == 0 {
		return
	}
	current, ok := m.wheelScrollTarget()
	if !ok || current != target {
		return
	}

	switch target {
	case wheelTargetNone:
		return
	case wheelTargetHelp:
		h := m.currentHelp()
		h.scrollTo(h.offset + delta)
	case wheelTargetPresetMenu:
		m.movePresetMenu(delta)
	case wheelTargetList:
		m.applyListWheelScroll(delta)
	case wheelTargetDiff:
		m.diffView.scroll(delta)
	case wheelTargetDetail:
		m.detailView.scroll(delta)
	case wheelTargetConfirm:
		if m.screen != nil && m.screen.dialog != nil {
			m.screen.dialog.scrollTo(m.screen.dialog.offset + delta)
		}
	}
}

func (m *tuiModel) applyListWheelScroll(delta int) {
	if delta == 0 {
		return
	}

	step := 1
	if delta < 0 {
		step = -1
		delta = -delta
	}

	for range delta {
		next, ok := m.nextVisible(step)
		if !ok {
			break
		}
		m.cursor = next
	}
	m.offset = m.scrolledOffset()
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

func (m tuiModel) detailViewport() int {
	h := 1 + m.helpLines(m.detailHelpPairs())
	if m.height <= h {
		return 1
	}
	return m.height - h
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

func (m tuiModel) confirmInputWidth() int {
	w := tuiConfirmInputWidth
	if m.confirmAction == tuiActionReview {
		w = tuiAIReviewConfirmInputWid
	}
	// Cap to terminal width minus border+padding so the modal never overflows.
	if maxW := m.width - tuiConfirmPadX*2 - 2; m.width > 0 && w > maxW { //nolint:mnd // border cols
		w = max(20, maxW) //nolint:mnd // minimum usable width
	}
	return w
}

// pillWrap adds powerline half-circle caps around solid-background content.
// Caps are fg-coloured on the surrounding default background -- no content
// background or padding is applied to them.
func pillWrap(rendered string, capColor color.Color, dim bool) string {
	if activeIcons.PillLeft == "" {
		return rendered
	}
	capStyle := lg.NewStyle().Foreground(capColor).Faint(dim)
	return capStyle.Render(activeIcons.PillLeft) + rendered +
		capStyle.Render(activeIcons.PillRight)
}

// pillButtonStyle adapts a Primer button style so its label is rendered first,
// then wrapped with separately styled caps. Keeping the wrapper as the outer
// transform prevents the button background and padding from reaching the caps.
// Blurred buttons reserve the cap width with spaces so focus changes don't shift them.
func pillButtonStyle(body lg.Style, capColor color.Color, dim bool) lg.Style {
	if activeIcons.PillLeft == "" {
		return body
	}
	return lg.NewStyle().Transform(func(label string) string {
		if dim {
			return strings.Repeat(" ", lg.Width(activeIcons.PillLeft)) + body.Render(label) +
				strings.Repeat(" ", lg.Width(activeIcons.PillRight))
		}
		return pillWrap(body.Render(label), capColor, dim)
	})
}

// confirmActionVerb maps confirm action names to in-progress verbs.
var confirmActionVerb = map[string]string{
	tuiActionApprove:       "Approving",
	tuiActionUnapprove:     "Unapproving",
	tuiActionApproveMerge:  "Approving & merging",
	tuiActionClose:         "Closing",
	tuiActionComment:       "Commenting",
	tuiActionCopilotReview: "Requesting Copilot review for",
	tuiActionForceMerge:    "Force-merging",
	tuiActionMerge:         "Merging",
	tuiActionSendSlack:     "Slacking",
	tuiActionUnassign:      "Unassigning",
	tuiActionUpdateBranch:  "Updating branch",
}

func (m tuiModel) confirmAccept() (tea.Model, tea.Cmd) {
	return m.confirmAcceptWithSubmission(confirmSubmission{})
}

func (m tuiModel) confirmAcceptWithSubmission(submission confirmSubmission) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.confirmHasInput && m.confirmCmdFn != nil {
		cmd = m.confirmCmdFn(submission)
	} else {
		cmd = m.confirmCmd
	}
	cmd = m.trackAction(cmd)
	if m.confirmOnAccept != nil {
		m.confirmOnAccept(&m)
	}
	verb := confirmActionVerb[m.confirmAction]
	subject := m.confirmSubject
	url := m.confirmURL
	m = m.forgetConfirmDraft()
	m = m.clearConfirm()
	if verb != "" {
		if subject != "" {
			styledSubject := styleRef.Render(subject)
			if url != "" {
				styledSubject = ansi.Force().Hyperlink(url, styledSubject)
			}
			m.flash.Msg = m.styles.statusPending.Render(
				verb,
			) + " " + styledSubject + valueEllipsis
		} else {
			m.flash.Msg = m.styles.statusPending.Render(verb) + valueEllipsis
		}
		m.flash.Err = false
	}
	return m, cmd
}

func (m tuiModel) confirmDismiss() (tea.Model, tea.Cmd) {
	m = m.clearConfirm()
	return m, nil
}
