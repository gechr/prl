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
