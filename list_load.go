package main

import (
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

type listLoader struct {
	rest    *api.RESTClient
	graphQL func() (*api.GraphQLClient, error)
	cache   *listMetadataCache
	policy  *listRefreshPolicy
	browse  bool // TUI rows need viewer approval and keep CI filtering local.
}

type listResult struct {
	prs        []PullRequest
	rawFetched int  // before local filters, for the GitHub search cap
	matched    bool // before enrichment, for the one-shot empty-result exit code
}

func (l listLoader) load(cli *CLI, params *SearchParams) (listResult, error) {
	needStatus := l.browse || needsListMergeStatus(cli)
	prs, err := executeListSearch(l.rest, l.graphQL, params, needStatus)
	if err != nil {
		return listResult{}, err
	}
	result := listResult{rawFetched: len(prs)}
	prs, err = applyFilters(cli, prs)
	if err != nil || len(prs) == 0 {
		return result, err
	}
	result.matched = true

	closed, err := l.timelineLogins(cli.ClosedBy.Values)
	if err != nil {
		return result, err
	}
	merged, err := l.timelineLogins(cli.MergedBy.Values)
	if err != nil {
		return result, err
	}
	req := listMetadataRequest{
		mergeStatus:    needStatus,
		viewerApproval: l.browse || cli.ReviewSelfRequired(),
		automerge: !l.browse && (cli.Merge != nil || !cli.Quick && cli.Send) &&
			!allAutomergeLoaded(prs),
		timelineClosed: len(closed) > 0,
		timelineMerged: len(merged) > 0,
	}
	prs, err = l.enrich(cli, prs, req, closed, merged)
	if err != nil {
		return result, err
	}
	if cli.Quick && !needStatus {
		for i := range prs {
			if prs[i].State == valueOpen {
				prs[i].MergeStatus = MergeStatusBlocked
			}
		}
	}
	if cli.PRState() == StateReady {
		prs = filterReady(prs)
	}
	if ci := cli.CIFilter(); !l.browse && ci.Active() {
		prs = filterByCI(prs, ci)
	}
	result.prs = prs
	return result, nil
}

func (l listLoader) timelineLogins(values []string) (map[string]bool, error) {
	logins, err := resolveTimelineLogins(l.rest, values)
	if err != nil && l.browse {
		clog.Debug().Err(err).Msg("timeline filters failed")
		return map[string]bool{}, nil
	}
	return logins, err
}

func (l listLoader) enrich(
	cli *CLI,
	prs []PullRequest,
	req listMetadataRequest,
	closed, merged map[string]bool,
) ([]PullRequest, error) {
	stacks := l.browse || cli.IsInteractive()
	if !stacks && req == (listMetadataRequest{}) {
		return prs, nil
	}

	gql, err := l.graphQL()
	var actors timelineActors
	if err == nil {
		if stacks {
			if stackErr := hydrateStackMetadata(gql, prs); stackErr != nil {
				clog.Debug().Err(stackErr).Msg("skipping stack metadata hydration")
			}
		}
		actors, err = hydrateListMetadataForRefresh(gql, prs, req, l.cache, l.policy)
	}
	if err != nil {
		if !l.browse &&
			(cli.Merge != nil || req.timelineClosed || req.timelineMerged || req.viewerApproval) {
			return nil, err
		}
		clog.Debug().Err(err).Msg("skipping list metadata hydration")
		return prs, nil
	}
	if !l.browse && cli.Merge != nil {
		prs = filterByAutomergeState(prs, !*cli.Merge)
	}
	if req.timelineClosed || req.timelineMerged {
		prs = filterByTimelineActorsLoaded(prs, closed, merged, actors)
	}
	if cli.ReviewSelfRequired() {
		prs = filterByViewerApproval(prs)
	}
	return prs, nil
}
