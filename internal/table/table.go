package table

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/gechr/prl/internal/ansiutil"
)

// Theme provides the styling methods needed by the table renderer.
type Theme interface {
	RenderBold(s string) string
	RenderDim(s string) string
	EntityColors() []color.Color
}

// Column defines a table column for items of type T.
type Column[T any] struct {
	Name   string
	Header string
	Render func(item T, ctx *RenderContext) Cell
	Flex   bool // if true, this column shrinks to fit terminal width
}

// Row pairs an item with its rendered cell values.
type Row[T any] struct {
	Item    T
	Cells   []Cell // rendered cell values
	Display string // aligned display line, set after formatting
}

// RenderContext holds shared state for column renderers.
type RenderContext struct {
	Theme        Theme
	Ansi         *ansiutil.Writer
	entityColors map[string]int
	colorIndex   int
}

// NewRenderContext creates a RenderContext.
func NewRenderContext(theme Theme, ansiWriter *ansiutil.Writer) *RenderContext {
	return &RenderContext{
		Theme:        theme,
		Ansi:         ansiWriter,
		entityColors: make(map[string]int),
	}
}

// AssignEntityColor returns a stable color for the given key, cycling through
// the theme's EntityColors palette.
func (ctx *RenderContext) AssignEntityColor(key string) color.Color {
	colors := ctx.Theme.EntityColors()
	if len(colors) == 0 {
		return nil
	}
	key = strings.ToLower(key)
	if _, exists := ctx.entityColors[key]; !exists {
		ctx.entityColors[key] = ctx.colorIndex
		ctx.colorIndex = (ctx.colorIndex + 1) % len(colors)
	}
	return colors[ctx.entityColors[key]]
}

// Hyperlink creates an OSC 8 terminal hyperlink using the context's ANSI writer.
func (ctx *RenderContext) Hyperlink(url, text string) string {
	return ctx.Ansi.Hyperlink(url, text)
}

// Renderer renders a slice of items as an aligned table.
type Renderer[T any] struct {
	columns []Column[T]
	ctx     *RenderContext
	cfg     config
}

// NewRenderer creates a Renderer for the given columns.
func NewRenderer[T any](
	columns []Column[T],
	ctx *RenderContext,
	opts ...Option,
) *Renderer[T] {
	r := &Renderer[T]{
		columns: columns,
		ctx:     ctx,
	}
	for _, opt := range opts {
		opt(&r.cfg)
	}
	return r
}

// Columns returns the renderer's column definitions.
func (r *Renderer[T]) Columns() []Column[T] {
	return r.columns
}

// Render renders items as a table and returns a RenderedTable with header and rows.
// The input items slice is never mutated.
func (r *Renderer[T]) Render(items []T) RenderedTable[T] {
	if len(r.columns) == 0 || len(items) == 0 {
		return RenderedTable[T]{}
	}
	rows := r.buildRows(items)
	return r.format(rows)
}

func (r *Renderer[T]) buildRows(items []T) []Row[T] {
	rows := make([]Row[T], len(items))
	for i, item := range items {
		cells := make([]Cell, len(r.columns))
		for j, col := range r.columns {
			cells[j] = col.Render(item, r.ctx)
		}
		rows[i] = Row[T]{Item: item, Cells: cells}
	}
	return rows
}

func (r *Renderer[T]) format(rows []Row[T]) RenderedTable[T] {
	if len(rows) == 0 {
		return RenderedTable[T]{}
	}

	// Default: oldest first, newest at bottom. Reverse flips to newest at top.
	if r.cfg.reverse {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}

	// Collect cell text grid for alignment.
	grid := make([][]string, len(rows))
	for i, row := range rows {
		grid[i] = make([]string, len(row.Cells))
		for j, cell := range row.Cells {
			grid[i][j] = cell.Text
		}
	}

	// Build header row.
	header := make([]string, len(r.columns))
	for i, col := range r.columns {
		if r.cfg.headerRenderer != nil {
			header[i] = r.cfg.headerRenderer(col.Name, col.Header, r.ctx)
		} else {
			header[i] = r.ctx.Theme.RenderBold(col.Header)
		}
	}

	// Add index numbers: #1 = most recent.
	if r.cfg.showIndex {
		total := len(grid)
		width := len(fmt.Sprintf("%d", total))
		header = append([]string{strings.Repeat(" ", width)}, header...)

		for i := range grid {
			var num int
			if r.cfg.reverse {
				num = i + 1
			} else {
				num = total - i
			}
			numStr := r.ctx.Theme.RenderDim(fmt.Sprintf("%*d", width, num))
			grid[i] = append([]string{numStr}, grid[i]...)
		}
	}

	// Determine flex column index in the grid (offset by 1 if index column prepended).
	flexCol := -1
	for i, col := range r.columns {
		if col.Flex {
			flexCol = i
			if r.cfg.showIndex {
				flexCol = i + 1
			}
			break
		}
	}

	// Prepend header and align.
	allRows := append([][]string{header}, grid...)
	g := NewGrid(allRows)
	g.TTY = r.cfg.tty
	if flexCol >= 0 && r.cfg.termWidth > 0 {
		g.FlexCol = flexCol
		g.MaxWidth = r.cfg.termWidth
	}
	aligned, colWidths := g.AlignColumns()

	// Copy aligned display lines back to rows (skip header at index 0).
	for i := range rows {
		rows[i].Display = aligned[i+1]
	}

	return RenderedTable[T]{
		Header:    aligned[0],
		Rows:      rows,
		ColWidths: colWidths,
	}
}
