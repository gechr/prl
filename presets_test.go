package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPresetFilterMatches(t *testing.T) {
	open := approvalTestPR(1)
	open.MergeStatus = MergeStatusBlocked

	approved := open
	approved.viewerApproved = true

	draft := open
	draft.IsDraft = true

	failing := open
	failing.MergeStatus = MergeStatusCIFailed

	ready := open
	ready.MergeStatus = MergeStatusReady
	ready.ReviewDecision = valueReviewApproved

	readyDraft := ready
	readyDraft.IsDraft = true

	conflicted := open
	conflicted.MergeStatus = MergeStatusConflict

	pending := open
	pending.MergeStatus = MergeStatusCIPending

	tests := []struct {
		filter string
		pr     PullRequest
		own    bool
		want   bool
	}{
		{"approvable", open, false, true},
		{"approvable", open, true, false},
		{"approvable", approved, false, false},
		{"approvable", draft, false, false},
		{"approvable", failing, false, false},
		{"approvable", ready, false, false},
		{"mergeable", ready, false, true},
		{"mergeable", readyDraft, false, false},
		{"mergeable", open, false, false},
		{"!mine !draft !approved !review:approved", ready, false, false},
		{"conflict", conflicted, false, true},
		{"conflict", failing, false, false},
		{"broken", conflicted, false, true},
		{"broken", failing, false, true},
		{"broken", pending, false, false},
		{"approvable", conflicted, false, false},
		{"!mine !draft !approved !review:approved ci:pending", pending, false, true},
		{"!mine !draft !approved !review:approved ci:pending", open, false, false},
		{"!mine !draft !approved", failing, false, true},
		{"-mine,-draft,-approved", approved, false, false},
		{"approved !mine", approved, false, true},
		{"ready", ready, false, true},
		{"ready", open, false, false},
		{"ci:failure", failing, false, true},
		{"ci:pass", open, false, true},
		{"!ci:failure", failing, false, false},
		{"review:required", open, false, true},
		{"review:changes-requested", open, false, false},
		{"review:approved", ready, false, true},
		{"mine draft", draft, true, true},
		{"MINE", open, true, true},
	}
	for _, tt := range tests {
		filter, err := parsePresetFilter(tt.filter)
		require.NoError(t, err, tt.filter)
		require.Equal(t, tt.want, filter.Matches(tt.pr, tt.own), tt.filter)
	}
}

func TestParsePresetFilterRejectsInvalid(t *testing.T) {
	for input, want := range map[string]string{
		"":              "empty filter",
		" , ":           "empty filter",
		"bogus":         `unknown term "bogus"`,
		"!":             `unknown term ""`,
		"ci:sometimes":  `unknown CI status "sometimes"`,
		"review:maybe":  `unknown review decision "maybe"`,
		"approvable no": `unknown term "no"`,
	} {
		_, err := parsePresetFilter(input)
		require.EqualError(t, err, want, input)
	}
}

func TestValidatePresets(t *testing.T) {
	require.NoError(t, validatePresets(defaultPresets))
	require.NoError(t, validatePresets(nil))
	require.EqualError(
		t,
		validatePresets([]TUIPresetConfig{{Filter: "ready"}}),
		"tui.presets.filters[0]: name is required",
	)
	require.EqualError(
		t,
		validatePresets([]TUIPresetConfig{
			{Name: "Ready", Filter: "ready"},
			{Name: "Ready", Filter: "approvable"},
		}),
		`tui.presets.filters: duplicate name "Ready"`,
	)
	require.EqualError(
		t,
		validatePresets([]TUIPresetConfig{{Name: "Odd", Filter: "sometimes"}}),
		`tui.presets.filters "Odd": unknown term "sometimes"`,
	)
	require.EqualError(
		t,
		validatePresets([]TUIPresetConfig{{Name: "Odd", Filter: "ready", Color: "chartreuse"}}),
		`tui.presets.filters "Odd": unknown colour "chartreuse"`,
	)
}

func TestParsePresetColor(t *testing.T) {
	for _, valid := range []string{"", "green", "Blue", "#1f6feb", "#abc", "0", "255"} {
		_, err := parsePresetColor(valid)
		require.NoError(t, err, valid)
	}
	for _, invalid := range []string{"chartreuse", "#12", "256", "-1", "#zzzzzz"} {
		_, err := parsePresetColor(invalid)
		require.Error(t, err, invalid)
	}
}
