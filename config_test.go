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
