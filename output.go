package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	xslices "github.com/gechr/x/slices"
)

type timelineActors struct {
	closed map[string]string
	merged map[string]string
}

func newTimelineActors() timelineActors {
	return timelineActors{
		closed: map[string]string{},
		merged: map[string]string{},
	}
}

type listMetadataCacheKey struct {
	nodeID    string
	updatedAt time.Time
}

type listMetadataCacheEntry struct {
	headSHA              string
	mergeStatus          MergeStatus
	mergeStatusLoaded    bool
	automerge            bool
	automergeLoaded      bool
	reviewDecision       string
	reviewLoaded         bool
	viewerApproved       bool
	viewerApprovalLoaded bool
	closedActor          string
	closedActorLoaded    bool
	mergedActor          string
	mergedActorLoaded    bool
}

type listMetadataCache struct {
	mu      sync.Mutex
	entries map[listMetadataCacheKey]listMetadataCacheEntry
}

func newListMetadataCache() *listMetadataCache {
	return &listMetadataCache{entries: make(map[listMetadataCacheKey]listMetadataCacheEntry)}
}

func listMetadataKey(pr PullRequest) (listMetadataCacheKey, bool) {
	if pr.NodeID == "" {
		return listMetadataCacheKey{}, false
	}
	return listMetadataCacheKey{nodeID: pr.NodeID, updatedAt: pr.UpdatedAt}, true
}

func (c *listMetadataCache) apply(
	pr *PullRequest,
	req listMetadataRequest,
	actors *timelineActors,
) bool {
	if c == nil || pr == nil {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key, ok := listMetadataKey(*pr)
	if !ok {
		return false
	}
	entry, ok := c.entries[key]
	if !ok {
		return false
	}

	if req.timelineClosed {
		if !entry.closedActorLoaded {
			return false
		}
		actors.closed[pr.NodeID] = entry.closedActor
	}
	if req.timelineMerged {
		if !entry.mergedActorLoaded {
			return false
		}
		actors.merged[pr.NodeID] = entry.mergedActor
	}
	if req.mergeStatus && pr.State == valueOpen {
		if !entry.mergeStatusLoaded || !entry.reviewLoaded {
			return false
		}
		pr.MergeStatus = entry.mergeStatus
		pr.ReviewDecision = entry.reviewDecision
		pr.reviewDecisionLoaded = true
		if req.automerge {
			if !entry.automergeLoaded {
				return false
			}
			pr.Automerge = entry.automerge
			pr.automergeLoaded = true
		}
	}
	if req.viewerApproval {
		if !entry.viewerApprovalLoaded {
			return false
		}
		pr.viewerApproved = entry.viewerApproved
		pr.viewerApprovalLoaded = true
	}
	if req.automerge && (!req.mergeStatus || pr.State != valueOpen) {
		if !entry.automergeLoaded {
			return false
		}
		pr.Automerge = entry.automerge
		pr.automergeLoaded = true
	}
	return true
}

func (c *listMetadataCache) pendingHeadChecks(prs []PullRequest) []string {
	if c == nil || len(prs) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	ids := make([]string, 0, len(prs))
	for _, pr := range prs {
		if pr.State != valueOpen || pr.NodeID == "" {
			continue
		}
		key, ok := listMetadataKey(pr)
		if !ok {
			continue
		}
		entry, ok := c.entries[key]
		if !ok || entry.headSHA == "" {
			continue
		}
		ids = append(ids, pr.NodeID)
	}
	return ids
}

func (c *listMetadataCache) invalidateHeadMismatches(
	prs []PullRequest,
	heads map[string]string,
) bool {
	if c == nil || len(prs) == 0 || len(heads) == 0 {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	changed := false
	for _, pr := range prs {
		if pr.State != valueOpen || pr.NodeID == "" {
			continue
		}
		key, ok := listMetadataKey(pr)
		if !ok {
			continue
		}
		entry, ok := c.entries[key]
		if !ok || entry.headSHA == "" {
			continue
		}
		headSHA, ok := heads[pr.NodeID]
		if !ok || headSHA == "" || headSHA == entry.headSHA {
			continue
		}
		delete(c.entries, key)
		changed = true
	}
	return changed
}

func (c *listMetadataCache) store(
	pr PullRequest,
	req listMetadataRequest,
	actors timelineActors,
) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key, ok := listMetadataKey(pr)
	if !ok {
		return
	}

	entry := c.entries[key]
	if req.timelineClosed {
		entry.closedActor = actors.closed[pr.NodeID]
		entry.closedActorLoaded = true
	}
	if req.timelineMerged {
		entry.mergedActor = actors.merged[pr.NodeID]
		entry.mergedActorLoaded = true
	}
	if req.mergeStatus && pr.State == valueOpen {
		entry.headSHA = pr.HeadSHA
		entry.mergeStatus = pr.MergeStatus
		entry.mergeStatusLoaded = true
		entry.reviewDecision = pr.ReviewDecision
		entry.reviewLoaded = pr.reviewDecisionLoaded
	}
	if req.viewerApproval {
		entry.viewerApproved = pr.viewerApproved
		entry.viewerApprovalLoaded = pr.viewerApprovalLoaded
	}
	if req.automerge && (pr.State != valueOpen || !req.mergeStatus) {
		entry.automerge = pr.Automerge
		entry.automergeLoaded = pr.automergeLoaded
	}
	if req.automerge && req.mergeStatus && pr.State == valueOpen && pr.automergeLoaded {
		entry.automerge = pr.Automerge
		entry.automergeLoaded = true
	}

	c.entries[key] = entry
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

	ids := make([]string, 0, len(prs))
	for _, pr := range prs {
		if pr.NodeID == "" || pr.automergeLoaded {
			continue
		}
		ids = append(ids, pr.NodeID)
	}
	if len(ids) == 0 {
		return map[string]bool{}, nil
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
		map[string]any{keyIDs: ids},
		&result,
	); err != nil {
		return nil, fmt.Errorf("querying automerge status: %w", err)
	}

	enabled := make(map[string]bool, len(ids))
	for _, id := range ids {
		enabled[id] = false
	}
	for _, node := range result.Nodes {
		enabled[node.ID] = node.AutomergeRequest != nil
	}

	return enabled, nil
}

func applyAutomergeStatus(prs []PullRequest, enabled map[string]bool) {
	for i := range prs {
		automerge, ok := enabled[prs[i].NodeID]
		if !ok {
			continue
		}
		prs[i].Automerge = automerge
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
		map[string]any{keyIDs: ids},
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
	viewerApproval bool
}

func buildTimelineRoot(req listMetadataRequest) string {
	var fields []string
	if req.timelineClosed {
		fields = append(
			fields,
			`closed:timelineItems(itemTypes:[CLOSED_EVENT],last:1){nodes{... on ClosedEvent{actor{login}}}}`,
		)
	}
	if req.timelineMerged {
		fields = append(
			fields,
			`merged:timelineItems(itemTypes:[MERGED_EVENT],last:1){nodes{... on MergedEvent{actor{login}}}}`,
		)
	}
	return `timelineNodes:nodes(ids:$timelineIDs){... on PullRequest{id ` + strings.Join(
		fields,
		" ",
	) + `}}`
}

func buildAutomergeRoot() string {
	return `automergeNodes:nodes(ids:$automergeIDs){... on PullRequest{id autoMergeRequest{enabledAt}}}`
}

func buildMergeStatusRoot(includeAutomerge bool) string {
	fields := []string{
		"id",
		"headRefOid",
		"mergeStateStatus",
		"reviewDecision",
		"commits(last:1){nodes{commit{statusCheckRollup{state} checkSuites(first:50){totalCount nodes{conclusion checkRuns(first:1){totalCount}}}}}}",
	}
	if includeAutomerge {
		fields = append(fields, "autoMergeRequest{enabledAt}")
	}
	return `mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{` + strings.Join(fields, " ") + `}}`
}

func buildViewerApprovalRoot() string {
	return `viewer{login} viewerReviewNodes:nodes(ids:$viewerReviewIDs){... on PullRequest{id latestOpinionatedReviews(last:100){nodes{author{login} state}}}}`
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
	HeadRefOID       string  `json:"headRefOid"`
	MergeStateStatus string  `json:"mergeStateStatus"`
	ReviewDecision   *string `json:"reviewDecision"`
	AutomergeRequest *struct {
		EnabledAt string `json:"enabledAt"`
	} `json:"autoMergeRequest"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				CheckSuites       listCheckSuites `json:"checkSuites"`
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

type listViewerReviewNode struct {
	ID                       string `json:"id"`
	LatestOpinionatedReviews struct {
		Nodes []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"latestOpinionatedReviews"`
}

// listMetadataResult is the combined response of the list-metadata query.
type listMetadataResult struct {
	TimelineNodes  []listTimelineNode    `json:"timelineNodes"`
	AutomergeNodes []listAutomergeNode   `json:"automergeNodes"`
	MergeNodes     []listMergeStatusNode `json:"mergeNodes"`
	Viewer         struct {
		Login string `json:"login"`
	} `json:"viewer"`
	ViewerReviewNodes []listViewerReviewNode `json:"viewerReviewNodes"`
}

type listCheckSuites struct {
	TotalCount int              `json:"totalCount"`
	Nodes      []listCheckSuite `json:"nodes"`
}

type listCheckSuite struct {
	CheckRuns *struct {
		TotalCount int `json:"totalCount"`
	} `json:"checkRuns"`
	Conclusion *string `json:"conclusion"`
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
	return openIDs
}

func resolveMergeStatus(
	ciState string,
	reviewDecision *string,
	mergeStateStatus string,
) MergeStatus {
	// A repo without a required-review rule reports mergeStateStatus=CLEAN even
	// when a reviewer has asked for changes, so GitHub would let the merge
	// through. Report it as blocked so the status agrees with the review column
	// (and with --state=ready) rather than inviting a merge over the request.
	if mergeStateStatus == valueMergeStateClean {
		if reviewDecision != nil && *reviewDecision == valueReviewChanges {
			return MergeStatusBlocked
		}
		return MergeStatusReady
	}
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

func checkSuitesCIState(suites listCheckSuites) (string, bool) {
	if suites.TotalCount == 0 && len(suites.Nodes) == 0 {
		return "", false
	}

	ran := false
	for _, suite := range suites.Nodes {
		conclusion := ""
		if suite.Conclusion != nil {
			conclusion = *suite.Conclusion
		}
		if isFailingCheckSuiteConclusion(conclusion) &&
			(checkSuiteRan(suite) || isTerminalFailCheckSuiteConclusion(conclusion)) {
			return valueCIFailure, true
		}
		if checkSuiteRan(suite) {
			ran = true
			if suite.Conclusion == nil {
				return valueCIPending, true
			}
		}
	}

	if !ran {
		return "", true
	}
	if suites.TotalCount > len(suites.Nodes) {
		return valueCIPending, true
	}
	return valueCISuccess, true
}

func checkSuiteRan(suite listCheckSuite) bool {
	return suite.CheckRuns != nil && suite.CheckRuns.TotalCount > 0
}

func isFailingCheckSuiteConclusion(conclusion string) bool {
	switch conclusion {
	case valueCIFailure,
		valueCIStartupFailed,
		valueCICancelled,
		valueCITimedOut,
		valueCIActionNeeded:
		return true
	default:
		return false
	}
}

func isTerminalFailCheckSuiteConclusion(conclusion string) bool {
	return conclusion == valueCIStartupFailed
}

func applyMergeStatusResult(
	prs []PullRequest,
	openIdx map[string][]int,
	nodeID string,
	headSHA string,
	ciState string,
	reviewDecision *string,
	mergeStateStatus string,
	automergeLoaded bool,
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
	status := resolveMergeStatus(ciState, reviewDecision, mergeStateStatus)
	for _, idx := range indices {
		prs[idx].HeadSHA = headSHA
		prs[idx].MergeStatus = status
		if automergeLoaded {
			prs[idx].Automerge = automergeEnabled
			prs[idx].automergeLoaded = true
		}
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

func applyListMergeStatusNodes(
	prs []PullRequest,
	nodes []listMergeStatusNode,
	includeAutomerge bool,
) {
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
		if node.MergeStateStatus == valueMergeStateDirty {
			applyConflictNode(prs, openIdx[node.ID], node, includeAutomerge)
			continue
		}
		var ciState string
		if len(node.Commits.Nodes) > 0 {
			commit := node.Commits.Nodes[0].Commit
			if checkState, ok := checkSuitesCIState(commit.CheckSuites); ok {
				ciState = checkState
			} else if rollup := commit.StatusCheckRollup; rollup != nil {
				ciState = rollup.State
			}
		}
		applyMergeStatusResult(
			prs,
			openIdx,
			node.ID,
			node.HeadRefOID,
			ciState,
			node.ReviewDecision,
			node.MergeStateStatus,
			includeAutomerge,
			node.AutomergeRequest != nil,
		)
	}
}

// applyConflictNode marks the given PRs as conflicted. The review decision is
// carried over too: a conflicted PR still has one, and reporting it as loaded
// but empty renders the review column as "none".
func applyConflictNode(
	prs []PullRequest,
	indices []int,
	node listMergeStatusNode,
	includeAutomerge bool,
) {
	decision := ""
	if node.ReviewDecision != nil {
		decision = *node.ReviewDecision
	}
	for _, idx := range indices {
		prs[idx].HeadSHA = node.HeadRefOID
		prs[idx].MergeStatus = MergeStatusConflict
		if includeAutomerge {
			prs[idx].Automerge = node.AutomergeRequest != nil
			prs[idx].automergeLoaded = true
		}
		prs[idx].ReviewDecision = decision
		prs[idx].reviewDecisionLoaded = true
	}
}

// hasListMetadataNodes reports whether a (possibly partial) response carried any
// usable nodes.
func hasListMetadataNodes(result *listMetadataResult) bool {
	return len(result.TimelineNodes) > 0 ||
		len(result.AutomergeNodes) > 0 ||
		len(result.MergeNodes) > 0 ||
		len(result.ViewerReviewNodes) > 0
}

func applyListViewerReviewNodes(prs []PullRequest, viewer string, nodes []listViewerReviewNode) {
	if viewer == "" {
		return
	}

	viewer = strings.ToLower(viewer)
	approved := make(map[string]bool, len(nodes))
	loaded := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		loaded[node.ID] = true
		for _, review := range node.LatestOpinionatedReviews.Nodes {
			if review.Author == nil || !strings.EqualFold(review.Author.Login, viewer) {
				continue
			}
			approved[node.ID] = review.State == valueReviewApproved
			break
		}
	}

	for i := range prs {
		if !loaded[prs[i].NodeID] {
			continue
		}
		prs[i].viewerApproved = approved[prs[i].NodeID]
		prs[i].viewerApprovalLoaded = true
		prs[i].viewerIsAuthor = strings.EqualFold(prs[i].Author.Login, viewer)
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

func fetchHeadRefOIDs(gql *api.GraphQLClient, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}

	var result struct {
		Nodes []struct {
			ID         string `json:"id"`
			HeadRefOID string `json:"headRefOid"`
		} `json:"nodes"`
	}

	if err := gql.Do(
		`query HeadRefOIDs($ids:[ID!]!){nodes(ids:$ids){... on PullRequest{id headRefOid}}}`,
		map[string]any{keyIDs: ids},
		&result,
	); err != nil {
		return nil, fmt.Errorf("querying head refs: %w", err)
	}

	heads := make(map[string]string, len(result.Nodes))
	for _, node := range result.Nodes {
		heads[node.ID] = node.HeadRefOID
	}
	return heads, nil
}

// hydrateListMetadata batches the list-view GraphQL lookups needed for
// automerge filtering, timeline filtering, and merge-status enrichment.
//
// The work is split into chunks of hydrateChunkSize PRs, each its own query, run
// concurrently. A single query covering every PR asks GitHub for a check-suite
// rollup per node, which times out once the result set gets large.
func hydrateListMetadata(
	gql *api.GraphQLClient,
	prs []PullRequest,
	req listMetadataRequest,
) (timelineActors, error) {
	if len(prs) == 0 {
		return newTimelineActors(), nil
	}
	if len(prs) <= hydrateChunkSize {
		return hydrateListMetadataChunk(gql, prs, req)
	}

	actors := newTimelineActors()
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)
	sem := make(chan struct{}, maxConcurrency)

	// Chunks are subslices of prs, so each batch enriches the caller's PRs in place.
	for start := 0; start < len(prs); start += hydrateChunkSize {
		chunk := prs[start:min(start+hydrateChunkSize, len(prs))]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			defer wg.Done()

			chunkActors, err := hydrateListMetadataChunk(gql, chunk, req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				clog.Warn().Err(err).Int("prs", len(chunk)).Msg("List metadata chunk failed")
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			maps.Copy(actors.closed, chunkActors.closed)
			maps.Copy(actors.merged, chunkActors.merged)
		}()
	}
	wg.Wait()

	return actors, firstErr
}

// hydrateListMetadataChunk queries one chunk, halving and retrying when GitHub
// gives up on it. How much work a PR costs the server varies enormously - a repo
// whose PRs each carry dozens of check suites exhausts the budget at a batch
// size another repo handles comfortably - so the size that works is discovered
// per request rather than assumed.
func hydrateListMetadataChunk(
	gql *api.GraphQLClient,
	prs []PullRequest,
	req listMetadataRequest,
) (timelineActors, error) {
	actors, err := hydrateListMetadataBatch(gql, prs, req)
	if err == nil || len(prs) < 2 || !isRetryableHydrateErr(err) {
		return actors, err
	}

	clog.Debug().Err(err).Int("prs", len(prs)).Msg("Retrying list metadata in halves")

	const halves = 2
	mid := len(prs) / halves
	left, leftErr := hydrateListMetadataChunk(gql, prs[:mid], req)
	right, rightErr := hydrateListMetadataChunk(gql, prs[mid:], req)

	merged := newTimelineActors()
	for _, half := range []timelineActors{left, right} {
		maps.Copy(merged.closed, half.closed)
		maps.Copy(merged.merged, half.merged)
	}
	if leftErr != nil {
		return merged, leftErr
	}
	return merged, rightErr
}

// isRetryableHydrateErr reports whether an error is GitHub giving up on the
// query's size rather than refusing the request outright.
func isRetryableHydrateErr(err error) bool {
	var httpErr *api.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	switch httpErr.StatusCode {
	case http.StatusRequestTimeout, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// hydrateListMetadataBatch performs one combined GraphQL query for the given PRs.
func hydrateListMetadataBatch(
	gql *api.GraphQLClient,
	prs []PullRequest,
	req listMetadataRequest,
) (timelineActors, error) {
	if len(prs) == 0 {
		return newTimelineActors(), nil
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

	viewerReviewIDs := []string{}
	if req.viewerApproval {
		viewerReviewIDs = collectMergeStatusNodeIDs(prs)
	}

	var (
		queryDefs  []string
		queryRoots []string
		variables  = make(map[string]any)
	)

	if len(timelineIDs) > 0 {
		queryDefs = append(queryDefs, "$timelineIDs: [ID!]!")
		queryRoots = append(queryRoots, buildTimelineRoot(req))
		variables["timelineIDs"] = timelineIDs
	}

	if len(automergeIDs) > 0 {
		queryDefs = append(queryDefs, "$automergeIDs: [ID!]!")
		queryRoots = append(queryRoots, buildAutomergeRoot())
		variables["automergeIDs"] = automergeIDs
	}

	if len(mergeIDs) > 0 {
		queryDefs = append(queryDefs, "$mergeIDs: [ID!]!")
		queryRoots = append(queryRoots, buildMergeStatusRoot(req.automerge))
		variables["mergeIDs"] = mergeIDs
	}

	if len(viewerReviewIDs) > 0 {
		queryDefs = append(queryDefs, "$viewerReviewIDs: [ID!]!")
		queryRoots = append(queryRoots, buildViewerApprovalRoot())
		variables["viewerReviewIDs"] = viewerReviewIDs
	}

	if len(queryRoots) == 0 {
		return newTimelineActors(), nil
	}

	var result listMetadataResult

	query := fmt.Sprintf(
		`query ListMetadata(%s){%s}`,
		strings.Join(queryDefs, ", "),
		strings.Join(queryRoots, " "),
	)
	if err := gql.Do(query, variables, &result); err != nil {
		// GitHub answers with usable data alongside field-level errors - a token
		// that cannot read one repo's check suites still gets that PR's merge
		// state and review decision. Discarding the whole response over those
		// leaves every row unenriched, so keep whatever came back.
		var gqlErr *api.GraphQLError
		if !errors.As(err, &gqlErr) || !hasListMetadataNodes(&result) {
			return timelineActors{}, fmt.Errorf("querying list metadata: %w", err)
		}
		clog.Debug().
			Int("errors", len(gqlErr.Errors)).
			Str("first", gqlErr.Errors[0].Message).
			Msg("Partial list metadata response")
	}

	applyListAutomergeNodes(prs, automergeIDs, result.AutomergeNodes)
	applyListMergeStatusNodes(prs, result.MergeNodes, req.automerge)
	applyListViewerReviewNodes(prs, result.Viewer.Login, result.ViewerReviewNodes)

	return timelineActorsFromNodes(result.TimelineNodes), nil
}

func hydrateListMetadataCached(
	gql *api.GraphQLClient,
	prs []PullRequest,
	req listMetadataRequest,
	cache *listMetadataCache,
) (timelineActors, error) {
	if cache == nil || len(prs) == 0 {
		return hydrateListMetadata(gql, prs, req)
	}

	actors := newTimelineActors()
	missingPRs := make([]PullRequest, 0, len(prs))
	missingIdx := make([]int, 0, len(prs))

	for i := range prs {
		if cache.apply(&prs[i], req, &actors) {
			continue
		}
		missingPRs = append(missingPRs, prs[i])
		missingIdx = append(missingIdx, i)
	}

	if len(missingPRs) == 0 {
		return actors, nil
	}

	// Chunks that succeeded are written back even when a sibling failed:
	// discarding them would grey out the whole list over one bad request. The
	// cache is left untouched in that case, so the next refresh retries rather
	// than remembering a half-filled round.
	freshActors, err := hydrateListMetadata(gql, missingPRs, req)
	for i, idx := range missingIdx {
		prs[idx] = missingPRs[i]
		if err == nil {
			cache.store(prs[idx], req, freshActors)
		}
	}
	maps.Copy(actors.closed, freshActors.closed)
	maps.Copy(actors.merged, freshActors.merged)

	return actors, err
}

func validateCachedHeads(
	gql *api.GraphQLClient,
	prs []PullRequest,
	cache *listMetadataCache,
) (bool, error) {
	if cache == nil || len(prs) == 0 {
		return false, nil
	}

	ids := cache.pendingHeadChecks(prs)
	if len(ids) == 0 {
		return false, nil
	}

	heads, err := fetchHeadRefOIDs(gql, ids)
	if err != nil {
		return false, err
	}
	return cache.invalidateHeadMismatches(prs, heads), nil
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

func filterByViewerApproval(prs []PullRequest) []PullRequest {
	filtered := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.viewerApprovalLoaded && (pr.viewerApproved || pr.viewerIsAuthor) {
			clog.Debug().
				Link("pr", pr.URL, pr.Ref()).
				Msg("Filtered by self-required review")
			continue
		}
		filtered = append(filtered, pr)
	}
	return filtered
}

// Number of PRs per list-metadata GraphQL query. Each node in the query pulls a
// check-suite rollup, so batches beyond this start tripping GitHub's timeout.
// Repositories with many check suites per PR blow the budget sooner, which is
// what hydrateListMetadataChunk's halving retry is for.
const hydrateChunkSize = 15

// filterByCI keeps only PRs whose enriched MergeStatus matches the given CI status.
// CISuccess matches PRs where CI passed (MergeStatusReady or MergeStatusBlocked).
// CIFailure matches MergeStatusCIFailed. CIPending matches MergeStatusCIPending.
func filterByCI(prs []PullRequest, ci CIStatus) []PullRequest {
	result := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if matchesCI(pr, ci) {
			result = append(result, pr)
		}
	}
	return result
}

// matchesCI reports whether a PR's enriched MergeStatus satisfies the CI filter.
func matchesCI(pr PullRequest, ci CIStatus) bool {
	switch ci {
	case CISuccess:
		return pr.MergeStatus == MergeStatusReady || pr.MergeStatus == MergeStatusBlocked
	case CIFailure:
		return pr.MergeStatus == MergeStatusCIFailed
	case CIPending:
		return pr.MergeStatus == MergeStatusCIPending
	case CINone:
		return false
	}
	return false
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
	names := make([]string, 0, len(prs))
	for _, pr := range prs {
		names = append(names, pr.Repository.Name)
	}
	names = xslices.Unique(names)
	xslices.SortNatural(names)
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
