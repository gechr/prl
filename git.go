package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// gitRemoteOwnerRepo returns the GitHub owner and repo name from the current
// directory's git remote origin. Fails if not in a git repo or if the remote
// is not a GitHub URL.
func gitRemoteOwnerRepo() (string, string, error) {
	for _, remote := range []string{"origin", "upstream"} {
		out, err := exec.CommandContext(context.Background(), "git", "remote", "get-url", remote).
			Output()
		if err == nil {
			return parseGitHubRemote(strings.TrimSpace(string(out)))
		}
	}
	return "", "", fmt.Errorf(
		"-R . / -O . requires a git repository with a remote named 'origin' or 'upstream'",
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

func replaceValue(ss []string, find, replace string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		if s == find {
			out[i] = replace
		} else {
			out[i] = s
		}
	}
	return out
}
