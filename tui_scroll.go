package main

import (
	"image/color"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/scrollbar"
	"github.com/gechr/primer/view"
	"github.com/gechr/x/ansi"
)

type scrollbarTarget uint8

const (
	scrollbarTargetNone scrollbarTarget = iota
	scrollbarTargetDiff
	scrollbarTargetDetail
	scrollbarTargetConfirm
)

type wheelTarget uint8

const (
	wheelTargetNone wheelTarget = iota
	wheelTargetList
	wheelTargetDiff
	wheelTargetDetail
	wheelTargetConfirm
)

type scrollbarDragState struct {
	scrollbar.Drag

	target scrollbarTarget
}

// wheelScrollTarget returns the scroll target for the current view state.
// Used as the resolver callback for [scrollwheel.Coalescer].
func (m tuiModel) wheelScrollTarget() (wheelTarget, bool) {
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
	case wheelTargetList:
		m.applyListWheelScroll(delta)
	case wheelTargetDiff:
		if delta > 0 {
			m.diffView.ScrollDown(delta)
		} else {
			m.diffView.ScrollUp(-delta)
		}
	case wheelTargetDetail:
		if delta > 0 {
			m.detailView.ScrollDown(delta)
		} else {
			m.detailView.ScrollUp(-delta)
		}
	case wheelTargetConfirm:
		if m.dialogs != nil {
			m.dialogs.ScrollBy(delta)
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

func newScrollView() viewport.Model {
	v := viewport.New()
	v.KeyMap = viewport.KeyMap{} // disable all key bindings - views handle their own
	v.MouseWheelEnabled = true
	v.MouseWheelDelta = 1
	return v
}

func (m *tuiModel) syncDiffView() {
	m.diffRenderLines = view.Sync(
		&m.diffView, m.diffLines, max(0, m.width-tuiScrollbarWidth), m.diffContentViewport(),
	)
}

func (m *tuiModel) syncDetailView() {
	m.detailRenderLines = view.Sync(
		&m.detailView, m.detailLines, max(0, m.width-tuiScrollbarWidth), m.detailViewport(),
	)
}

func (m *tuiModel) handleScrollbarPress(msg tea.Mouse) bool {
	if !m.confirmUsesDialog() {
		switch m.view {
		case tuiViewList:
			return false
		case tuiViewDiff:
			m.syncDiffView()
		case tuiViewDetail:
			m.syncDetailView()
		default:
			return false
		}
	}

	hitbox, target, ok := m.scrollbarHitboxAt(msg.X, msg.Y)
	if !ok {
		return false
	}

	percent := m.scrollbarPercent(target)
	offset := m.scrollDrag.Press(hitbox, msg.Y, percent)
	m.scrollDrag.target = target

	if vp := m.scrollbarViewport(target); vp != nil {
		vp.SetYOffset(offset)
	} else if target == scrollbarTargetConfirm && m.dialogs != nil {
		m.dialogs.SetScrollOffset(offset)
	}
	return true
}

func (m *tuiModel) handleScrollbarMotion(msg tea.Mouse) bool {
	hitbox, ok := m.scrollbarHitbox(m.scrollDrag.target)
	if !ok {
		m.scrollDrag = scrollbarDragState{}
		return false
	}
	offset, active := m.scrollDrag.Motion(hitbox, msg.Y)
	if !active {
		return false
	}
	if vp := m.scrollbarViewport(m.scrollDrag.target); vp != nil {
		vp.SetYOffset(offset)
	} else if m.scrollDrag.target == scrollbarTargetConfirm && m.dialogs != nil {
		m.dialogs.SetScrollOffset(offset)
	}
	return true
}

func (m *tuiModel) scrollbarViewport(target scrollbarTarget) *viewport.Model {
	switch target {
	case scrollbarTargetNone:
		return nil
	case scrollbarTargetDiff:
		return &m.diffView
	case scrollbarTargetDetail:
		return &m.detailView
	case scrollbarTargetConfirm:
		return nil
	default:
		return nil
	}
}

func (m tuiModel) scrollbarPercent(target scrollbarTarget) float64 {
	if target == scrollbarTargetConfirm && m.dialogs != nil {
		return m.dialogs.ScrollPercent()
	}
	if vp := m.scrollbarViewport(target); vp != nil {
		return vp.ScrollPercent()
	}
	return 0
}

func (m tuiModel) scrollbarHitboxAt(x, y int) (scrollbar.Hitbox, scrollbarTarget, bool) {
	for _, target := range []scrollbarTarget{
		scrollbarTargetConfirm,
		scrollbarTargetDiff,
		scrollbarTargetDetail,
	} {
		hitbox, ok := m.scrollbarHitbox(target)
		if !ok {
			continue
		}
		if hitbox.Contains(x, y) {
			return hitbox, target, true
		}
	}
	return scrollbar.Hitbox{}, scrollbarTargetNone, false
}

func (m tuiModel) viewportScrollbarHitbox(
	vp *viewport.Model,
	view tuiView,
	y int,
) (scrollbar.Hitbox, bool) {
	totalLines := vp.TotalLineCount()
	height := vp.Height()
	if totalLines <= height || height <= 0 || m.width <= 0 || m.view != view ||
		m.confirmAction != "" {
		return scrollbar.Hitbox{}, false
	}
	return scrollbar.Hitbox{
		X:          m.width - 1,
		Y:          y,
		Height:     height,
		TotalLines: totalLines,
	}, true
}

func (m tuiModel) scrollbarHitbox(target scrollbarTarget) (scrollbar.Hitbox, bool) {
	switch target {
	case scrollbarTargetNone:
		return scrollbar.Hitbox{}, false
	case scrollbarTargetDiff:
		y := 0
		if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
			y = 2
		}
		return m.viewportScrollbarHitbox(&m.diffView, tuiViewDiff, y)
	case scrollbarTargetDetail:
		return m.viewportScrollbarHitbox(&m.detailView, tuiViewDetail, 0)
	case scrollbarTargetConfirm:
		if !m.confirmUsesDialog() || m.dialogs == nil {
			return scrollbar.Hitbox{}, false
		}
		return m.dialogs.ScrollbarHitbox()
	default:
		return scrollbar.Hitbox{}, false
	}
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
func pillButtonStyle(body lg.Style, capColor color.Color, dim bool) lg.Style {
	if activeIcons.PillLeft == "" {
		return body
	}
	return lg.NewStyle().Transform(func(label string) string {
		return pillWrap(body.Render(label), capColor, dim)
	})
}

// confirmActionVerb maps confirm action names to in-progress verbs.
var confirmActionVerb = map[string]string{
	tuiActionApprove:       "Approving",
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
	verb := confirmActionVerb[m.confirmAction]
	subject := m.confirmSubject
	url := m.confirmURL
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
