package main

import (
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"

	lg "charm.land/lipgloss/v2"
)

// TUIPresetsConfig holds the TUI's presets.
type TUIPresetsConfig struct {
	// Filters are the named filters the f key cycles through.
	Filters []TUIPresetConfig `koanf:"filters" yaml:"filters"`
}

// TUIPresetConfig is a named filter that the TUI's f key cycles through.
type TUIPresetConfig struct {
	Name   string `koanf:"name"   yaml:"name"`
	Filter string `koanf:"filter" yaml:"filter"`
	// Color tints the name in the TUI: a name (green, blue, ...), a hex
	// value (#1f6feb) or an ANSI number (0-255).
	Color string `koanf:"color" yaml:"color,omitempty"`
}

// defaultPresets apply when the config sets no tui.presets.filters.
var defaultPresets = []TUIPresetConfig{
	{Name: "Approvable", Filter: presetTermApprovable, Color: "blue"},
	{
		Name:   "Waiting for CI",
		Filter: "!mine !draft !approved !review:approved ci:pending",
		Color:  "208",
	},
	{Name: "Approved by you", Filter: "approved !mine", Color: "green"},
	{Name: "Mergeable", Filter: presetTermMergeable, Color: "magenta"},
	{Name: "Outdated", Filter: presetTermOutdated, Color: "yellow"},
	{Name: "Blocked", Filter: presetTermBlocked, Color: "red"},
}

// Preset filter terms.
const (
	presetTermApproved   = "approved"
	presetTermApprovable = "approvable"
	presetTermBlocked    = "blocked"
	presetTermBroken     = "broken"
	presetTermConflict   = "conflict"
	presetTermDraft      = "draft"
	presetTermMergeable  = "mergeable"
	presetTermMine       = "mine"
	presetTermOutdated   = "outdated"
	presetTermReady      = "ready"
	presetPrefixCI       = "ci:"
	presetPrefixReview   = "review:"
)

// presetFilter is a parsed preset filter: every term must match.
type presetFilter []presetTerm

type presetTerm struct {
	negate bool
	match  func(pr PullRequest, own bool) bool
}

// parsePresetFilter parses space- or comma-separated terms, each optionally
// negated with a leading "!" or "-":
//
//	mine, draft, approved (by you), approvable, mergeable, ready, conflict,
//	broken (failing CI or merge conflicts), blocked (not a draft, and broken
//	or changes requested), outdated (not a draft, and behind a base branch
//	that requires it to be up to date),
//	ci:<success|failure|pending>, review:<required|approved|changes_requested|none>
func parsePresetFilter(s string) (presetFilter, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' })
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty filter")
	}
	filter := make(presetFilter, 0, len(fields))
	for _, field := range fields {
		name := strings.TrimLeft(field, "!-")
		match, err := presetTermMatcher(strings.ToLower(name))
		if err != nil {
			return nil, err
		}
		filter = append(filter, presetTerm{negate: name != field, match: match})
	}
	return filter, nil
}

func presetTermMatcher(name string) (func(PullRequest, bool) bool, error) {
	switch name {
	case presetTermMine:
		return func(_ PullRequest, own bool) bool { return own }, nil
	case presetTermDraft:
		return func(pr PullRequest, _ bool) bool { return pr.IsDraft }, nil
	case presetTermApproved:
		return func(pr PullRequest, _ bool) bool { return isViewerApproved(pr) }, nil
	case presetTermApprovable:
		// Open, not yours, not a draft, CI passing, and still awaiting
		// approval: neither you nor the PR as a whole has approved it.
		return func(pr PullRequest, own bool) bool {
			return !own && !pr.IsDraft && !isViewerApproved(pr) &&
				(!pr.reviewDecisionLoaded || pr.ReviewDecision != valueReviewApproved) &&
				strings.EqualFold(pr.State, valueOpen) && matchesCI(pr, CISuccess)
		}, nil
	case presetTermMergeable:
		// Open, not a draft, approved, CI passing and no conflicts.
		return func(pr PullRequest, _ bool) bool {
			return !pr.IsDraft && strings.EqualFold(pr.State, valueOpen) &&
				pr.MergeStatus == MergeStatusReady
		}, nil
	case presetTermConflict:
		return func(pr PullRequest, _ bool) bool { return pr.MergeStatus == MergeStatusConflict }, nil
	case presetTermBroken:
		// Something the author must fix: failing CI or merge conflicts.
		return func(pr PullRequest, _ bool) bool {
			return matchesCI(pr, CIFailure) || pr.MergeStatus == MergeStatusConflict
		}, nil
	case presetTermBlocked:
		// Open, not a draft, and unmergeable until someone acts, as opposed to
		// waiting on review or running CI.
		return func(pr PullRequest, _ bool) bool {
			return !pr.IsDraft && strings.EqualFold(pr.State, valueOpen) &&
				(matchesCI(pr, CIFailure) ||
					pr.MergeStatus == MergeStatusConflict ||
					deriveReviewStatus(pr) == valueReviewFilterChanges)
		}, nil
	case presetTermOutdated:
		return func(pr PullRequest, _ bool) bool {
			return !pr.IsDraft && strings.EqualFold(pr.State, valueOpen) && pr.behind
		}, nil
	case presetTermReady:
		return func(pr PullRequest, _ bool) bool { return pr.MergeStatus == MergeStatusReady }, nil
	}
	if value, ok := strings.CutPrefix(name, presetPrefixCI); ok {
		ci, valid := parseCIStatus(value)
		if !valid {
			return nil, fmt.Errorf("unknown CI status %q", value)
		}
		return func(pr PullRequest, _ bool) bool { return matchesCI(pr, ci) }, nil
	}
	if value, ok := strings.CutPrefix(name, presetPrefixReview); ok {
		review := normalizeReviewFilterValue(value)
		switch review {
		case valueReviewFilterRequired, valueReviewFilterApproved,
			valueReviewFilterChanges, valueReviewFilterNone:
		default:
			return nil, fmt.Errorf("unknown review decision %q", value)
		}
		return func(pr PullRequest, _ bool) bool { return deriveReviewStatus(pr) == review }, nil
	}
	return nil, fmt.Errorf("unknown term %q", name)
}

// Matches reports whether pr satisfies every term. own is whether the viewer
// authored pr.
func (f presetFilter) Matches(pr PullRequest, own bool) bool {
	for _, term := range f {
		if term.match(pr, own) == term.negate {
			return false
		}
	}
	return true
}

// configuredPresets returns the presets in cycle order.
func configuredPresets(cfg *Config) []TUIPresetConfig {
	if cfg == nil || len(cfg.TUI.Presets.Filters) == 0 {
		return defaultPresets
	}
	return cfg.TUI.Presets.Filters
}

// presetFilterByName returns the parsed filter for a named preset.
func presetFilterByName(cfg *Config, name string) (presetFilter, bool) {
	preset, ok := presetByName(cfg, name)
	if !ok {
		return nil, false
	}
	filter, err := parsePresetFilter(preset.Filter)
	return filter, err == nil
}

func presetByName(cfg *Config, name string) (TUIPresetConfig, bool) {
	if name == "" {
		return TUIPresetConfig{}, false
	}
	for _, preset := range configuredPresets(cfg) {
		if preset.Name == name {
			return preset, true
		}
	}
	return TUIPresetConfig{}, false
}

// presetColorNames maps colour names to the terminal's ANSI palette, so they
// follow the user's theme.
var presetColorNames = map[string]string{
	"black":   "0",
	"red":     "1",
	"green":   "2",
	"yellow":  "3",
	"blue":    "4",
	"magenta": "5",
	"cyan":    "6",
	"white":   "7",
	"gray":    "8",
	"grey":    "8",
}

var hexColorPattern = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// parsePresetColor parses a preset colour; an empty value means none.
func parsePresetColor(s string) (color.Color, error) {
	value := strings.ToLower(strings.TrimSpace(s))
	if value == "" {
		return nil, nil //nolint:nilnil // nil colour is the documented "no colour"
	}
	if ansi, ok := presetColorNames[value]; ok {
		return lg.Color(ansi), nil
	}
	if hexColorPattern.MatchString(value) {
		return lg.Color(value), nil
	}
	if n, err := strconv.Atoi(value); err == nil && n >= 0 && n <= 255 {
		return lg.Color(value), nil
	}
	return nil, fmt.Errorf("unknown colour %q", s)
}

// validatePresets checks configured presets for names and parseable filters.
func validatePresets(presets []TUIPresetConfig) error {
	seen := make(map[string]bool, len(presets))
	for i, preset := range presets {
		if strings.TrimSpace(preset.Name) == "" {
			return fmt.Errorf("tui.presets.filters[%d]: name is required", i)
		}
		if seen[preset.Name] {
			return fmt.Errorf("tui.presets.filters: duplicate name %q", preset.Name)
		}
		seen[preset.Name] = true
		if _, err := parsePresetFilter(preset.Filter); err != nil {
			return fmt.Errorf("tui.presets.filters %q: %w", preset.Name, err)
		}
		if _, err := parsePresetColor(preset.Color); err != nil {
			return fmt.Errorf("tui.presets.filters %q: %w", preset.Name, err)
		}
	}
	return nil
}
