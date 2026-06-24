package main

import (
	"context"
	"fmt"
	"net/url"
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
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ".git")

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
