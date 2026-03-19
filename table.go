package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clog"
	"github.com/gechr/prl/internal/ansiutil"
	"github.com/gechr/prl/internal/table"
	"github.com/gechr/prl/internal/term"
)

// tableLayout holds width-aware rendering decisions.
type tableLayout struct {
	compact bool            // use compact time format
	hidden  map[string]bool // columns to hide due to narrow terminal
}

// Type aliases for generic table types parameterized on PRRowModel.
type (
	Column   = table.Column[PRRowModel]
	TableRow = table.Row[PRRowModel]
)

// NewTableRenderer creates a table renderer for pull request row models.
func (p *prl) NewTableRenderer(
	cli *CLI, tty bool, opts ...table.Option,
) *table.Renderer[PRRowModel] {
	return p.newTableRenderer(cli, tty, term.Width(os.Stdout), opts...)
}

func (p *prl) newTableRenderer(
	cli *CLI, tty bool, termWidth int, opts ...table.Option,
) *table.Renderer[PRRowModel] {
	ansiOpts := []ansiutil.Option{ansiutil.WithTerminal(tty)}
	if !tty {
		ansiOpts = append(ansiOpts, ansiutil.WithHyperlinkFallback(ansiutil.HyperlinkFallbackURL))
	}
	ctx := table.NewRenderContext(p, ansiutil.New(ansiOpts...))
	columns := resolveColumns(cli)
	layout := computeLayout(termWidth, columns)
	defs := p.allColumnDefs(layout)

	var showIndex bool
	var cols []Column

	for _, colName := range columns {
		if layout.hidden[colName] {
			continue
		}
		if colName == "index" || colName == "idx" || colName == "i" {
			if !cli.IsInteractive() {
				showIndex = true
			}
			continue
		}
		if def, ok := defs[colName]; ok {
			cols = append(cols, def)
			// Inject status column after ref when not a terminal (plain text
			// substitute for the color-coded merge status).
			if colName == "ref" && !tty {
				cols = append(cols, defs["status"])
				cols = append(cols, defs["reason"])
			}
		} else {
			clog.Warn().Str("column", colName).Msg("unknown column, ignoring")
		}
	}

	renderOpts := []table.Option{
		// prl default: newest at top → clib reverse=true.
		// --reverse flag means oldest at top → clib reverse=false.
		// Non-TTY / interactive multi-select: always newest at top.
		table.WithReverse(cli.Interactive || cli.IsInteractive() || !cli.Reverse || !tty),
		table.WithShowIndex(showIndex),
		table.WithTermWidth(termWidth),
		table.WithTTY(tty),
	}
	renderOpts = append(renderOpts, opts...)

	return table.NewRenderer[PRRowModel](cols, ctx, renderOpts...)
}

// allColumnDefs returns all available column definitions.
// Columns render from PRRowModel; enrichment (author resolution, ref text,
// merge reason) is pre-computed in buildPRRowModels.
func (p *prl) allColumnDefs(layout tableLayout) map[string]Column {
	return map[string]Column{
		"index": {Name: "index", Header: "", Render: nil},
		"idx":   {Name: "index", Header: "", Render: nil},
		"i":     {Name: "index", Header: "", Render: nil},
		"org": {
			Name:   "org",
			Header: "ORG",
			Render: func(row PRRowModel, _ *table.RenderContext) table.Cell {
				return table.TextCell(row.RepoNWO)
			},
		},
		"owner": {
			Name:   "org",
			Header: "ORG",
			Render: func(row PRRowModel, _ *table.RenderContext) table.Cell {
				return table.TextCell(row.RepoNWO)
			},
		},
		"ref": {
			Name:   "ref",
			Header: "PR",
			Render: func(row PRRowModel, ctx *table.RenderContext) table.Cell {
				style := p.prMergeStyle(row.PR)
				display := ctx.Hyperlink(row.URL, style.Render(row.Ref))
				return table.StyledCell(display, row.Ref)
			},
		},
		"status": {
			Name:   "status",
			Header: "STATUS",
			Render: func(row PRRowModel, _ *table.RenderContext) table.Cell {
				return table.TextCell(p.renderMergeStatus(row.PR))
			},
		},
		"reason": {
			Name:   "reason",
			Header: "REASON",
			Render: func(row PRRowModel, _ *table.RenderContext) table.Cell {
				return table.TextCell(row.MergeReason)
			},
		},
		"repo": {
			Name:   "repo",
			Header: "REPO",
			Render: func(row PRRowModel, ctx *table.RenderContext) table.Cell {
				url := fmt.Sprintf("https://github.com/%s", row.RepoNWO)
				display := ctx.Hyperlink(url, row.Repo)
				return table.StyledCell(display, row.Repo)
			},
		},
		"number": {
			Name:   "number",
			Header: "NUMBER",
			Render: func(row PRRowModel, ctx *table.RenderContext) table.Cell {
				num := fmt.Sprintf("#%d", row.Number)
				style := p.prMergeStyle(row.PR)
				display := ctx.Hyperlink(row.URL, style.Render(num))
				return table.SortableCell(display, num, row.Number)
			},
		},
		colTitle: {
			Name:   colTitle,
			Header: "TITLE",
			Flex:   true,
			Render: func(row PRRowModel, ctx *table.RenderContext) table.Cell {
				title := truncateTitle(row.Title)
				if ctx.Ansi.Terminal() {
					displayTitle := normalizeTUIDisplayText(title)
					if rendered := p.theme.RenderMarkdown(displayTitle); rendered != "" {
						return table.StyledCell(rendered, title)
					}
					return table.StyledCell(displayTitle, title)
				}
				return table.TextCell(title)
			},
		},
		"labels": {
			Name:   "labels",
			Header: "LABELS",
			Render: func(row PRRowModel, _ *table.RenderContext) table.Cell {
				return table.TextCell(strings.Join(row.Labels, ", "))
			},
		},
		"author": {
			Name:   "author",
			Header: "AUTHOR",
			Render: func(row PRRowModel, ctx *table.RenderContext) table.Cell {
				am := row.Author
				if !ctx.Ansi.Terminal() {
					return table.TextCell(am.Display)
				}
				color := ctx.AssignEntityColor(am.Login)
				style := lipgloss.NewStyle().Foreground(color)
				if am.IsBot {
					style = style.Faint(true)
				} else if am.IsDeparted {
					style = style.Strikethrough(true)
				}
				display := ctx.Hyperlink(am.URL, style.Render(am.Display))
				return table.StyledCell(display, am.Display)
			},
		},
		"state": {
			Name:   "state",
			Header: "STATE",
			Render: func(row PRRowModel, _ *table.RenderContext) table.Cell {
				return table.TextCell(row.State)
			},
		},
		valueCreated: {
			Name:   valueCreated,
			Header: "CREATED",
			Render: func(row PRRowModel, ctx *table.RenderContext) table.Cell {
				var text string
				if layout.compact {
					text = p.theme.RenderTimeAgoCompact(row.CreatedAt, ctx.Ansi.Terminal())
				} else {
					text = p.theme.RenderTimeAgo(row.CreatedAt, ctx.Ansi.Terminal())
				}
				return table.TimeCell(text, row.CreatedAt)
			},
		},
		valueUpdated: {
			Name:   valueUpdated,
			Header: "UPDATED",
			Render: func(row PRRowModel, ctx *table.RenderContext) table.Cell {
				var text string
				if layout.compact {
					text = p.theme.RenderTimeAgoCompact(row.UpdatedAt, ctx.Ansi.Terminal())
				} else {
					text = p.theme.RenderTimeAgo(row.UpdatedAt, ctx.Ansi.Terminal())
				}
				return table.TimeCell(text, row.UpdatedAt)
			},
		},
		"url": {
			Name:   "url",
			Header: "URL",
			Render: func(row PRRowModel, _ *table.RenderContext) table.Cell {
				return table.TextCell(row.URL)
			},
		},
	}
}

// defaultColumns returns the default column names for standard mode.
func defaultColumns() []string {
	return []string{"index", colTitle, "ref", "created", "updated"}
}

// defaultColumnsWithAuthor returns columns with the author column added.
func defaultColumnsWithAuthor() []string {
	return []string{"index", colTitle, "ref", "created", "updated", "author"}
}

// truncateTitle truncates a title to maxTitleLen runes, appending an ellipsis if needed.
func truncateTitle(title string) string {
	if runes := []rune(title); len(runes) > maxTitleLen {
		return string(runes[:maxTitleLen-1]) + valueEllipsis
	}
	return title
}

// normalizeTUIDisplayText preserves emoji presentation while compensating for
// variation-selector sequences that some TUI renderers/font combinations draw
// a cell wider than their measured width, visually eating the following space.
func normalizeTUIDisplayText(text string) string {
	return strings.ReplaceAll(text, "\ufe0f ", "\ufe0f  ")
}

// Estimated column widths for layout decisions (compact mode, column hiding).
// These are rough estimates - actual truncation is handled by the grid's flex
// column support, which uses real measured widths.
//
//nolint:mnd // width estimates are inherently magic numbers
var columnWidthEstimate = map[string]int{
	"index": 3, "idx": 3, "i": 3,
	"ref": 20, "repo": 15, "org": 25, "owner": 25,
	"number": 5, "author": 12, "state": 6, "labels": 15,
	"status": 8, "reason": 12, "url": 50,
	"created": 14, "updated": 14,
	colTitle: 30,
}

//nolint:mnd // width estimates are inherently magic numbers
var columnWidthEstimateCompact = map[string]int{
	"created": 7, "updated": 7,
}

// Column groups to progressively hide when the terminal is too narrow, in order.
// Created is hidden first (least important), then updated, then title as a last resort.
// Index and ref are never hidden.
var hideOrder = [][]string{
	{valueCreated}, // hide created first
	{valueUpdated}, // then updated
	{colTitle},     // title last resort
}

// computeLayout determines compact time mode and which columns to hide based on
// the terminal width and active column set. Actual title truncation is handled
// by the grid's flex column, so no title budget is computed here.
func computeLayout(termWidth int, columns []string) tableLayout {
	if termWidth <= 0 {
		return tableLayout{}
	}

	hidden := make(map[string]bool)

	// Effective columns after hiding.
	effective := func() []string {
		var out []string
		for _, c := range columns {
			if !hidden[c] {
				out = append(out, c)
			}
		}
		return out
	}

	compact := false
	eff := effective()

	// Switch to compact time when terminal is narrow and time columns are present.
	if termWidth < compactTimeThreshold && hasTimeColumns(eff) {
		compact = true
	}

	// Progressively hide column groups if estimated total exceeds terminal width.
	for _, group := range hideOrder {
		if estimatedWidth(eff, compact) <= termWidth {
			break
		}
		changed := false
		for _, candidate := range group {
			if hasColumn(columns, candidate) && !hidden[candidate] {
				hidden[candidate] = true
				changed = true
			}
		}
		if changed {
			eff = effective()
		}
	}

	return tableLayout{compact: compact, hidden: hidden}
}

// estimatedWidth returns the estimated total table width for the given columns.
func estimatedWidth(columns []string, compact bool) int {
	total := 0
	for _, col := range columns {
		w := columnWidthEstimate[col]
		if compact {
			if cw, ok := columnWidthEstimateCompact[col]; ok {
				w = cw
			}
		}
		total += w
	}
	if len(columns) > 1 {
		total += (len(columns) - 1) * columnGap
	}
	return total
}

// hasColumn returns true if the column name appears in the list.
func hasColumn(columns []string, name string) bool {
	return slices.Contains(columns, name)
}

// hasTimeColumns returns true if any time column (created, updated) is present.
func hasTimeColumns(columns []string) bool {
	for _, col := range columns {
		if col == valueCreated || col == valueUpdated {
			return true
		}
	}
	return false
}

// normalizeColumns lowercases and trims column names.
func normalizeColumns(values []string) []string {
	var cols []string
	for _, v := range values {
		col := strings.TrimSpace(strings.ToLower(v))
		if col != "" {
			cols = append(cols, col)
		}
	}
	return cols
}

// singleOrg returns the org name if exactly one non-"all" org is specified, otherwise "".
func singleOrg(values []string) string {
	filtered := filterAllValue(values)
	if len(filtered) == 1 {
		return filtered[0]
	}
	return ""
}

// resolveColumns returns the column list for rendering.
func resolveColumns(cli *CLI) []string {
	if len(cli.Columns.Values) > 0 {
		return normalizeColumns(cli.Columns.Values)
	}
	if shouldShowAuthor(cli) {
		return defaultColumnsWithAuthor()
	}
	return defaultColumns()
}
