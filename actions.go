package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

// ActionRunner executes PR actions using GitHub REST and GraphQL APIs.
type ActionRunner struct {
	rest *api.RESTClient
	gql  *api.GraphQLClient
}

// NewActionRunner creates an ActionRunner with the given API clients.
// gql may be nil if no GraphQL actions are needed.
func NewActionRunner(rest *api.RESTClient, gql *api.GraphQLClient) *ActionRunner {
	return &ActionRunner{rest: rest, gql: gql}
}

// Execute runs PR actions (approve, close, merge, etc.) on the given PRs.
func (a *ActionRunner) Execute(cli *CLI, prs []PullRequest) error {
	if len(prs) == 0 {
		return nil
	}

	// Phase 1: Comments (if --comment without --close)
	if cli.Comment != "" && !cli.Close {
		if err := a.runParallel(prs, func(pr PullRequest) error {
			owner, repo := prOwnerRepo(pr)
			if err := a.comment(owner, repo, pr.Number, cli.Comment); err != nil {
				return err
			}
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Commented")
			return nil
		}); err != nil {
			return err
		}
	}

	// Phase 2: All other actions (parallel per-PR)
	if err := a.runParallel(prs, func(pr PullRequest) error {
		return a.executeForPR(cli, pr)
	}); err != nil {
		return err
	}

	// Phase 3: Force-merge (sequential - each PR blocks on check polling)
	if cli.ForceMerge {
		for _, pr := range prs {
			if err := a.forceMerge(pr); err != nil {
				return err
			}
		}
	}

	// Phase 4: Open in browser (if --open)
	if cli.Open {
		urls := make([]string, len(prs))
		for i, pr := range prs {
			urls[i] = pr.URL
		}
		if err := openBrowser(urls...); err != nil {
			return fmt.Errorf("failed to open browser: %w", err)
		}
	}

	return nil
}

func (a *ActionRunner) executeForPR(cli *CLI, pr PullRequest) error {
	var errs []string
	owner, repo := prOwnerRepo(pr)

	if cli.Update {
		if err := a.updateBranch(owner, repo, pr.Number); err != nil {
			errs = append(errs, fmt.Sprintf("update-branch %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Updated branch")
		}
	}

	if cli.Close {
		if err := a.closePR(owner, repo, pr.Number, cli.Comment, cli.DeleteBranch); err != nil {
			errs = append(errs, fmt.Sprintf("close %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Closed")
		}
	}

	if cli.Approve {
		if err := a.approve(owner, repo, pr.Number); err != nil {
			errs = append(errs, fmt.Sprintf("approve %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Approved")
		}
	}

	if cli.MarkDraft {
		if err := a.markDraft(pr.NodeID); err != nil {
			errs = append(errs, fmt.Sprintf("mark-draft %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Marked as draft")
		}
	}

	if cli.MarkReady {
		if err := a.markReady(pr.NodeID); err != nil {
			errs = append(errs, fmt.Sprintf("mark-ready %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Marked as ready")
		}
	}

	if cli.Merge != nil && *cli.Merge {
		msg, err := a.mergeOrAutoMerge(owner, repo, pr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("merge %s: %v", pr.URL, err))
		} else {
			clog.Info().Link("pr", pr.URL, pr.Ref()).Str("title", truncateTitle(pr.Title)).Msg(msg)
		}
	}
	if cli.Merge != nil && !*cli.Merge {
		if err := a.disableAutoMerge(pr.NodeID); err != nil {
			errs = append(errs, fmt.Sprintf("disable-auto-merge %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Disabled automerge")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *ActionRunner) runParallel(prs []PullRequest, fn func(PullRequest) error) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string

	sem := make(chan struct{}, maxConcurrency)
	for _, pr := range prs {
		wg.Add(1)
		sem <- struct{}{}
		go func(p PullRequest) {
			defer func() { <-sem }()
			defer wg.Done()
			if err := fn(p); err != nil {
				mu.Lock()
				errs = append(errs, err.Error())
				mu.Unlock()
			}
		}(pr)
	}
	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("action errors:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

// prOwnerRepo splits NameWithOwner into owner and repo.
func prOwnerRepo(pr PullRequest) (string, string) {
	const numParts = 2
	parts := strings.SplitN(pr.Repository.NameWithOwner, "/", numParts)
	if len(parts) == numParts {
		return parts[0], parts[1]
	}
	return "", pr.Repository.Name
}

// jsonBody marshals v to a JSON io.Reader for REST API calls.
func jsonBody(v any) *bytes.Buffer {
	// json.Marshal only fails for unmarshalable types (channels, funcs, etc.);
	// all callers pass simple map[string]string literals, so the error is safe to ignore.
	b, _ := json.Marshal(v)
	return bytes.NewBuffer(b)
}

// REST actions

func (a *ActionRunner) comment(owner, repo string, number int, body string) error {
	path := fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, repo, number)
	return a.rest.Post(path, jsonBody(map[string]string{"body": body}), nil)
}

func (a *ActionRunner) approve(owner, repo string, number int) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	return a.rest.Post(path, jsonBody(map[string]string{"event": "APPROVE"}), nil)
}

func (a *ActionRunner) closePR(
	owner, repo string,
	number int,
	comment string,
	deleteBranch bool,
) error {
	if comment != "" {
		if err := a.comment(owner, repo, number, comment); err != nil {
			return fmt.Errorf("comment: %w", err)
		}
	}

	path := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
	if err := a.rest.Patch(path, jsonBody(map[string]string{"state": "closed"}), nil); err != nil {
		return fmt.Errorf("close PR: %w", err)
	}

	if deleteBranch {
		var prData struct {
			Head struct {
				Ref string `json:"ref"`
			} `json:"head"`
		}
		if err := a.rest.Get(path, &prData); err != nil {
			return fmt.Errorf("get head ref: %w", err)
		}
		if prData.Head.Ref != "" {
			refPath := fmt.Sprintf("repos/%s/%s/git/refs/heads/%s", owner, repo, prData.Head.Ref)
			if err := a.rest.Delete(refPath, nil); err != nil {
				return fmt.Errorf("delete branch: %w", err)
			}
		}
	}

	return nil
}

// mergeOrAutoMerge enables automerge, or merges directly if the PR is already in clean status.
// Returns the log message on success.
func (a *ActionRunner) mergeOrAutoMerge(owner, repo string, pr PullRequest) (string, error) {
	err := a.enableAutoMerge(pr.NodeID)
	if err == nil {
		return "Enabled automerge", nil
	}
	if !strings.Contains(err.Error(), "clean status") {
		return "", err
	}
	// PR is already mergeable - merge it directly.
	if mergeErr := a.mergePR(owner, repo, pr.Number); mergeErr != nil {
		return "", mergeErr
	}
	return "Merged", nil
}

func (a *ActionRunner) mergePR(owner, repo string, number int) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/merge", owner, repo, number)
	return a.rest.Put(path, jsonBody(map[string]string{"merge_method": "squash"}), nil)
}

func (a *ActionRunner) updatePR(owner, repo string, number int, title, body string) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
	return a.rest.Patch(path, jsonBody(map[string]string{"title": title, "body": body}), nil)
}

func (a *ActionRunner) fetchPRBody(owner, repo string, number int) (string, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
	var pr struct {
		Body string `json:"body"`
	}
	if err := a.rest.Get(path, &pr); err != nil {
		return "", err
	}
	return pr.Body, nil
}

func (a *ActionRunner) updateBranch(owner, repo string, number int) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/update-branch", owner, repo, number)
	return a.rest.Put(path, nil, nil)
}

// GraphQL actions

// doNodeMutation runs a GraphQL mutation that takes a single node ID variable.
func (a *ActionRunner) doNodeMutation(query, nodeID string) error {
	if a.gql == nil {
		return fmt.Errorf("GraphQL client is not configured")
	}
	var result map[string]any
	return a.gql.Do(query, map[string]any{"id": nodeID}, &result)
}

func (a *ActionRunner) markReady(nodeID string) error {
	return a.doNodeMutation(
		`mutation MarkReady($id: ID!) {
			markPullRequestReadyForReview(input: {pullRequestId: $id}) {
				clientMutationId
			}
		}`, nodeID)
}

func (a *ActionRunner) markDraft(nodeID string) error {
	return a.doNodeMutation(
		`mutation MarkDraft($id: ID!) {
			convertPullRequestToDraft(input: {pullRequestId: $id}) {
				clientMutationId
			}
		}`, nodeID)
}

func (a *ActionRunner) enableAutoMerge(nodeID string) error {
	return a.doNodeMutation(
		`mutation EnableAutoMerge($id: ID!) {
			enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: SQUASH}) {
				clientMutationId
			}
		}`, nodeID)
}

func (a *ActionRunner) disableAutoMerge(nodeID string) error {
	return a.doNodeMutation(
		`mutation DisableAutoMerge($id: ID!) {
			disablePullRequestAutoMerge(input: {pullRequestId: $id}) {
				clientMutationId
			}
		}`, nodeID)
}

// Force-merge: poll for checks, then merge with bypass.

// forceMerge polls checks until they pass, then merges the PR using the standard
// mergePullRequest mutation. This works like gh pr merge --admin: the mutation
// itself requires the caller to have bypass/admin permissions on the repo;
// no special API field is needed.
func (a *ActionRunner) forceMerge(pr PullRequest) error {
	clog.Info().
		Link("pr", pr.URL, pr.Ref()).
		Str("title", truncateTitle(pr.Title)).
		Msg("Waiting for checks")

	if err := a.waitForChecks(pr); err != nil {
		return err
	}

	clog.Info().
		Link("pr", pr.URL, pr.Ref()).
		Str("title", truncateTitle(pr.Title)).
		Msg("Force-merging with bypass permissions")
	return a.forceMergePR(pr.NodeID)
}

// checkState represents the aggregate CI check state for a PR.
type checkState int

const (
	checksSuccess checkState = iota
	checksPending
	checksFailed
	checksNone
)

// forceMergeInterval is the polling interval for check status.
const forceMergeInterval = 10 * time.Second

// waitForChecks polls the PR's statusCheckRollup until checks pass, fail, or the PR is no longer open.
func (a *ActionRunner) waitForChecks(pr PullRequest) error {
	ref := pr.Ref()
	for {
		state, prState, err := a.queryCheckState(pr.NodeID)
		if err != nil {
			return fmt.Errorf("querying check status for %s: %w", pr.URL, err)
		}

		// Check if PR state changed
		if prState == "MERGED" {
			clog.Info().
				Link("pr", pr.URL, ref).
				Str("title", truncateTitle(pr.Title)).
				Msg("PR already merged")
			return fmt.Errorf("PR already merged: %s", pr.URL)
		}
		if prState == "CLOSED" {
			return fmt.Errorf("PR was closed: %s", pr.URL)
		}

		switch state {
		case checksSuccess:
			clog.Info().
				Link("pr", pr.URL, ref).
				Str("title", truncateTitle(pr.Title)).
				Msg("All checks passed")
			return nil
		case checksFailed:
			return fmt.Errorf("checks failed for %s", pr.URL)
		case checksPending:
			time.Sleep(forceMergeInterval)
		case checksNone:
			// No checks configured - proceed immediately
			return nil
		}
	}
}

// queryCheckState fetches the aggregate CI status and PR state in a single GraphQL query.
func (a *ActionRunner) queryCheckState(nodeID string) (checkState, string, error) {
	if a.gql == nil {
		return checksNone, "", fmt.Errorf("GraphQL client is not configured")
	}

	var result struct {
		Node struct {
			State   string `json:"state"`
			Commits struct {
				Nodes []struct {
					Commit struct {
						StatusCheckRollup *struct {
							State string `json:"state"`
						} `json:"statusCheckRollup"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"commits"`
		} `json:"node"`
	}

	err := a.gql.Do(
		`query CheckState($id: ID!) {
			node(id: $id) {
				... on PullRequest {
					state
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
		map[string]any{"id": nodeID},
		&result,
	)
	if err != nil {
		return checksNone, "", err
	}

	prState := result.Node.State

	if len(result.Node.Commits.Nodes) == 0 {
		return checksNone, prState, nil
	}
	rollup := result.Node.Commits.Nodes[0].Commit.StatusCheckRollup
	if rollup == nil {
		return checksNone, prState, nil
	}

	switch rollup.State {
	case "SUCCESS":
		return checksSuccess, prState, nil
	case "FAILURE", "ERROR":
		return checksFailed, prState, nil
	case "PENDING", "EXPECTED":
		return checksPending, prState, nil
	default:
		return checksNone, prState, nil
	}
}

// forceMergePR merges a PR via the mergePullRequest GraphQL mutation with SQUASH method.
// This is the same mutation gh uses - admin/bypass permissions are enforced server-side
// based on the caller's token permissions, not via a special field.
func (a *ActionRunner) forceMergePR(nodeID string) error {
	if a.gql == nil {
		return fmt.Errorf("GraphQL client is not configured")
	}
	var result map[string]any
	return a.gql.Do(
		`mutation ForceMerge($input: MergePullRequestInput!) {
			mergePullRequest(input: $input) {
				clientMutationId
			}
		}`,
		map[string]any{
			"input": map[string]any{
				"pullRequestId": nodeID,
				"mergeMethod":   "SQUASH",
			},
		},
		&result,
	)
}
