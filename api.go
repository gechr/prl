package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

// HintError wraps an error with a command suggestion for the user.
// The TUI displays the hint as an info popover instead of a hard failure.
type HintError struct {
	Err  error
	Hint string
}

func (e *HintError) Error() string { return e.Err.Error() }
func (e *HintError) Unwrap() error { return e.Err }

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

	if cli.Approve && a.gql != nil {
		if err := ensureReviewDecisions(a.gql, prs); err != nil {
			clog.Debug().Err(err).Msg("Failed to preload review decisions")
		}
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

	// Phase 3: Force-merge (parallel per-PR so multiple selections wait concurrently)
	if cli.ForceMerge {
		a.forceMergeAll(prs)
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

func (a *ActionRunner) forceMergeAll(prs []PullRequest) {
	if len(prs) == 0 {
		return
	}

	group := clog.Group(
		context.Background(),
		clog.WithParallelism(maxConcurrency),
		clog.WithHideDone(),
		clog.WithMaxHeightPercent(0.5), //nolint:mnd // half the terminal window
		clog.WithFooter(
			clog.Spinner("Force-merging"),
			func(done, total int, u *clog.Update) {
				msg := "Force-merging"
				if done == total {
					msg = "Force-merged"
				}
				u.Msg(msg).Fraction("progress", done, total).Send()
			},
		),
	)

	type forceMergeTask struct {
		pr     PullRequest
		result *clog.TaskResult
	}

	tasks := make([]forceMergeTask, 0, len(prs))
	for _, pr := range prs {
		b := clog.Spinner("Waiting for checks").
			Symbol("·").
			Link("pr", pr.URL, pr.Ref())
		if pr.Title != "" {
			b = b.Str("title", truncateTitle(pr.Title))
		}
		result := group.Add(b).Progress(func(ctx context.Context, update *clog.Update) error {
			return a.forceMerge(ctx, pr, update)
		})
		tasks = append(tasks, forceMergeTask{pr: pr, result: result})
	}

	_ = group.Wait().Silent()

	var failed []PullRequest
	for _, task := range tasks {
		if err := task.result.Silent(); err != nil {
			failed = append(failed, task.pr)
			clog.Warn().
				Err(err).
				Link("pr", task.pr.URL, task.pr.Ref()).
				Str("title", truncateTitle(task.pr.Title)).
				Msg("Force-merge failed")
		}
	}

	if len(failed) == 0 {
		clog.Info().
			Int("count", len(prs)).
			Msgf("All %s force-merged", pluralize(len(prs), "PR", "PRs"))
	}
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
		if err := a.approvePR(pr); err != nil {
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
		msg, err := a.mergeOrAutomerge(owner, repo, pr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("merge %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg(msg)
		}
	}
	if cli.Merge != nil && !*cli.Merge {
		if err := a.disableAutomerge(pr.NodeID); err != nil {
			errs = append(errs, fmt.Sprintf("disable-auto-merge %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Disabled automerge")
		}
	}

	if cli.Copilot {
		if err := a.requestReview(owner, repo, pr.Number, copilotReviewer); err != nil {
			errs = append(errs, fmt.Sprintf("copilot-review %s: %v", pr.URL, err))
		} else {
			clog.Info().
				Link("pr", pr.URL, pr.Ref()).
				Str("title", truncateTitle(pr.Title)).
				Msg("Copilot review requested")
		}
	}

	if cli.Unsubscribe {
		if unsubErrs := a.unsubscribeAll(cli, owner, repo, pr); len(unsubErrs) > 0 {
			errs = append(errs, unsubErrs...)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *ActionRunner) unsubscribeAll(cli *CLI, owner, repo string, pr PullRequest) []string {
	logins := cli.ReviewRequested.Values
	if len(logins) == 0 {
		login, err := getCurrentLogin(a.rest)
		if err != nil {
			return []string{fmt.Sprintf("unsubscribe %s: %v", pr.URL, err)}
		}
		logins = []string{login}
	}
	var errs []string
	for _, login := range logins {
		if err := a.removeReviewRequest(owner, repo, pr.Number, login, pr.NodeID); err != nil {
			errs = append(errs, fmt.Sprintf("unsubscribe %s: %v", pr.URL, err))
			continue
		}
		clog.Info().
			Link("pr", pr.URL, pr.Ref()).
			Str("title", truncateTitle(pr.Title)).
			Msg("Unsubscribed")
	}
	return errs
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
		return fmt.Errorf("action errors:\n%s", strings.Join(errs, nl))
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

func (a *ActionRunner) approvePR(pr PullRequest) error {
	owner, repo := prOwnerRepo(pr)

	// Skip if the PR's overall review decision is already APPROVED.
	if pr.reviewDecisionLoaded && pr.ReviewDecision == valueReviewApproved {
		return nil
	}

	if !pr.reviewDecisionLoaded && a.gql != nil && pr.NodeID != "" {
		snapshot := []PullRequest{pr}
		if err := ensureReviewDecisions(a.gql, snapshot); err == nil {
			pr = snapshot[0]
			if pr.reviewDecisionLoaded && pr.ReviewDecision == valueReviewApproved {
				return nil
			}
		}
	}

	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, pr.Number)
	return a.rest.Post(path, jsonBody(map[string]string{"event": "APPROVE"}), nil)
}

func (a *ActionRunner) reopenPR(owner, repo string, number int) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
	return a.rest.Patch(path, jsonBody(map[string]string{"state": "open"}), nil)
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
	var prData struct {
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	var closeResult any
	if deleteBranch {
		closeResult = &prData
	}
	if err := a.rest.Patch(
		path,
		jsonBody(map[string]string{"state": "closed"}),
		closeResult,
	); err != nil {
		return fmt.Errorf("close PR: %w", err)
	}
	if deleteBranch && prData.Head.Ref != "" {
		refPath := fmt.Sprintf("repos/%s/%s/git/refs/heads/%s", owner, repo, prData.Head.Ref)
		if err := a.rest.Delete(refPath, nil); err != nil {
			return fmt.Errorf("delete branch: %w", err)
		}
	}

	return nil
}

// mergeOrAutomerge picks the right merge strategy based on the PR's known status:
//   - Ready/Unknown: try direct merge, then enqueue (merge queue), then automerge.
//   - Not ready: try automerge, then enqueue.
//
// Returns the log message on success.
func (a *ActionRunner) mergeOrAutomerge(owner, repo string, pr PullRequest) (string, error) {
	if pr.MergeStatus == MergeStatusReady || pr.MergeStatus == MergeStatusUnknown {
		// Try direct merge first (instant).
		if err := a.mergePR(owner, repo, pr.Number); err == nil {
			return resultMerged, nil
		}
		// Direct merge failed - try merge queue.
		if err := a.enqueuePR(pr.NodeID); err == nil {
			return resultEnqueued, nil
		}
		// Fall back to automerge.
		if err := a.enableAutomerge(pr.NodeID); err != nil {
			return "", err
		}
		return resultAutomerged, nil
	}
	// Not ready - enable automerge first.
	autoErr := a.enableAutomerge(pr.NodeID)
	if autoErr == nil {
		return resultAutomerged, nil
	}
	// Try merge queue.
	queueErr := a.enqueuePR(pr.NodeID)
	if queueErr == nil {
		return resultEnqueued, nil
	}
	return "", errors.Join(
		fmt.Errorf("enable automerge: %w", autoErr),
		fmt.Errorf("enqueue PR: %w", queueErr),
	)
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

// PRDetail holds the full detail for a PR detail view.
type PRDetail struct {
	Body           string
	MergeableState string // clean, dirty, unstable, behind, blocked, unknown
	Reviews        []PRReview
	Checks         []PRCheck
	Files          []PRFile
}

// PRCheck holds a single CI check run.
type PRCheck struct {
	Name       string
	Status     string // queued, in_progress, completed, waiting, requested, pending
	Conclusion string // success, failure, neutral, cancelled, skipped, timed_out, action_required, stale
	Duration   time.Duration
}

// PRReview holds a single review.
type PRReview struct {
	User  string
	State string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
}

// PRFile holds a changed file in a PR.
type PRFile struct {
	Filename  string
	Status    string // added, modified, removed, renamed
	Additions int
	Deletions int
}

func (a *ActionRunner) fetchPRDetail(
	owner, repo string,
	number int,
	nodeID string,
) (PRDetail, error) {
	var detail PRDetail

	var (
		pr struct {
			Body           string `json:"body"`
			MergeableState string `json:"mergeable_state"`
		}
		reviews []struct {
			User  struct{ Login string } `json:"user"`
			State string                 `json:"state"`
		}
		files []struct {
			Filename  string `json:"filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		}
		checks   []PRCheck
		prErr    error
		revErr   error
		filesErr error
	)

	var wg sync.WaitGroup

	// Fetch body + mergeable state.
	wg.Go(func() {
		prPath := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)
		prErr = a.rest.Get(prPath, &pr)
	})

	// Fetch reviews.
	wg.Go(func() {
		reviewPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
		revErr = a.rest.Get(reviewPath, &reviews)
	})

	// Fetch changed files.
	wg.Go(func() {
		filesPath := fmt.Sprintf("repos/%s/%s/pulls/%d/files", owner, repo, number)
		filesErr = a.rest.Get(filesPath, &files)
	})

	// Fetch checks (GraphQL, optional).
	if nodeID != "" {
		wg.Go(func() {
			var err error
			checks, err = a.fetchChecksGraphQL(nodeID)
			if err != nil {
				clog.Debug().Err(err).Str("node_id", nodeID).Msg("Failed to fetch detail checks")
			}
		})
	}

	wg.Wait()

	if prErr != nil {
		return detail, prErr
	}
	if revErr != nil {
		return detail, revErr
	}
	if filesErr != nil {
		return detail, filesErr
	}

	detail.Body = pr.Body
	detail.MergeableState = pr.MergeableState
	detail.Checks = checks

	// Keep only the latest review per user.
	seen := make(map[string]int)
	for _, r := range reviews {
		if idx, ok := seen[r.User.Login]; ok {
			detail.Reviews[idx] = PRReview{User: r.User.Login, State: r.State}
		} else {
			seen[r.User.Login] = len(detail.Reviews)
			detail.Reviews = append(detail.Reviews, PRReview{User: r.User.Login, State: r.State})
		}
	}

	detail.Files = make([]PRFile, len(files))
	for i, f := range files {
		detail.Files[i] = PRFile{
			Filename:  f.Filename,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
		}
	}

	return detail, nil
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

func (a *ActionRunner) enableAutomerge(nodeID string) error {
	return a.doNodeMutation(
		`mutation EnableAutomerge($id: ID!) {
			enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: SQUASH}) {
				clientMutationId
			}
		}`, nodeID)
}

func (a *ActionRunner) enqueuePR(nodeID string) error {
	return a.doNodeMutation(
		`mutation EnqueuePR($id: ID!) {
			enqueuePullRequest(input: {pullRequestId: $id}) {
				clientMutationId
			}
		}`, nodeID)
}

func (a *ActionRunner) disableAutomerge(nodeID string) error {
	return a.doNodeMutation(
		`mutation DisableAutomerge($id: ID!) {
			disablePullRequestAutoMerge(input: {pullRequestId: $id}) {
				clientMutationId
			}
		}`, nodeID)
}

func (a *ActionRunner) requestReview(
	owner, repo string,
	number int,
	login string, //nolint:unparam // login is intentionally general-purpose
) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, number)
	body := jsonBody(map[string][]string{"reviewers": {login}})
	return a.rest.Do(http.MethodPost, path, body, nil)
}

func (a *ActionRunner) removeReviewRequest(
	owner, repo string,
	number int,
	login, nodeID string,
) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, number)
	body := jsonBody(map[string][]string{"reviewers": {login}})
	if err := a.rest.Do(http.MethodDelete, path, body, nil); err != nil {
		return err
	}
	if err := a.unsubscribe(nodeID); err != nil {
		return &HintError{
			Err:  err,
			Hint: "gh auth refresh -s notifications",
		}
	}
	return nil
}

func (a *ActionRunner) unsubscribe(nodeID string) error {
	return a.doNodeMutation(
		`mutation Unsubscribe($id: ID!) {
			updateSubscription(input: {subscribableId: $id, state: UNSUBSCRIBED}) {
				clientMutationId
			}
		}`, nodeID)
}

func (a *ActionRunner) fetchDiff(owner, repo string, number int) (string, string, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number)

	var (
		diff    string
		diffErr error
		headSHA string
		shaErr  error
	)

	var wg sync.WaitGroup

	wg.Go(func() {
		diffClient, err := api.NewRESTClient(api.ClientOptions{
			Headers: map[string]string{"Accept": "application/vnd.github.diff"},
		})
		if err != nil {
			diffErr = err
			return
		}
		resp, err := diffClient.Request(http.MethodGet, path, nil)
		if err != nil {
			diffErr = err
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		diff = string(b)
		diffErr = err
	})

	wg.Go(func() {
		var pr struct {
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		shaErr = a.rest.Get(path, &pr)
		headSHA = pr.Head.SHA
	})

	wg.Wait()

	if diffErr != nil {
		return "", "", diffErr
	}
	return diff, headSHA, shaErr
}

// Force-merge: poll for checks, then merge with bypass.

// forceMerge polls checks until they pass, then merges the PR using the standard
// mergePullRequest mutation. This works like gh pr merge --admin: the mutation
// itself requires the caller to have bypass/admin permissions on the repo;
// no special API field is needed.
func (a *ActionRunner) forceMerge(ctx context.Context, pr PullRequest, update *clog.Update) error {
	if err := a.waitForChecks(ctx, pr, update); err != nil {
		return err
	}

	if update != nil {
		update.Msg("Force-merging with bypass permissions").Send()
	}
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

// Check sort priorities: failures first, in-progress, successes, skipped last.
const (
	checkOrderFailure    = iota // 0
	checkOrderInProgress        // 1
	checkOrderSuccess           // 2
	checkOrderSkipped           // 3
)

func sortChecks(checks []PRCheck) {
	slices.SortStableFunc(checks, func(a, b PRCheck) int {
		if d := checkSortOrder(a) - checkSortOrder(b); d != 0 {
			return d
		}
		if a.Duration != b.Duration {
			return int(a.Duration - b.Duration)
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

func mapStatusContextState(state string) (string, string) {
	switch strings.ToUpper(state) {
	case valueCISuccess:
		return ciStatusCompleted, ciStatusSuccess
	case valueCIFailure, valueCIError:
		return ciStatusCompleted, ciStatusFailure
	case valueCIPending, valueCIExpected:
		return ciStatusPending, ""
	default:
		return ciStatusPending, ""
	}
}

func (a *ActionRunner) fetchChecksGraphQL(nodeID string) ([]PRCheck, error) {
	if a.gql == nil {
		return nil, fmt.Errorf("GraphQL client is not configured")
	}

	query := `query DetailChecks($id: ID!, $after: String) {
		node(id: $id) {
			... on PullRequest {
				commits(last: 1) {
					nodes {
						commit {
							statusCheckRollup {
								contexts(first: 100, after: $after) {
									pageInfo {
										hasNextPage
										endCursor
									}
									nodes {
										__typename
										... on CheckRun {
											name
											status
											conclusion
											startedAt
											completedAt
										}
										... on StatusContext {
											context
											state
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}`

	var (
		after  *string
		checks []PRCheck
	)

	for {
		var result struct {
			Node *struct {
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *struct {
								Contexts struct {
									PageInfo struct {
										HasNextPage bool    `json:"hasNextPage"`
										EndCursor   *string `json:"endCursor"`
									} `json:"pageInfo"`
									Nodes []struct {
										TypeName    string     `json:"__typename"`
										Name        string     `json:"name"`
										Status      string     `json:"status"`
										Conclusion  string     `json:"conclusion"`
										StartedAt   *time.Time `json:"startedAt"`
										CompletedAt *time.Time `json:"completedAt"`
										Context     string     `json:"context"`
										State       string     `json:"state"`
									} `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"node"`
		}

		if err := a.gql.Do(
			query,
			map[string]any{"id": nodeID, "after": after},
			&result,
		); err != nil {
			return nil, err
		}

		if result.Node == nil || len(result.Node.Commits.Nodes) == 0 {
			return nil, nil
		}
		rollup := result.Node.Commits.Nodes[0].Commit.StatusCheckRollup
		if rollup == nil {
			return nil, nil
		}

		contexts := rollup.Contexts
		for _, node := range contexts.Nodes {
			switch node.TypeName {
			case "CheckRun":
				var dur time.Duration
				if node.StartedAt != nil && node.CompletedAt != nil {
					dur = node.CompletedAt.Sub(*node.StartedAt)
				}
				checks = append(checks, PRCheck{
					Name:       node.Name,
					Status:     strings.ToLower(node.Status),
					Conclusion: strings.ToLower(node.Conclusion),
					Duration:   dur,
				})
			case "StatusContext":
				status, conclusion := mapStatusContextState(node.State)
				checks = append(checks, PRCheck{
					Name:       node.Context,
					Status:     status,
					Conclusion: conclusion,
				})
			}
		}

		if !contexts.PageInfo.HasNextPage || contexts.PageInfo.EndCursor == nil {
			break
		}
		after = contexts.PageInfo.EndCursor
	}

	sortChecks(checks)
	return checks, nil
}

// checkSortOrder returns a sort key for PRCheck: failures first, then
// in-progress, then successes, then skipped/neutral last.
func checkSortOrder(c PRCheck) int {
	if c.Status != ciStatusCompleted {
		return checkOrderInProgress
	}
	switch c.Conclusion {
	case ciStatusFailure, "timed_out", "cancelled", "action_required", "stale":
		return checkOrderFailure
	case ciStatusSuccess:
		return checkOrderSuccess
	case "skipped", "neutral":
		return checkOrderSkipped
	default:
		return checkOrderSuccess
	}
}

// forceMergeInterval is the polling interval for check status.
const forceMergeInterval = 10 * time.Second

// waitForChecks polls the PR's statusCheckRollup until checks pass, fail, or the PR is no longer open.
func (a *ActionRunner) waitForChecks(
	ctx context.Context,
	pr PullRequest,
	update *clog.Update,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		state, prState, err := a.queryCheckState(pr.NodeID)
		if err != nil {
			return fmt.Errorf("querying check status for %s: %w", pr.URL, err)
		}

		// Check if PR state changed
		if prState == "MERGED" {
			if update != nil {
				update.Msg("PR already merged").SetSymbol("✔︎").Send()
			}
			return fmt.Errorf("PR already merged: %s", pr.URL)
		}
		if prState == "CLOSED" {
			if update != nil {
				update.Msg("PR was closed").SetSymbol("✘").SetLevel(clog.LevelWarn).Send()
			}
			return fmt.Errorf("PR was closed: %s", pr.URL)
		}

		switch state {
		case checksSuccess:
			if update != nil {
				update.Msg("All checks passed").Send()
			}
			return nil
		case checksFailed:
			if update != nil {
				update.Msg("Checks failed").SetSymbol("✘").SetLevel(clog.LevelError).Send()
			}
			return fmt.Errorf("checks failed for %s", pr.URL)
		case checksPending:
			if update != nil {
				update.Msg("Waiting for checks").Send()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(forceMergeInterval):
			}
		case checksNone:
			// No checks configured - proceed immediately
			if update != nil {
				update.Msg("No checks configured").Send()
			}
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
	case valueCISuccess:
		return checksSuccess, prState, nil
	case valueCIFailure, valueCIError:
		return checksFailed, prState, nil
	case valueCIPending, valueCIExpected:
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
