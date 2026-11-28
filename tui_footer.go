package main

import (
	"slices"
	"strings"

	"bloomfx.dev/bloom"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/scrollbar"
)

const (
	footerKeyPrefix          = "footer-key/"
	filterKeyPrefix          = "filter-key/"
	shortcutBarID   bloom.ID = "shortcut-bar"
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

func (m tuiModel) shortcutRenderer() key.Renderer {
	return key.Renderer{
		Styles: key.Styles{Key: m.styles.helpKey, Text: m.styles.helpText},
		Inline: true, Prefix: new(""),
	}
}

type shortcutLayout struct {
	width, height int
	method        ansi.Method
	hints         []key.Hint
	labels        []string
	frame         *bloom.Frame
}

func (l *shortcutLayout) buttons(m tuiModel, enabled bool) []bloom.Child {
	buttons := make([]bloom.Child, 0, len(l.hints))
	for i, hint := range l.hints {
		button := footerButton(
			l.labels[i],
			bloom.ID(footerKeyPrefix+hint.Key),
			enabled && m.footerKeyEnabled(hint.Key),
		)
		var item bloom.Child = button
		if i == 0 {
			item = bloom.Row(bloom.Fixed(1, bloom.Space()), button)
		}
		buttons = append(buttons, item)
	}
	return buttons
}

func shortcutFlow(buttons []bloom.Child) bloom.WrapNode {
	return bloom.Wrap(buttons...).Gap(len(helpGap), 0)
}

// Use one natural-height frame for reserving viewport rows and fitting status.
// Only a resize, a width-mode change or different shortcuts needs another layout.
func (m tuiModel) shortcutLayout(hints []key.Hint) *shortcutLayout {
	method := m.reviewWidthMethod()
	if m.screen != nil {
		for i, l := range m.screen.shortcuts {
			if l != nil && l.width == m.width && l.method == method &&
				slices.Equal(l.hints, hints) {
				m.screen.shortcuts[0], m.screen.shortcuts[i] = l, m.screen.shortcuts[0]
				return l
			}
		}
	}
	l := &shortcutLayout{width: m.width, height: 1, method: method, hints: slices.Clone(hints)}
	renderer := m.shortcutRenderer()
	for _, hint := range hints {
		l.labels = append(l.labels, renderer.Render([]key.Hint{hint}))
	}
	if m.width > 0 {
		root := bloom.New(bloom.WithWidthMethod(method))
		root.Render(m.width, 0, shortcutFlow(l.buttons(m, false)).ID(shortcutBarID))
		l.frame = root.Frame()
		rect, _ := l.frame.Rect(shortcutBarID)
		l.height = max(1, rect.Dy())
	}
	if m.screen != nil {
		m.screen.shortcuts[1], m.screen.shortcuts[0] = m.screen.shortcuts[0], l
	}
	return l
}

func (m tuiModel) helpLines(hints []key.Hint) int {
	return m.shortcutLayout(hints).height
}

func (m tuiModel) shortcutFooter(hints []key.Hint, status string) bloom.Child {
	l := m.shortcutLayout(hints)
	buttons := l.buttons(m, m.footerRoot() != nil)
	flow := shortcutFlow(buttons)
	if status == "" || m.width <= 0 {
		return flow
	}

	// The status owns the right end of the last line. Drop whole shortcuts
	// that would touch it, keeping the other lines and their hit targets.
	usable := max(1, m.width-1)
	status = l.method.Truncate(status, usable, valueEllipsis)
	start := usable - l.method.StringWidth(status)
	for i := range slices.Backward(buttons) {
		rect, ok := l.frame.Rect(bloom.ID(footerKeyPrefix + hints[i].Key))
		if ok && (rect.Min.Y < l.height-1 || rect.Max.X < start) {
			break
		}
		buttons = buttons[:i]
	}
	flow = shortcutFlow(buttons)
	last := bloom.Layer(bloom.Text(status).NoWrap()).Anchor(bloom.Start, bloom.End).Offset(start, 0)
	return bloom.Column(bloom.Fixed(l.height, bloom.Overlay(flow, last)))
}

// Keep the syntax divider on a single line. When the hints wrap, put the
// actions first so a short window can clip the legends without hiding them.
func (m tuiModel) filterHints(maxRows int) (bloom.WrapNode, bloom.WrapNode, int) {
	method := m.reviewWidthMethod()
	gap := len(helpGap)
	renderer := m.shortcutRenderer()
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
