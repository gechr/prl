package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

type timelineActors struct {
	closed map[string]string
	merged map[string]string
}

// applyFilters applies local filters (bots, drift) and sorts PRs.
func applyFilters(cli *CLI, prs []PullRequest) ([]PullRequest, error) {
	if cli.NoBot {
		prs = filterBots(prs)
	}
	if cli.Drift != "" {
		op, threshold, err := parseDrift(cli.Drift)
		if err != nil {
			return nil, fmt.Errorf("invalid drift: %w", err)
		}
		prs = filterByDrift(prs, op, threshold)
	}
	// Table mode defaults to updated sort (name sort is not supported server-side).
	// Only override when --sort was not explicitly provided.
	sf := cli.SortField()
	if cli.OutputFormat() == OutputTable && sf == SortName && !cli.SortExplicit() {
		sf = SortUpdated
	}
	sortPRs(prs, sf)
	return prs, nil
}

// filterBots removes PRs authored by bot accounts.
func filterBots(prs []PullRequest) []PullRequest {
	result := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if !strings.HasSuffix(strings.ToLower(pr.Author.Login), BotSuffix) {
			result = append(result, pr)
		} else {
			clog.Debug().
				Str("author", pr.Author.Login).
				Link("pr", pr.URL, pr.Ref()).
				Msg("Filtered bot")
		}
	}
	if filtered := len(prs) - len(result); filtered > 0 {
		clog.Debug().Int("filtered", filtered).Msg("Bot filter applied")
	}
	return result
}

// filterByDrift filters PRs by the time gap between createdAt and updatedAt.
func filterByDrift(prs []PullRequest, op string, threshold int64) []PullRequest {
	result := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		drift := int64(pr.UpdatedAt.Sub(pr.CreatedAt).Seconds())
		if drift < 0 {
			drift = -drift
		}

		var match bool
		switch op {
		case "<=":
			match = drift <= threshold
		case "<":
			match = drift < threshold
		case ">=":
			match = drift >= threshold
		case ">":
			match = drift > threshold
		case "=", "==":
			match = drift == threshold
		default:
			match = drift >= threshold
		}
		if match {
			result = append(result, pr)
		}
	}
	return result
}

func allAutomergeLoaded(prs []PullRequest) bool {
	for _, pr := range prs {
		if !pr.automergeLoaded {
			return false
		}
	}
	return len(prs) > 0
}

func fetchAutomergeStatus(gql *api.GraphQLClient, prs []PullRequest) (map[string]bool, error) {
	if len(prs) == 0 {
		return map[string]bool{}, nil
	}

	ids := make([]string, len(prs))
	for i, pr := range prs {
		ids[i] = pr.NodeID
	}

	var result struct {
		Nodes []struct {
			ID               string `json:"id"`
			AutomergeRequest *struct {
				EnabledAt string `json:"enabledAt"`
			} `json:"autoMergeRequest"`
		} `json:"nodes"`
	}

	if err := gql.Do(
		`query AutomergeStatus($ids: [ID!]!) {
			nodes(ids: $ids) {
				... on PullRequest {
					id
					autoMergeRequest { enabledAt }
				}
			}
		}`,
		map[string]any{"ids": ids},
		&result,
	); err != nil {
		return nil, fmt.Errorf("querying automerge status: %w", err)
	}

	enabled := make(map[string]bool, len(result.Nodes))
	for _, node := range result.Nodes {
		enabled[node.ID] = node.AutomergeRequest != nil
	}

	return enabled, nil
}

func applyAutomergeStatus(prs []PullRequest, enabled map[string]bool) {
	for i := range prs {
		prs[i].Automerge = enabled[prs[i].NodeID]
		prs[i].automergeLoaded = true
	}
}

func filterByAutomergeState(prs []PullRequest, wantEnabled bool) []PullRequest {
	filtered := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.Automerge == wantEnabled {
			filtered = append(filtered, pr)
		} else {
			clog.Debug().
				Link("pr", pr.URL, pr.Ref()).
				Bool("automerge", pr.Automerge).
				Msg("Filtered out")
		}
	}

	clog.Debug().
		Int("before", len(prs)).
		Int("after", len(filtered)).
		Bool("want-automerge", wantEnabled).
		Msg("Automerge filter applied")

	return filtered
}

func allReviewDecisionsLoaded(prs []PullRequest) bool {
	for _, pr := range prs {
		if !pr.reviewDecisionLoaded {
			return false
		}
	}
	return len(prs) > 0
}

func fetchReviewDecisions(gql *api.GraphQLClient, prs []PullRequest) (map[string]string, error) {
	if len(prs) == 0 {
		return map[string]string{}, nil
	}

	ids := make([]string, 0, len(prs))
	for _, pr := range prs {
		if pr.NodeID == "" {
			continue
		}
		ids = append(ids, pr.NodeID)
	}
	if len(ids) == 0 {
		return map[string]string{}, nil
	}

	var result struct {
		Nodes []struct {
			ID             string  `json:"id"`
			ReviewDecision *string `json:"reviewDecision"`
		} `json:"nodes"`
	}

	if err := gql.Do(
		`query ReviewDecisions($ids: [ID!]!) {
			nodes(ids: $ids) {
				... on PullRequest {
					id
					reviewDecision
				}
			}
		}`,
		map[string]any{"ids": ids},
		&result,
	); err != nil {
		return nil, fmt.Errorf("querying review decisions: %w", err)
	}

	decisions := make(map[string]string, len(ids))
	for _, node := range result.Nodes {
		if node.ReviewDecision != nil {
			decisions[node.ID] = *node.ReviewDecision
			continue
		}
		decisions[node.ID] = ""
	}
	for _, id := range ids {
		if _, ok := decisions[id]; !ok {
			decisions[id] = ""
		}
	}

	return decisions, nil
}

func applyReviewDecisions(prs []PullRequest, decisions map[string]string) {
	for i := range prs {
		decision, ok := decisions[prs[i].NodeID]
		if !ok {
			continue
		}
		prs[i].ReviewDecision = decision
		prs[i].reviewDecisionLoaded = true
	}
}

func ensureReviewDecisions(gql *api.GraphQLClient, prs []PullRequest) error {
	if len(prs) == 0 || allReviewDecisionsLoaded(prs) {
		return nil
	}

	missing := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.reviewDecisionLoaded || pr.NodeID == "" {
			continue
		}
		missing = append(missing, pr)
	}
	if len(missing) == 0 {
		return nil
	}

	decisions, err := fetchReviewDecisions(gql, missing)
	if err != nil {
		return err
	}
	applyReviewDecisions(prs, decisions)
	return nil
}

// filterByAutomerge queries automerge status via GraphQL and filters PRs.
// When wantEnabled is true, only PRs WITH automerge are kept (for --no-merge).
// When wantEnabled is false, only PRs WITHOUT automerge are kept (for --merge).
func filterByAutomerge(
	gql *api.GraphQLClient,
	prs []PullRequest,
	wantEnabled bool,
) ([]PullRequest, error) {
	if len(prs) == 0 {
		return prs, nil
	}
	if !allAutomergeLoaded(prs) {
		enabled, err := fetchAutomergeStatus(gql, prs)
		if err != nil {
			return nil, err
		}
		applyAutomergeStatus(prs, enabled)
	}
	return filterByAutomergeState(prs, wantEnabled), nil
}

// applyTimelineFilters applies --closed-by and --merged-by post-fetch filters.
// Errors are logged and swallowed so the TUI refresh path can call this without error handling.
func resolveTimelineLogins(rest *api.RESTClient, logins []string) (map[string]bool, error) {
	if len(logins) == 0 {
		return map[string]bool{}, nil
	}

	resolved := make(map[string]bool, len(logins))
	var currentLogin string
	var haveCurrentLogin bool

	for _, login := range logins {
		if strings.EqualFold(login, valueAtMe) {
			if !haveCurrentLogin {
				var err error
				currentLogin, err = getCurrentLogin(rest)
				if err != nil {
					return nil, fmt.Errorf("resolving %s: %w", valueAtMe, err)
				}
				haveCurrentLogin = true
			}
			resolved[strings.ToLower(currentLogin)] = true
			continue
		}
		resolved[strings.ToLower(login)] = true
	}

	return resolved, nil
}

type listMetadataRequest struct {
	automerge      bool
	mergeStatus    bool
	timelineClosed bool
	timelineMerged bool
}

type listTimelineNode struct {
	ID     string `json:"id"`
	Closed struct {
		Nodes []struct {
			Actor struct {
				Login string `json:"login"`
			} `json:"actor"`
		} `json:"nodes"`
	} `json:"closed"`
	Merged struct {
		Nodes []struct {
			Actor struct {
				Login string `json:"login"`
			} `json:"actor"`
		} `json:"nodes"`
	} `json:"merged"`
}

type listAutomergeNode struct {
	ID               string `json:"id"`
	AutomergeRequest *struct {
		EnabledAt string `json:"enabledAt"`
	} `json:"autoMergeRequest"`
}

type listMergeStatusNode struct {
	ID               string  `json:"id"`
	ReviewDecision   *string `json:"reviewDecision"`
	AutomergeRequest *struct {
		EnabledAt string `json:"enabledAt"`
	} `json:"autoMergeRequest"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func collectPRNodeIDs(prs []PullRequest) []string {
	ids := make([]string, 0, len(prs))
	for _, pr := range prs {
		if pr.NodeID == "" {
			continue
		}
		ids = append(ids, pr.NodeID)
	}
	return ids
}

func collectMergeStatusNodeIDs(prs []PullRequest) []string {
	openIDs := make([]string, 0, len(prs))
	for _, pr := range prs {
		if pr.State != valueOpen || pr.NodeID == "" {
			continue
		}
		openIDs = append(openIDs, pr.NodeID)
	}
	if len(openIDs) <= maxEnrichCount {
		return openIDs
	}

	clog.Debug().
		Int("open", len(openIDs)).
		Int("max", maxEnrichCount).
		Msg("Enriching most recent PRs only, too expensive")

	return openIDs[len(openIDs)-maxEnrichCount:]
}

func resolveMergeStatus(ciState string, reviewDecision *string) MergeStatus {
	switch {
	case ciState == valueCIFailure || ciState == valueCIError:
		return MergeStatusCIFailed
	case ciState == valueCIPending || ciState == valueCIExpected:
		return MergeStatusCIPending
	case ciState == valueCISuccess &&
		reviewDecision != nil &&
		*reviewDecision == valueReviewApproved:
		return MergeStatusReady
	default:
		return MergeStatusBlocked
	}
}

func applyMergeStatusResult(
	prs []PullRequest,
	openIdx map[string][]int,
	nodeID string,
	ciState string,
	reviewDecision *string,
	automergeEnabled bool,
) {
	indices, ok := openIdx[nodeID]
	if !ok {
		return
	}

	review := ""
	if reviewDecision != nil {
		review = *reviewDecision
	}
	status := resolveMergeStatus(ciState, reviewDecision)
	for _, idx := range indices {
		prs[idx].MergeStatus = status
		prs[idx].Automerge = automergeEnabled
		prs[idx].automergeLoaded = true
		prs[idx].ReviewDecision = review
		prs[idx].reviewDecisionLoaded = true
	}
}

func applyListAutomergeNodes(prs []PullRequest, ids []string, nodes []listAutomergeNode) {
	if len(ids) == 0 {
		return
	}

	enabled := make(map[string]bool, len(ids))
	for _, id := range ids {
		enabled[id] = false
	}
	for _, node := range nodes {
		enabled[node.ID] = node.AutomergeRequest != nil
	}
	for i := range prs {
		automerge, ok := enabled[prs[i].NodeID]
		if !ok {
			continue
		}
		prs[i].Automerge = automerge
		prs[i].automergeLoaded = true
	}
}

func applyListMergeStatusNodes(prs []PullRequest, nodes []listMergeStatusNode) {
	if len(nodes) == 0 {
		return
	}

	openIdx := make(map[string][]int)
	for i := range prs {
		if prs[i].State != valueOpen || prs[i].NodeID == "" {
			continue
		}
		openIdx[prs[i].NodeID] = append(openIdx[prs[i].NodeID], i)
	}

	for _, node := range nodes {
		var ciState string
		if len(node.Commits.Nodes) > 0 {
			if rollup := node.Commits.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
				ciState = rollup.State
			}
		}
		applyMergeStatusResult(
			prs,
			openIdx,
			node.ID,
			ciState,
			node.ReviewDecision,
			node.AutomergeRequest != nil,
		)
	}
}

func timelineActorsFromNodes(nodes []listTimelineNode) timelineActors {
	actors := timelineActors{
		closed: make(map[string]string, len(nodes)),
		merged: make(map[string]string, len(nodes)),
	}
	for _, node := range nodes {
		if len(node.Closed.Nodes) > 0 {
			actors.closed[node.ID] = strings.ToLower(node.Closed.Nodes[0].Actor.Login)
		}
		if len(node.Merged.Nodes) > 0 {
			actors.merged[node.ID] = strings.ToLower(node.Merged.Nodes[0].Actor.Login)
		}
	}
	return actors
}

// hydrateListMetadata batches the list-view GraphQL lookups needed for
// automerge filtering, timeline filtering, and merge-status enrichment.
func hydrateListMetadata(
	gql *api.GraphQLClient,
	prs []PullRequest,
	req listMetadataRequest,
) (timelineActors, error) {
	if len(prs) == 0 {
		return timelineActors{
			closed: map[string]string{},
			merged: map[string]string{},
		}, nil
	}

	timelineIDs := []string{}
	if req.timelineClosed || req.timelineMerged {
		timelineIDs = collectPRNodeIDs(prs)
	}

	mergeIDs := []string{}
	if req.mergeStatus {
		mergeIDs = collectMergeStatusNodeIDs(prs)
	}
	mergeIDSet := make(map[string]bool, len(mergeIDs))
	for _, id := range mergeIDs {
		mergeIDSet[id] = true
	}

	automergeIDs := []string{}
	if req.automerge {
		for _, pr := range prs {
			if pr.NodeID == "" || mergeIDSet[pr.NodeID] {
				continue
			}
			automergeIDs = append(automergeIDs, pr.NodeID)
		}
	}

	var (
		queryDefs  []string
		queryRoots []string
		variables  = make(map[string]any)
	)

	if len(timelineIDs) > 0 {
		queryDefs = append(queryDefs, "$timelineIDs: [ID!]!")
		var fields []string
		if req.timelineClosed {
			fields = append(fields, `closed: timelineItems(itemTypes: [CLOSED_EVENT], last: 1) {
				nodes {
					... on ClosedEvent {
						actor { login }
					}
				}
			}`)
		}
		if req.timelineMerged {
			fields = append(fields, `merged: timelineItems(itemTypes: [MERGED_EVENT], last: 1) {
				nodes {
					... on MergedEvent {
						actor { login }
					}
				}
			}`)
		}
		queryRoots = append(
			queryRoots,
			fmt.Sprintf(`timelineNodes: nodes(ids: $timelineIDs) {
				... on PullRequest {
					id
					%s
				}
			}`, strings.Join(fields, nl)),
		)
		variables["timelineIDs"] = timelineIDs
	}

	if len(automergeIDs) > 0 {
		queryDefs = append(queryDefs, "$automergeIDs: [ID!]!")
		queryRoots = append(queryRoots, `automergeNodes: nodes(ids: $automergeIDs) {
			... on PullRequest {
				id
				autoMergeRequest { enabledAt }
			}
		}`)
		variables["automergeIDs"] = automergeIDs
	}

	if len(mergeIDs) > 0 {
		queryDefs = append(queryDefs, "$mergeIDs: [ID!]!")
		queryRoots = append(queryRoots, `mergeNodes: nodes(ids: $mergeIDs) {
			... on PullRequest {
				id
				reviewDecision
				autoMergeRequest { enabledAt }
				commits(last: 1) {
					nodes {
						commit {
							statusCheckRollup { state }
						}
					}
				}
			}
		}`)
		variables["mergeIDs"] = mergeIDs
	}

	if len(queryRoots) == 0 {
		return timelineActors{
			closed: map[string]string{},
			merged: map[string]string{},
		}, nil
	}

	var result struct {
		TimelineNodes  []listTimelineNode    `json:"timelineNodes"`
		AutomergeNodes []listAutomergeNode   `json:"automergeNodes"`
		MergeNodes     []listMergeStatusNode `json:"mergeNodes"`
	}

	query := fmt.Sprintf(
		`query ListMetadata(%s) {
			%s
		}`,
		strings.Join(queryDefs, ", "),
		strings.Join(queryRoots, nl),
	)
	if err := gql.Do(query, variables, &result); err != nil {
		return timelineActors{}, fmt.Errorf("querying list metadata: %w", err)
	}

	applyListAutomergeNodes(prs, automergeIDs, result.AutomergeNodes)
	applyListMergeStatusNodes(prs, result.MergeNodes)

	return timelineActorsFromNodes(result.TimelineNodes), nil
}

func filterByTimelineActorsLoaded(
	prs []PullRequest,
	closedAllowed map[string]bool,
	mergedAllowed map[string]bool,
	actors timelineActors,
) []PullRequest {
	filtered := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if len(closedAllowed) > 0 && !closedAllowed[actors.closed[pr.NodeID]] {
			clog.Debug().
				Link("pr", pr.URL, pr.Ref()).
				Str("actor", actors.closed[pr.NodeID]).
				Msg("Filtered by closed-by")
			continue
		}
		if len(mergedAllowed) > 0 && !mergedAllowed[actors.merged[pr.NodeID]] {
			clog.Debug().
				Link("pr", pr.URL, pr.Ref()).
				Str("actor", actors.merged[pr.NodeID]).
				Msg("Filtered by merged-by")
			continue
		}
		filtered = append(filtered, pr)
	}
	return filtered
}

// filterByTimelineActors batches closed-by and merged-by actor lookups into one GraphQL query.
func filterByTimelineActors(
	rest *api.RESTClient,
	gql *api.GraphQLClient,
	prs []PullRequest,
	closedLogins []string,
	mergedLogins []string,
) ([]PullRequest, error) {
	if len(prs) == 0 || (len(closedLogins) == 0 && len(mergedLogins) == 0) {
		return prs, nil
	}

	closedAllowed, err := resolveTimelineLogins(rest, closedLogins)
	if err != nil {
		return nil, err
	}
	mergedAllowed, err := resolveTimelineLogins(rest, mergedLogins)
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(prs))
	for i, pr := range prs {
		ids[i] = pr.NodeID
	}

	var result struct {
		Nodes []struct {
			ID     string `json:"id"`
			Closed struct {
				Nodes []struct {
					Actor struct {
						Login string `json:"login"`
					} `json:"actor"`
				} `json:"nodes"`
			} `json:"closed"`
			Merged struct {
				Nodes []struct {
					Actor struct {
						Login string `json:"login"`
					} `json:"actor"`
				} `json:"nodes"`
			} `json:"merged"`
		} `json:"nodes"`
	}

	var fields []string
	if len(closedAllowed) > 0 {
		fields = append(fields, `closed: timelineItems(itemTypes: [CLOSED_EVENT], last: 1) {
			nodes {
				... on ClosedEvent {
					actor { login }
				}
			}
		}`)
	}
	if len(mergedAllowed) > 0 {
		fields = append(fields, `merged: timelineItems(itemTypes: [MERGED_EVENT], last: 1) {
			nodes {
				... on MergedEvent {
					actor { login }
				}
			}
		}`)
	}

	query := fmt.Sprintf(
		`query TimelineActors($ids: [ID!]!) {
			nodes(ids: $ids) {
				... on PullRequest {
					id
					%s
				}
			}
		}`, strings.Join(fields, nl))

	if err := gql.Do(query, map[string]any{"ids": ids}, &result); err != nil {
		return nil, fmt.Errorf("querying timeline actors: %w", err)
	}

	actors := timelineActors{
		closed: make(map[string]string, len(result.Nodes)),
		merged: make(map[string]string, len(result.Nodes)),
	}
	for _, node := range result.Nodes {
		if len(node.Closed.Nodes) > 0 {
			actors.closed[node.ID] = strings.ToLower(node.Closed.Nodes[0].Actor.Login)
		}
		if len(node.Merged.Nodes) > 0 {
			actors.merged[node.ID] = strings.ToLower(node.Merged.Nodes[0].Actor.Login)
		}
	}

	filtered := filterByTimelineActorsLoaded(prs, closedAllowed, mergedAllowed, actors)
	clog.Debug().
		Int("before", len(prs)).
		Int("after", len(filtered)).
		Bool("closed-by", len(closedAllowed) > 0).
		Bool("merged-by", len(mergedAllowed) > 0).
		Msg("Timeline filters applied")

	return filtered, nil
}

// enrichAutomerge queries automerge status via GraphQL and sets Automerge on each PR.
func enrichAutomerge(gql *api.GraphQLClient, prs []PullRequest) error {
	if len(prs) == 0 || allAutomergeLoaded(prs) {
		return nil
	}
	enabled, err := fetchAutomergeStatus(gql, prs)
	if err != nil {
		return err
	}
	applyAutomergeStatus(prs, enabled)
	return nil
}

// Maximum number of PRs to enrich with merge status via GraphQL.
const maxEnrichCount = 50

// enrichMergeStatus queries CI and review status via GraphQL and sets MergeStatus on each open PR.
func enrichMergeStatus(gql *api.GraphQLClient, prs []PullRequest) {
	// Collect indices of open PRs that need enrichment.
	var openIDs []string
	openIdx := make(map[string][]int) // nodeID -> indices in prs
	for i := range prs {
		if prs[i].State == valueOpen {
			openIDs = append(openIDs, prs[i].NodeID)
			openIdx[prs[i].NodeID] = append(openIdx[prs[i].NodeID], i)
		}
	}
	if len(openIDs) == 0 {
		return
	}
	if len(openIDs) > maxEnrichCount {
		clog.Debug().
			Int("open", len(openIDs)).
			Int("max", maxEnrichCount).
			Msg("Enriching most recent PRs only, too expensive")
		openIDs = openIDs[len(openIDs)-maxEnrichCount:]
	}

	var result struct {
		Nodes []struct {
			ID               string  `json:"id"`
			ReviewDecision   *string `json:"reviewDecision"`
			AutomergeRequest *struct {
				EnabledAt string `json:"enabledAt"`
			} `json:"autoMergeRequest"`
			Commits struct {
				Nodes []struct {
					Commit struct {
						StatusCheckRollup *struct {
							State string `json:"state"`
						} `json:"statusCheckRollup"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"commits"`
		} `json:"nodes"`
	}

	if err := gql.Do(
		`query MergeStatus($ids: [ID!]!) {
			nodes(ids: $ids) {
				... on PullRequest {
					id
					reviewDecision
					autoMergeRequest { enabledAt }
					commits(last: 1) {
						nodes {
							commit {
								statusCheckRollup { state }
							}
						}
					}
				}
			}
		}`,
		map[string]any{"ids": openIDs},
		&result,
	); err != nil {
		clog.Debug().Err(err).Msg("Failed to query merge status")
		return
	}

	for _, node := range result.Nodes {
		var ciState string
		if len(node.Commits.Nodes) > 0 {
			if rollup := node.Commits.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
				ciState = rollup.State
			}
		}
		applyMergeStatusResult(
			prs,
			openIdx,
			node.ID,
			ciState,
			node.ReviewDecision,
			node.AutomergeRequest != nil,
		)
	}
}

// filterByCI keeps only PRs whose enriched MergeStatus matches the given CI status.
// CISuccess matches PRs where CI passed (MergeStatusReady or MergeStatusBlocked).
// CIFailure matches MergeStatusCIFailed. CIPending matches MergeStatusCIPending.
func filterByCI(prs []PullRequest, ci CIStatus) []PullRequest {
	result := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		switch ci {
		case CISuccess:
			if pr.MergeStatus == MergeStatusReady || pr.MergeStatus == MergeStatusBlocked {
				result = append(result, pr)
			}
		case CIFailure:
			if pr.MergeStatus == MergeStatusCIFailed {
				result = append(result, pr)
			}
		case CIPending:
			if pr.MergeStatus == MergeStatusCIPending {
				result = append(result, pr)
			}
		case CINone:
			break
		}
	}
	return result
}

// filterReady keeps only PRs with MergeStatusReady (CI passing + approved).
func filterReady(prs []PullRequest) []PullRequest {
	result := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.MergeStatus == MergeStatusReady {
			result = append(result, pr)
		}
	}
	return result
}

// sortPRs sorts pull requests by the given field.
func sortPRs(prs []PullRequest, field SortField) {
	switch field {
	case SortName:
		sort.SliceStable(prs, func(i, j int) bool {
			return strings.ToLower(prs[i].Repository.Name) < strings.ToLower(prs[j].Repository.Name)
		})
	case SortCreated:
		sort.SliceStable(prs, func(i, j int) bool {
			return prs[i].CreatedAt.Before(prs[j].CreatedAt)
		})
	case SortUpdated:
		sort.SliceStable(prs, func(i, j int) bool {
			return prs[i].UpdatedAt.Before(prs[j].UpdatedAt)
		})
	}
}

// renderURLs outputs one URL per line.
func renderURLs(prs []PullRequest) string {
	lines := make([]string, 0, len(prs))
	for _, pr := range prs {
		lines = append(lines, pr.URL)
	}
	return strings.Join(lines, nl)
}

// renderBullets outputs "* <url>" per line.
func renderBullets(prs []PullRequest) string {
	lines := make([]string, 0, len(prs))
	for _, pr := range prs {
		lines = append(lines, "* "+pr.URL)
	}
	return strings.Join(lines, nl)
}

// renderRepos outputs unique repo names in alphabetical order.
func renderRepos(prs []PullRequest) string {
	seen := make(map[string]struct{})
	var names []string
	for _, pr := range prs {
		name := pr.Repository.Name
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, nl)
}

// renderJSON outputs pretty-printed sorted JSON.
func renderJSON(prs []PullRequest) (string, error) {
	data, err := json.MarshalIndent(prs, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling JSON: %w", err)
	}
	return string(data), nil
}
