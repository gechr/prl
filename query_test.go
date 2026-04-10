package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDate_ISO_Passthrough(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2024-01-15", "2024-01-15"},
		{">2024-01-15", ">2024-01-15"},
		{">=2024-01-15", ">=2024-01-15"},
		{"<2024-01-15", "<2024-01-15"},
		{"<=2024-01-15", "<=2024-01-15"},
		{"2024-01-15T10:30:00", "2024-01-15T10:30:00"},
	}
	for _, tt := range tests {
		got, err := parseDate(tt.input)
		require.NoError(t, err, "parseDate(%q)", tt.input)
		require.Equal(t, tt.want, got, "parseDate(%q)", tt.input)
	}
}

func TestParseDate_Today(t *testing.T) {
	got, err := parseDate("today")
	require.NoError(t, err)
	today := time.Now().UTC().Format("2006-01-02")
	want := ">=" + today
	require.Equal(t, want, got)
}

func TestParseDate_Yesterday(t *testing.T) {
	got, err := parseDate("yesterday")
	require.NoError(t, err)
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	require.Equal(t, yesterday, got)
}

func TestParseDate_YesterdayWithOperator(t *testing.T) {
	got, err := parseDate(">yesterday")
	require.NoError(t, err)
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	want := ">" + yesterday
	require.Equal(t, want, got)
}

func TestParseDate_RelativeDays(t *testing.T) {
	got, err := parseDate("3days")
	require.NoError(t, err)
	threeDaysAgo := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	want := ">=" + threeDaysAgo
	require.Equal(t, want, got)
}

func TestParseDate_RelativeWeeks(t *testing.T) {
	got, err := parseDate("2weeks")
	require.NoError(t, err)
	twoWeeksAgo := time.Now().UTC().AddDate(0, 0, -14).Format("2006-01-02")
	want := ">=" + twoWeeksAgo
	require.Equal(t, want, got)
}

func TestParseDate_OperatorFlipping(t *testing.T) {
	tests := []struct {
		input  string
		wantOp string
	}{
		{">2weeks", "<"}, // more than 2 weeks ago -> earlier date
		{"<2weeks", ">"}, // less than 2 weeks ago -> more recent
		{">=3days", "<="},
		{"<=3days", ">="},
	}
	for _, tt := range tests {
		got, err := parseDate(tt.input)
		require.NoError(t, err, "parseDate(%q)", tt.input)
		require.True(t, strings.HasPrefix(got, tt.wantOp),
			"parseDate(%q) = %q, want prefix %q", tt.input, got, tt.wantOp)
	}
}

func TestParseDate_RelativeHours(t *testing.T) {
	expected := ">=" + time.Now().UTC().Add(-2*time.Hour).Format("2006-01-02T15:04:05Z")
	got, err := parseDate("2hours")
	require.NoError(t, err)
	require.Equal(t, expected, got)
}

func TestParseDate_UnitAliases(t *testing.T) {
	aliases := []string{
		"1d",
		"1day",
		"1days",
		"1w",
		"1week",
		"1weeks",
		"1mo",
		"1month",
		"1months",
		"1y",
		"1year",
		"1years",
	}
	for _, a := range aliases {
		got, err := parseDate(a)
		require.NoError(t, err, "parseDate(%q)", a)
		require.NotEqual(t, a, got, "parseDate(%q) was not parsed (returned unchanged)", a)
	}
}

func TestParseDate_CompoundDurations(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		input string
		want  time.Time
		dt    bool // expect datetime format (T15:04:05)
	}{
		// Compound date-only
		{"1y6mo", now.AddDate(-1, -6, 0), false},
		{"1w3d", now.AddDate(0, 0, -10), false},
		{"2y1mo", now.AddDate(-2, -1, 0), false},
		{"1mo2w", now.AddDate(0, -1, -14), false},
		// Compound with datetime units
		{"1d12h", now.AddDate(0, 0, -1).Add(-12 * time.Hour), true},
		{"2h30m", now.Add(-2*time.Hour - 30*time.Minute), true},
		{"1w1h", now.AddDate(0, 0, -7).Add(-1 * time.Hour), true},
		{"1d1h30m", now.AddDate(0, 0, -1).Add(-1*time.Hour - 30*time.Minute), true},
	}
	for _, tt := range tests {
		got, err := parseDate(tt.input)
		require.NoError(t, err, "parseDate(%q)", tt.input)
		var wantDate string
		if tt.dt {
			wantDate = ">=" + tt.want.Format("2006-01-02T15:04:05Z")
		} else {
			wantDate = ">=" + tt.want.Format("2006-01-02")
		}
		require.Equal(t, wantDate, got, "parseDate(%q)", tt.input)
	}
}

func TestParseDate_CompoundWithOperator(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		input  string
		wantOp string
		want   time.Time
	}{
		{">1w3d", "<", now.AddDate(0, 0, -10)},
		{"<1y6mo", ">", now.AddDate(-1, -6, 0)},
		{">=2w1d", "<=", now.AddDate(0, 0, -15)},
		{"<=1mo2w", ">=", now.AddDate(0, -1, -14)},
	}
	for _, tt := range tests {
		got, err := parseDate(tt.input)
		require.NoError(t, err, "parseDate(%q)", tt.input)
		wantDate := tt.wantOp + tt.want.Format("2006-01-02")
		require.Equal(t, wantDate, got, "parseDate(%q)", tt.input)
	}
}

func TestParseDate_CompoundOrderingViolation(t *testing.T) {
	tests := []string{
		"2m5y",
		"1d2w",
		"1h1d",
		"1w1w",
	}
	for _, input := range tests {
		_, err := parseDate(input)
		require.Error(t, err, "parseDate(%q) should error on ordering violation", input)
	}
}

func TestParseDate_Empty(t *testing.T) {
	got, err := parseDate("")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestParseDate_InvalidInput(t *testing.T) {
	tests := []string{
		"foobar",
		"notadate",
		">",
	}
	for _, input := range tests {
		_, err := parseDate(input)
		require.Error(t, err, "parseDate(%q) should error on invalid input", input)
	}
}

func TestParseDrift(t *testing.T) {
	tests := []struct {
		input    string
		wantOp   string
		wantSecs int64
		wantErr  bool
	}{
		{"0", "<=", 0, false},
		{"3600", "<=", 3600, false},
		{"1day", "<=", 86400, false},
		{"2weeks", "<=", 1209600, false},
		{"1week", "<=", 604800, false},
		{">1week", ">", 604800, false},
		{">=1day", ">=", 86400, false},
		{"<1hour", "<", 3600, false},
		{"1mo", "<=", 2592000, false},
		{"1year", "<=", 31536000, false},
		// All single short units
		{"1y", "<=", 31536000, false},
		{"1w", "<=", 604800, false},
		{"1d", "<=", 86400, false},
		{"1h", "<=", 3600, false},
		{"1m", "<=", 60, false},
		{"1s", "<=", 1, false},
		// Long-form unit aliases (single segment)
		{"1sec", "<=", 1, false},
		{"1secs", "<=", 1, false},
		{"1second", "<=", 1, false},
		{"1seconds", "<=", 1, false},
		{"1min", "<=", 60, false},
		{"1mins", "<=", 60, false},
		{"1minute", "<=", 60, false},
		{"1minutes", "<=", 60, false},
		{"1hr", "<=", 3600, false},
		{"1hrs", "<=", 3600, false},
		{"1hours", "<=", 3600, false},
		{"1days", "<=", 86400, false},
		{"1weeks", "<=", 604800, false},
		{"1month", "<=", 2592000, false},
		{"1months", "<=", 2592000, false},
		{"1years", "<=", 31536000, false},
		// Large numbers
		{"100d", "<=", 100 * 86400, false},
		{"365d", "<=", 365 * 86400, false},
		{"52w", "<=", 52 * 604800, false},
		// Zero with unit
		{"0s", "<=", 0, false},
		{"0d", "<=", 0, false},
		// Compound durations
		{"5y2m", "<=", 5*31536000 + 2*60, false},
		{"1w3d", "<=", 1*604800 + 3*86400, false},
		{"1d12h", "<=", 1*86400 + 12*3600, false},
		{"1y6mo", "<=", 1*31536000 + 6*2592000, false},
		{"2h30m", "<=", 2*3600 + 30*60, false},
		{"1w2d3h", "<=", 1*604800 + 2*86400 + 3*3600, false},
		// Full chain: all seven units descending
		{"1y1mo1w1d1h1m1s", "<=", 31536000 + 2592000 + 604800 + 86400 + 3600 + 60 + 1, false},
		// Skipping intermediate units is fine
		{"1y1d", "<=", 31536000 + 86400, false},
		{"1y1s", "<=", 31536000 + 1, false},
		{"1w1h", "<=", 604800 + 3600, false},
		{"1mo1s", "<=", 2592000 + 1, false},
		// Compound with long-form aliases
		{"1year2months", "<=", 31536000 + 2*2592000, false},
		{"1week3days", "<=", 604800 + 3*86400, false},
		{"2hours30minutes", "<=", 2*3600 + 30*60, false},
		{"1day12hrs", "<=", 86400 + 12*3600, false},
		// Mixed short and long-form aliases
		{"1y2months", "<=", 31536000 + 2*2592000, false},
		{"1week3d", "<=", 604800 + 3*86400, false},
		// Compound with every operator
		{">5y2m", ">", 5*31536000 + 2*60, false},
		{">=1w3d", ">=", 604800 + 3*86400, false},
		{"<1d12h", "<", 86400 + 12*3600, false},
		{"<=1w3d", "<=", 1*604800 + 3*86400, false},
		{"=1y6mo", "=", 31536000 + 6*2592000, false},
		{"==2h30m", "==", 2*3600 + 30*60, false},
		// Ordering violations
		{"2m5y", "", 0, true},
		{"1d2w", "", 0, true},
		{"1s1m", "", 0, true},
		{"1h1d", "", 0, true},
		{"1mo1y", "", 0, true},
		{"1s1y", "", 0, true},
		// Duplicate units (non-descending)
		{"1w1w", "", 0, true},
		{"3h3h", "", 0, true},
		{"1d1d", "", 0, true},
		{"1y1y", "", 0, true},
		{"1s1s", "", 0, true},
		// Negative values
		{"-1", "", 0, true},
		{"==-1", "", 0, true},
		{">-100", "", 0, true},
		// Invalid input
		{"", "", 0, true},
		{"abc", "", 0, true},
		{">", "", 0, true},
		{"<=", "", 0, true},
		{"1dfoo", "", 0, true},
		{"1d3", "", 0, true},
		{"foo1d", "", 0, true},
	}
	for _, tt := range tests {
		op, secs, err := parseDrift(tt.input)
		if tt.wantErr {
			require.Error(t, err, "parseDrift(%q)", tt.input)
			continue
		}
		require.NoError(t, err, "parseDrift(%q)", tt.input)
		require.Equal(t, tt.wantOp, op, "parseDrift(%q) op", tt.input)
		require.Equal(t, tt.wantSecs, secs, "parseDrift(%q) secs", tt.input)
	}
}

func TestBuildORQualifier_Single(t *testing.T) {
	got := buildORQualifier("author", []string{"user1"})
	require.Equal(t, "author:user1", got)
}

func TestBuildORQualifier_Multiple(t *testing.T) {
	got := buildORQualifier("author", []string{"user1", "user2", "user3"})
	require.Equal(t, "(author:user1 OR author:user2 OR author:user3)", got)
}

func TestBuildORQualifier_Empty(t *testing.T) {
	got := buildORQualifier("author", []string{})
	require.Empty(t, got)
}

func TestBuildORQualifier_Two(t *testing.T) {
	got := buildORQualifier("author", []string{"a", "b"})
	require.Equal(t, "(author:a OR author:b)", got)
}

func TestBuildOwnerQualifier(t *testing.T) {
	got := buildOwnerQualifier([]string{"acme", "octocat"})
	require.Equal(t, "(user:acme OR user:octocat)", got)
}

func TestBuildExcludedOwnerQualifiers(t *testing.T) {
	got := buildExcludedOwnerQualifiers([]string{"acme", "octocat"})
	require.Equal(t, []string{"-org:acme", "-user:acme", "-org:octocat", "-user:octocat"}, got)
}

func TestBuildSearchQuery_MergesAuthorAndTeamWithoutDuplicates(t *testing.T) {
	author := CSVFlag{Values: []string{"user-1", "user-2"}}
	params, err := buildSearchQuery(&CLI{
		Author: &author,
		Team:   CSVFlag{Values: []string{"sg2"}},
	}, &Config{
		Teams: map[string][]string{
			"sg2": {"user-1", "USER-2"},
		},
	})
	require.NoError(t, err)
	require.Contains(t, params.Query, "(author:user-1 OR author:user-2)")
	require.Equal(t, 1, strings.Count(params.Query, "author:user-1"))
	require.Equal(t, 1, strings.Count(params.Query, "author:user-2"))
	require.NotContains(t, params.Query, "author:USER-2")
}

func TestBuildSearchQuery_UsesOwnerQualifier(t *testing.T) {
	params, err := buildSearchQuery(&CLI{
		Owner: CSVFlag{Values: []string{"acme"}},
	}, &Config{})
	require.NoError(t, err)
	require.Equal(t, "type:pr archived:false state:open user:acme", params.Query)
}

func TestBuildSearchQuery_TopicRequiresPlugin(t *testing.T) {
	resetPluginCacheForTest(t)
	t.Setenv("PATH", "")

	_, err := buildSearchQuery(&CLI{
		Topic: "platform",
	}, &Config{})
	require.ErrorContains(t, err, "--topic requires a plugin")
}
