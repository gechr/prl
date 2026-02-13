package table

import "time"

// Cell represents a single table cell with structured content.
type Cell struct {
	Text    string // rendered output for the current mode; may contain ANSI/OSC 8
	Plain   string // semantic visible text; used for filtering and fallback behavior
	SortKey any    // typed key used for sorting; nil means unsortable
}

// TextCell creates a Cell with identical text and plain values, and a string sort key.
func TextCell(text string) Cell {
	return Cell{Text: text, Plain: text, SortKey: text}
}

// StyledCell creates a Cell with styled display text and plain text for filtering,
// using the plain text as a string sort key.
func StyledCell(text, plain string) Cell {
	return Cell{Text: text, Plain: plain, SortKey: plain}
}

// TimeCell creates a Cell with a time.Time sort key.
func TimeCell(text string, t time.Time) Cell {
	return Cell{Text: text, Plain: text, SortKey: t}
}

// SortableCell creates a Cell with explicit text, plain, and sort key values.
func SortableCell(text, plain string, key any) Cell {
	return Cell{Text: text, Plain: plain, SortKey: key}
}

// DisplayOnly creates a Cell with no sort key (unsortable).
func DisplayOnly(text, plain string) Cell {
	return Cell{Text: text, Plain: plain}
}
