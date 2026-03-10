package table

// Padding controls the text alignment within a column.
type Padding int

const (
	PaddingLeft   Padding = iota // Default: left-aligned (pad right).
	PaddingCenter                // Center-aligned (pad both sides).
	PaddingRight                 // Right-aligned (pad left).
)

// GridOption configures a Grid.
type GridOption func(*Grid)

// WithColumnPadding sets the number of spaces between columns.
func WithColumnPadding(n int) GridOption {
	return func(g *Grid) {
		g.ColumnPadding = n
	}
}

// WithPadding sets the text alignment within columns.
func WithPadding(p Padding) GridOption {
	return func(g *Grid) {
		g.Padding = p
	}
}

// Option configures a Renderer.
type Option func(*config)

// HeaderRenderer customizes how column headers are rendered before alignment.
// The returned string may include ANSI styling.
type HeaderRenderer func(name, header string, ctx *RenderContext) string

type config struct {
	reverse        bool
	showIndex      bool
	termWidth      int // terminal width for flex column truncation (0 = disabled)
	headerRenderer HeaderRenderer
}

// WithReverse sets whether to reverse row order (newest first at top).
func WithReverse(v bool) Option { return func(c *config) { c.reverse = v } }

// WithShowIndex sets whether to show row indices.
func WithShowIndex(v bool) Option { return func(c *config) { c.showIndex = v } }

// WithTermWidth sets the terminal width for flex column truncation.
// When set, columns marked Flex=true are truncated so rows fit within this width.
func WithTermWidth(w int) Option { return func(c *config) { c.termWidth = w } }

// WithHeaderRenderer sets a custom header renderer used before column alignment.
func WithHeaderRenderer(fn HeaderRenderer) Option {
	return func(c *config) { c.headerRenderer = fn }
}
