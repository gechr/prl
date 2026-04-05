package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

// SearchParams holds the parameters for a GitHub search API call.
type SearchParams struct {
	Query      string
	Sort       string
	Order      string
	PerPage    int
	TotalLimit int
}

// buildSearchQuery constructs a GitHub search query and parameters.
func buildSearchQuery(cli *CLI, cfg *Config) (*SearchParams, error) {
	var qualifiers []string
	qualifiers = append(qualifiers, "type:pr")
	if !cli.Archived {
		qualifiers = append(qualifiers, "archived:false")
	}

	state := cli.PRState()
	switch state {
	case StateOpen, StateReady:
		qualifiers = append(qualifiers, "state:open")
	case StateClosed:
		qualifiers = append(qualifiers, "state:closed")
	case StateMerged:
		qualifiers = append(qualifiers, "is:merged")
	case StateAll:
		// no state filter
	}

	// Resolve owner values (strip "all")
	ownerVals := filterAllValue(cli.Owner.Values)

	// Repo filter
	if cli.Repo != "" {
		repo := cli.Repo
		if !strings.Contains(repo, "/") && len(ownerVals) == 1 {
			repo = ownerVals[0] + "/" + repo
		}
		qualifiers = append(qualifiers, "repo:"+repo)
	}

	// Owner filter
	if cli.Repo == "" {
		if q := buildOwnerQualifier(ownerVals); q != "" {
			qualifiers = append(qualifiers, q)
		}
	}

	// Ignored owners (config-only, always applied)
	qualifiers = append(qualifiers, buildExcludedOwnerQualifiers(cfg.IgnoredOwners)...)

	// Date filters
	if cli.Created != "" {
		qualifiers = append(qualifiers, "created:"+parseDate(cli.Created))
	}
	if cli.Updated != "" {
		qualifiers = append(qualifiers, "updated:"+parseDate(cli.Updated))
	}
	if cli.Merged != "" {
		qualifiers = append(qualifiers, "merged:"+parseDate(cli.Merged))
	}

	// Review filter - review:required only makes sense for open PRs (it means
	// "review required but not yet given"). For closed/merged PRs it filters
	// almost everything out, so skip it for non-open states.
	if cli.Review != "" {
		if cli.Review != valueReviewFilterRequired || state == StateOpen || state == StateReady {
			qualifiers = append(qualifiers, "review:"+cli.Review)
		}
	}

	var authorValues []string
	if cli.Author != nil {
		authorValues = cli.Author.Values
	}
	authorFilters := deduplicate(filterAllValue(authorValues), true)

	// Commenter filter
	commenterVals := filterAllValue(cli.Commenter.Values)
	if q := buildORQualifier("commenter", commenterVals); q != "" {
		qualifiers = append(qualifiers, q)
	}

	// Involves filter
	involvesVals := filterAllValue(cli.Involves.Values)
	if q := buildORQualifier("involves", involvesVals); q != "" {
		qualifiers = append(qualifiers, q)
	}

	// Reviewed-by filter
	reviewedByVals := filterAllValue(cli.ReviewedBy.Values)
	if q := buildORQualifier("reviewed-by", reviewedByVals); q != "" {
		qualifiers = append(qualifiers, q)
	}

	// Review-requested: split into user and team
	reqVals := filterAllValue(cli.ReviewRequested.Values)
	if len(reqVals) > 0 {
		var userReqs, teamReqs []string
		for _, v := range reqVals {
			if after, ok := strings.CutPrefix(v, "team:"); ok {
				teamReqs = append(teamReqs, after)
			} else {
				userReqs = append(userReqs, v)
			}
		}
		if q := buildORQualifier("user-review-requested", userReqs); q != "" {
			qualifiers = append(qualifiers, q)
		}
		if q := buildORQualifier("team-review-requested", teamReqs); q != "" {
			qualifiers = append(qualifiers, q)
		}
	}

	// Team filter: resolve members and merge with explicit authors.
	if len(cli.Team.Values) > 0 {
		plug, err := discoverPlugin(cfg)
		if err != nil {
			return nil, err
		}
		var allMembers []string
		for _, team := range cli.Team.Values {
			members, err := plug.ResolveTeam(team, cfg)
			if err != nil {
				return nil, fmt.Errorf("resolving team %q: %w", team, err)
			}
			if len(members) == 0 {
				return nil, fmt.Errorf("no members found for team %q", team)
			}
			allMembers = append(allMembers, members...)
		}
		authorFilters = deduplicate(append(authorFilters, allMembers...), true)
	}
	if q := buildORQualifier("author", authorFilters); q != "" {
		qualifiers = append(qualifiers, q)
	}

	// Topic filter: resolve repos and add as repo OR filter
	if cli.Topic != "" {
		qualifiedRepos, err := resolveTopicReposForSearch(cli.Topic, ownerVals, cfg)
		if err != nil {
			return nil, err
		}
		qualifiers = append(qualifiers, buildORQualifier("repo", qualifiedRepos))
	}

	// Draft filter
	if cli.Draft != nil {
		if *cli.Draft {
			qualifiers = append(qualifiers, "draft:true")
		} else {
			qualifiers = append(qualifiers, "draft:false")
		}
	}

	// Comments filter
	if cli.Comments != "" {
		qualifiers = append(qualifiers, "comments:"+cli.Comments)
	}

	// Language filter
	if cli.Language != "" {
		qualifiers = append(qualifiers, "language:"+cli.Language)
	}

	// Explicit filter values
	qualifiers = append(qualifiers, cli.Filter...)

	// Approve implicit filter: -review:approved when --approve is used and --review is NOT set
	if cli.Approve && cli.Review == "" {
		qualifiers = append(qualifiers, "-review:approved")
		clog.Debug().Msg("--approve implied -review:approved filter")
	}

	// Unsubscribe implicit filters: default to --requested=@me and exclude own PRs.
	if cli.Unsubscribe {
		if len(reqVals) == 0 {
			qualifiers = append(qualifiers, "user-review-requested:@me")
			clog.Debug().Msg("--unsubscribe implied --requested=@me")
		}
		qualifiers = append(qualifiers, "-author:@me")
		clog.Debug().Msg("--unsubscribe implied -author:@me filter")
	}

	// Draft implicit filters: skip PRs already in the target state.
	// mark-draft uses draft:false to find non-draft PRs that can be converted TO draft.
	// mark-ready uses draft:true to find draft PRs that can be marked as ready for review.
	// force-merge uses draft:false because draft PRs cannot be merged.
	if cli.MarkDraft || cli.ForceMerge {
		qualifiers = append(qualifiers, "draft:false")
		if cli.MarkDraft {
			clog.Debug().Msg("--mark-draft implied --no-draft filter")
		} else {
			clog.Debug().Msg("--force-merge implied --no-draft filter")
		}
	}
	if cli.MarkReady {
		qualifiers = append(qualifiers, "draft:true")
		clog.Debug().Msg("--mark-ready implied --draft filter")
	}

	// Match (only when there's a query string)
	query := cli.QueryString()
	if query != "" {
		qualifiers = append(qualifiers, query)
		if cli.Match != "" {
			qualifiers = append(qualifiers, "in:"+cli.Match)
		}
	}

	// Sorting (API-level).
	// Order is always "desc" regardless of --reverse. The --reverse flag only
	// affects display order after results are fetched. We always ask the API for
	// descending order so that the most recent/relevant results are returned
	// first, which matters when results are truncated by the limit.
	sortField := ""
	order := "desc"
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
		Query:      strings.Join(qualifiers, " "),
		Sort:       sortField,
		Order:      order,
		PerPage:    perPage,
		TotalLimit: limit,
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
	if state == "closed" && item.PullRequest.MergedAt != nil {
		state = valueMerged
	}

	// Parse repository from repository_url: https://api.github.com/repos/{owner}/{repo}
	var repo Repository
	if idx := strings.Index(item.RepoURL, "/repos/"); idx >= 0 {
		nwo := item.RepoURL[idx+len("/repos/"):]
		repo.NameWithOwner = nwo
		if slashIdx := strings.LastIndex(nwo, "/"); slashIdx >= 0 {
			repo.Name = nwo[slashIdx+1:]
		} else {
			repo.Name = nwo
		}
	}

	labels := make([]Label, len(item.Labels))
	for i, l := range item.Labels {
		labels[i] = Label(l)
	}

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
	u := "https://github.com/search?q=" + url.QueryEscape(params.Query) + "&type=pullrequests"
	return openBrowser(u)
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
