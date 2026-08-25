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
	content := string(data)
	require.Equal(t, 1, strings.Count(content, "icons: auto"+nl))

	sortIdx := strings.Index(content, "  sort:"+nl)
	require.NotEqual(t, -1, sortIdx)
	sortBlock := "  sort:" + nl + `    key: ""` + nl + `    order: ""` + nl
	require.Equal(t, sortBlock, content[sortIdx:sortIdx+len(sortBlock)])

	tail := "team_aliases: {}" + nl
	require.Equal(t, tail, content[len(content)-len(tail):])
}

func TestLoadConfigRejectsInvalidIcons(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(t, os.WriteFile(cp, []byte("icons: bogus"+nl), 0o600))

	_, err = loadConfig()
	require.EqualError(
		t,
		err,
		`invalid icons "bogus" (expected "auto", "unicode", or "nerd")`,
	)
}

func TestLoadConfigRejectsInvalidAIReviewPromptPlaceholder(t *testing.T) {
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
	require.EqualError(
		t,
		err,
		"invalid tui.review.providers.claude.prompt: unknown placeholder(s): unknownPlaceholder (available: prNumber, repo, owner, ownerWithRepo, prURL, prRef, title)",
	)
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
      model: banana
`),
			0o600,
		),
	)

	_, err = loadConfig()
	require.EqualError(t, err, `invalid tui.review.default.model "banana" for provider "codex"`)
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
	require.EqualError(
		t,
		err,
		`invalid tui.review.default.effort "max" for provider "codex" model "gpt-5.4"`,
	)
}

func TestLoadConfigUsesConfiguredReviewModelAndEffortChoices(t *testing.T) {
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
    providers:
      codex:
        models: [gpt-5.5, gpt-5.5-mini]
        efforts: [minimal, deep]
`),
			0o600,
		),
	)

	cfg, err := loadConfig()
	require.NoError(t, err)
	require.Equal(t, "gpt-5.5", cfg.TUI.Review.Default.Model)
	require.Equal(t, "minimal", cfg.TUI.Review.Default.Effort)
}

func TestLoadConfigUsesConfiguredReviewProviders(t *testing.T) {
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
    enabled: [codex, claude]
`),
			0o600,
		),
	)

	cfg, err := loadConfig()
	require.NoError(t, err)
	require.Equal(t, []string{"codex", "claude"}, cfg.TUI.Review.Enabled)
	require.Equal(t, "codex", cfg.TUI.Review.Default.Provider)
}

func TestLoadConfigNormalizesDefaultColumns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(
		t,
		os.WriteFile(
			cp,
			[]byte(`default:
  columns: [" Title ", REF, ""]
`),
			0o600,
		),
	)

	cfg, err := loadConfig()
	require.NoError(t, err)
	require.Equal(t, []string{colTitle, colRef}, cfg.Default.Columns)
}

func TestLoadConfigRejectsInvalidDefaultColumns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(
		t,
		os.WriteFile(
			cp,
			[]byte(`default:
  columns: [title, bogus]
`),
			0o600,
		),
	)

	_, err = loadConfig()
	require.EqualError(t, err, `invalid default.columns entry "bogus"`)
}

func TestLoadConfigMigratesLegacySlackDefaultOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(
		t,
		os.WriteFile(
			cp,
			[]byte(`default:
  output: slack
`),
			0o600,
		),
	)

	cfg, err := loadConfig()
	require.NoError(t, err)
	require.Equal(t, valueTable, cfg.Default.Output)
}
