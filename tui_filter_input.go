package main

import (
	"unicode/utf8"

	"bloomfx.dev/bloom"
	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/ansi"
)

type filterWindow struct {
	value, visible   string
	pos, width, left int
	visiblePos       int
	method           ansi.Method
}

func filterPrefix(value string, width int, method ansi.Method) string {
	at := 0
	for at < len(value) {
		cluster, cells := ansi.FirstGraphemeCluster(value[at:], method)
		if cells > width {
			break
		}
		width -= cells
		at += len(cluster)
	}
	return value[:at]
}

// Keep the caret's whole grapheme in view. Bubbles edits in runes, while
// Bloom clips in terminal cells. Only render the visible part on cursor blinks.
func (w *filterWindow) fit(value string, pos, width int, method ansi.Method) {
	if w.value == value && w.pos == pos && w.width == width && w.method == method {
		return
	}
	w.value, w.pos, w.width, w.method = value, pos, width, method
	if pos < 0 { // Applied filters show the start of the query.
		w.left, w.visiblePos = 0, 0
		w.visible = filterPrefix(value, width, method)
		if len(w.visible) < len(value) && width > 0 {
			w.visible = filterPrefix(w.visible, width-1, method) + valueEllipsis
		}
		return
	}
	at, caret, charWidth := 0, 0, 1
	for at < len(value) {
		cluster, cells := ansi.FirstGraphemeCluster(value[at:], method)
		n := utf8.RuneCountInString(cluster)
		if pos < n {
			charWidth = max(1, cells)
			break
		}
		pos -= n
		at += len(cluster)
		caret += cells
	}
	end := caret + charWidth
	total := end
	if at < len(value) {
		total = max(end, method.StringWidth(value))
	}
	w.left = min(w.left, caret, max(0, total-width))
	if end > w.left+width {
		w.left = min(caret, end-width)
	}
	start := len(filterPrefix(value[:at], w.left, method))
	if method.StringWidth(value[start:at]) > caret-w.left {
		// The left edge falls inside a wide grapheme.
		first, _ := ansi.FirstGraphemeCluster(value[start:at], method)
		start += len(first)
	}
	w.visiblePos = utf8.RuneCountInString(value[start:at])
	w.visible = filterPrefix(value[start:], width, method)
}

func (w *filterWindow) line(
	input *textinput.Model,
	value string,
	width int,
	method ansi.Method,
) bloom.Child {
	width = max(0, width-1-method.StringWidth(input.Prompt))
	if len(value)+1 <= width {
		*w = filterWindow{}
	} else {
		pos := input.Position()
		if !input.Focused() {
			pos = -1
		}
		w.fit(value, pos, width, method)
		display := *input
		display.SetValue(w.visible)
		display.SetCursor(w.visiblePos)
		input = &display
	}
	return bloom.Row(
		bloom.Text(styleHeading.Bold(true).Render("/")),
		bloom.Fill(bloom.View(input)),
	)
}
