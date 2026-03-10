package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	cliansi "github.com/gechr/clib/ansi"
	"github.com/gechr/clib/table"
	"github.com/gechr/clib/terminal"
	"github.com/gechr/clog"
)

// tableLayout holds width-aware rendering decisions.
type tableLayout struct {
	compact bool            // use compact time format
	hidden  map[string]bool // columns to hide due to narrow terminal
}

// Type aliases for clib generic types.
type (
	Column   = table.Column[PullRequest]
	TableRow = table.Row[PullRequest]
)

// NewTableRenderer creates a table renderer for pull requests.
func (p *prl) NewTableRenderer(
	cli *CLI, tty bool, resolver *AuthorResolver,
) *table.Renderer[PullRequest] {
	return p.newTableRenderer(cli, tty, resolver, terminal.Width(os.Stdout))
}

func (p *prl) newTableRenderer(
	cli *CLI, tty bool, resolver *AuthorResolver, termWidth int,
) *table.Renderer[PullRequest] {
	ansiOpts := []cliansi.Option{cliansi.WithTerminal(tty)}
	if !tty {
		ansiOpts = append(ansiOpts, cliansi.WithHyperlinkFallback(cliansi.HyperlinkFallbackURL))
	}
	ctx := table.NewRenderContext(p.theme, cliansi.New(ansiOpts...))
	orgFilter := singleOrg(cli.Organization.Values)
	columns := resolveColumns(cli)
	layout := computeLayout(termWidth, columns)
	defs := p.allColumnDefs(orgFilter, resolver, layout)

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

	return table.NewRenderer[PullRequest](
		cols,
		ctx,
		// prl default: newest at top → clib reverse=true.
		// --reverse flag means oldest at top → clib reverse=false.
		// Non-TTY: ignore --reverse, always newest at top (#1, #2, #3… from top).
		table.WithReverse(cli.Interactive || !cli.Reverse || !tty),
		table.WithShowIndex(showIndex),
		table.WithTermWidth(termWidth),
	)
}

// allColumnDefs returns all available column definitions.
func (p *prl) allColumnDefs(
	orgFilter string,
	resolver *AuthorResolver,
	layout tableLayout,
) map[string]Column {
	return map[string]Column{
		"index": {Name: "index", Header: "", Render: nil},
		"idx":   {Name: "index", Header: "", Render: nil},
		"i":     {Name: "index", Header: "", Render: nil},
		"org": {
			Name:   "org",
			Header: "ORG",
			Render: func(pr PullRequest, _ *table.RenderContext) string {
				return pr.Repository.NameWithOwner
			},
		},
		"owner": {
			Name:   "org",
			Header: "ORG",
			Render: func(pr PullRequest, _ *table.RenderContext) string {
				return pr.Repository.NameWithOwner
			},
		},
		"ref": {
			Name:   "ref",
			Header: "PR",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				name := pr.Repository.Name
				if orgFilter == "" || orgFilter == valueAll {
					name = pr.Repository.NameWithOwner
				}
				ref := fmt.Sprintf("%s#%d", name, pr.Number)
				style := p.prMergeStyle(pr)
				return ctx.Hyperlink(pr.URL, style.Render(ref))
			},
		},
		"status": {
			Name:   "status",
			Header: "STATUS",
			Render: func(pr PullRequest, _ *table.RenderContext) string {
				return p.renderMergeStatus(pr)
			},
		},
		"reason": {
			Name:   "reason",
			Header: "REASON",
			Render: func(pr PullRequest, _ *table.RenderContext) string {
				return p.renderMergeReason(pr)
			},
		},
		"repo": {
			Name:   "repo",
			Header: "REPO",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				url := fmt.Sprintf("https://github.com/%s", pr.Repository.NameWithOwner)
				return ctx.Hyperlink(url, pr.Repository.Name)
			},
		},
		"number": {
			Name:   "number",
			Header: "NUMBER",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				num := fmt.Sprintf("#%d", pr.Number)
				style := p.prMergeStyle(pr)
				return ctx.Hyperlink(pr.URL, style.Render(num))
			},
		},
		colTitle: {
			Name:   colTitle,
			Header: "TITLE",
			Flex:   true,
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				title := truncateTitle(pr.Title)
				if ctx.Ansi.Terminal() {
					if rendered := p.theme.RenderMarkdown(title); rendered != "" {
						return rendered
					}
				}
				return title
			},
		},
		"labels": {
			Name:   "labels",
			Header: "LABELS",
			Render: func(pr PullRequest, _ *table.RenderContext) string {
				names := make([]string, len(pr.Labels))
				for i, l := range pr.Labels {
					names[i] = l.Name
				}
				return strings.Join(names, ", ")
			},
		},
		"author": {
			Name:   "author",
			Header: "AUTHOR",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				if resolver != nil {
					return renderAuthor(pr, ctx, resolver)
				}
				return pr.Author.Login
			},
		},
		"state": {
			Name:   "state",
			Header: "STATE",
			Render: func(pr PullRequest, _ *table.RenderContext) string {
				return pr.State
			},
		},
		valueCreated: {
			Name:   valueCreated,
			Header: "CREATED",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				if layout.compact {
					return p.theme.RenderTimeAgoCompact(pr.CreatedAt, ctx.Ansi.Terminal())
				}
				return p.theme.RenderTimeAgo(pr.CreatedAt, ctx.Ansi.Terminal())
			},
		},
		valueUpdated: {
			Name:   valueUpdated,
			Header: "UPDATED",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				if layout.compact {
					return p.theme.RenderTimeAgoCompact(pr.UpdatedAt, ctx.Ansi.Terminal())
				}
				return p.theme.RenderTimeAgo(pr.UpdatedAt, ctx.Ansi.Terminal())
			},
		},
		"url": {
			Name:   "url",
			Header: "URL",
			Render: func(pr PullRequest, _ *table.RenderContext) string {
				return pr.URL
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
		return string(runes[:maxTitleLen-1]) + "…"
	}
	return title
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
