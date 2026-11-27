package main

import (
	"slices"
	"strings"

	"bloomfx.dev/bloom"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
)

const footerKeyPrefix = "footer-key/"

func (m tuiModel) footerKeyAt(x, y int) (string, bool) {
	if !m.canOpenPresetMenu() || m.filterInput.Focused() || m.screen == nil {
		return "", false
	}
	return strings.CutPrefix(string(m.screen.hit(x, y)), footerKeyPrefix)
}

func (m tuiModel) shortcutFooter(hints []key.Hint, status string) bloom.Child {
	renderer := m.helpBar(nil).Renderer
	renderer.Prefix = new("")
	method := m.reviewWidthMethod()
	hover := lg.NewStyle().Transform(func(text string) string {
		return layout.PreserveBackground(text, hoverBG)
	})
	enabled := m.canOpenPresetMenu()
	buttons := make([]bloom.Child, 0, len(hints))
	for i, hint := range hints {
		text := renderer.Render([]key.Hint{hint})
		button := bloom.Text(text).NoWrap().ID(bloom.ID(footerKeyPrefix + hint.Key))
		if enabled {
			button = button.HoverStyle(hover)
		}
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
