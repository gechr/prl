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

	changesRequested := open
	changesRequested.ReviewDecision = valueReviewChanges

	behind := ready
	behind.MergeStatus = MergeStatusBehind
	behind.behind = true

	failingDraft := failing
	failingDraft.IsDraft = true

	behindDraft := behind
	behindDraft.IsDraft = true

	closedFailing := failing
	closedFailing.State = valueClosed

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
		{"broken", changesRequested, false, false},
		{"blocked", failing, false, true},
		{"blocked", conflicted, false, true},
		{"blocked", changesRequested, false, true},
		{"blocked", draft, false, false},
		{"blocked", failingDraft, false, false},
		{"blocked", behind, false, false},
		{"outdated", behind, false, true},
		{"outdated", ready, false, false},
		{"outdated", behindDraft, false, false},
		{"blocked", closedFailing, false, false},
		{"blocked", open, false, false},
		{"blocked", pending, false, false},
		{"blocked", ready, false, false},
		{"mergeable", behind, false, false},
		{"ready", behind, false, false},
		{"ci:success", behind, false, true},
		{"approvable", conflicted, false, false},
		{"!mine !draft !approved !review:approved ci:pending", pending, false, true},
		{"!mine !draft !approved !review:approved ci:pending", open, false, false},
		{"draft OR conflict", draft, false, true},
		{"draft OR conflict", conflicted, false, true},
		{"draft OR conflict", open, false, false},
		{"mine draft OR ready", draft, false, false},
		{"mine draft OR ready", ready, false, true},
		{"(mine AND draft) OR approved", draft, true, true},
		{"(mine AND draft) OR approved", draft, false, false},
		{"(mine AND draft) OR approved", approved, false, true},
		{"mine AND (draft OR ready)", ready, true, true},
		{"mine AND (draft OR ready)", ready, false, false},
		{"!(draft OR conflict)", open, false, true},
		{"-(draft OR conflict)", draft, false, false},
		{"((draft))", draft, false, true},
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
		require.Equal(t, tt.want, filter.Matches(tt.pr, presetContext{own: tt.own}), tt.filter)
	}
}

func TestParsePresetFilterRejectsInvalid(t *testing.T) {
	for input, want := range map[string]string{
		"":                  "empty filter",
		" , ":               "empty filter",
		"bogus":             `unknown term "bogus"`,
		"!":                 `unknown term ""`,
		"ci:sometimes":      `unknown CI status "sometimes"`,
		"review:maybe":      `unknown review decision "maybe"`,
		"approvable no":     `unknown term "no"`,
		"scope:team":        `unknown scope "team"`,
		"OR mine":           `missing term before "OR"`,
		"mine OR":           `missing term after "OR"`,
		"mine AND OR draft": `missing term before "OR"`,
		"mine AND":          `missing term after "AND"`,
		"(mine":             `unclosed "("`,
		"mine)":             `unexpected ")"`,
		"()":                `unexpected ")"`,
		"!(":                `missing term after "("`,
		"mine or draft":     `unknown term "or"`,
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

func TestWaitingForCIPresetFollowsSearchScope(t *testing.T) {
	filter, ok := presetFilterByName(nil, "Waiting for CI")
	require.True(t, ok)

	pending := approvalTestPR(1)
	pending.MergeStatus = MergeStatusCIPending

	approved := pending
	approved.viewerApproved = true

	reviewed := pending
	reviewed.ReviewDecision = valueReviewApproved

	draft := pending
	draft.IsDraft = true

	passing := approvalTestPR(1)

	tests := []struct {
		name string
		pr   PullRequest
		ctx  presetContext
		want bool
	}{
		{"own PR, own search", pending, presetContext{own: true, selfScope: true}, true},
		{"own approved PR, own search", approved, presetContext{own: true, selfScope: true}, true},
		{"own draft, own search", draft, presetContext{own: true, selfScope: true}, false},
		{"own PR with CI passing", passing, presetContext{own: true, selfScope: true}, false},
		{"own PR, wider search", pending, presetContext{own: true}, false},
		{"other PR, wider search", pending, presetContext{}, true},
		{"other PR you approved", approved, presetContext{}, false},
		{"other PR already approved", reviewed, presetContext{}, false},
		{"other draft", draft, presetContext{}, false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, filter.Matches(tt.pr, tt.ctx), tt.name)
	}
}
