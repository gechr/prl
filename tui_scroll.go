package main

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/prompt"
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
		m.syncConfirmView()
		if delta > 0 {
			m.confirmView.ScrollDown(delta)
		} else {
			m.confirmView.ScrollUp(-delta)
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

func newScrollViewSoftWrap() viewport.Model {
	v := newScrollView()
	v.SoftWrap = true
	return v
}

// syncConfirmView updates the persistent viewport with the current modal
// content and dimensions so that scroll operations (mouse wheel, page up/down)
// have accurate line counts. Called in Update paths before scrolling.
func (m *tuiModel) syncConfirmView() {
	boxWidth := m.confirmInputWidth() + tuiConfirmPadX*2 //nolint:mnd // border + padding
	scrollbarWidth := tuiScrollbarWidth
	m.confirmView.SetWidth(max(1, boxWidth-(tuiConfirmPadX-1)*2-2-scrollbarWidth))
	m.confirmView.SetHeight(m.confirmViewport())
	m.confirmView.SetContent(layout.ExpandTabs(m.confirmModalContent()))
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

// confirmViewport returns the maximum number of inner content lines that fit
// within the terminal, leaving room for the box border and vertical padding.
func (m tuiModel) confirmViewport() int {
	// 2 border rows + 2×1 vertical padding (top/bottom inside the box).
	const chrome = 4
	return max(chrome-1, m.height-chrome)
}

func (m *tuiModel) handleScrollbarPress(msg tea.Mouse) bool {
	if m.confirmAction != "" {
		m.syncConfirmView()
	} else {
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
		return &m.confirmView
	default:
		return nil
	}
}

func (m tuiModel) scrollbarPercent(target scrollbarTarget) float64 {
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
		if m.confirmAction == "" {
			return scrollbar.Hitbox{}, false
		}
		totalLines := m.confirmView.TotalLineCount()
		height := m.confirmView.Height()
		if totalLines <= height || height <= 0 {
			return scrollbar.Hitbox{}, false
		}
		return m.confirmScrollbarHitbox()
	default:
		return scrollbar.Hitbox{}, false
	}
}

func (m tuiModel) confirmScrollbarHitbox() (scrollbar.Hitbox, bool) {
	const centerDivisor = 2

	lines := strings.Split(ansi.Strip(m.renderConfirmModal()), nl)
	if len(lines) == 0 {
		return scrollbar.Hitbox{}, false
	}

	fgWidth := 0
	for _, line := range lines {
		if w := ansi.WcWidth.StringWidth(line); w > fgWidth {
			fgWidth = w
		}
	}
	startRow := max(0, (m.height-len(lines))/centerDivisor)
	startCol := max(0, (m.width-fgWidth)/centerDivisor)

	for row, line := range lines {
		col := strings.IndexAny(line, "█┃")
		if col < 0 {
			continue
		}
		return scrollbar.Hitbox{
			X:          startCol + ansi.WcWidth.StringWidth(line[:col]),
			Y:          startRow + row,
			Height:     m.confirmView.Height(),
			TotalLines: m.confirmView.TotalLineCount(),
		}, true
	}
	return scrollbar.Hitbox{}, false
}

func (m tuiModel) renderConfirmModal() string {
	boxStyle := m.styles.overlayBox.Padding(
		1,
		tuiConfirmPadX-1,
		1,
		tuiConfirmPadX-1,
	)

	var buttons string
	if m.confirmCmd == nil && m.confirmCmdFn == nil {
		// Info-only modal - single OK button.
		buttons = lg.NewStyle().
			Background(colorTitle).
			Foreground(colorBlack).
			Padding(0, 1).
			Bold(true).
			Render("OK")
	} else {
		var yes, no string
		if m.confirmState.Yes {
			yes = m.styles.confirmYes.Render("Yes")
			no = m.styles.confirmNoDim.Render("No")
		} else {
			yes = m.styles.confirmYesDim.Render("Yes")
			no = m.styles.confirmNo.Render("No")
		}
		buttons = no + "  " + yes
	}

	if m.confirmHasInput {
		// Fix width so the border stays aligned as the textarea grows.
		boxWidth := m.confirmInputWidth() + tuiConfirmPadX*2 //nolint:mnd // border + padding
		return m.renderConfirmScrollable(boxStyle, boxWidth, m.confirmModalContent())
	}

	content := m.confirmModalContent()
	promptWidth := lg.Width(m.confirmPrompt)
	return m.renderConfirmScrollable(boxStyle, 0, content+prompt.CenterRow(buttons, promptWidth))
}

// renderConfirmScrollable renders inner content inside a styled box, using a
// viewport for clipping and a scrollbar when the content exceeds the terminal.
func (m tuiModel) renderConfirmScrollable(boxStyle lg.Style, boxWidth int, content string) string {
	vpHeight := m.confirmViewport()
	lines := strings.Split(content, nl)
	if len(lines) <= vpHeight {
		if boxWidth > 0 {
			return boxStyle.Width(boxWidth).Render(content)
		}
		return boxStyle.Render(content)
	}

	// Use the viewport for clipping + scroll offset.
	view := m.confirmView                             // copy - scroll offset persists via Update, content is ephemeral
	innerWidth := boxWidth - (tuiConfirmPadX-1)*2 - 2 //nolint:mnd // subtract padding + border
	scrollbarWidth := tuiScrollbarWidth
	viewWidth := max(1, innerWidth-scrollbarWidth)
	view.SetWidth(viewWidth)
	view.SetHeight(vpHeight)

	// Re-render content at the narrower viewport width so the textarea
	// lines aren't soft-wrapped by the viewport (value receiver - local copy).
	if m.confirmHasInput {
		m.confirmInput.SetWidth(viewWidth)
		content = m.confirmModalContent()
	}
	if boxWidth > 0 {
		boxWidth -= tuiConfirmPadX - 2 //nolint:mnd // keep 1 space between scrollbar and border
	}
	return prompt.RenderScrollable(prompt.ScrollableModel{
		BoxStyle:       boxStyle,
		BoxWidth:       boxWidth,
		Content:        layout.ExpandTabs(content),
		View:           view,
		ViewportHeight: vpHeight,
		ViewWidth:      viewWidth,
		Styles: prompt.Styles{
			Scrollbar: scrollbar.Styles{
				Thumb: lg.NewStyle().Foreground(colorAccent),
				Track: lg.NewStyle().Foreground(colorAccent).Faint(true),
			},
		},
	})
}

// confirmModalContent returns the inner content string for the confirm modal
// (prompt + options + textarea + hints). Hints are omitted when the modal
// needs scrolling to maximise usable space.
func (m tuiModel) confirmModalContent() string {
	if !m.confirmHasInput {
		return prompt.ComposeContent(prompt.ContentModel{
			Prompt:   m.confirmPrompt,
			HasField: false,
		})
	}

	label := m.confirmInputLabel
	if label == "" {
		label = "Comment"
	}

	hints := m.confirmInputHints()
	options := ""
	if m.hasConfirmOptions() {
		options = m.confirmOptionsHeader()
	}
	content := prompt.ComposeContent(prompt.ContentModel{
		Prompt:     m.confirmPrompt,
		Options:    options,
		FieldLabel: m.styles.helpKey.Render(label),
		FieldBody:  m.confirmInput.View(),
		Hints:      hints,
		HasField:   true,
	})
	withHints := prompt.ComposeContent(prompt.ContentModel{
		Prompt:       m.confirmPrompt,
		Options:      options,
		FieldLabel:   m.styles.helpKey.Render(label),
		FieldBody:    m.confirmInput.View(),
		Hints:        hints,
		IncludeHints: true,
		HasField:     true,
	})
	if strings.Count(withHints, nl)+1 <= m.confirmViewport() {
		return withHints
	}
	return content
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

// confirmTextareaMaxHeight returns a dynamic MaxHeight for the confirm textarea
// so it shrinks on small terminals instead of overflowing.
func (m tuiModel) confirmTextareaMaxHeight() int {
	if m.height <= 0 {
		return tuiConfirmInputMaxHeight
	}
	// Reserve space for: border (2) + padding (2) + prompt (~2) + label (1) +
	// options (~6) + hints (1) + blank lines (~3).
	const overhead = 17
	available := max(tuiConfirmInputMinHeight, m.height-overhead)
	return min(available, tuiConfirmInputMaxHeight)
}

// scrollConfirmToFocus scrolls the confirm viewport so the currently focused
// element (option row or textarea) is visible. Call after any focus change.
func (m *tuiModel) scrollConfirmToFocus() {
	m.syncConfirmView()
	line := m.confirmState.FocusLine(len(m.confirmOptions), m.confirmHasInput)
	if line >= 0 {
		m.confirmView.EnsureVisible(line, 0, 0)
	}
}

func (m tuiModel) confirmOptionsHeader() string {
	groups := make([]prompt.ChoiceGroup, 0, len(m.confirmOptions))
	for _, def := range m.confirmOptions {
		choices := make([]prompt.Choice, 0, len(def.choices))
		for _, choice := range def.choices {
			choices = append(choices, prompt.Choice{Label: choice.label})
		}
		groups = append(groups, prompt.ChoiceGroup{
			Label:   def.label,
			Choices: choices,
		})
	}
	return prompt.RenderChoiceGroups(
		groups,
		m.confirmState.OptValues,
		m.confirmState.OptCursor,
		m.confirmState.OptFocus,
		prompt.ChoiceGroupStyles{
			Label:          m.styles.helpKey,
			SelectedActive: styleHighlight.Bold(true),
			Selected:       styleTitle.Bold(true),
			Active:         styleHighlight.Faint(true),
			Inactive:       lg.NewStyle().Faint(true),
		},
	)
}

func (m tuiModel) confirmInputHints() string {
	secondLine := []prompt.Hint{
		{Key: key.AltEnter, Text: "submit"},
		{Key: key.Esc, Text: "cancel"},
	}
	if !m.hasConfirmOptions() {
		return prompt.RenderHintLines(
			[][]prompt.Hint{secondLine},
			helpGap,
			m.styles.helpKey,
			m.styles.helpText,
		)
	}

	firstLine := []prompt.Hint{
		{Key: key.Tab, Text: "next"},
		{Key: key.ShiftTab, Text: "prev"},
		{Key: key.ArrowsLeftRight, Text: "select"},
		{Key: key.Space, Text: "cycle"},
	}
	return prompt.RenderHintLines(
		[][]prompt.Hint{firstLine, secondLine},
		helpGap,
		m.styles.helpKey,
		m.styles.helpText,
	)
}

func newConfirmInput() textarea.Model {
	ci := input.NewTextArea(
		input.WithWidth(tuiConfirmInputWidth),
		input.WithMinHeight(tuiConfirmInputMinHeight),
		input.WithMaxHeight(tuiConfirmInputMaxHeight),
	)
	ciStyles := ci.Styles()
	ciStyles.Focused.Text = styleHighlight
	ciStyles.Focused.Placeholder = styleSubtle
	ciStyles.Focused.CursorLine = styleHighlight
	ciStyles.Blurred.Text = styleText
	ciStyles.Blurred.CursorLine = styleText
	ciStyles.Cursor.Color = colorHighlight
	ci.SetStyles(ciStyles)
	return ci
}

func (m tuiModel) setConfirmInputPlaceholder(placeholder string) tuiModel {
	m.confirmInput.Placeholder = placeholder
	return m
}

// prepareConfirmInput configures the confirm textarea dimensions for the
// current terminal size. Call this whenever confirmHasInput is set to true.
func (m tuiModel) prepareConfirmInput() tuiModel {
	m.confirmInput.SetWidth(m.confirmInputWidth())
	m.confirmInput.MaxHeight = m.confirmTextareaMaxHeight()
	return m
}

func (m tuiModel) focusConfirmInput() (tuiModel, tea.Cmd) {
	m.confirmState.OptFocus = false
	if !m.confirmHasInput {
		return m, nil
	}
	return m, m.confirmInput.Focus()
}

func (m tuiModel) updateConfirmOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Info-only modal (no confirmCmd) - any key dismisses.
	if m.confirmCmd == nil && m.confirmCmdFn == nil {
		switch msg.String() {
		case key.Enter, "q", key.Esc, "y", "n", " ":
			return m.confirmDismiss()
		default:
			return m, nil
		}
	}
	// Modal with text input (e.g. close comment).
	if m.confirmHasInput && m.confirmInput.Focused() {
		switch msg.String() {
		case key.Esc:
			return m.confirmDismiss()
		case key.Tab, key.ShiftTab:
			if m.hasConfirmOptions() {
				m.confirmState.EnterOptions(len(m.confirmOptions), msg.String() == key.ShiftTab)
				m.confirmInput.Blur()
				m.scrollConfirmToFocus()
				return m, nil
			}
		case key.AltEnter:
			m.confirmState.Yes = true
			return m.confirmAccept()
		default:
			var cmd tea.Cmd
			m.confirmInput, cmd = m.confirmInput.Update(msg)
			return m, cmd
		}
	}
	if !m.confirmState.OptFocus || !m.hasConfirmOptions() {
		switch msg.String() {
		case key.Left, key.Right, tuiKeybindVimLeft, tuiKeybindVimRight, key.Space, key.Tab:
			m.confirmState.ToggleYes()
			return m, nil
		case key.Y:
			return m.confirmAccept()
		case tuiKeybindConfirmNo, tuiKeybindQuit, key.Esc:
			return m.confirmDismiss()
		case key.Enter:
			if m.confirmState.Yes {
				return m.confirmAccept()
			}
			return m.confirmDismiss()
		default:
			return m, nil
		}
	}

	previousProvider := m.selectedReviewProvider()
	switch msg.String() {
	case tuiKeybindVimDown, key.Down:
		if m.confirmState.Navigate(
			prompt.NavDown,
			len(m.confirmOptions),
			m.confirmHasInput,
		).MoveToInput {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.scrollConfirmToFocus()
	case tuiKeybindVimUp, key.Up:
		if m.confirmState.Navigate(
			prompt.NavUp,
			len(m.confirmOptions),
			m.confirmHasInput,
		).MoveToInput {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.scrollConfirmToFocus()
	case tuiKeybindVimRight, key.Right:
		m.confirmState.Step(len(m.confirmOptions[m.confirmState.OptCursor].choices), 1, false)
	case key.Space:
		m.confirmState.Step(len(m.confirmOptions[m.confirmState.OptCursor].choices), 1, true)
	case tuiKeybindVimLeft, key.Left:
		m.confirmState.Step(len(m.confirmOptions[m.confirmState.OptCursor].choices), -1, false)
	case key.Tab, key.ShiftTab:
		dir := prompt.NavTab
		if msg.String() == key.ShiftTab {
			dir = prompt.NavShiftTab
		}
		if m.confirmState.Navigate(dir, len(m.confirmOptions), m.confirmHasInput).MoveToInput {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.scrollConfirmToFocus()
		return m, nil
	case key.AltEnter, key.Y:
		m.confirmState.Yes = true
		return m.confirmAccept()
	case tuiKeybindConfirmNo, tuiKeybindQuit, key.Esc:
		return m.confirmDismiss()
	case key.Enter:
		if m.confirmHasInput {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.confirmState.Yes = true
		return m.confirmAccept()
	default:
		return m, nil
	}

	m = m.syncReviewConfirmOptions(previousProvider)
	return m, nil
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
	var cmd tea.Cmd
	if m.confirmHasInput && m.confirmCmdFn != nil {
		cmd = m.confirmCmdFn(m.buildConfirmSubmission())
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
