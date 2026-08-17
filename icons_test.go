package main

import (
	"reflect"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func TestParseIconMode(t *testing.T) {
	tests := []struct {
		input string
		want  IconMode
		ok    bool
	}{
		{"auto", IconAuto, true},
		{"unicode", IconUnicode, true},
		{"nerd", IconNerd, true},
		{" NeRd ", IconNerd, true},
		{"UNICODE", IconUnicode, true},
		{"", 0, false},
		{"invalid", 0, false},
	}

	for _, tt := range tests {
		got, ok := parseIconMode(tt.input)
		require.Equal(t, tt.ok, ok, "parseIconMode(%q) ok", tt.input)
		if ok {
			require.Equal(t, tt.want, got, "parseIconMode(%q)", tt.input)
			require.Equal(t, tt.want.String(), got.String())
		}
	}
}

func TestIconModeStringUnknown(t *testing.T) {
	require.Equal(t, valueUnknown, IconMode(-1).String())
}

func TestResolveIconMode(t *testing.T) {
	t.Setenv("NERD_FONTS", "")
	require.Equal(t, IconUnicode, resolveIconMode(IconAuto))

	t.Setenv("NERD_FONTS", "1")
	require.Equal(t, IconNerd, resolveIconMode(IconAuto))
	require.Equal(t, IconUnicode, resolveIconMode(IconUnicode))
	require.Equal(t, IconNerd, resolveIconMode(IconNerd))
}

func TestNerdIconsHaveSingleCellWidth(t *testing.T) {
	icons := reflect.ValueOf(iconsNerd)
	typeOfIcons := icons.Type()
	for i := range icons.NumField() {
		name := typeOfIcons.Field(i).Name
		glyph := icons.Field(i).String()
		require.NotEmpty(t, glyph, name)
		require.Equal(t, 1, lipgloss.Width(glyph), name)
	}
}

func TestUnicodeIcons(t *testing.T) {
	want := iconSet{
		Approved:          "✅",
		Rejected:          "❌",
		Dismissed:         "🥀",
		Commented:         "💬",
		Copilot:           "🤖",
		CIInProgress:      "🔄",
		CISkipped:         "⏭️",
		CITimedOut:        "⏱️",
		CIActionRequired:  "⚠️",
		CINeutral:         "➖",
		CIStale:           "💤",
		CIUnknown:         "❓",
		Check:             "✔︎",
		Cross:             "✘",
		Warning:           "⚠",
		PillLeft:          "",
		PillRight:         "",
		StatusMerged:      "",
		StatusDraft:       "",
		StatusClosed:      "",
		StatusReady:       "",
		StatusCIPending:   "",
		StatusCIFailed:    "",
		StatusNeedsReview: "",
		StatusConflict:    "",
		StatusUnknown:     "",
	}
	require.Equal(t, want, iconsUnicode)
}

func TestUseIcons(t *testing.T) {
	original := activeIcons
	t.Cleanup(func() { activeIcons = original })

	useIcons(iconsNerd)
	require.Equal(t, iconsNerd, activeIcons)
}

func TestNerdCheckIconsUseUnicodeHeavyCheck(t *testing.T) {
	require.Equal(t, "\u2714", iconsNerd.Approved)
	require.Equal(t, "\u2714", iconsNerd.Check)
}

func TestNerdCrossIconsUseUnicodeHeavyBallotX(t *testing.T) {
	require.Equal(t, "\u2718", iconsNerd.Rejected)
	require.Equal(t, "\u2718", iconsNerd.Cross)
	require.Equal(t, "\u2718", iconsNerd.StatusCIFailed)
}

func TestIconsFor(t *testing.T) {
	t.Setenv("NERD_FONTS", "")
	require.Equal(t, iconsUnicode, iconsFor(IconAuto))
	require.Equal(t, iconsUnicode, iconsFor(IconUnicode))
	require.Equal(t, iconsNerd, iconsFor(IconNerd))

	t.Setenv("NERD_FONTS", "1")
	require.Equal(t, iconsNerd, iconsFor(IconAuto))
}
