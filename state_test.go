package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatePathHonoursXDGStateHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	sp, err := statePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "prl", "state.yaml"), sp)
}

func TestStatePathFallsBackToLocalState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")

	sp, err := statePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".local", "state", "prl", "state.yaml"), sp)
}

func TestLoadTUIStateMissingFileReturnsNil(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	st, err := loadTUIState()
	require.NoError(t, err)
	require.Nil(t, st)
}

// TestSaveTUIStateMirrorsConfigSubtree verifies the on-disk file mirrors the
// config's tui subtree: nested under `tui:`, booleans as bools, and "no filter"
// rendered as null.
func TestSaveTUIStateMirrorsConfigSubtree(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	cfg := &Config{TUI: TUIConfig{
		AutoRefresh: TUIAutoRefreshConfig{Enabled: true},
		Sort:        TUISortConfig{Key: "updated", Order: "desc"},
		Filters: TUIFiltersConfig{
			State:    valueOpen,
			Bots:     new(false),
			Archived: new(false),
			Draft:    nil, // no draft filter -> null
		},
	}}

	require.NoError(t, saveTUIState(cfg))

	sp, err := statePath()
	require.NoError(t, err)
	data, err := os.ReadFile(sp)
	require.NoError(t, err)

	require.Equal(t, `tui:
  filters:
    state: open
    draft: null
    bots: false
    archived: false
    ci: ""
    review: ""
  refresh:
    enabled: true
  sort:
    key: updated
    order: desc
`, string(data))
}

// TestSaveLoadTUIStateRoundTrip verifies a snapshot survives a save/load cycle.
func TestSaveLoadTUIStateRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	cfg := &Config{TUI: TUIConfig{
		AutoRefresh: TUIAutoRefreshConfig{Enabled: false},
		Sort:        TUISortConfig{Key: "repo", Order: valueAsc},
		Filters: TUIFiltersConfig{
			State:    valueMerged,
			Bots:     new(true),
			Archived: nil,
			CI:       "success",
		},
	}}

	require.NoError(t, saveTUIState(cfg))

	st, err := loadTUIState()
	require.NoError(t, err)
	require.NotNil(t, st)
	require.False(t, st.Refresh.Enabled)
	require.Equal(t, "repo", st.Sort.Key)
	require.Equal(t, valueAsc, st.Sort.Order)
	require.Equal(t, valueMerged, st.Filters.State)
	require.NotNil(t, st.Filters.Bots)
	require.True(t, *st.Filters.Bots)
	require.Nil(t, st.Filters.Archived)
	require.Equal(t, "success", st.Filters.CI)
}

// TestApplyTUIStateOverridesConfig verifies the persisted snapshot takes
// precedence over config defaults, including clearing a bool filter to null.
func TestApplyTUIStateOverridesConfig(t *testing.T) {
	cfg := &Config{TUI: TUIConfig{
		AutoRefresh: TUIAutoRefreshConfig{Enabled: true},
		Sort:        TUISortConfig{Key: "title", Order: valueAsc},
		Filters: TUIFiltersConfig{
			State:    valueMerged,
			Draft:    new(true),
			Bots:     new(false),
			Archived: new(true),
		},
	}}

	applyTUIState(cfg, &tuiState{
		Refresh: TUIAutoRefreshConfig{Enabled: false},
		Sort:    TUISortConfig{Key: "", Order: ""},
		Filters: TUIFiltersConfig{
			State:    "",
			Draft:    nil,       // clears the config default
			Bots:     new(true), // overrides config's false
			Archived: nil,       // clears the config default
		},
	})

	require.False(t, cfg.TUI.AutoRefresh.Enabled)
	require.Empty(t, cfg.TUI.Sort.Key)
	require.Empty(t, cfg.TUI.Sort.Order)
	require.Empty(t, cfg.TUI.Filters.State)
	require.Nil(t, cfg.TUI.Filters.Draft)
	require.NotNil(t, cfg.TUI.Filters.Bots)
	require.True(t, *cfg.TUI.Filters.Bots)
	require.Nil(t, cfg.TUI.Filters.Archived)
}

func TestApplyTUIStateNilLeavesConfigUntouched(t *testing.T) {
	cfg := &Config{TUI: TUIConfig{
		AutoRefresh: TUIAutoRefreshConfig{Enabled: true},
		Sort:        TUISortConfig{Key: "title", Order: valueAsc},
	}}

	applyTUIState(cfg, nil)

	require.True(t, cfg.TUI.AutoRefresh.Enabled)
	require.Equal(t, "title", cfg.TUI.Sort.Key)
	require.Equal(t, valueAsc, cfg.TUI.Sort.Order)
}
