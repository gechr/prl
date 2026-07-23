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
	case valueLabel, colLabels, "l":
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
		return valueLabel
	default:
		return "?"
	}
}

// groupAuthorResolver lazily creates an author resolver only when an author
// appears in the requested grouping keys.
func groupAuthorResolver(keys []groupKey, cfg *Config) *AuthorResolver {
	if slices.Contains(keys, groupAuthor) {
		return NewAuthorResolver(cfg)
	}
	return nil
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

// shouldStripGroupRepoOwner reports whether repo buckets can safely use bare
// names. Multiple positive owner scopes stay qualified even when the fetched
// sample happens to contain results from only one of them.
func shouldStripGroupRepoOwner(prs []PullRequest, owners []string) bool {
	positive, _ := splitNegated(filterAllValue(owners))
	return len(positive) <= 1 && commonOwner(prs) != ""
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
	colorKey string
	url      string
}

type groupSearchFilter struct {
	key   groupKey
	query string
	repo  string
}

// buildGroupNodes buckets prs by the first key, recursing into the rest to
// produce a nested breakdown. Buckets are sorted by count (desc) then value.
func buildGroupNodes(prs []PullRequest, keys []groupKey, stripRepoOwner bool) []groupNode {
	return buildGroupNodesRecursive(prs, keys, stripRepoOwner, nil, nil, false)
}

// buildGroupNodesWithLinks builds the same breakdown while attaching a GitHub
// search URL for each bucket's complete ancestor path.
func buildGroupNodesWithLinks(
	prs []PullRequest,
	keys []groupKey,
	stripRepoOwner bool,
	params *SearchParams,
) []groupNode {
	return buildGroupNodesRecursive(
		prs,
		keys,
		stripRepoOwner,
		params,
		nil,
		params != nil && params.Query != "",
	)
}

// buildGroupNodesRecursive carries the accumulated search path through nested
// buckets. A branch becomes unlinkable when one of its values cannot be
// expressed faithfully as a GitHub search qualifier.
func buildGroupNodesRecursive(
	prs []PullRequest,
	keys []groupKey,
	stripRepoOwner bool,
	params *SearchParams,
	path []groupSearchFilter,
	linkable bool,
) []groupNode {
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
		nodePath := path
		nodeLinkable := linkable
		if nodeLinkable {
			qualifier := groupSearchQualifier(key, value, sub)
			nodeLinkable = qualifier != ""
			if nodeLinkable {
				filter := groupSearchFilter{
					key:   key,
					query: qualifier,
				}
				if key == groupRepo {
					filter.repo = groupSearchRepo(value, sub)
				}
				nodePath = append(slices.Clone(path), filter)
			}
		}

		nodeURL := ""
		if nodeLinkable {
			nodeURL = groupSearchURL(params, nodePath)
		}
		nodes = append(nodes, groupNode{
			Value: value,
			Count: len(sub),
			Children: buildGroupNodesRecursive(
				sub,
				rest,
				stripRepoOwner,
				params,
				nodePath,
				nodeLinkable,
			),
			url: nodeURL,
		})
	}
	sortGroupNodes(nodes, key)
	return nodes
}

func groupSearchURL(params *SearchParams, path []groupSearchFilter) string {
	for _, filter := range slices.Backward(path) {
		if filter.repo != "" {
			return githubRepoPullsURL(
				filter.repo,
				params.groupSearchQueryScoped(path, true),
			)
		}
	}
	for _, term := range params.queryTerms {
		if term.repo != "" {
			return githubRepoPullsURL(
				term.repo,
				params.groupSearchQueryScoped(path, true),
			)
		}
	}
	return githubSearchURL(params.groupSearchQuery(path))
}

// groupSearchQualifier maps a bucket to the GitHub search expression that
// selects it. prs supplies the unshortened repository identity when the
// displayed repo bucket omits its common owner.
func groupSearchQualifier(key groupKey, value string, prs []PullRequest) string {
	switch key {
	case groupAuthor:
		if value != groupNoneValue {
			return "author:" + value
		}
	case groupRepo:
		repo := groupSearchRepo(value, prs)
		if repo != groupNoneValue {
			return "repo:" + repo
		}
	case groupOwner:
		if value != groupNoneValue {
			return "user:" + value
		}
	case groupState:
		switch value {
		case valueMerged:
			return "is:merged"
		case valueOpen:
			return "state:open"
		case valueClosed:
			return "state:closed is:unmerged"
		}
	case groupDraft:
		switch value {
		case valueDraft:
			return "draft:true"
		case valueReady:
			return "draft:false"
		}
	case groupLabel:
		if value == groupNoneValue {
			return "no:label"
		}
		return valueLabel + ":" + strconv.Quote(value)
	}
	return ""
}

func groupSearchRepo(value string, prs []PullRequest) string {
	if len(prs) > 0 && prs[0].Repository.NameWithOwner != "" {
		return prs[0].Repository.NameWithOwner
	}
	return value
}

// resolveGroupAuthorNames replaces author login bucket labels with their
// configured or plugin-provided display names. Resolution happens after
// bucketing so distinct GitHub accounts remain distinct even when they share a
// display name.
func resolveGroupAuthorNames(
	nodes []groupNode,
	keys []groupKey,
	resolver *AuthorResolver,
) {
	if len(keys) == 0 || resolver == nil {
		return
	}

	key, rest := keys[0], keys[1:]
	for i := range nodes {
		if key == groupAuthor && nodes[i].Value != groupNoneValue {
			login := nodes[i].Value
			display := buildAuthorModel(
				PullRequest{Author: Author{Login: login}},
				resolver,
			).Display
			if display != login {
				nodes[i].Value = display
				nodes[i].colorKey = login
			}
		}
		resolveGroupAuthorNames(nodes[i].Children, rest, resolver)
	}

	// Keep equal-count buckets naturally ordered by what the user sees.
	sortGroupNodes(nodes, key)
}

// sortGroupNodes orders buckets by descending count. State and draft buckets
// break ties by lifecycle (merged, open, closed; ready before draft),
// everything else with a natural comparison on the value so output is stable
// and readable.
func sortGroupNodes(nodes []groupNode, key groupKey) {
	slices.SortStableFunc(nodes, func(a, b groupNode) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		if key == groupState || key == groupDraft {
			if r := cmp.Compare(groupStateRank(a.Value), groupStateRank(b.Value)); r != 0 {
				return r
			}
		}
		return xstrings.CompareNatural(a.Value, b.Value)
	})
}

// groupStateOrder is the tie-break order for state and draft bucket values;
// unknown values sort after all of these.
var groupStateOrder = []string{valueMerged, valueOpen, valueReady, valueClosed, valueDraft}

// groupStateRank returns a value's position in groupStateOrder.
func groupStateRank(value string) int {
	if i := slices.Index(groupStateOrder, value); i >= 0 {
		return i
	}
	return len(groupStateOrder)
}

// groupColGap is the number of spaces between grid columns.
const groupColGap = 2

// Tree guide glyphs prefixed to nested buckets; dimmed on a TTY.
const (
	groupTreeBranch = "├─ "
	groupTreeLast   = "└─ "
	groupTreePipe   = "│  "
	groupTreeSpace  = "   "
)

// renderGroup renders a breakdown of prs bucketed by keys. When asJSON is true
// it emits the nested node tree as JSON. Otherwise each top-level group becomes
// a block - a header followed by its indented members, one per line - and the
// blocks stack in a single column when they fit the terminal height, or flow
// column-major (read down, then across) into as many side-by-side columns as
// fit termWidth when they would overflow it. Every bucket renders as
// "name (count)"; on a TTY the count is dim, headers are bold, and every
// bucket except a top-level header is coloured - state and draft buckets with
// prl's semantic state colours, the rest via entityColor (nil disables entity
// colour). On a TTY, every exactly representable bucket links to a GitHub
// search for its complete ancestor path.
func renderGroup(
	prs []PullRequest,
	keys []groupKey,
	asJSON, tty bool,
	entityColor func(string) color.Color,
	termWidth, termHeight int,
	authorResolver *AuthorResolver,
	params *SearchParams,
	stripRepoOwner bool,
) (string, error) {
	nodes := buildGroupNodesWithLinks(
		prs,
		keys,
		stripRepoOwner,
		params,
	)
	resolveGroupAuthorNames(nodes, keys, authorResolver)

	if asJSON {
		data, err := json.MarshalIndent(nodes, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshaling group-by JSON: %w", err)
		}
		return string(data), nil
	}

	nested := len(keys) > 1

	blocks := make([][]string, len(nodes))
	for i, n := range nodes {
		blocks[i] = groupBlock(n, keys, tty, entityColor)
	}

	return strings.Join(groupGrid(blocks, nested, termWidth, termHeight), nl), nil
}

// groupBlock flattens a top-level group into its display lines: a header
// followed by each descendant on its own line, deepest last, connected by
// tree guides (dimmed on a TTY).
func groupBlock(
	n groupNode,
	keys []groupKey,
	tty bool,
	entityColor func(string) color.Color,
) []string {
	var lines []string
	var walk func(node groupNode, depth int, prefix, childPrefix string)
	walk = func(node groupNode, depth int, prefix, childPrefix string) {
		guide := prefix
		if tty && guide != "" {
			guide = styleDim.Render(guide)
		}
		lines = append(lines, guide+groupNodeLabel(node, depth, keys, tty, entityColor))
		for i, c := range node.Children {
			if i == len(node.Children)-1 {
				walk(c, depth+1, childPrefix+groupTreeLast, childPrefix+groupTreeSpace)
			} else {
				walk(c, depth+1, childPrefix+groupTreeBranch, childPrefix+groupTreePipe)
			}
		}
	}
	walk(n, 0, "", "")
	return lines
}

// groupNodeLabel renders a bucket as "name (count)" (the caller prepends any
// tree guides), with the count dim. Header buckets (those with children)
// render the name bold. Every bucket except a top-level header is coloured:
// state and draft buckets use prl's semantic state colours, everything else
// the stable per-entity colour.
func groupNodeLabel(
	n groupNode,
	depth int,
	keys []groupKey,
	tty bool,
	entityColor func(string) color.Color,
) string {
	name := n.Value
	count := "(" + strconv.Itoa(n.Count) + ")"
	if !tty {
		return name + " " + count
	}

	var bucketColor color.Color
	if depth < len(keys) {
		colorKey := n.Value
		if n.colorKey != "" {
			colorKey = n.colorKey
		}
		bucketColor = groupBucketColor(keys[depth], colorKey, entityColor)
	}
	if len(n.Children) > 0 {
		style := styleText.Bold(true)
		if depth > 0 && bucketColor != nil { // top-level headers stay plain
			style = lg.NewStyle().Bold(true).Foreground(bucketColor)
		}
		name = style.Render(name)
	} else {
		name = styleGroupName(name, bucketColor)
	}
	count = styleDim.Render(count)
	if n.url != "" {
		name = xansi.Force().Hyperlink(n.url, name)
	}
	return name + " " + count
}

// groupBucketColor resolves the colour for a bucket value: prl's semantic
// state colours for state and draft keys, the stable per-entity colour for
// everything else.
func groupBucketColor(
	key groupKey,
	value string,
	entityColor func(string) color.Color,
) color.Color {
	switch key {
	case groupState, groupDraft:
		return groupStateColor(value)
	case groupAuthor, groupRepo, groupOwner, groupLabel:
		fallthrough
	default:
		if entityColor != nil {
			return entityColor(value)
		}
		return nil
	}
}

// groupStateColor maps a state or draft bucket value to the colour prl uses
// for that state elsewhere (see prMergeStyle).
func groupStateColor(value string) color.Color {
	switch value {
	case valueMerged:
		return colorMerged
	case valueClosed:
		return colorRed
	case valueOpen, valueReady:
		return colorGreen
	case valueDraft:
		return colorDraft
	default:
		return nil
	}
}

// styleGroupName renders a bucket name in its resolved colour.
func styleGroupName(name string, c color.Color) string {
	if c != nil {
		return lg.NewStyle().Foreground(c).Render(name)
	}
	return name
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
			return groupJoin(groupBalance(blocks, sep, limit, termWidth, cols))
		}
	}
}

// groupBalance re-cuts blocks using the smallest height limit that still
// yields no more columns than the given split, evening out column heights so
// a trailing block doesn't tuck under an earlier column that happens to have
// room beneath it. Keeps the original split when no shorter cut fits the
// terminal width.
func groupBalance(blocks [][]string, sep, limit, termWidth int, cols [][]string) [][]string {
	if len(cols) <= 1 {
		return cols
	}
	for h := 1; h <= limit; h++ {
		balanced := groupSplit(blocks, sep, h)
		if len(balanced) <= len(cols) &&
			(termWidth <= 0 || groupGridWidth(balanced) <= termWidth) {
			return balanced
		}
	}
	return cols
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
