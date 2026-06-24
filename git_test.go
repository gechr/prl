package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGitHubRemoteAcceptsGitHubForms(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/owner/repo.git",
		"https://github.com/owner/repo",
		"git@github.com:owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
	} {
		owner, repo, err := parseGitHubRemote(remote)
		require.NoError(t, err, remote)
		require.Equal(t, "owner", owner)
		require.Equal(t, "repo", repo)
	}
}

func TestParseGitHubRemoteRejectsLookalikeHosts(t *testing.T) {
	for _, remote := range []string{
		"https://github.com.evil.tld/owner/repo.git",
		"https://notgithub.com/owner/repo.git",
		"git@github.com.evil.tld:owner/repo.git",
		"ssh://git@notgithub.com/owner/repo.git",
	} {
		_, _, err := parseGitHubRemote(remote)
		require.Error(t, err, remote)
	}
}

func TestParseGitHubRemoteRejectsInvalidPaths(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/owner",
		"https://github.com/owner/repo/extra.git",
		"git@github.com:owner/repo/extra.git",
	} {
		_, _, err := parseGitHubRemote(remote)
		require.Error(t, err, remote)
	}
}
