package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveConfiguredPluginPathSupportsShortName(t *testing.T) {
	dir := t.TempDir()
	want := writeExecutable(t, dir, "prl-plugin-example", "#!/bin/sh\nexit 0\n")

	resetPluginCacheForTest(t)
	t.Setenv("PATH", dir)

	got, err := resolveConfiguredPluginPath("example")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDiscoverPluginErrorsOnAmbiguousPATH(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "prl-plugin-alpha", "#!/bin/sh\nexit 0\n")
	writeExecutable(t, dir, "prl-plugin-beta", "#!/bin/sh\nexit 0\n")

	resetPluginCacheForTest(t)
	t.Setenv("PATH", dir)

	plug, err := discoverPlugin(&Config{})
	require.Nil(t, plug)
	require.ErrorContains(t, err, "multiple prl-plugin-* plugins found on PATH")
}

func TestPluginCompleteTreatsExitCodeOneAsNotImplemented(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", "#!/bin/sh\nexit 1\n")

	results, err := (&Plugin{path: path}).Complete("author")
	require.NoError(t, err)
	require.Nil(t, results)
}

func TestPluginResolveSurfacesRealFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "prl-plugin-example", "#!/bin/sh\necho boom >&2\nexit 2\n")

	results, err := (&Plugin{path: path}).Resolve("team", "ops")
	require.Nil(t, results)
	require.ErrorContains(t, err, "boom")
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
