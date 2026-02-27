package main

import (
	"fmt"
	"strings"

	cliansi "github.com/gechr/clib/ansi"
	"github.com/gechr/clib/table"
	"github.com/gechr/clog"
)

// Type aliases for clib generic types.
type (
	Column   = table.Column[PullRequest]
	TableRow = table.Row[PullRequest]
)

// NewTableRenderer creates a table renderer for pull requests.
func (p *prl) NewTableRenderer(
	cli *CLI, tty bool, resolver *AuthorResolver,
) *table.Renderer[PullRequest] {
	ansiOpts := []cliansi.Option{cliansi.WithTerminal(tty)}
	if !tty {
		ansiOpts = append(ansiOpts, cliansi.WithHyperlinkFallback(cliansi.HyperlinkFallbackURL))
	}
	ctx := table.NewRenderContext(p.theme, cliansi.New(ansiOpts...))
	orgFilter := singleOrg(cli.Organization.Values)
	columns := resolveColumns(cli)
	defs := p.allColumnDefs(orgFilter, resolver)

	var showIndex bool
	var cols []Column

	for _, colName := range columns {
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
		table.WithReverse(!cli.Reverse || !tty),
		table.WithShowIndex(showIndex),
	)
}

// allColumnDefs returns all available column definitions.
func (p *prl) allColumnDefs(orgFilter string, resolver *AuthorResolver) map[string]Column {
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
		"title": {
			Name:   "title",
			Header: "TITLE",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
				title := pr.Title
				title = truncateTitle(title)
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
				return p.theme.RenderTimeAgo(pr.CreatedAt, ctx.Ansi.Terminal())
			},
		},
		valueUpdated: {
			Name:   valueUpdated,
			Header: "UPDATED",
			Render: func(pr PullRequest, ctx *table.RenderContext) string {
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
	return []string{"index", "title", "ref", "created", "updated"}
}

// defaultColumnsWithAuthor returns columns with the author column added.
func defaultColumnsWithAuthor() []string {
	return []string{"index", "title", "ref", "created", "updated", "author"}
}

// truncateTitle truncates a title to maxTitleLen runes, appending an ellipsis if needed.
func truncateTitle(title string) string {
	if runes := []rune(title); len(runes) > maxTitleLen {
		return string(runes[:maxTitleLen-1]) + "…"
	}
	return title
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
