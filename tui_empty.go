package main

import (
	"strings"

	"bloomfx.dev/bloom"
	lg "charm.land/lipgloss/v2"
)

func (m tuiModel) emptyLayer(height int) bloom.LayerNode {
	dim, keyStyle := m.styles.helpText, m.styles.helpKey
	box := m.styles.overlayBox.Padding(1, tuiConfirmPadX)
	if height <= box.GetVerticalFrameSize()+2 || m.width <= box.GetHorizontalFrameSize()+1 {
		box = box.Padding(0, 1)
	}
	if height < box.GetVerticalFrameSize()+2 || m.width <= box.GetHorizontalFrameSize() {
		box = lg.NewStyle()
	}
	width := max(1, m.width-box.GetHorizontalFrameSize())
	room := max(1, height-box.GetVerticalFrameSize())
	method := m.reviewWidthMethod()
	message := dim.Render("No pull requests found for the current query")
	if m.hasFilteredRows() {
		message = dim.Render("No pull requests match the current filter")
	}
	instructions := dim.Render("Press ") + keyStyle.Render("q") + dim.Render(" to quit, or ") +
		keyStyle.Render("esc") + dim.Render(" to dismiss and wait for new results")
	shortHint := keyStyle.Render(
		"q",
	) + dim.Render(
		" quit · ",
	) + keyStyle.Render(
		"esc",
	) + dim.Render(
		" dismiss",
	)
	if query := m.filterInput.Value(); query != "" {
		query = method.Truncate(query, width*room, valueEllipsis)
		message = dim.Render(
			"No pull requests match \"",
		) + styleFilter.Render(
			query,
		) + dim.Render(
			"\"",
		)
		instructions = dim.Render(
			"Refine your search, or press ",
		) + keyStyle.Render(
			"esc",
		) + dim.Render(
			" to clear the filter",
		)
		shortHint = keyStyle.Render("esc") + dim.Render(" clear filter")
	}
	if room == 1 {
		label := dim.Render("No matches · ") + shortHint
		if method.StringWidth(label) > width {
			label = shortHint
		}
		return bloom.Layer(bloom.Text(label).Truncate()).
			Size(min(width, method.StringWidth(label)), 1)
	}
	hint := bloom.Text(instructions)
	hintSize := bloom.Measure(width, 0, hint)
	if hintSize.H >= room {
		hint = bloom.Text(shortHint)
		hintSize = bloom.Measure(width, 0, hint)
	}
	hintRows := min(room-1, hintSize.H)
	msg := bloom.Text(message)
	size := bloom.Measure(width, 0, msg)
	msgRows := min(room-hintRows, size.H)
	if size.H > msgRows {
		// Leave space for recovery keys even when a pasted filter spans many lines.
		lines := strings.SplitN(method.Hardwrap(message, width, true), nl, msgRows+1)
		if len(lines) > msgRows {
			lines = lines[:msgRows]
			lines[msgRows-1] = method.Truncate(lines[msgRows-1]+valueEllipsis, width, valueEllipsis)
		}
		msg = bloom.Text(strings.Join(lines, nl)).NoWrap()
		size = bloom.Measure(width, 0, msg)
	}
	gap := min(1, room-msgRows-hintRows)
	panel := bloom.Column(
		bloom.Fixed(msgRows, msg),
		bloom.Fixed(gap, bloom.Space()),
		bloom.Fixed(hintRows, hint),
	)
	return bloom.Layer(bloom.Box(box, panel)).Size(
		min(width, max(size.W, hintSize.W))+box.GetHorizontalFrameSize(),
		msgRows+gap+hintRows+box.GetVerticalFrameSize())
}
