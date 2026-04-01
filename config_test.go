package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSaveConfigKeyClearsPersistedSortWithoutPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(t, os.WriteFile(cp, []byte(defaultConfigYAML), 0o600))

	require.NoError(t, saveConfigKey(keyTUISortKey, "repo"))
	require.NoError(t, saveConfigKey(keyTUISortOrder, "asc"))
	require.NoError(t, saveConfigKey(keyTUISortKey, ""))
	require.NoError(t, saveConfigKey(keyTUISortOrder, ""))

	cfg, err := loadConfig()
	require.NoError(t, err)
	require.Empty(t, cfg.TUI.Sort.Key)
	require.Empty(t, cfg.TUI.Sort.Order)

	data, err := os.ReadFile(cp)
	require.NoError(t, err)
	require.Contains(t, string(data), `key: ""`)
	require.Contains(t, string(data), `order: ""`)
	require.True(t, strings.HasSuffix(string(data), "\n"))
	require.False(t, strings.HasSuffix(string(data), "\n\n"))
}

func TestLoadConfigRejectsInvalidClaudeReviewPromptPlaceholder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(
		t,
		os.WriteFile(
			cp,
			[]byte(`tui:
  review:
    providers:
      claude:
        prompt: "Review {unknownPlaceholder}"
`),
			0o600,
		),
	)

	_, err = loadConfig()
	require.ErrorContains(t, err, "invalid tui.review.providers.claude.prompt")
	require.ErrorContains(t, err, "unknown placeholder(s): unknownPlaceholder")
}

func TestLoadConfigRejectsInvalidReviewDefaultModelForProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(
		t,
		os.WriteFile(
			cp,
			[]byte(`tui:
  review:
    default:
      provider: codex
      model: opus
`),
			0o600,
		),
	)

	_, err = loadConfig()
	require.ErrorContains(t, err, `invalid tui.review.default.model "opus" for provider "codex"`)
}

func TestLoadConfigRejectsInvalidReviewDefaultEffortForProviderAndModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(
		t,
		os.WriteFile(
			cp,
			[]byte(`tui:
  review:
    default:
      provider: codex
      model: gpt-5.4
      effort: max
`),
			0o600,
		),
	)

	_, err = loadConfig()
	require.ErrorContains(
		t,
		err,
		`invalid tui.review.default.effort "max" for provider "codex" model "gpt-5.4"`,
	)
}
