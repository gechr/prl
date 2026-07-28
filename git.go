package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	fvcs "github.com/gechr/forge/vcs"
	xos "github.com/gechr/x/os"
)

const (
	// Markers naming the root of a checkout.
	dotGit = ".git"
	dotJJ  = ".jj"

	// Remotes consulted for a GitHub URL, in preference order.
	remoteOrigin   = "origin"
	remoteUpstream = "upstream"
)

// gitRemoteOwnerRepo returns the GitHub owner and repo name from the remote
// origin (or upstream) of the repository containing the given directory.
// Fails if the directory is not inside a repository or if the remote is not a
// GitHub URL.
func gitRemoteOwnerRepo(dir string) (string, string, error) {
	root, vcs := repoRoot(dir)
	if vcs == "" {
		return "", "", fmt.Errorf("%q must be inside a git or jj repository", dir)
	}

	for _, remote := range []string{remoteOrigin, remoteUpstream} {
		if remoteURL := repoRemoteURL(root, vcs, remote); remoteURL != "" {
			return parseGitHubRemote(remoteURL)
		}
	}

	return "", "", fmt.Errorf(
		"repository %q has no remote named %q or %q",
		root,
		remoteOrigin,
		remoteUpstream,
	)
}

// repoRoot returns the root of the repository containing dir and the VCS to
// read its remotes with, or "" when dir is not inside a repository prl can
// read. Root resolution is [fvcs.Resolver]'s; what stays here is prl's policy
// about the answer.
//
// Resolving the *nearest* root is what keeps a non-colocated jj repo - one
// with no top-level .git, its git store tucked inside .jj/repo/store/git -
// from being misread. `git -C <dir>` walks up past such a repo and reports an
// unrelated ancestor's remote, which is a wrong answer rather than an error.
func repoRoot(dir string) (string, string) {
	// prl names a directory, not a file. forge resolves a path that does not
	// exist to the repository above it - correct for a file not yet created,
	// wrong for a mistyped --repo, which would silently scope the search to an
	// unrelated repository.
	if ok, _ := xos.Exists(dir); !ok {
		return "", ""
	}

	root, driver := fvcs.NewResolver().RootVCS(dir)
	if root == "" {
		return "", ""
	}

	// forge reports jj for a colocated repo because jj owns the working copy.
	// prl only wants to read a remote, and a colocated repo's git remotes are
	// the same ones, so it prefers git wherever a .git is present and needs no
	// jj binary to answer.
	if ok, _ := xos.Exists(filepath.Join(root, dotGit)); ok {
		return root, vcsGit
	}

	// A mercurial or subversion root: a repository, but not one prl can read a
	// GitHub remote from.
	if driver == "" {
		return "", ""
	}
	return root, driver
}

// repoRemoteURL returns the URL configured for the named remote at root, or ""
// when the VCS cannot supply one.
func repoRemoteURL(root, vcs, remote string) string {
	ctx := context.Background()

	if vcs == vcsJJ {
		out, err := exec.CommandContext(ctx, vcsJJ, "--repository", root, "git", "remote", "list").
			Output()
		if err != nil {
			return ""
		}
		return jjRemoteURL(string(out), remote)
	}

	out, err := exec.CommandContext(ctx, vcsGit, "-C", root, "remote", "get-url", remote).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// jjRemoteURL extracts the URL for the named remote from `jj git remote list`
// output, whose lines are "<name> <url>".
func jjRemoteURL(out, remote string) string {
	for line := range strings.SplitSeq(out, nl) {
		name, remoteURL, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && name == remote {
			return strings.TrimSpace(remoteURL)
		}
	}
	return ""
}

// parseGitHubRemote parses owner/repo from HTTPS or SSH GitHub remote URLs.
func parseGitHubRemote(remote string) (string, string, error) {
	remote = strings.TrimSuffix(strings.TrimSpace(remote), dotGit)

	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		if !strings.EqualFold(u.Hostname(), "github.com") {
			return "", "", fmt.Errorf("remote %q is not a GitHub URL", remote)
		}
		return splitOwnerRepo(strings.TrimPrefix(u.Path, "/"))
	}

	if userHost, path, ok := strings.Cut(remote, ":"); ok {
		if _, host, ok := strings.Cut(userHost, "@"); ok && strings.EqualFold(host, "github.com") {
			return splitOwnerRepo(path)
		}
	}

	return "", "", fmt.Errorf("remote %q is not a GitHub URL", remote)
}

func splitOwnerRepo(path string) (string, string, error) {
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("could not parse owner/repo from remote path %q", path)
}
