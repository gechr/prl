package main

import (
	"fmt"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
)

// eventPRs copies just the rows that can be updated by this batch.
func eventPRs(previous []PullRequest, changed prEvents) []PullRequest {
	var prs []PullRequest
	for _, pr := range previous {
		if _, ok := changed[makePRKey(pr)]; ok && pr.NodeID != "" {
			prs = append(prs, pr)
		}
	}
	return prs
}

func refreshEventFields(
	gql *api.GraphQLClient,
	prs []PullRequest,
	events prEvents,
	req listMetadataRequest,
) error {
	_, err := hydrateListMetadataChunk(gql, prs, req, events)
	return err
}

// Apply current lifecycle fields before computing readiness, including when a
// previously closed PR reopens. A missing node leaves the published list intact.
func applyEventState(prs []PullRequest, nodes []listMergeStatusNode) error {
	byID := make(map[string]listMergeStatusNode, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	for i := range prs {
		pr := &prs[i]
		node := byID[pr.NodeID]
		if node.State == "" || node.UpdatedAt.IsZero() {
			return fmt.Errorf("missing event refresh data for %s", makePRKey(*pr))
		}
		pr.State = strings.ToLower(node.State)
		pr.IsDraft = node.IsDraft
		pr.UpdatedAt = node.UpdatedAt
		pr.HeadSHA = node.HeadRefOID
		pr.Automerge = node.AutomergeRequest != nil
		pr.automergeLoaded = true
		pr.ReviewDecision = ""
		if node.ReviewDecision != nil {
			pr.ReviewDecision = *node.ReviewDecision
		}
		pr.reviewDecisionLoaded = true
		if pr.State != valueOpen {
			pr.MergeStatus = MergeStatusUnknown
			pr.behind = false
		}
	}
	return nil
}

func refreshWatchEvents(
	gql *api.GraphQLClient,
	cli *CLI,
	params *SearchParams,
	previous []PullRequest,
	changed prEvents,
	cache *listMetadataCache,
) ([]PullRequest, error) {
	updates := eventPRs(previous, changed)
	req := listMetadataRequest{
		automerge:      true,
		mergeStatus:    needsListMergeStatus(cli),
		viewerApproval: cli.ReviewSelfRequired(),
	}
	if err := refreshEventFields(gql, updates, changed, req); err != nil {
		return nil, err
	}
	byKey := make(map[prKey]PullRequest, len(updates))
	for _, pr := range updates {
		key := makePRKey(pr)
		if cli.Quick && !req.mergeStatus && pr.State == valueOpen {
			pr.MergeStatus = MergeStatusBlocked
		}
		byKey[key] = pr
		cache.store(pr, listMetadataRequest{
			automerge: true, mergeStatus: req.mergeStatus,
			viewerApproval: req.viewerApproval && changed[key]&eventReviews != 0,
		}, newTimelineActors())
	}

	prs := make([]PullRequest, 0, len(previous))
	for _, pr := range previous {
		if update, ok := byKey[makePRKey(pr)]; ok {
			pr = update
			if !watchEventMatches(cli, params, pr) {
				continue
			}
		}
		prs = append(prs, pr)
	}
	return applyFilters(cli, prs)
}

// The full search reconciles server-only qualifiers. Direct references skip
// search qualifiers, just as they do on a full refresh.
func watchEventMatches(cli *CLI, params *SearchParams, pr PullRequest) bool {
	state := cli.PRState()
	if state == StateReady && pr.MergeStatus != MergeStatusReady ||
		!cli.CIFilter().Matches(pr) ||
		cli.Merge != nil && pr.Automerge == *cli.Merge ||
		cli.ReviewSelfRequired() && (pr.viewerIsAuthor || isViewerApproved(pr)) {
		return false
	}
	if len(params.PRRefs) > 0 {
		return true
	}
	if cli.Draft != nil && pr.IsDraft != *cli.Draft {
		return false
	}
	switch state {
	case StateOpen, StateReady:
		if pr.State != valueOpen {
			return false
		}
	case StateClosed:
		if pr.State == valueOpen {
			return false
		}
	case StateMerged:
		if pr.State != valueMerged {
			return false
		}
	case StateAll:
	}
	if (cli.Review == valueReviewFilterRequired || cli.ReviewSelfRequired()) &&
		state != StateOpen &&
		state != StateReady {
		return true
	}
	return matchesReviewDecision(pr, cli.Review)
}
