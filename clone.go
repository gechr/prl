package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
)

// cloneTarget represents a repository to clone with an optional branch.
type cloneTarget struct {
	NameWithOwner string
	Branch        string // non-empty when exactly one PR exists for this repo
	Number        int    // PR number when exactly one PR exists for this repo
}

// cloneRepos clones unique repositories from the given PRs in parallel.
// It uses the SSH remote format (git@github.com:org/repo) and the VCS
// configured via PRL_VCS (default: git).
// When a repo has exactly one PR, the PR's head branch is checked out via --branch.
func cloneRepos(rest *api.RESTClient, prs []PullRequest, vcs string) error {
	ctx := context.Background()
	targets := buildCloneTargets(rest, prs)
	if len(targets) == 0 {
		return nil
	}

	// If all repos share the same org, omit it from display names.
	displayName := func(nwo string) string { return nwo }
	if prefix := commonOrgPrefix(targets); prefix != "" {
		displayName = func(nwo string) string { return strings.TrimPrefix(nwo, prefix) }
	}

	// prLink returns the clog key, URL, and display label for a target.
	// Single-PR targets render as pr=repo#123 linking to the PR;
	// multi-PR targets render as repo=repo linking to the repo.
	prLink := func(t cloneTarget) (key, url, label string) {
		repoURL := "https://github.com/" + t.NameWithOwner
		name := displayName(t.NameWithOwner)
		if t.Number > 0 {
			return "pr", fmt.Sprintf("%s/pull/%d", repoURL, t.Number), fmt.Sprintf("%s#%d", name, t.Number)
		}
		return "repo", repoURL, name
	}

	useJJ := strings.EqualFold(vcs, vcsJJ)

	// First pass: determine which targets will actually be cloned (skip existing).
	var toClone []cloneTarget
	for _, t := range targets {
		dir := cloneDir(t)
		if _, err := os.Stat(dir); err == nil {
			key, url, label := prLink(t)
			clog.Warn().
				Link(key, url, label).
				Msg("Skipping (already exists)")
			continue
		}
		toClone = append(toClone, t)
	}

	total := len(toClone)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []string
	var cloned atomic.Int32

	// Limit concurrent clones to avoid overwhelming the network/system.
	sem := make(chan struct{}, maxConcurrency)

	for _, t := range toClone {
		key, url, label := prLink(t)
		ev := clog.Info().Link(key, url, label)
		if t.Branch != "" {
			branchURL := "https://github.com/" + t.NameWithOwner + "/tree/" + t.Branch
			ev = ev.Link("branch", branchURL, t.Branch)
		}
		ev.Msg("Cloning")

		wg.Add(1)
		go func(target cloneTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// SSH-only: uses git@github.com:org/repo format.
			remote := "git@github.com:" + target.NameWithOwner
			if err := runClone(ctx, remote, target.Branch, cloneDir(target), useJJ); err != nil {
				mu.Lock()
				failed = append(failed, target.NameWithOwner)
				mu.Unlock()
			}
			n := cloned.Add(1)
			key, url, label := prLink(target)
			clog.Info().
				Link(key, url, label).
				Str("progress", fmt.Sprintf("%d/%d", n, total)).
				Msg("Cloned")
		}(t)
	}
	wg.Wait()

	if len(failed) > 0 {
		if total == 1 {
			return fmt.Errorf("clone failed")
		}
		return fmt.Errorf("%d of %d clones failed (%s)",
			len(failed), total, strings.Join(failed, ", "))
	}

	if total > 0 {
		clog.Info().
			Int("count", total).
			Msgf("All %s cloned", pluralize(total, "repository", "repositories"))
	}
	return nil
}

// cloneDir returns the local directory name for a clone target.
// When the target has a PR number (single-PR repo), the format is "repo#123".
// Otherwise it falls back to just the repo name.
func cloneDir(t cloneTarget) string {
	repo := filepath.Base(t.NameWithOwner)
	if t.Number > 0 {
		return fmt.Sprintf("%s#%d", repo, t.Number)
	}
	return repo
}

// runClone executes the clone command for a single repository.
func runClone(ctx context.Context, remote, branch, dir string, useJJ bool) error {
	var args []string
	if useJJ {
		args = append(args, "git", "clone", "--quiet")
	} else {
		args = append(args, "clone", "--quiet")
	}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, remote, dir)

	bin := "git"
	if useJJ {
		bin = "jj"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// buildCloneTargets groups PRs by repo and resolves branch names for single-PR repos in parallel.
func buildCloneTargets(rest *api.RESTClient, prs []PullRequest) []cloneTarget {
	// Group PRs by NameWithOwner
	grouped := make(map[string][]PullRequest)
	var order []string
	for _, pr := range prs {
		nwo := pr.Repository.NameWithOwner
		if _, ok := grouped[nwo]; !ok {
			order = append(order, nwo)
		}
		grouped[nwo] = append(grouped[nwo], pr)
	}
	sort.Strings(order)

	// Identify which repos need branch resolution
	type branchResult struct {
		nwo    string
		branch string
	}

	var wg sync.WaitGroup
	results := make(chan branchResult, len(order))

	for _, nwo := range order {
		repoPRs := grouped[nwo]
		if len(repoPRs) == 1 {
			wg.Add(1)
			go func(nameWithOwner string, number int) {
				defer wg.Done()
				results <- branchResult{
					nwo:    nameWithOwner,
					branch: fetchHeadBranch(rest, nameWithOwner, number),
				}
			}(nwo, repoPRs[0].Number)
		}
	}

	wg.Wait()
	close(results)

	// Collect branch results into a map
	branches := make(map[string]string)
	for r := range results {
		branches[r.nwo] = r.branch
	}

	targets := make([]cloneTarget, 0, len(order))
	for _, nwo := range order {
		t := cloneTarget{
			NameWithOwner: nwo,
			Branch:        branches[nwo],
		}
		if repoPRs := grouped[nwo]; len(repoPRs) == 1 {
			t.Number = repoPRs[0].Number
		}
		targets = append(targets, t)
	}
	return targets
}

// commonOrgPrefix returns "org/" if all targets share the same org, otherwise "".
func commonOrgPrefix(targets []cloneTarget) string {
	if len(targets) == 0 {
		return ""
	}
	org, _, _ := strings.Cut(targets[0].NameWithOwner, "/")
	prefix := org + "/"
	for _, t := range targets[1:] {
		if !strings.HasPrefix(t.NameWithOwner, prefix) {
			return ""
		}
	}
	return prefix
}

// fetchHeadBranch fetches the head branch name for a PR via the GitHub REST API.
func fetchHeadBranch(rest *api.RESTClient, nwo string, number int) string {
	var pr struct {
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	path := fmt.Sprintf("repos/%s/pulls/%d", nwo, number)
	if err := rest.Get(path, &pr); err != nil {
		return ""
	}
	return pr.Head.Ref
}
