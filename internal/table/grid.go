package table

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
)

// Grid is a table of cell values (rows x columns) with alignment options.
type Grid struct {
	Rows          [][]string
	ColumnPadding int
	Padding       Padding
	FlexCol       int // index of the flex column (-1 = disabled)
	MaxWidth      int // terminal width; flex column shrinks to fit (0 = disabled)
}

const defaultColumnPadding = 2

// NewGrid creates a Grid with the given rows and applies any options.
// Default column padding is 2 spaces, left-aligned.
func NewGrid(rows [][]string, opts ...GridOption) *Grid {
	g := &Grid{
		Rows:          rows,
		ColumnPadding: defaultColumnPadding,
		Padding:       PaddingLeft,
		FlexCol:       -1,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// VisibleWidth computes the visible width of a string, ignoring ANSI escapes.
func VisibleWidth(s string) int {
	return xansi.WcWidth.StringWidth(s)
}

// spaces returns n space characters wrapped in SGR 8 (conceal/hidden).
// This prevents bubbletea v2's hard-tab cursor optimization from collapsing
// runs of plain spaces into tab characters, which breaks column alignment
// in TUI contexts. SGR 8 is visually invisible on space characters.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return "\x1b[8m" + strings.Repeat(" ", n) + "\x1b[28m"
}

// truncateVisible truncates s to maxWidth visible characters, appending "…" if
// truncated. ANSI escape sequences are preserved but the visible text is cut.
func truncateVisible(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	w := VisibleWidth(s)
	if w <= maxWidth {
		return s
	}
	return xansi.WcWidth.Truncate(s, maxWidth-1, "…")
}

// AlignColumns aligns the grid into padded strings with gaps between columns.
// It returns the aligned strings and the computed visible width of each column.
func (g *Grid) AlignColumns() ([]string, []int) {
	if len(g.Rows) == 0 {
		return nil, nil
	}

	// Compute max visible width per column.
	maxCols := 0
	for _, row := range g.Rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	colWidths := make([]int, maxCols)
	for _, row := range g.Rows {
		for c, field := range row {
			w := VisibleWidth(field)
			if w > colWidths[c] {
				colWidths[c] = w
			}
		}
	}

	// Truncate flex column to fit within MaxWidth.
	if g.FlexCol >= 0 && g.FlexCol < maxCols && g.MaxWidth > 0 {
		totalGaps := (maxCols - 1) * g.ColumnPadding
		fixedWidth := totalGaps
		for c, w := range colWidths {
			if c != g.FlexCol {
				fixedWidth += w
			}
		}
		flexBudget := g.MaxWidth - fixedWidth
		if flexBudget < colWidths[g.FlexCol] && flexBudget > 0 {
			colWidths[g.FlexCol] = flexBudget
			// Truncate cell values that exceed the budget.
			for i, row := range g.Rows {
				if g.FlexCol < len(row) {
					g.Rows[i][g.FlexCol] = truncateVisible(row[g.FlexCol], flexBudget)
				}
			}
		}
	}

	// Format output with padding.
	gap := spaces(g.ColumnPadding)
	result := make([]string, len(g.Rows))
	for i, row := range g.Rows {
		var sb strings.Builder
		for c, field := range row {
			if c > 0 {
				sb.WriteString(gap)
			}
			pad := colWidths[c] - VisibleWidth(field)
			lastCol := c == len(row)-1
			switch g.Padding {
			case PaddingLeft:
				sb.WriteString(field)
				if !lastCol {
					sb.WriteString(spaces(pad))
				}
			case PaddingRight:
				sb.WriteString(spaces(pad))
				sb.WriteString(field)
			case PaddingCenter:
				left := pad / 2 //nolint:mnd // halve for centering
				right := pad - left
				sb.WriteString(spaces(left))
				sb.WriteString(field)
				if !lastCol {
					sb.WriteString(spaces(right))
				}
			}
		}
		result[i] = sb.String()
	}
	return result, colWidths
}
