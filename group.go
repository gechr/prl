package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"

	lg "charm.land/lipgloss/v2"
	xansi "github.com/gechr/x/ansi"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"
)

// groupKey identifies a field that a --group breakdown buckets on.
type groupKey int

const (
	groupAuthor groupKey = iota
	groupRepo
	groupOwner
	groupState
	groupDraft
	groupLabel
)

// groupNoneValue is the bucket label for PRs missing a value for the key
// (e.g. an unlabelled PR when grouping by label).
const groupNoneValue = "(none)"

// parseGroupKey resolves a --group token to a groupKey.
func parseGroupKey(s string) (groupKey, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case colAuthor, "a":
		return groupAuthor, true
	case valueRepo, "repository", "r":
		return groupRepo, true
	case colOwner, "org", "o":
		return groupOwner, true
	case colState, "s":
		return groupState, true
	case valueDraft, "d":
		return groupDraft, true
	case "label", colLabels, "l":
		return groupLabel, true
	default:
		return 0, false
	}
}

func (k groupKey) String() string {
	switch k {
	case groupAuthor:
		return colAuthor
	case groupRepo:
		return valueRepo
	case groupOwner:
		return colOwner
	case groupState:
		return colState
	case groupDraft:
		return valueDraft
	case groupLabel:
		return "label"
	default:
		return "?"
	}
}

// ownerOf returns the owner portion of a PR's repository (the part before the
// "/" in "owner/repo"), or "" when unknown.
func ownerOf(pr PullRequest) string {
	if before, _, ok := strings.Cut(pr.Repository.NameWithOwner, "/"); ok {
		return before
	}
	return ""
}

// commonOwner returns the owner shared by every PR, or "" when the results span
// more than one owner. Used to drop a redundant owner prefix when grouping by
// repo, mirroring the single-owner shortening applied elsewhere.
func commonOwner(prs []PullRequest) string {
	if len(prs) == 0 {
		return ""
	}
	owner := ownerOf(prs[0])
	for _, pr := range prs[1:] {
		if ownerOf(pr) != owner {
			return ""
		}
	}
	return owner
}

// groupValues returns the bucket value(s) a PR contributes for the given key.
// Most keys yield exactly one value; label fans out to one per label (and
// groupNoneValue when a PR carries none), so a PR can land in several buckets.
// When stripRepoOwner is set, repo buckets use the bare repo name because every
// result shares one owner.
func groupValues(pr PullRequest, key groupKey, stripRepoOwner bool) []string {
	switch key {
	case groupAuthor:
		return []string{orNone(pr.Author.Login)}
	case groupRepo:
		if stripRepoOwner && pr.Repository.Name != "" {
			return []string{pr.Repository.Name}
		}
		name := pr.Repository.NameWithOwner
		if name == "" {
			name = pr.Repository.Name
		}
		return []string{orNone(name)}
	case groupOwner:
		return []string{orNone(ownerOf(pr))}
	case groupState:
		return []string{orNone(pr.State)}
	case groupDraft:
		if pr.IsDraft {
			return []string{valueDraft}
		}
		return []string{valueReady}
	case groupLabel:
		if len(pr.Labels) == 0 {
			return []string{groupNoneValue}
		}
		return xslices.Map(pr.Labels, func(l Label) string { return orNone(l.Name) })
	default:
		return []string{groupNoneValue}
	}
}

func orNone(s string) string {
	if s == "" {
		return groupNoneValue
	}
	return s
}

// groupNode is one bucket in a (possibly nested) breakdown.
type groupNode struct {
	Value    string      `json:"value"`
	Count    int         `json:"count"`
	Children []groupNode `json:"children,omitempty"`
}

// buildGroupNodes buckets prs by the first key, recursing into the rest to
// produce a nested breakdown. Buckets are sorted by count (desc) then value.
func buildGroupNodes(prs []PullRequest, keys []groupKey, stripRepoOwner bool) []groupNode {
	if len(keys) == 0 {
		return nil
	}
	key, rest := keys[0], keys[1:]

	buckets := make(map[string][]PullRequest)
	for _, pr := range prs {
		for _, v := range groupValues(pr, key, stripRepoOwner) {
			buckets[v] = append(buckets[v], pr)
		}
	}

	nodes := make([]groupNode, 0, len(buckets))
	for value, sub := range buckets {
		nodes = append(nodes, groupNode{
			Value:    value,
			Count:    len(sub),
			Children: buildGroupNodes(sub, rest, stripRepoOwner),
		})
	}
	sortGroupNodes(nodes)
	return nodes
}

// sortGroupNodes orders buckets by descending count, breaking ties with a
// natural comparison on the value so output is stable and readable.
func sortGroupNodes(nodes []groupNode) {
	slices.SortStableFunc(nodes, func(a, b groupNode) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		return xstrings.CompareNatural(a.Value, b.Value)
	})
}

// groupColGap is the number of spaces between grid columns.
const groupColGap = 3

// groupIndent is the number of spaces each nesting level indents by.
const groupIndent = 2

// renderGroup renders a breakdown of prs bucketed by keys. When asJSON is true
// it emits the nested node tree as JSON. Otherwise each top-level group becomes
// a block - a header followed by its indented members, one per line - and the
// blocks stack in a single column when they fit the terminal height, or flow
// column-major (read down, then across) into as many side-by-side columns as
// fit termWidth when they would overflow it. On a TTY, leaf buckets use prl's
// stable per-entity colouring via entityColor (nil disables all colour) with
// bold counts; header buckets render as bold "name (count)" with a dim count.
func renderGroup(
	prs []PullRequest,
	keys []groupKey,
	asJSON, tty bool,
	entityColor func(string) color.Color,
	termWidth, termHeight int,
) (string, error) {
	nodes := buildGroupNodes(prs, keys, commonOwner(prs) != "")

	if asJSON {
		data, err := json.MarshalIndent(nodes, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshaling group-by JSON: %w", err)
		}
		return string(data), nil
	}

	nested := len(keys) > 1

	countWidth := len(strconv.Itoa(groupMaxLeafCount(nodes)))

	blocks := make([][]string, len(nodes))
	for i, n := range nodes {
		blocks[i] = groupBlock(n, countWidth, tty, entityColor)
	}

	return strings.Join(groupGrid(blocks, nested, termWidth, termHeight), nl), nil
}

// groupBlock flattens a top-level group into its display lines: a "count name"
// header followed by each descendant on its own line, deepest last and indented
// by depth.
func groupBlock(
	n groupNode,
	countWidth int,
	tty bool,
	entityColor func(string) color.Color,
) []string {
	var lines []string
	var walk func(node groupNode, depth int)
	walk = func(node groupNode, depth int) {
		lines = append(lines, groupNodeLabel(node, depth, countWidth, tty, entityColor))
		for _, c := range node.Children {
			walk(c, depth+1)
		}
	}
	walk(n, 0)
	return lines
}

// groupMaxLeafCount returns the largest count among leaf buckets - the widest
// count the "count name" leaf rows right-align to (headers render their count
// as a suffix instead).
func groupMaxLeafCount(nodes []groupNode) int {
	count := 0
	for _, n := range nodes {
		if len(n.Children) == 0 {
			count = max(count, n.Count)
		} else {
			count = max(count, groupMaxLeafCount(n.Children))
		}
	}
	return count
}

// groupNodeLabel renders a bucket, indented two spaces per nesting level.
// Header buckets (those with children) render as "name (count)" - the name
// bold, the count dim. Leaf buckets render as "count name" - the count
// right-aligned to countWidth and bold, both in the bucket's per-entity
// colour.
func groupNodeLabel(
	n groupNode,
	depth, countWidth int,
	tty bool,
	entityColor func(string) color.Color,
) string {
	indent := strings.Repeat(" ", groupIndent*depth)
	if len(n.Children) > 0 {
		name := n.Value
		count := "(" + strconv.Itoa(n.Count) + ")"
		if tty {
			name = styleText.Bold(true).Render(name)
			count = styleDim.Render(count)
		}
		return indent + name + " " + count
	}
	name := n.Value
	count := strconv.Itoa(n.Count)
	pad := strings.Repeat(" ", max(0, countWidth-len(count)))
	if tty {
		name = styleGroupName(name, n.Value, entityColor)
		count = styleGroupCount(count, n.Value, entityColor)
	}
	return indent + pad + count + " " + name
}

// styleGroupName colours a bucket name with prl's stable per-entity colour.
// key is the raw value used for the colour lookup.
func styleGroupName(name, key string, entityColor func(string) color.Color) string {
	if entityColor != nil {
		if c := entityColor(key); c != nil {
			return lg.NewStyle().Foreground(c).Render(name)
		}
	}
	return name
}

// styleGroupCount renders a bucket count in bold in the bucket's entity
// colour, falling back to plain bold when no colour is assigned.
func styleGroupCount(count, key string, entityColor func(string) color.Color) string {
	if entityColor != nil {
		if c := entityColor(key); c != nil {
			return lg.NewStyle().Foreground(c).Bold(true).Render(count)
		}
	}
	return styleText.Bold(true).Render(count)
}

// groupStack lays blocks out in one column, with a blank line between groups
// when nested.
func groupStack(blocks [][]string, nested bool) []string {
	var lines []string
	for i, b := range blocks {
		if nested && i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, b...)
	}
	return lines
}

// groupGrid lays blocks out column-major - read down one column, then continue
// at the top of the next. Columns are as tall as the terminal allows (so a
// breakdown that fits the height stays a single stacked column) and only grow
// taller when more columns than fit termWidth would otherwise be needed.
// Blocks stay whole within a column, blank-separated when nested. Falls back
// to a single stacked column when the width is unknown, or to the fewest rows
// that fit the width when only the height is unknown.
func groupGrid(blocks [][]string, nested bool, termWidth, termHeight int) []string {
	if len(blocks) == 0 {
		return nil
	}
	if termWidth <= 0 {
		return groupStack(blocks, nested)
	}
	sep := 0
	if nested {
		sep = 1
	}
	tallest, total := 0, 0
	for i, b := range blocks {
		if i > 0 {
			total += sep
		}
		tallest = max(tallest, len(b))
		total += len(b)
	}

	// Raise the column-height limit until the columns it implies fit the
	// terminal width; a single column always fits.
	limit := max(tallest, termHeight)
	for ; ; limit++ {
		cols := groupSplit(blocks, sep, limit)
		if len(cols) == 1 || limit >= total || groupGridWidth(cols) <= termWidth {
			return groupJoin(cols)
		}
	}
}

// groupSplit cuts blocks into contiguous columns, each rendered no taller than
// limit lines (a column always takes at least one block). Each column is
// returned as its flattened lines, blank-separated when sep is 1.
func groupSplit(blocks [][]string, sep, limit int) [][]string {
	var cols [][]string
	var cur []string
	for _, b := range blocks {
		if len(cur) > 0 && len(cur)+sep+len(b) > limit {
			cols = append(cols, cur)
			cur = nil
		}
		if len(cur) > 0 && sep > 0 {
			cur = append(cur, "")
		}
		cur = append(cur, b...)
	}
	return append(cols, cur)
}

// groupColWidth is the width of a column's widest line.
func groupColWidth(lines []string) int {
	width := 0
	for _, l := range lines {
		width = max(width, xansi.StringWidth(l))
	}
	return width
}

// groupGridWidth is the rendered width of columns laid side by side.
func groupGridWidth(cols [][]string) int {
	width := 0
	for i, c := range cols {
		if i > 0 {
			width += groupColGap
		}
		width += groupColWidth(c)
	}
	return width
}

// groupJoin renders columns side by side, padding every column but the last to
// its widest line.
func groupJoin(cols [][]string) []string {
	if len(cols) == 1 {
		return cols[0]
	}
	widths := make([]int, len(cols))
	height := 0
	for i, c := range cols {
		widths[i] = groupColWidth(c)
		height = max(height, len(c))
	}
	gap := strings.Repeat(" ", groupColGap)
	lines := make([]string, 0, height)
	for r := range height {
		parts := make([]string, 0, len(cols))
		for i, c := range cols {
			cell := ""
			if r < len(c) {
				cell = c[r]
			}
			if i < len(cols)-1 { // pad all but the last column so the next aligns
				cell += strings.Repeat(" ", widths[i]-xansi.StringWidth(cell))
			}
			parts = append(parts, cell)
		}
		lines = append(lines, strings.TrimRight(strings.Join(parts, gap), " "))
	}
	return lines
}

// groupCapNotice returns the warning shown when the match set exceeds GitHub's
// 1000-result search cap, so a sampled breakdown is not read as exhaustive.
func groupCapNotice(shown, total int, tty bool) string {
	msg := fmt.Sprintf(
		"Showing the %d most recent of %d matches (GitHub caps search results at %d)",
		shown, total, maxGroupResults,
	)
	if tty {
		return styleWarning.Render("⚠ " + msg)
	}
	return msg
}
