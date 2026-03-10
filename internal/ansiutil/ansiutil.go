package ansiutil

import (
	xansi "github.com/charmbracelet/x/ansi"
)

// HyperlinkFallback controls how hyperlinks render when the output is not a terminal.
type HyperlinkFallback int

const (
	// HyperlinkFallbackExpanded renders "text (url)".
	HyperlinkFallbackExpanded HyperlinkFallback = iota
	// HyperlinkFallbackMarkdown renders "[text](url)".
	HyperlinkFallbackMarkdown
	// HyperlinkFallbackText renders only the display text, discarding the URL.
	HyperlinkFallbackText
	// HyperlinkFallbackURL renders only the URL, discarding the display text.
	HyperlinkFallbackURL
)

// Writer produces ANSI-aware output, falling back to plain text
// when the output is not a terminal.
type Writer struct {
	terminal          bool
	hyperlinkFallback HyperlinkFallback
}

// Option configures a Writer.
type Option func(*Writer)

// WithHyperlinkFallback sets how hyperlinks render when the output is not a terminal.
func WithHyperlinkFallback(fallback HyperlinkFallback) Option {
	return func(w *Writer) {
		w.hyperlinkFallback = fallback
	}
}

// WithTerminal sets whether the output target is a terminal.
func WithTerminal(v bool) Option {
	return func(w *Writer) {
		w.terminal = v
	}
}

// New creates a Writer with the given options.
func New(opts ...Option) *Writer {
	w := &Writer{}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Terminal reports whether the output target is a terminal.
func (w *Writer) Terminal() bool { return w.terminal }

// Hyperlink creates an OSC 8 terminal hyperlink.
// When the output is not a terminal, the HyperlinkFallback mode controls
// how the link is rendered in plain text.
func (w *Writer) Hyperlink(url, text string) string {
	if !w.terminal {
		switch w.hyperlinkFallback {
		case HyperlinkFallbackExpanded:
			return text + " (" + url + ")"
		case HyperlinkFallbackMarkdown:
			return "[" + text + "](" + url + ")"
		case HyperlinkFallbackText:
			return text
		case HyperlinkFallbackURL:
			return url
		}
	}
	return xansi.SetHyperlink(url) + text + xansi.ResetHyperlink()
}
