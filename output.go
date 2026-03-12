package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

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

	filtered := make([]PullRequest, 0, len(prs))
	for _, pr := range prs {
		if enabled[pr.NodeID] == wantEnabled {
			filtered = append(filtered, pr)
		} else {
			clog.Debug().
				Link("pr", pr.URL, pr.Ref()).
				Bool("automerge", enabled[pr.NodeID]).
				Msg("Filtered out")
		}
	}

	clog.Debug().
		Int("before", len(prs)).
		Int("after", len(filtered)).
		Bool("want-automerge", wantEnabled).
		Msg("Automerge filter applied")

	return filtered, nil
}

// enrichAutomerge queries automerge status via GraphQL and sets Automerge on each PR.
func enrichAutomerge(gql *api.GraphQLClient, prs []PullRequest) error {
	if len(prs) == 0 {
		return nil
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
		return fmt.Errorf("querying automerge status: %w", err)
	}

	enabled := make(map[string]bool, len(result.Nodes))
	for _, node := range result.Nodes {
		enabled[node.ID] = node.AutomergeRequest != nil
	}

	for i := range prs {
		prs[i].Automerge = enabled[prs[i].NodeID]
	}

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
			ID             string  `json:"id"`
			ReviewDecision *string `json:"reviewDecision"`
			Commits        struct {
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
		indices, ok := openIdx[node.ID]
		if !ok {
			continue
		}

		var ciState string
		if len(node.Commits.Nodes) > 0 {
			if rollup := node.Commits.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
				ciState = rollup.State
			}
		}

		var status MergeStatus
		switch {
		case ciState == valueCIFailure || ciState == valueCIError:
			status = MergeStatusCIFailed
		case ciState == valueCIPending || ciState == valueCIExpected:
			status = MergeStatusCIPending
		case ciState == valueCISuccess && node.ReviewDecision != nil && *node.ReviewDecision == valueReviewApproved:
			status = MergeStatusReady
		default:
			status = MergeStatusBlocked
		}

		for _, idx := range indices {
			prs[idx].MergeStatus = status
		}
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
	return strings.Join(lines, "\n")
}

// renderBullets outputs "* <url>" per line.
func renderBullets(prs []PullRequest) string {
	lines := make([]string, 0, len(prs))
	for _, pr := range prs {
		lines = append(lines, "* "+pr.URL)
	}
	return strings.Join(lines, "\n")
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
	return strings.Join(names, "\n")
}

// renderJSON outputs pretty-printed sorted JSON.
func renderJSON(prs []PullRequest) (string, error) {
	data, err := json.MarshalIndent(prs, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling JSON: %w", err)
	}
	return string(data), nil
}
