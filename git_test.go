package main

import (
	"os"
	"path/filepath"
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

func TestRepoRoot(t *testing.T) {
	t.Parallel()

	// t.TempDir sits under a symlinked path on macOS (/var -> /private/var),
	// and repoRoot resolves before walking, so the wanted roots must too.
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	mkdir := func(parts ...string) string {
		dir := filepath.Join(append([]string{base}, parts...)...)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		return dir
	}
	mark := func(dir string, markers ...string) {
		for _, marker := range markers {
			require.NoError(t, os.Mkdir(filepath.Join(dir, marker), 0o755))
		}
	}

	plain := mkdir("plain")
	mark(plain, dotGit)
	plainDeep := mkdir("plain", "sub", "deep")

	coloc := mkdir("coloc")
	mark(coloc, dotJJ, dotGit)

	pure := mkdir("pure")
	mark(pure, dotJJ)

	// The bug shape: a non-colocated jj repo nested inside an unrelated git
	// checkout. `git -C inner` walks up to outer and reports its remote.
	outer := mkdir("outer")
	mark(outer, dotGit)
	inner := mkdir("outer", "inner")
	mark(inner, dotJJ)
	innerDeep := mkdir("outer", "inner", "sub")

	loose := mkdir("loose")

	// Reaching a repository through a symlink must still name the real root,
	// or the same checkout answers to two different paths.
	linked := filepath.Join(base, "linked")
	require.NoError(t, os.Symlink(plain, linked))

	tests := []struct {
		name     string
		dir      string
		wantRoot string
		wantVCS  string
	}{
		{name: "git repo root", dir: plain, wantRoot: plain, wantVCS: vcsGit},
		{name: "git repo subdirectory", dir: plainDeep, wantRoot: plain, wantVCS: vcsGit},
		{name: "colocated repo reads as git", dir: coloc, wantRoot: coloc, wantVCS: vcsGit},
		{name: "non-colocated jj repo", dir: pure, wantRoot: pure, wantVCS: vcsJJ},
		{
			name:     "nested jj repo wins over git ancestor",
			dir:      inner,
			wantRoot: inner,
			wantVCS:  vcsJJ,
		},
		{
			name:     "nested jj subdirectory wins over git ancestor",
			dir:      innerDeep,
			wantRoot: inner,
			wantVCS:  vcsJJ,
		},
		{
			name:     "symlinked path resolves to real root",
			dir:      linked,
			wantRoot: plain,
			wantVCS:  vcsGit,
		},
		{name: "outside any repository", dir: loose},
		{name: "nonexistent path", dir: filepath.Join(base, "nope")},
		// Resolving before walking is what stops a path that does not exist
		// from being attributed to whichever repository happens to be above it.
		{name: "nonexistent path inside a repository", dir: filepath.Join(plain, "nope")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, vcs := repoRoot(tt.dir)
			require.Equal(t, tt.wantRoot, root)
			require.Equal(t, tt.wantVCS, vcs)
		})
	}
}

// A relative path must still climb out of the directory it names. Note t.Chdir
// sets $PWD to the unresolved path and os.Getwd may hand that back verbatim, so
// resolving after making the path absolute is what keeps the answer canonical.
func TestRepoRootRelativePaths(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	repo := filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, dotGit), 0o755))
	deep := filepath.Join(repo, "sub", "deep")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	t.Chdir(deep)

	for _, dir := range []string{".", "..", filepath.Join("..", "..")} {
		root, vcs := repoRoot(dir)
		require.Equal(t, repo, root, dir)
		require.Equal(t, vcsGit, vcs, dir)
	}
}

func TestJJRemoteURL(t *testing.T) {
	t.Parallel()

	const out = "origin git@github.com:owner/repo\nupstream https://github.com/other/repo\n"

	tests := []struct {
		name   string
		out    string
		remote string
		want   string
	}{
		{name: "origin", out: out, remote: remoteOrigin, want: "git@github.com:owner/repo"},
		{
			name:   "upstream",
			out:    out,
			remote: remoteUpstream,
			want:   "https://github.com/other/repo",
		},
		{name: "absent remote", out: out, remote: "fork"},
		{name: "no remotes configured", out: "", remote: remoteOrigin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, jjRemoteURL(tt.out, tt.remote))
		})
	}
}
