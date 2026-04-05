package main

import (
	"math"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
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
	active bool
	target scrollbarTarget
	grab   int
}

type scrollbarHitbox struct {
	target     scrollbarTarget
	x          int
	y          int
	height     int
	totalLines int
}

type tuiWheelFilter struct {
	delay time.Duration
	send  func(tea.Msg)

	mu            sync.Mutex
	active        bool
	pendingTarget wheelTarget
	pendingDelta  int
	timer         *time.Timer
}

func newTUIWheelFilter(delay time.Duration, send func(tea.Msg)) *tuiWheelFilter {
	return &tuiWheelFilter{delay: delay, send: send}
}

func (f *tuiWheelFilter) filter(model tea.Model, msg tea.Msg) tea.Msg {
	target, delta, ok := f.wheelEvent(model, msg)
	if !ok {
		return msg
	}

	f.enqueue(target, delta)
	return nil
}

func (f *tuiWheelFilter) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.active = false
	f.pendingTarget = wheelTargetNone
	f.pendingDelta = 0
}

func (f *tuiWheelFilter) wheelEvent(model tea.Model, msg tea.Msg) (wheelTarget, int, bool) {
	wheelMsg, ok := msg.(tea.MouseWheelMsg)
	if !ok {
		return wheelTargetNone, 0, false
	}

	var delta int
	switch wheelMsg.Button {
	case tea.MouseWheelDown:
		delta = 1
	case tea.MouseWheelUp:
		delta = -1
	default:
		return wheelTargetNone, 0, false
	}

	tui, ok := model.(tuiModel)
	if !ok {
		return wheelTargetNone, 0, false
	}
	target, ok := tui.wheelScrollTarget()
	if !ok {
		return wheelTargetNone, 0, false
	}
	return target, delta, true
}

func (f *tuiWheelFilter) enqueue(target wheelTarget, delta int) {
	var immediate *batchedWheelMsg
	startTimer := false

	f.mu.Lock()
	switch {
	case !f.active:
		f.active = true
		f.pendingTarget = target
		f.pendingDelta = delta
		startTimer = true
	case f.pendingTarget == target:
		f.pendingDelta += delta
	default:
		if f.pendingDelta != 0 && f.pendingTarget != wheelTargetNone {
			immediate = &batchedWheelMsg{target: f.pendingTarget, delta: f.pendingDelta}
		}
		if f.timer != nil {
			f.timer.Stop()
			f.timer = nil
		}
		f.active = true
		f.pendingTarget = target
		f.pendingDelta = delta
		startTimer = true
	}
	f.mu.Unlock()

	if immediate != nil {
		f.dispatch(*immediate)
	}
	if startTimer {
		f.scheduleFlush()
	}
}

func (f *tuiWheelFilter) scheduleFlush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.timer != nil {
		f.timer.Stop()
	}
	f.timer = time.AfterFunc(f.delay, f.flush)
}

func (f *tuiWheelFilter) flush() {
	f.mu.Lock()
	target := f.pendingTarget
	delta := f.pendingDelta
	f.active = false
	f.pendingTarget = wheelTargetNone
	f.pendingDelta = 0
	f.timer = nil
	f.mu.Unlock()

	if delta == 0 || target == wheelTargetNone {
		return
	}
	f.dispatch(batchedWheelMsg{target: target, delta: delta})
}

func (f *tuiWheelFilter) dispatch(msg tea.Msg) {
	if f.send == nil || msg == nil {
		return
	}
	go f.send(msg)
}

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

// scrollPercent returns the scroll position as a percentage in the style of
// less(1): the percentage of the file above and including the bottom of the
// viewport. This means it never shows 0% and reaches 100% at the end.
func scrollPercent(offset, total, viewport int) int {
	const percentMax = 100
	if total <= 0 {
		return percentMax
	}
	return min(percentMax*(offset+viewport)/total, percentMax)
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
	m.confirmView.SetContent(expandTabs(m.confirmModalContent()))
}

func (m *tuiModel) syncDiffView() {
	m.diffRenderLines = m.syncViewport(&m.diffView, m.diffLines, m.diffContentViewport())
}

func (m *tuiModel) syncDetailView() {
	m.detailRenderLines = m.syncViewport(&m.detailView, m.detailLines, m.detailViewport())
}

func (m *tuiModel) syncViewport(vp *viewport.Model, lines []string, height int) []string {
	width := max(0, m.width-tuiScrollbarWidth)
	renderLines := normalizeViewportRenderLines(lines, width)
	vp.SetWidth(width)
	vp.SetHeight(height)
	vp.SetContentLines(renderLines)
	vp.FillHeight = true
	return renderLines
}

// confirmViewport returns the maximum number of inner content lines that fit
// within the terminal, leaving room for the box border and vertical padding.
func (m tuiModel) confirmViewport() int {
	// 2 border rows + 2×1 vertical padding (top/bottom inside the box).
	const chrome = 4
	return max(chrome-1, m.height-chrome)
}

// scrollbarChars returns a slice of styled scrollbar characters (one per line)
// with a thumb sized proportionally to the visible/total content ratio.
func (m tuiModel) scrollbarChars(height, totalLines int, percent float64) []string {
	if height <= 0 {
		return nil
	}
	thumbPos, thumbSize := scrollbarThumbMetrics(height, totalLines, percent)
	thumb := lg.NewStyle().Foreground(colorAccent)
	track := lg.NewStyle().Foreground(colorAccent).Faint(true)
	chars := make([]string, height)
	for i := range height {
		if i >= thumbPos && i < thumbPos+thumbSize {
			chars[i] = thumb.Render("█")
		} else {
			chars[i] = track.Render("┃")
		}
	}
	return chars
}

// renderScrollbar returns a single-column scrollbar string for use with
// lg.JoinHorizontal in the confirm modal overlay.
func (m tuiModel) renderScrollbar(height, totalLines int, percent float64) string {
	return strings.Join(m.scrollbarChars(height, totalLines, percent), nl)
}

func scrollbarThumbMetrics(height, totalLines int, percent float64) (int, int) {
	if height <= 0 {
		return 0, 0
	}
	maxThumb := height / 2 //nolint:mnd // cap at 50%
	thumbSize := min(maxThumb, max(1, height*height/max(1, totalLines)))
	trackSpace := max(0, height-thumbSize)
	thumbPos := 0
	if trackSpace > 0 {
		thumbPos = int(math.Round(percent * float64(trackSpace)))
	}
	return thumbPos, thumbSize
}

func (m *tuiModel) handleScrollbarPress(msg tea.Mouse) bool {
	const thumbCenterDivisor = 2

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

	hitbox, ok := m.scrollbarHitboxAt(msg.X, msg.Y)
	if !ok {
		return false
	}
	percent := m.scrollbarPercent(hitbox.target)
	thumbPos, thumbSize := scrollbarThumbMetrics(hitbox.height, hitbox.totalLines, percent)
	row := min(max(msg.Y-hitbox.y, 0), hitbox.height-1)
	grab := thumbSize / thumbCenterDivisor
	if row >= thumbPos && row < thumbPos+thumbSize {
		grab = row - thumbPos
	}
	m.scrollDrag = scrollbarDragState{
		active: true,
		target: hitbox.target,
		grab:   grab,
	}
	m.scrollbarScrollToRow(hitbox, row, grab)
	return true
}

func (m *tuiModel) handleScrollbarMotion(msg tea.Mouse) bool {
	if !m.scrollDrag.active {
		return false
	}
	hitbox, ok := m.scrollbarHitbox(m.scrollDrag.target)
	if !ok {
		m.scrollDrag = scrollbarDragState{}
		return false
	}
	m.scrollbarScrollToRow(hitbox, msg.Y-hitbox.y, m.scrollDrag.grab)
	return true
}

func (m *tuiModel) scrollbarScrollToRow(hitbox scrollbarHitbox, row, grab int) {
	if hitbox.height <= 0 {
		return
	}
	vp := m.scrollbarViewport(hitbox.target)
	if vp == nil {
		return
	}
	maxOffset := max(0, hitbox.totalLines-hitbox.height)
	if maxOffset == 0 {
		vp.SetYOffset(0)
		return
	}
	_, thumbSize := scrollbarThumbMetrics(hitbox.height, hitbox.totalLines, vp.ScrollPercent())
	trackSpace := max(0, hitbox.height-thumbSize)
	topRow := min(max(row-grab, 0), trackSpace)
	if trackSpace == 0 {
		vp.SetYOffset(maxOffset)
		return
	}
	offset := int(math.Round(float64(topRow) / float64(trackSpace) * float64(maxOffset)))
	vp.SetYOffset(offset)
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

func (m tuiModel) scrollbarHitboxAt(x, y int) (scrollbarHitbox, bool) {
	for _, target := range []scrollbarTarget{
		scrollbarTargetConfirm,
		scrollbarTargetDiff,
		scrollbarTargetDetail,
	} {
		hitbox, ok := m.scrollbarHitbox(target)
		if !ok {
			continue
		}
		if x == hitbox.x && y >= hitbox.y && y < hitbox.y+hitbox.height {
			return hitbox, true
		}
	}
	return scrollbarHitbox{}, false
}

func (m tuiModel) viewportScrollbarHitbox(
	target scrollbarTarget,
	vp *viewport.Model,
	view tuiView,
	y int,
) (scrollbarHitbox, bool) {
	totalLines := vp.TotalLineCount()
	height := vp.Height()
	if totalLines <= height || height <= 0 || m.width <= 0 || m.view != view ||
		m.confirmAction != "" {
		return scrollbarHitbox{}, false
	}
	return scrollbarHitbox{
		target:     target,
		x:          m.width - 1,
		y:          y,
		height:     height,
		totalLines: totalLines,
	}, true
}

func (m tuiModel) scrollbarHitbox(target scrollbarTarget) (scrollbarHitbox, bool) {
	switch target {
	case scrollbarTargetNone:
		return scrollbarHitbox{}, false
	case scrollbarTargetDiff:
		y := 0
		if idx := m.resolveIndex(m.diffKey, -1); idx >= 0 && idx < len(m.rows) {
			y = 2
		}
		return m.viewportScrollbarHitbox(target, &m.diffView, tuiViewDiff, y)
	case scrollbarTargetDetail:
		return m.viewportScrollbarHitbox(target, &m.detailView, tuiViewDetail, 0)
	case scrollbarTargetConfirm:
		if m.confirmAction == "" {
			return scrollbarHitbox{}, false
		}
		totalLines := m.confirmView.TotalLineCount()
		height := m.confirmView.Height()
		if totalLines <= height || height <= 0 {
			return scrollbarHitbox{}, false
		}
		return m.confirmScrollbarHitbox()
	default:
		return scrollbarHitbox{}, false
	}
}

func (m tuiModel) confirmScrollbarHitbox() (scrollbarHitbox, bool) {
	const centerDivisor = 2

	lines := strings.Split(xansi.Strip(m.renderConfirmModal()), nl)
	if len(lines) == 0 {
		return scrollbarHitbox{}, false
	}

	fgWidth := 0
	for _, line := range lines {
		if w := xansi.WcWidth.StringWidth(line); w > fgWidth {
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
		return scrollbarHitbox{
			target:     scrollbarTargetConfirm,
			x:          startCol + xansi.WcWidth.StringWidth(line[:col]),
			y:          startRow + row,
			height:     m.confirmView.Height(),
			totalLines: m.confirmView.TotalLineCount(),
		}, true
	}
	return scrollbarHitbox{}, false
}

func normalizeViewportRenderLines(lines []string, width int) []string {
	if len(lines) == 0 {
		return nil
	}

	normalized := make([]string, len(lines))
	for i, line := range lines {
		normalized[i] = normalizeViewportRenderLine(line, width)
	}
	return normalized
}

func normalizeViewportRenderLine(line string, width int) string {
	line = expandTabs(line)
	if width <= 0 {
		return line
	}

	lineWidth := xansi.WcWidth.StringWidth(line)
	if lineWidth > width {
		line = xansi.WcWidth.Truncate(line, width, "")
		lineWidth = width
	}
	if pad := width - lineWidth; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func (m tuiModel) renderViewportContent(
	lines []string,
	vp viewport.Model,
	withScrollbar bool,
) string {
	height := vp.Height()
	width := max(0, vp.Width())
	if height <= 0 {
		return ""
	}

	start := min(vp.YOffset(), len(lines))
	end := min(start+height, len(lines))
	scrollbar := []string(nil)
	if withScrollbar {
		scrollbar = m.scrollbarChars(height, vp.TotalLineCount(), vp.ScrollPercent())
	}
	blank := strings.Repeat(" ", width)

	var b strings.Builder
	for row := range height {
		if row > 0 {
			b.WriteByte('\n')
		}

		line := blank
		idx := start + row
		if idx < end {
			line = lines[idx]
		}
		b.WriteString(line)

		if row < len(scrollbar) {
			b.WriteString(scrollbar[row])
		}
	}
	return b.String()
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
		if m.confirmYes {
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
	buttonsWidth := lg.Width(buttons)
	centered := buttons
	if pad := (promptWidth - buttonsWidth) / 2; pad > 0 { //nolint:mnd // center
		centered = strings.Repeat(" ", pad) + buttons
	}
	return m.renderConfirmScrollable(boxStyle, 0, content+centered)
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
	view.SetContent(expandTabs(content))

	scrollbar := m.renderScrollbar(vpHeight, view.TotalLineCount(), view.ScrollPercent())
	inner := lg.JoinHorizontal(lg.Top, view.View(), scrollbar)
	// Reduce right padding so the scrollbar sits snug against the border.
	boxStyle = boxStyle.PaddingRight(1)
	if boxWidth > 0 {
		boxWidth -= tuiConfirmPadX - 2 //nolint:mnd // keep 1 space between scrollbar and border
		return boxStyle.Width(boxWidth).Render(inner)
	}
	return boxStyle.Render(inner)
}

// confirmModalContent returns the inner content string for the confirm modal
// (prompt + options + textarea + hints). Hints are omitted when the modal
// needs scrolling to maximise usable space.
func (m tuiModel) confirmModalContent() string {
	var b strings.Builder
	b.WriteString(m.confirmPrompt)
	if m.confirmHasInput {
		label := m.confirmInputLabel
		if label == "" {
			label = "Comment"
		}
		b.WriteString(nl + nl)
		if m.hasConfirmOptions() {
			b.WriteString(m.renderConfirmOptionsHeader())
		}
		b.WriteString(m.styles.helpKey.Render(label))
		b.WriteString(nl)
		b.WriteString(m.confirmInput.View())

		// Only include hints when there is enough room.
		hints := nl + nl + m.renderConfirmInputHints()
		total := strings.Count(b.String(), nl) + 1 + strings.Count(hints, nl)
		if total <= m.confirmViewport() {
			b.WriteString(hints)
		}
	} else {
		b.WriteString(nl + nl)
	}
	return b.String()
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
	// options (~6) + hints (2) + blank lines (~3).
	const overhead = 18
	available := max(tuiConfirmInputMinHeight, m.height-overhead)
	return min(available, tuiConfirmInputMaxHeight)
}

// scrollConfirmToFocus scrolls the confirm viewport so the currently focused
// element (option row or textarea) is visible. Call after any focus change.
func (m *tuiModel) scrollConfirmToFocus() {
	m.syncConfirmView()

	// Calculate the line offset of the focused element within the content.
	// Layout: prompt (1) + blank (2) + [options: 3 lines each] + label (1) + textarea + hints.
	const promptLines = 3 // prompt + 2 blank lines (\n\n)
	const linesPerOption = 3

	if m.confirmOptFocus && m.hasConfirmOptions() {
		line := promptLines + m.confirmOptCursor*linesPerOption
		m.confirmView.EnsureVisible(max(0, line-1), 0, 0)
		return
	}

	if m.confirmHasInput {
		// Textarea starts after prompt + all options + label.
		line := promptLines + len(m.confirmOptions)*linesPerOption + 1
		m.confirmView.EnsureVisible(max(0, line-1), 0, 0)
	}
}

func (m tuiModel) renderConfirmOptionsHeader() string {
	var b strings.Builder
	for i, def := range m.confirmOptions {
		b.WriteString(m.styles.helpKey.Render(def.label))
		b.WriteString(nl)

		var choicesLine strings.Builder
		for j, choice := range def.choices {
			if j > 0 {
				choicesLine.WriteString("  ")
			}
			selected := i < len(m.confirmOptValues) && m.confirmOptValues[i] == j
			active := m.confirmOptFocus && i == m.confirmOptCursor
			switch {
			case selected && active:
				choicesLine.WriteString(styleHighlight.Bold(true).Render(choice.label))
			case selected:
				choicesLine.WriteString(styleTitle.Bold(true).Render(choice.label))
			case active:
				choicesLine.WriteString(styleHighlight.Faint(true).Render(choice.label))
			default:
				choicesLine.WriteString(lg.NewStyle().Faint(true).Render(choice.label))
			}
		}
		b.WriteString(choicesLine.String())
		b.WriteString(nl + nl)
	}
	return b.String()
}

func (m tuiModel) renderConfirmInputHints() string {
	helpKey := m.styles.helpKey
	helpText := m.styles.helpText

	secondLine := []string{
		helpKey.Render(tuiKeybindConfirmSubmit) + " " + helpText.Render("submit"),
		helpKey.Render(tuiKeyEsc) + " " + helpText.Render("cancel"),
	}
	if !m.hasConfirmOptions() {
		return strings.Join(secondLine, "  ")
	}

	firstLine := []string{
		helpKey.Render(tuiKeyTab) + " " + helpText.Render("next"),
		helpKey.Render(tuiKeyShiftTab) + " " + helpText.Render("prev"),
		helpKey.Render("←/→") + " " + helpText.Render("select"),
		helpKey.Render(tuiKeySpace) + " " + helpText.Render("cycle"),
	}
	return strings.Join(firstLine, "  ") + nl + strings.Join(secondLine, "  ")
}

func newConfirmInput() textarea.Model {
	ci := textarea.New()
	ci.Prompt = ""
	ci.Placeholder = "Enter text..."
	ci.ShowLineNumbers = false
	ci.SetWidth(tuiConfirmInputWidth)
	ci.DynamicHeight = true
	ci.MinHeight = tuiConfirmInputMinHeight
	ci.MaxHeight = tuiConfirmInputMaxHeight
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
	m.confirmOptFocus = false
	if !m.confirmHasInput {
		return m, nil
	}
	return m, m.confirmInput.Focus()
}

func (m tuiModel) updateConfirmOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Info-only modal (no confirmCmd) - any key dismisses.
	if m.confirmCmd == nil && m.confirmCmdFn == nil {
		switch msg.String() {
		case tuiKeyEnter, "q", tuiKeyEsc, "y", "n", " ":
			return m.confirmDismiss()
		default:
			return m, nil
		}
	}
	// Modal with text input (e.g. close comment).
	if m.confirmHasInput && m.confirmInput.Focused() {
		switch msg.String() {
		case tuiKeyEsc:
			return m.confirmDismiss()
		case tuiKeyTab, tuiKeyShiftTab:
			if m.hasConfirmOptions() {
				m.confirmOptFocus = true
				m.confirmInput.Blur()
				if msg.String() == tuiKeyShiftTab {
					m.confirmOptCursor = len(m.confirmOptions) - 1
				} else {
					m.confirmOptCursor = 0
				}
				m.scrollConfirmToFocus()
				return m, nil
			}
		case tuiKeybindConfirmSubmit:
			m.confirmYes = true
			return m.confirmAccept()
		default:
			var cmd tea.Cmd
			m.confirmInput, cmd = m.confirmInput.Update(msg)
			return m, cmd
		}
	}
	if !m.confirmOptFocus || !m.hasConfirmOptions() {
		switch msg.String() {
		case tuiKeyLeft, tuiKeyRight, tuiKeybindVimLeft, tuiKeybindVimRight, tuiKeySpace, tuiKeyTab:
			m.confirmYes = !m.confirmYes
			return m, nil
		case tuiKeybindConfirmYes:
			return m.confirmAccept()
		case tuiKeybindConfirmNo, tuiKeybindQuit, tuiKeyEsc:
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

	previousProvider := m.selectedReviewProvider()
	switch msg.String() {
	case tuiKeybindVimDown, tuiKeyDown:
		if m.confirmHasInput && m.confirmOptCursor == len(m.confirmOptions)-1 {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.confirmOptCursor = min(m.confirmOptCursor+1, len(m.confirmOptions)-1)
		m.scrollConfirmToFocus()
	case tuiKeybindVimUp, tuiKeyUp:
		if m.confirmHasInput && m.confirmOptCursor == 0 {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.confirmOptCursor = max(m.confirmOptCursor-1, 0)
		m.scrollConfirmToFocus()
	case tuiKeybindVimRight, tuiKeyRight:
		n := len(m.confirmOptions[m.confirmOptCursor].choices)
		if n > 0 {
			m.confirmOptValues[m.confirmOptCursor] = min(
				m.confirmOptValues[m.confirmOptCursor]+1,
				n-1,
			)
		}
	case tuiKeySpace:
		n := len(m.confirmOptions[m.confirmOptCursor].choices)
		if n > 0 {
			m.confirmOptValues[m.confirmOptCursor] = (m.confirmOptValues[m.confirmOptCursor] + 1) % n
		}
	case tuiKeybindVimLeft, tuiKeyLeft:
		m.confirmOptValues[m.confirmOptCursor] = max(
			m.confirmOptValues[m.confirmOptCursor]-1,
			0,
		)
	case tuiKeyTab, tuiKeyShiftTab:
		if msg.String() == tuiKeyShiftTab {
			if m.confirmHasInput && m.confirmOptCursor == 0 {
				focused, cmd := m.focusConfirmInput()
				focused.scrollConfirmToFocus()
				return focused, cmd
			}
			m.confirmOptCursor = (m.confirmOptCursor - 1 + len(m.confirmOptions)) % len(
				m.confirmOptions,
			)
			m.scrollConfirmToFocus()
			return m, nil
		}
		if m.confirmHasInput && m.confirmOptCursor == len(m.confirmOptions)-1 {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.confirmOptCursor = (m.confirmOptCursor + 1) % len(m.confirmOptions)
		m.scrollConfirmToFocus()
		return m, nil
	case tuiKeybindConfirmSubmit, tuiKeybindConfirmYes:
		m.confirmYes = true
		return m.confirmAccept()
	case tuiKeybindConfirmNo, tuiKeybindQuit, tuiKeyEsc:
		return m.confirmDismiss()
	case tuiKeyEnter:
		if m.confirmHasInput {
			focused, cmd := m.focusConfirmInput()
			focused.scrollConfirmToFocus()
			return focused, cmd
		}
		m.confirmYes = true
		return m.confirmAccept()
	default:
		return m, nil
	}

	m = m.syncReviewConfirmOptions(previousProvider)
	return m, nil
}

// confirmActionVerb maps confirm action names to in-progress verbs.
var confirmActionVerb = map[string]string{
	tuiActionApprove:      "Approving",
	tuiActionApproveMerge: "Approving & merging",
	tuiActionClose:        "Closing",
	tuiActionComment:      "Commenting",
	tuiActionForceMerge:   "Force-merging",
	tuiActionMerge:        "Merging",
	tuiActionSendSlack:    "Slacking",
	tuiActionUnassign:     "Unassigning",
	tuiActionUpdateBranch: "Updating branch",
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
				styledSubject = xansi.SetHyperlink(url) + styledSubject + xansi.ResetHyperlink()
			}
			m.statusMsg = m.styles.statusPending.Render(
				verb,
			) + " " + styledSubject + valueEllipsis
		} else {
			m.statusMsg = m.styles.statusPending.Render(verb) + valueEllipsis
		}
		m.statusErr = false
	}
	return m, cmd
}

func (m tuiModel) confirmDismiss() (tea.Model, tea.Cmd) {
	m = m.clearConfirm()
	return m, nil
}
