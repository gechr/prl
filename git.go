package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// gitRemoteOwnerRepo returns the GitHub owner and repo name from the git
// remote origin (or upstream) of the given directory. Fails if not a git
// repo or if the remote is not a GitHub URL.
func gitRemoteOwnerRepo(dir string) (string, string, error) {
	for _, remote := range []string{"origin", "upstream"} {
		cmd := exec.CommandContext(
			context.Background(),
			"git",
			"-C",
			dir,
			"remote",
			"get-url",
			remote,
		)
		out, err := cmd.Output()
		if err == nil {
			return parseGitHubRemote(strings.TrimSpace(string(out)))
		}
	}
	return "", "", fmt.Errorf(
		"%q must be a git repository with a remote named 'origin' or 'upstream'",
		dir,
	)
}

// parseGitHubRemote parses owner/repo from HTTPS or SSH GitHub remote URLs.
func parseGitHubRemote(remote string) (string, string, error) {
	// SSH: git@github.com:owner/repo.git
	if after, ok := strings.CutPrefix(remote, "git@github.com:"); ok {
		path := strings.TrimSuffix(after, ".git")
		return splitOwnerRepo(path)
	}
	// HTTPS: https://github.com/owner/repo.git  or  https://github.com/owner/repo
	if strings.Contains(remote, "github.com/") {
		_, after, _ := strings.Cut(remote, "github.com/")
		path := strings.TrimSuffix(after, ".git")
		return splitOwnerRepo(path)
	}
	return "", "", fmt.Errorf("remote %q is not a GitHub URL", remote)
}

func splitOwnerRepo(path string) (string, string, error) {
	parts := strings.SplitN(path, "/", 2) //nolint:mnd // self-explanatory
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("could not parse owner/repo from remote path %q", path)
	}
	return parts[0], parts[1], nil
}

// isPathLike reports whether a CLI repo/owner value should be interpreted as
// a filesystem path rather than a GitHub owner or owner/repo slug. GitHub
// owners cannot begin with "." or "/" or "~", so these prefixes unambiguously
// signal a path.
func isPathLike(s string) bool {
	for _, p := range []string{".", "/", "~"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
