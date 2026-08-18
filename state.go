package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gechr/clog"
	"github.com/gechr/x/shell"
	goyaml "github.com/goccy/go-yaml"
)

// TUI state is mutable runtime state that prl rewrites as the user interacts
// with the interactive browser (toggling auto-refresh, sorting columns, and
// applying filters). Unlike config, which the user authors and version
// controls, this state churns constantly, so it lives in the XDG state
// directory rather than ~/.config/prl/config.yaml.
//
// The state file mirrors the equivalent config keys exactly - same nesting,
// same key names, same value shapes - so the two are copy-paste compatible.
// State takes precedence over config: config provides first-run defaults,
// state records the settings the user last chose in the TUI.

// tuiState is the persisted subset of TUIConfig, reusing the config types so
// the state file is a structural mirror of the config's `tui:` subtree.
type tuiState struct {
	Filters TUIFiltersConfig     `yaml:"filters"`
	Refresh TUIAutoRefreshConfig `yaml:"refresh"`
	Review  tuiReviewState       `yaml:"review"`
	Sort    TUISortConfig        `yaml:"sort"`
}

// tuiReviewState nests the remembered review defaults under `review.default`,
// mirroring the config's `tui.review.default` subtree.
type tuiReviewState struct {
	Default TUIReviewDefaultConfig `yaml:"default"`
}

// stateDocument is the on-disk shape of the state file: the persisted TUI
// settings nested under a top-level `tui:` key, identical to config.
type stateDocument struct {
	TUI tuiState `yaml:"tui"`
}

// statePath returns the path to the TUI state file, honouring XDG_STATE_HOME.
func statePath() (string, error) {
	dir, err := shell.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "prl", "state.yaml"), nil
}

// loadTUIState reads persisted TUI state. A missing file is not an error and
// returns a nil state, meaning "use config defaults".
func loadTUIState() (*tuiState, error) {
	sp, err := statePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(sp)
	if err != nil {
		if os.IsNotExist(err) {
			// No state file yet: not an error, just "use config defaults".
			return nil, nil //nolint:nilnil // nil state is the documented "absent" signal
		}
		return nil, fmt.Errorf("reading state %s: %w", sp, err)
	}
	var doc stateDocument
	if err := goyaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing state %s: %w", sp, err)
	}
	return &doc.TUI, nil
}

// saveTUIState writes a complete snapshot of the persisted TUI settings, taken
// from cfg, to disk. Writing the full `tui:` subtree every time (rather than
// sparse per-field deltas) keeps the file a faithful, unambiguous mirror of the
// effective settings - e.g. `draft: null` always means "no draft filter".
func saveTUIState(cfg *Config) error {
	sp, err := statePath()
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(filepath.Dir(sp), 0o755); mkErr != nil {
		return fmt.Errorf("creating state directory: %w", mkErr)
	}
	doc := stateDocument{TUI: tuiState{
		Filters: cfg.TUI.Filters,
		Refresh: cfg.TUI.AutoRefresh,
		Review:  tuiReviewState{Default: cfg.TUI.Review.Default},
		Sort:    cfg.TUI.Sort,
	}}
	data, err := goyaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}
	//nolint:gosec // state file, not sensitive
	return os.WriteFile(sp, data, 0o644)
}

// applyTUIState overlays persisted state onto cfg.TUI. The whole persisted
// subtree is authoritative when present, so the user's last interactive choices
// take precedence over config defaults - except the review defaults, where an
// explicit tui.review.default key in the config always wins.
func applyTUIState(cfg *Config, st *tuiState) {
	if st == nil {
		return
	}
	cfg.TUI.Filters = st.Filters
	cfg.TUI.AutoRefresh = st.Refresh
	cfg.TUI.Sort = st.Sort
	applyReviewState(cfg, st.Review.Default)
}

// applyReviewState restores the last-chosen review provider/model/effort for
// the keys the config doesn't pin. The remembered model and effort belong to
// the remembered provider, so they only apply when that provider is the one
// the dialog will open with; stale or invalid values fall back to defaults
// through the configuredReview* normalizers at use time.
func applyReviewState(cfg *Config, saved TUIReviewDefaultConfig) {
	if !cfg.reviewExplicit.provider && saved.Provider != "" {
		if p := normalizeReviewProvider(saved.Provider); p != reviewProviderUnknown &&
			isChoiceValue(reviewProviderChoices(cfg), string(p)) {
			cfg.TUI.Review.Default.Provider = string(p)
		}
	}
	if saved.Provider != string(configuredReviewProvider(cfg)) {
		return
	}
	if !cfg.reviewExplicit.model && saved.Model != "" {
		cfg.TUI.Review.Default.Model = saved.Model
	}
	if !cfg.reviewExplicit.effort && saved.Effort != "" {
		cfg.TUI.Review.Default.Effort = saved.Effort
	}
}

// warnStateSaveErr logs a non-fatal state persistence failure.
func warnStateSaveErr(err error, what string) {
	clog.Warn().Err(err).Msgf("Failed to persist %s", what)
}
