package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveConfiguredPluginPathSupportsShortName(t *testing.T) {
	dir := t.TempDir()
	want := writeExecutable(t, dir, "prl-plugin-example", `#!/bin/sh
exit 0
`)

	resetPluginCacheForTest(t)
	t.Setenv("PATH", dir)

	got, err := resolveConfiguredPluginPath("example")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDiscoverPluginErrorsOnAmbiguousPATH(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "prl-plugin-alpha", `#!/bin/sh
exit 0
`)
	writeExecutable(t, dir, "prl-plugin-beta", `#!/bin/sh
exit 0
`)

	resetPluginCacheForTest(t)
	t.Setenv("PATH", dir)

	plug, err := discoverPlugin(&Config{})
	require.Nil(t, plug)
	require.ErrorContains(t, err, "multiple prl-plugin-* plugins found on PATH")
}

func TestPluginCompleteTreatsExitCodeOneAsNotImplemented(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", `#!/bin/sh
exit 1
`)

	results, err := (&Plugin{path: path}).Complete("author")
	require.NoError(t, err)
	require.Nil(t, results)
}

func TestPluginResolveSurfacesRealFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", `#!/bin/sh
echo boom >&2
exit 2
`)

	results, err := (&Plugin{path: path}).Resolve("team", "ops")
	require.Nil(t, results)
	require.EqualError(t, err, "plugin resolve team ops: exit status 2: boom")
}

func TestPluginSlackParsesRawJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", `#!/bin/sh
cat >/dev/null
printf '{"channel":"#pull-requests"}\n'
`)

	result, err := (&Plugin{path: path}).Slack([]byte(`[]`), "")
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "#pull-requests", result.Messages[0].Channel)
}

func TestPluginSlackParsesLineDelimitedJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", `#!/bin/sh
cat >/dev/null
printf 'INFO posted\n'
printf '{"channel":"#pull-requests","reactions":[":one:",":automerged:"]}\n'
`)

	result, err := (&Plugin{path: path}).Slack([]byte(`[]`), "")
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "#pull-requests", result.Messages[0].Channel)
	require.Equal(t, []string{":one:", ":automerged:"}, result.Messages[0].Reactions)
}

func TestParseSlackSendMessagesParsesArraySingleAndLines(t *testing.T) {
	t.Run("array", func(t *testing.T) {
		messages := parseSlackSendMessages(
			[]byte(`[{"channel":"#a"},{"channel":"#b","reactions":["x"]}]`),
			nil,
		)
		require.Len(t, messages, 2)
		require.Equal(t, "#a", messages[0].Channel)
		require.Equal(t, "#b", messages[1].Channel)
		require.Equal(t, []string{"x"}, messages[1].Reactions)
	})

	t.Run("single", func(t *testing.T) {
		messages := parseSlackSendMessages([]byte(`{"channel":"#a"}`), nil)
		require.Equal(t, []slackSendMessage{{Channel: "#a"}}, messages)
	})

	t.Run("lines", func(t *testing.T) {
		messages := parseSlackSendMessages(
			[]byte("noise\n{\"channel\":\"#a\"}\n{\"reactions\":[\"x\"]}\n"),
			[]string{"noise", `{"channel":"#a"}`, `{"reactions":["x"]}`},
		)
		require.Equal(
			t,
			[]slackSendMessage{{Channel: "#a"}, {Reactions: []string{"x"}}},
			messages,
		)
	})
}

func TestClassifySlackRecipient(t *testing.T) {
	tests := []struct {
		name      string
		recipient string
		wantField string
		wantVals  []string
	}{
		{name: "empty", recipient: "", wantField: "", wantVals: nil},
		{
			name:      "bare channel",
			recipient: "pull-requests",
			wantField: "channel",
			wantVals:  []string{"#pull-requests"},
		},
		{
			name:      "prefixed channel",
			recipient: "#pull-requests",
			wantField: "channel",
			wantVals:  []string{"#pull-requests"},
		},
		{
			name:      "channel id",
			recipient: "C123456",
			wantField: "channel",
			wantVals:  []string{"C123456"},
		},
		{name: "user handle", recipient: "@alice", wantField: "user", wantVals: []string{"@alice"}},
		{
			name:      "email",
			recipient: "alice@example.com",
			wantField: "user",
			wantVals:  []string{"alice@example.com"},
		},
		{
			name:      "group dm emails",
			recipient: "alice@example.com,bob@example.com",
			wantField: "users",
			wantVals:  []string{"alice@example.com", "bob@example.com"},
		},
		{
			name:      "group dm handles",
			recipient: "@alice, @bob",
			wantField: "users",
			wantVals:  []string{"@alice", "@bob"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotField, gotVals := classifySlackRecipient(tt.recipient)
			require.Equal(t, tt.wantField, gotField)
			require.Equal(t, tt.wantVals, gotVals)
		})
	}
}

func TestPluginSlackSurfacesStderrFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", `#!/bin/sh
cat >/dev/null
echo 'ERR failed to authenticate with slack: token missing' >&2
exit 1
`)

	_, err := (&Plugin{path: path}).Slack([]byte(`[]`), "")
	require.EqualError(t, err, "plugin slack: ERR failed to authenticate with slack: token missing")
}

func TestPluginSlackFallsBackToStdoutFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", `#!/bin/sh
cat >/dev/null
echo 'plain failure output'
exit 1
`)

	_, err := (&Plugin{path: path}).Slack([]byte(`[]`), "")
	require.EqualError(t, err, "plugin slack: plain failure output")
}

func TestFilterSkippedPRs(t *testing.T) {
	prs := []PullRequest{
		{
			URL:        "https://github.com/org/alpha/pull/1",
			Repository: Repository{NameWithOwner: "org/alpha"},
		},
		{
			URL:        "https://github.com/org/alpha/pull/2",
			Repository: Repository{NameWithOwner: "org/alpha"},
		},
		{
			URL:        "https://github.com/org/beta/pull/3",
			Repository: Repository{NameWithOwner: "org/beta"},
		},
		{
			URL:        "https://github.com/org/gamma/pull/4",
			Repository: Repository{NameWithOwner: "org/gamma"},
		},
	}

	t.Run("no skipped", func(t *testing.T) {
		got := filterSkippedPRs(prs, nil)
		require.Equal(t, prs, got)
	})

	t.Run("skip by PR URL", func(t *testing.T) {
		got := filterSkippedPRs(prs, []string{"https://github.com/org/alpha/pull/1"})
		require.Len(t, got, 3)
		require.Equal(t, "https://github.com/org/alpha/pull/2", got[0].URL)
	})

	t.Run("skip by repo name", func(t *testing.T) {
		got := filterSkippedPRs(prs, []string{"org/alpha"})
		require.Len(t, got, 2)
		require.Equal(t, "https://github.com/org/beta/pull/3", got[0].URL)
		require.Equal(t, "https://github.com/org/gamma/pull/4", got[1].URL)
	})

	t.Run("skip by repo and URL", func(t *testing.T) {
		got := filterSkippedPRs(prs, []string{"org/beta", "https://github.com/org/gamma/pull/4"})
		require.Len(t, got, 2)
		require.Equal(t, "https://github.com/org/alpha/pull/1", got[0].URL)
		require.Equal(t, "https://github.com/org/alpha/pull/2", got[1].URL)
	})
}

func resetPluginCacheForTest(t *testing.T) {
	t.Helper()

	pluginMu.Lock()
	pluginCache = make(map[pluginCacheKey]pluginDiscoveryResult)
	pluginMu.Unlock()

	t.Cleanup(func() {
		pluginMu.Lock()
		pluginCache = make(map[pluginCacheKey]pluginDiscoveryResult)
		pluginMu.Unlock()
	})
}

func writeExecutable(t *testing.T, dir, name, contents string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o755))
	return path
}
