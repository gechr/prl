package table

import "strings"

// RenderedTable holds the result of rendering items into a table.
type RenderedTable[T any] struct {
	Header    string
	Rows      []Row[T]
	ColWidths []int // visible width of each column (including padding/indicators)
}

// String returns the full table output with header and rows joined by newlines.
func (t RenderedTable[T]) String() string {
	if len(t.Rows) == 0 {
		return ""
	}
	lines := make([]string, 0, 1+len(t.Rows))
	if t.Header != "" {
		lines = append(lines, t.Header)
	}
	for _, row := range t.Rows {
		lines = append(lines, row.Display)
	}
	return strings.Join(lines, "\n")
}
