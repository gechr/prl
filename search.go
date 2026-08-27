package main

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	xslices "github.com/gechr/x/slices"
)

// SearchParams holds the parameters for a GitHub search API call.
type SearchParams struct {
	Query      string
	Sort       string
	Order      string
	PerPage    int
	TotalLimit int
	queryTerms []searchQueryTerm
}

// searchQueryTerm retains the dimension that produced a generated qualifier so
// group links can replace broad scopes without parsing the rendered query.
type searchQueryTerm struct {
	query   string
	key     groupKey
	grouped bool
	repo    string
}

func (p *SearchParams) groupSearchQuery(path []groupSearchFilter) string {
	return p.groupSearchQueryScoped(path, false)
}

func (p *SearchParams) groupSearchQueryScoped(
	path []groupSearchFilter,
	repoScoped bool,
) string {
	replaced := make(map[groupKey]bool, len(path))
	var pathQueries []string
	for _, filter := range path {
		if filter.key != groupLabel {
			replaced[filter.key] = true
		}
		if !repoScoped || (filter.key != groupRepo && filter.key != groupOwner) {
			pathQueries = append(pathQueries, filter.query)
		}
	}
	if repoScoped {
		replaced[groupRepo] = true
		replaced[groupOwner] = true
	}

	terms := p.queryTerms
	if len(terms) == 0 && p.Query != "" {
		terms = []searchQueryTerm{{query: p.Query}}
	}
	kept := xslices.Filter(terms, func(term searchQueryTerm) bool {
		return !term.grouped || !replaced[term.key]
	})

	// Insert exact path qualifiers before any remaining generated OR scope.
	// This preserves the query shape GitHub already accepts from buildSearchQuery.
	insertAt := len(kept)
	for i, term := range kept {
		if strings.HasPrefix(term.query, "(") && strings.Contains(term.query, " OR ") {
			insertAt = i
			break
		}
	}
	queries := make([]string, 0, len(kept)+len(pathQueries))
	queries = append(queries, xslices.Map(kept[:insertAt], func(term searchQueryTerm) string {
		return term.query
	})...)
	queries = append(queries, pathQueries...)
	queries = append(queries, xslices.Map(kept[insertAt:], func(term searchQueryTerm) string {
		return term.query
	})...)
	return strings.Join(queries, " ")
}

// buildSearchQuery constructs a GitHub search query and parameters.
func buildSearchQuery(cli *CLI, cfg *Config) (*SearchParams, error) {
	var terms []searchQueryTerm
	add := func(queries ...string) {
		for _, query := range queries {
			if query != "" {
				terms = append(terms, searchQueryTerm{query: query})
			}
		}
	}
	addGroup := func(key groupKey, queries ...string) {
		for _, query := range queries {
			if query != "" {
				terms = append(terms, searchQueryTerm{
					query:   query,
					key:     key,
					grouped: true,
				})
			}
		}
	}

	add("is:pr")
	if !cli.Archived {
		add("archived:false")
	}

	state := cli.PRState()
	switch state {
	case StateOpen, StateReady:
		addGroup(groupState, "state:open")
	case StateClosed:
		addGroup(groupState, "state:closed")
	case StateMerged:
		addGroup(groupState, "is:merged")
	case StateAll:
		// no state filter
	}

	// Resolve owner values (strip "all") and keep exclusions separate from the
	// positive owner scope used to qualify shorthand repository names.
	ownerVals := filterAllValue(cli.Owner.Values)
	positiveOwners, negativeOwners := splitNegated(ownerVals)
	qualifiedOwner := ""
	if len(positiveOwners) == 1 {
		qualifiedOwner = positiveOwners[0]
	}

	// Repo filter
	repos := cli.Repo.Values
	var positiveRepos []string
	if len(repos) > 0 {
		qualify := func(repo string) string {
			if !strings.Contains(repo, "/") && qualifiedOwner != "" {
				repo = qualifiedOwner + "/" + repo
			}
			return repo
		}
		var negativeRepos []string
		positiveRepos, negativeRepos = splitNegated(repos)
		positiveRepos = xslices.Map(positiveRepos, qualify)
		negativeRepos = xslices.Map(negativeRepos, qualify)
		positiveRepos, negativeRepos = removeOpposingValues(positiveRepos, negativeRepos)
		if q := buildORQualifier("repo", positiveRepos); q != "" {
			addGroup(groupRepo, q)
			if len(positiveRepos) == 1 && strings.Contains(positiveRepos[0], "/") {
				terms[len(terms)-1].repo = positiveRepos[0]
			}
		}
		for _, r := range negativeRepos {
			addGroup(groupRepo, "-repo:"+r)
		}
	}

	// Owner filter: skip only when positive repo qualifiers already scope the
	// search - a repo filter that is purely exclusion (e.g. -R !owner/repo)
	// still needs the owner qualifier, or the search is unbounded.
	if len(positiveRepos) == 0 {
		if q := buildOwnerQualifier(positiveOwners); q != "" {
			addGroup(groupOwner, q)
		}
	}
	// Unlike a positive owner, an exclusion remains meaningful when explicit
	// positive repositories are present (and may deliberately make the query empty).
	addGroup(groupOwner, buildExcludedOwnerQualifiers(negativeOwners)...)

	// Ignored owners (config-only, always applied)
	add(buildExcludedOwnerQualifiers(cfg.IgnoredOwners)...)

	// Date filters
	if cli.Created != "" {
		d, dErr := parseDate(cli.Created)
		if dErr != nil {
			return nil, fmt.Errorf("invalid --created value: %w", dErr)
		}
		add("created:" + d)
	}
	if cli.Updated != "" {
		d, dErr := parseDate(cli.Updated)
		if dErr != nil {
			return nil, fmt.Errorf("invalid --updated value: %w", dErr)
		}
		add("updated:" + d)
	}
	if cli.Merged != "" {
		d, dErr := parseDate(cli.Merged)
		if dErr != nil {
			return nil, fmt.Errorf("invalid --merged value: %w", dErr)
		}
		add("merged:" + d)
	}

	// Review filter - review:required only makes sense for open PRs (it means
	// "review required but not yet given"). For closed/merged PRs it filters
	// almost everything out, so skip it for non-open states.
	if cli.Review != "" {
		review := cli.Review
		if review == valueReviewFilterSelfRequired {
			review = valueReviewFilterRequired
		}
		if review != valueReviewFilterRequired || state == StateOpen || state == StateReady {
			add("review:" + review)
		}
	}

	var authorValues []string
	if cli.Author != nil {
		authorValues = cli.Author.Values
	}
	authorPositive, authorNegative := splitNegated(xslices.UniqueFold(filterAllValue(authorValues)))
	bots := discoverBotAuthors(cfg)

	// Commenter filter
	commenterVals := filterAllValue(cli.Commenter.Values)
	add(buildFilterQualifiers("commenter", commenterVals)...)

	// Involves filter
	involvesVals := filterAllValue(cli.Involves.Values)
	add(buildFilterQualifiers("involves", involvesVals)...)

	// Reviewed-by filter
	reviewedByVals := filterAllValue(cli.ReviewedBy.Values)
	add(buildFilterQualifiers("reviewed-by", reviewedByVals)...)

	// Review-requested: split into user and team while preserving negation.
	reqVals := filterAllValue(cli.ReviewRequested.Values)
	if len(reqVals) > 0 {
		var userReqs, teamReqs []string
		for _, v := range reqVals {
			prefix := ""
			if positive, negative := splitNegated([]string{v}); len(negative) > 0 {
				v = negative[0]
				prefix = "!"
			} else {
				v = positive[0]
			}
			if after, ok := strings.CutPrefix(v, "team:"); ok {
				teamReqs = append(teamReqs, prefix+after)
			} else {
				userReqs = append(userReqs, prefix+v)
			}
		}
		add(
			buildFilterQualifiers("user-review-requested", userReqs)...,
		)
		add(
			buildFilterQualifiers("team-review-requested", teamReqs)...,
		)
	}

	// Team filter: resolve members and merge with explicit authors. A negated team
	// (e.g. "!foo") excludes its members instead of restricting authors to them.
	if len(cli.Team.Values) > 0 {
		plug, err := discoverPlugin(cfg)
		if err != nil {
			return nil, err
		}
		positiveTeams, negativeTeams := splitNegated(cli.Team.Values)
		posMembers, err := resolveTeamMembers(plug, cfg, positiveTeams)
		if err != nil {
			return nil, err
		}
		negMembers, err := resolveTeamMembers(plug, cfg, negativeTeams)
		if err != nil {
			return nil, err
		}
		authorPositive = xslices.UniqueFold(append(authorPositive, posMembers...))
		authorNegative = xslices.UniqueFold(append(authorNegative, negMembers...))
	}
	for i, author := range authorPositive {
		authorPositive[i] = normalizeBotAuthorValue(author, bots)
	}
	for i, author := range authorNegative {
		authorNegative[i] = normalizeBotAuthorValue(author, bots)
	}
	authorPositive, authorNegative = removeOpposingValues(authorPositive, authorNegative)
	if q := buildORQualifier("author", authorPositive); q != "" {
		addGroup(groupAuthor, q)
	}
	for _, a := range authorNegative {
		addGroup(groupAuthor, "-author:"+a)
	}

	// Topic filter: resolve repos and add as repo OR filter
	if cli.Topic != "" {
		qualifiedRepos, err := resolveTopicReposForSearch(cli.Topic, positiveOwners, cfg)
		if err != nil {
			return nil, err
		}
		addGroup(groupRepo, buildORQualifier("repo", qualifiedRepos))
		if len(qualifiedRepos) == 1 && strings.Contains(qualifiedRepos[0], "/") {
			terms[len(terms)-1].repo = qualifiedRepos[0]
		}
	}

	// Draft filter
	if cli.Draft != nil {
		if *cli.Draft {
			addGroup(groupDraft, "draft:true")
		} else {
			addGroup(groupDraft, "draft:false")
		}
	}

	// Comments filter
	if cli.Comments != "" {
		add("comments:" + cli.Comments)
	}

	// Language filter
	if cli.Language != "" {
		add("language:" + cli.Language)
	}

	// Explicit filter values
	add(cli.Filter...)

	// Approve implicit filter: -review:approved when --approve is used and --review is NOT set
	if cli.Approve && cli.Review == "" {
		add("-review:approved")
		clog.Debug().Msg("--approve implied -review:approved filter")
	}

	// Unsubscribe implicit filters: default to --requested=@me and exclude own PRs.
	if cli.Unsubscribe {
		if len(reqVals) == 0 {
			add("user-review-requested:@me")
			clog.Debug().Msg("--unsubscribe implied --requested=@me")
		}
		addGroup(groupAuthor, "-author:@me")
		clog.Debug().Msg("--unsubscribe implied -author:@me filter")
	}

	// Draft implicit filters: skip PRs already in the target state.
	// mark-draft uses draft:false to find non-draft PRs that can be converted TO draft.
	// mark-ready uses draft:true to find draft PRs that can be marked as ready for review.
	// force-merge uses draft:false because draft PRs cannot be merged.
	if cli.MarkDraft || cli.ForceMerge {
		addGroup(groupDraft, "draft:false")
		if cli.MarkDraft {
			clog.Debug().Msg("--mark-draft implied --no-draft filter")
		} else {
			clog.Debug().Msg("--force-merge implied --no-draft filter")
		}
	}
	if cli.MarkReady {
		addGroup(groupDraft, "draft:true")
		clog.Debug().Msg("--mark-ready implied --draft filter")
	}

	// Match (only when there's a query string)
	query := cli.QueryString()
	if query != "" {
		add(query)
		if cli.Match != "" {
			add("in:" + cli.Match)
		}
	}

	// Sorting (API-level).
	// Order is always "desc" regardless of --reverse. The --reverse flag only
	// affects display order after results are fetched. We always ask the API for
	// descending order so that the most recent/relevant results are returned
	// first, which matters when results are truncated by the limit.
	sortField := ""
	order := valueDesc
	switch cli.SortField() {
	case SortCreated:
		sortField = valueCreated
	case SortUpdated:
		sortField = valueUpdated
	case SortName:
		// GitHub API has no name sort; use created as a proxy so the API
		// always returns newest results first. Without this, the API falls
		// back to "best match" relevance which returns an arbitrary subset
		// when results exceed the limit.
		sortField = valueCreated
	}

	limit := cli.LimitValue()
	perPage := min(limit, maxPerPage)

	return &SearchParams{
		Query: strings.Join(xslices.Map(terms, func(term searchQueryTerm) string {
			return term.query
		}), " "),
		Sort:       sortField,
		Order:      order,
		PerPage:    perPage,
		TotalLimit: limit,
		queryTerms: slices.Clone(terms),
	}, nil
}

func resolveTopicReposForSearch(topic string, ownerVals []string, cfg *Config) ([]string, error) {
	plug, err := discoverPlugin(cfg)
	if err != nil {
		return nil, err
	}

	repos, err := plug.ResolveTopic(topic)
	if err != nil {
		switch {
		case errors.Is(err, errNoPluginAvailable):
			return nil, fmt.Errorf("--topic requires a plugin (no prl-plugin-* binary found)")
		case errors.Is(err, errPluginNotImplemented):
			return nil, fmt.Errorf("--topic requires a plugin that implements topic resolution")
		}
		return nil, err
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("no repos found for topic %q", topic)
	}

	qualifiedRepos := make([]string, 0, len(repos))
	for _, repo := range repos {
		if len(ownerVals) == 1 {
			qualifiedRepos = append(qualifiedRepos, ownerVals[0]+"/"+repo)
			continue
		}
		qualifiedRepos = append(qualifiedRepos, repo)
	}
	return qualifiedRepos, nil
}

// shouldShowAuthor returns true if the author column should be shown in table mode.
func shouldShowAuthor(cli *CLI) bool {
	if len(cli.Team.Values) > 0 {
		return true
	}
	if len(cli.Author.Values) > 0 {
		for _, v := range cli.Author.Values {
			if strings.ToLower(v) == valueAll {
				return true
			}
		}
		if len(cli.Author.Values) > 1 {
			return true
		}
	}
	return false
}

// searchResponse matches the GitHub Search Issues API JSON response.
type searchResponse struct {
	Items      []searchItem `json:"items"`
	TotalCount int          `json:"total_count"`
}

type searchItem struct {
	CreatedAt   time.Time      `json:"created_at"`
	Draft       bool           `json:"draft"`
	HTMLURL     string         `json:"html_url"`
	Labels      []searchLabel  `json:"labels"`
	NodeID      string         `json:"node_id"`
	Number      int            `json:"number"`
	PullRequest searchPRDetail `json:"pull_request"`
	RepoURL     string         `json:"repository_url"`
	State       string         `json:"state"`
	Title       string         `json:"title"`
	UpdatedAt   time.Time      `json:"updated_at"`
	User        searchUser     `json:"user"`
}

type searchUser struct {
	Login string `json:"login"`
}

type searchLabel struct {
	Name string `json:"name"`
}

type searchPRDetail struct {
	MergedAt *time.Time `json:"merged_at"`
}

func toPullRequest(item searchItem) PullRequest {
	// Determine state: the API returns "open" or "closed"; we infer "merged"
	state := strings.ToLower(item.State)
	if state == valueClosed && item.PullRequest.MergedAt != nil {
		state = valueMerged
	}

	// Parse repository from repository_url: https://api.github.com/repos/{owner}/{repo}
	var repo Repository
	if idx := strings.Index(item.RepoURL, "/repos/"); idx >= 0 {
		nwo := item.RepoURL[idx+len("/repos/"):]
		repo.NameWithOwner = nwo
		if _, name, found := strings.CutLast(nwo, "/"); found {
			repo.Name = name
		} else {
			repo.Name = nwo
		}
	}

	labels := xslices.Map(item.Labels, func(l searchLabel) Label { return Label(l) })

	return PullRequest{
		Number:     item.Number,
		Title:      strings.TrimSpace(item.Title),
		TitleRaw:   item.Title,
		URL:        item.HTMLURL,
		State:      state,
		IsDraft:    item.Draft,
		NodeID:     item.NodeID,
		Repository: repo,
		Author:     Author{Login: item.User.Login},
		Labels:     labels,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
	}
}

// executeSearch queries the GitHub Search Issues API and returns parsed PRs.
func executeSearch(rest *api.RESTClient, params *SearchParams) ([]PullRequest, error) {
	var allPRs []PullRequest
	page := 1

	for len(allPRs) < params.TotalLimit {
		path := fmt.Sprintf(
			"search/issues?advanced_search=true&q=%s&per_page=%d&page=%d",
			url.QueryEscape(params.Query),
			params.PerPage,
			page,
		)
		if params.Sort != "" {
			path += "&sort=" + params.Sort + "&order=" + params.Order
		}

		var resp searchResponse
		if err := rest.Get(path, &resp); err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}

		if len(resp.Items) == 0 {
			break
		}

		for _, item := range resp.Items {
			if len(allPRs) >= params.TotalLimit {
				break
			}
			allPRs = append(allPRs, toPullRequest(item))
		}

		if len(resp.Items) < params.PerPage {
			break
		}
		page++
	}

	clog.Debug().Int("pages", page).Int("results", len(allPRs)).Msg("Search complete")

	return allPRs, nil
}

func executeListSearch(
	rest *api.RESTClient,
	getGQL func() (*api.GraphQLClient, error),
	params *SearchParams,
	preferGraphQL bool,
) ([]PullRequest, error) {
	if preferGraphQL {
		if prs, ok := tryExecuteListSearchGraphQL(getGQL, params); ok {
			return prs, nil
		}
	}
	return executeSearch(rest, params)
}

func tryExecuteListSearchGraphQL(
	getGQL func() (*api.GraphQLClient, error),
	params *SearchParams,
) ([]PullRequest, bool) {
	if getGQL == nil {
		return nil, false
	}
	gql, err := getGQL()
	if err != nil {
		clog.Debug().Err(err).Msg("GraphQL search unavailable")
		return nil, false
	}
	prs, err := executeSearchGraphQL(gql, params)
	if err != nil {
		clog.Warn().Err(err).Msg("GraphQL search failed, falling back to REST search")
		return nil, false
	}
	if len(prs) == 0 {
		// REST search is authoritative for list membership; the GraphQL path
		// is an enrichment fast path for non-empty result sets.
		clog.Debug().Msg("GraphQL search returned no results")
		return nil, false
	}
	return prs, true
}

type graphQLSearchResponse struct {
	Search graphQLSearchConnection `json:"search"`
}

type graphQLSearchConnection struct {
	Nodes    []graphQLSearchPRNode `json:"nodes"`
	PageInfo graphQLPageInfo       `json:"pageInfo"`
}

type graphQLPageInfo struct {
	EndCursor   string `json:"endCursor"`
	HasNextPage bool   `json:"hasNextPage"`
}

type graphQLSearchPRNode struct {
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	AutoMergeRequest *struct {
		EnabledAt string `json:"enabledAt"`
	} `json:"autoMergeRequest"`
	CreatedAt  time.Time `json:"createdAt"`
	HeadRefOID string    `json:"headRefOid"`
	ID         string    `json:"id"`
	IsDraft    bool      `json:"isDraft"`
	Labels     struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	MergedAt   *time.Time `json:"mergedAt"`
	Number     int        `json:"number"`
	Repository Repository `json:"repository"`
	State      string     `json:"state"`
	Title      string     `json:"title"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	URL        string     `json:"url"`
}

const graphQLVarFirst = "first"

// executeSearchGraphQL queries GitHub's GraphQL search endpoint and returns
// parsed PRs. It deliberately selects only cheap per-node fields: merge status
// and review decision are hydrated afterwards by hydrateListMetadata, which
// batches them into chunked node queries. Selecting them here as well made a
// full page of results expensive enough for GitHub to time the search out at
// higher --limit values.
func executeSearchGraphQL(gql *api.GraphQLClient, params *SearchParams) ([]PullRequest, error) {
	var allPRs []PullRequest
	var cursor *string
	query := graphQLSearchQuery(params)

	for len(allPRs) < params.TotalLimit {
		first := min(params.PerPage, params.TotalLimit-len(allPRs))
		var resp graphQLSearchResponse
		if err := gql.Do(
			`query SearchPullRequests($query: String!, $first: Int!, $after: String) {
				search(type: ISSUE, query: $query, first: $first, after: $after) {
					nodes {
						... on PullRequest {
							id
							number
							title
							url
							state
							isDraft
							createdAt
							updatedAt
							mergedAt
							headRefOid
							autoMergeRequest { enabledAt }
							author { login }
							repository { name nameWithOwner }
							labels(first: 100) { nodes { name } }
						}
					}
					pageInfo { hasNextPage endCursor }
				}
			}`,
			map[string]any{
				"query":         query,
				graphQLVarFirst: first,
				"after":         cursor,
			},
			&resp,
		); err != nil {
			return nil, fmt.Errorf("search failed: %w", err)
		}

		if len(resp.Search.Nodes) == 0 {
			break
		}
		for _, node := range resp.Search.Nodes {
			if len(allPRs) >= params.TotalLimit {
				break
			}
			allPRs = append(allPRs, toPullRequestGraphQL(node))
		}
		if !resp.Search.PageInfo.HasNextPage {
			break
		}
		cursor = &resp.Search.PageInfo.EndCursor
	}

	clog.Debug().Int("results", len(allPRs)).Msg("GraphQL search complete")

	return allPRs, nil
}

func graphQLSearchQuery(params *SearchParams) string {
	query := params.Query
	if params.Sort != "" {
		query += " sort:" + params.Sort + "-" + params.Order
	}
	return query
}

func toPullRequestGraphQL(node graphQLSearchPRNode) PullRequest {
	state := strings.ToLower(node.State)
	if state == valueMerged || (state == valueClosed && node.MergedAt != nil) {
		state = valueMerged
	}

	labels := make([]Label, len(node.Labels.Nodes))
	for i, l := range node.Labels.Nodes {
		labels[i] = Label{Name: l.Name}
	}

	author := ""
	if node.Author != nil {
		author = node.Author.Login
	}

	// Merge status and review decision are left unloaded here;
	// hydrateListMetadata fills them in for both the GraphQL and REST search
	// paths. Auto-merge stays because it is a plain scalar lookup, not one of
	// the per-node rollups that made a full page too expensive to return.
	return PullRequest{
		Automerge:  node.AutoMergeRequest != nil,
		Author:     Author{Login: author},
		CreatedAt:  node.CreatedAt,
		HeadSHA:    node.HeadRefOID,
		IsDraft:    node.IsDraft,
		Labels:     labels,
		NodeID:     node.ID,
		Number:     node.Number,
		Repository: node.Repository,
		State:      state,
		Title:      strings.TrimSpace(node.Title),
		TitleRaw:   node.Title,
		UpdatedAt:  node.UpdatedAt,
		URL:        node.URL,

		automergeLoaded: true,
	}
}

// executeCount queries the GitHub Search Issues API and returns the total result count.
// It fetches a single item to minimise data transfer.
func executeCount(rest *api.RESTClient, params *SearchParams) (int, error) {
	path := fmt.Sprintf(
		"search/issues?advanced_search=true&q=%s&per_page=1&page=1",
		url.QueryEscape(params.Query),
	)
	if params.Sort != "" {
		path += "&sort=" + params.Sort + "&order=" + params.Order
	}

	var resp searchResponse
	if err := rest.Get(path, &resp); err != nil {
		return 0, fmt.Errorf("search failed: %w", err)
	}

	return resp.TotalCount, nil
}

// executeWebSearch opens the GitHub search in the browser.
func executeWebSearch(params *SearchParams) error {
	return openBrowser(githubSearchURL(params.Query))
}

// githubSearchURL returns the GitHub pull-request search page for query.
func githubSearchURL(query string) string {
	return "https://github.com/search?q=" + url.QueryEscape(query) + "&type=pullrequests"
}

// githubRepoPullsURL returns a repository-local pull-request search page.
func githubRepoPullsURL(repo, query string) string {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return githubSearchURL(query)
	}
	return "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(name) +
		"/pulls?q=" + url.QueryEscape(query)
}

// buildDryRunOutput returns the search query string for dry-run display.
func (p *prl) buildDryRunOutput(params *SearchParams, cli *CLI) string {
	var parts []string
	parts = append(parts, p.theme.Bold.Render("query:")+" "+params.Query)
	if params.Sort != "" {
		parts = append(parts, p.theme.Bold.Render("sort:")+" "+params.Sort)
		parts = append(parts, p.theme.Bold.Render("order:")+" "+params.Order)
	}
	parts = append(parts, fmt.Sprintf("%s %d", p.theme.Bold.Render("limit:"), params.TotalLimit))
	if cli.Drift != "" {
		if op, threshold, err := parseDrift(cli.Drift); err == nil {
			parts = append(parts, p.theme.Bold.Render("drift:")+" "+formatDrift(op, threshold))
		}
	}
	if cli.Send {
		parts = append(parts, p.theme.Bold.Render("slack:")+" "+formatSlackDryRun(cli))
	}
	return strings.Join(parts, nl)
}

// formatSlackDryRun returns a human-readable summary of where --send will route.
func formatSlackDryRun(cli *CLI) string {
	if cli.SendTo != "" {
		return cli.SendTo + " (--send-to override)"
	}
	return "(via plugin)"
}

// filterAllValue removes "all" from a values slice (meaning "don't filter").
func filterAllValue(values []string) []string {
	var result []string
	for _, v := range values {
		if strings.ToLower(v) != valueAll {
			result = append(result, v)
		}
	}
	return result
}
