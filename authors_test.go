package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorResolverUsesBuiltInAliases(t *testing.T) {
	resolver := NewAuthorResolver(&Config{})
	require.Equal(t, "Copilot", resolver.Resolve(copilotReviewer))
}

func TestAuthorResolverConfigOverridesBuiltInAliases(t *testing.T) {
	resolver := NewAuthorResolver(&Config{
		Authors: map[string]string{
			copilotReviewer: "Custom Copilot",
		},
	})
	require.Equal(t, "Custom Copilot", resolver.Resolve(copilotReviewer))
}

func TestAuthorResolverFallsBackToConfigWhenPluginDiscoveryFails(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "prl-plugin-alpha", `#!/bin/sh
exit 0
`)
	writeExecutable(t, dir, "prl-plugin-beta", `#!/bin/sh
exit 0
`)

	resetPluginCacheForTest(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	resolver := NewAuthorResolver(&Config{
		Authors: map[string]string{
			"alice": "Alice Example",
		},
	})
	require.Equal(t, "Alice Example", resolver.Resolve("alice"))
}

func TestCompleteAuthorsFallsBackToConfigWhenPluginDiscoveryFails(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "prl-plugin-alpha", `#!/bin/sh
exit 0
`)
	writeExecutable(t, dir, "prl-plugin-beta", `#!/bin/sh
exit 0
`)

	resetPluginCacheForTest(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	results := completeAuthors(&Config{
		Authors: map[string]string{
			"alice": "Alice Example",
		},
	})
	require.Equal(t, []string{
		"@me\tCurrent user",
		"all\tAll authors",
		"alice\tAlice Example",
	}, results)
}

func TestCompleteAuthorsIncludesConfigAuthorsAlongsidePluginResults(t *testing.T) {
	dir := t.TempDir()
	pluginPath := writeExecutable(
		t,
		dir,
		"prl-plugin-example",
		`#!/bin/sh
printf '@me\tCurrent user\nall\tAll authors\n'
`,
	)

	resetPluginCacheForTest(t)

	results := completeAuthors(&Config{
		Plugin: pluginPath,
		Authors: map[string]string{
			"alice": "Alice Example",
		},
	})
	require.Equal(t, []string{
		"@me\tCurrent user",
		"all\tAll authors",
		"alice\tAlice Example",
	}, results)
}

func TestCompleteAuthorsAppendsBotSuffixForPluginProvidedBots(t *testing.T) {
	dir := t.TempDir()
	pluginPath := writeExecutable(
		t,
		dir,
		"prl-plugin-example",
		`#!/bin/sh
if [ "$1" = "complete" ] && [ "$2" = "authors" ]; then
	printf 'dependabot\tDependabot\nalice\tAlice Example\n'
	exit 0
fi
if [ "$1" = "resolve" ] && [ "$2" = "bots" ]; then
	printf 'dependabot\n'
	exit 0
fi
exit 1
`,
	)

	resetPluginCacheForTest(t)

	results := completeAuthors(&Config{Plugin: pluginPath})
	require.Equal(t, []string{
		"@me\tCurrent user",
		"all\tAll authors",
		"dependabot[bot]\tDependabot",
		"alice\tAlice Example",
	}, results)
}
