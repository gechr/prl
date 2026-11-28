package main

import (
	"slices"
	"strings"

	"bloomfx.dev/bloom"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/scrollbar"
)

const (
	footerKeyPrefix = "footer-key/"
	filterKeyPrefix = "filter-key/"
)

func (m tuiModel) footerRoot() *bloom.Root {
	if reader := m.activeReviewViewport(); reader != nil {
		return reader.root
	}
	if m.screen != nil && m.listScrollbarActive() &&
		(m.filterInput.Focused() || m.canOpenPresetMenu(m.listSeparatorY())) {
		return m.screen.root
	}
	return nil
}

// The scroll legend describes two keys. During review navigation only the
// dismiss shortcut can act while the next PR is loading.
func (m tuiModel) footerKeyEnabled(binding string) bool {
	if binding == key.ArrowsVertical {
		return false
	}
	switch m.view {
	case tuiViewDiff:
		return !m.diffLoading || binding == tuiKeybindDiff
	case tuiViewDetail:
		return (!m.diffLoading && !m.detailLoading) || binding == tuiKeybindQuit
	case tuiViewList:
		return true
	}
	return false
}

func (m tuiModel) footerKeyAt(x, y int) (string, bool) {
	root := m.footerRoot()
	if root == nil {
		return "", false
	}
	prefix := footerKeyPrefix
	if m.view == tuiViewList && m.filterInput.Focused() {
		prefix = filterKeyPrefix
	}
	binding, ok := strings.CutPrefix(string(hitID(root, x, y)), prefix)
	return binding, ok && m.footerKeyEnabled(binding)
}

func footerButton(text string, id bloom.ID, enabled bool) bloom.TextNode {
	// Register hover even while hidden so the first motion after a keypress
	// can paint the control using the preceding frame's hit targets.
	style := lg.NewStyle().Inline(true)
	if enabled {
		style = style.Transform(func(text string) string {
			return layout.PreserveBackground(text, hoverBG)
		})
	}
	return bloom.Text(text).NoWrap().ID(id).HoverStyle(style)
}

func (m tuiModel) shortcutFooter(hints []key.Hint, status string) bloom.Child {
	renderer := m.helpBar(nil).Renderer
	renderer.Prefix = new("")
	method := m.reviewWidthMethod()
	enabled := m.footerRoot() != nil
	buttons := make([]bloom.Child, 0, len(hints))
	for i, hint := range hints {
		text := renderer.Render([]key.Hint{hint})
		button := footerButton(
			text,
			bloom.ID(footerKeyPrefix+hint.Key),
			enabled && m.footerKeyEnabled(hint.Key),
		)
		var item bloom.Child = button
		if i == 0 {
			item = bloom.Row(bloom.Fixed(1, bloom.Space()), button)
		}
		buttons = append(buttons, item)
	}
	flow := bloom.Wrap(buttons...).Gap(len(helpGap), 0)
	if status == "" || m.width <= 0 {
		return flow
	}

	// The status owns the right end of the last line. Drop whole shortcuts
	// that would touch it, keeping the other lines and their hit targets.
	usable := max(1, m.width-1)
	status = method.Truncate(status, usable, valueEllipsis)
	start := usable - method.StringWidth(status)
	height := bloom.Measure(m.width, 0, flow).H
	frame := bloom.Solve(m.width, height, flow)
	for i := range slices.Backward(buttons) {
		rect, ok := frame.Rect(bloom.ID(footerKeyPrefix + hints[i].Key))
		if ok && (rect.Min.Y < height-1 || rect.Max.X < start) {
			break
		}
		buttons = buttons[:i]
	}
	flow = bloom.Wrap(buttons...).Gap(len(helpGap), 0)
	last := bloom.Layer(bloom.Text(status).NoWrap()).Anchor(bloom.Start, bloom.End).Offset(start, 0)
	return bloom.Column(bloom.Fixed(height, bloom.Overlay(flow, last)))
}

// Keep the syntax divider on a single line. When the hints wrap, put the
// actions first so a short window can clip the legends without hiding them.
func (m tuiModel) filterHints(maxRows int) (bloom.WrapNode, bloom.WrapNode, int) {
	method := m.reviewWidthMethod()
	gap := len(helpGap)
	renderer := m.helpBar(nil).Renderer
	renderer.Prefix = new("")
	render := func(binding, desc string) string {
		return renderer.Render([]key.Hint{{Key: binding, Desc: desc}})
	}
	enabled := m.listScrollbarActive()
	applyText, exitText := render(key.Enter, "apply"), render(key.Esc, "exit")
	if m.width < method.StringWidth(applyText) ||
		(maxRows == 1 && m.width < method.StringWidth(applyText+exitText)+gap) {
		applyText = renderer.Styles.Key.Render("apply")
		exitText = renderer.Styles.Key.Render("exit")
	}
	apply := footerButton(applyText, filterKeyPrefix+key.Enter, enabled)
	exit := footerButton(exitText, filterKeyPrefix+key.Esc, enabled)
	navigate := bloom.Text(render(key.ArrowsVertical, "prev/next")).NoWrap()
	renderer.Styles = key.Styles{Key: styleHeading.Bold(true), Text: styleFilter}
	start := bloom.Text(render("^", "start")).NoWrap()
	end := bloom.Text(render("$", "end")).NoWrap()
	negate := bloom.Text(render("!", "negate")).NoWrap()

	syntax := bloom.Row(bloom.Fixed(1, bloom.Space()), bloom.Wrap(start, end, negate).Gap(gap, 0))
	pipe := bloom.Text(m.styles.separator.Render("│")).NoWrap()
	actions := bloom.Wrap(apply, exit).Gap(gap, 0)
	hints := bloom.Wrap(syntax, pipe, navigate, apply, exit).Gap(gap, 0)
	if bloom.Measure(0, 0, hints).W <= m.width {
		return hints, actions, bloom.Measure(0, 0, syntax).W + gap
	}
	return bloom.Wrap(apply, exit, navigate, start, end, negate).Gap(gap, 0), actions, -1
}

func (m tuiModel) filterFooter(lines []tuiListLine) (bloom.Child, int, int) {
	room := m.height - m.listHeaderAndFilterRows() - 1 // Leave room for the separator.
	maxRows := max(1, room-1)                          // Keep one PR visible while editing.
	hints, actions, junction := m.filterHints(maxRows)
	size := bloom.Measure(m.width, 0, hints)
	rows := min(size.H, maxRows)
	viewport := max(1, room-rows)
	status := m.listStatus(lines, viewport)
	if status == "" || m.width <= 0 {
		return hints, viewport, junction
	}

	method := m.reviewWidthMethod()
	usable := max(1, m.width-1)
	free := usable - size.W - len(helpGap)
	statusWidth := method.StringWidth(status)
	if m.flash.Msg == "" {
		// Keep the viewport height stable as the range gains digits during a scroll.
		total := lines[len(lines)-1].group
		statusWidth = len(scrollbar.Position(total-1, total, total))
	}
	if statusWidth > free {
		actionSize := bloom.Measure(m.width, 0, actions)
		if maxRows > actionSize.H {
			// The range includes only the PR rows above the final footer.
			rows = min(rows, maxRows-1)
			viewport = max(1, room-rows-1)
			status = method.Truncate(m.listStatus(lines, viewport), usable, valueEllipsis)
			last := bloom.Row(
				bloom.Space(),
				bloom.Text(status).NoWrap(),
				bloom.Fixed(1, bloom.Space()),
			)
			return bloom.Column(bloom.Fixed(rows, hints), bloom.Fixed(1, last)), viewport, junction
		}
		hints, junction = actions, -1
		free = usable - actionSize.W - len(helpGap)
		if free <= 0 {
			return hints, viewport, junction
		}
		status = method.Truncate(status, free, valueEllipsis)
	}
	last := bloom.Layer(bloom.Text(status).NoWrap()).Anchor(bloom.End, bloom.End).Offset(-1, 0)
	return bloom.Overlay(hints, last), viewport, junction
}
