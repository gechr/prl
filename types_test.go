package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOutputFormat(t *testing.T) {
	tests := []struct {
		input string
		want  OutputFormat
		ok    bool
	}{
		{"table", OutputTable, true},
		{"t", OutputTable, true},
		{"url", OutputURL, true},
		{"u", OutputURL, true},
		{"bullet", OutputBullet, true},
		{"b", OutputBullet, true},
		{"json", OutputJSON, true},
		{"j", OutputJSON, true},
		{"repo", OutputRepo, true},
		{"r", OutputRepo, true},
		{"invalid", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseOutputFormat(tt.input)
		require.Equal(t, tt.ok, ok, "parseOutputFormat(%q) ok", tt.input)
		if ok {
			require.Equal(t, tt.want, got, "parseOutputFormat(%q)", tt.input)
		}
	}
}

func TestParseSortField(t *testing.T) {
	tests := []struct {
		input string
		want  SortField
		ok    bool
	}{
		{"name", SortName, true},
		{"n", SortName, true},
		{"created", SortCreated, true},
		{"c", SortCreated, true},
		{"updated", SortUpdated, true},
		{"u", SortUpdated, true},
		{"invalid", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseSortField(tt.input)
		require.Equal(t, tt.ok, ok, "parseSortField(%q) ok", tt.input)
		if ok {
			require.Equal(t, tt.want, got, "parseSortField(%q)", tt.input)
		}
	}
}

func TestParsePRState(t *testing.T) {
	tests := []struct {
		input string
		want  PRState
		ok    bool
	}{
		{"open", StateOpen, true},
		{"o", StateOpen, true},
		{"closed", StateClosed, true},
		{"c", StateClosed, true},
		{"ready", StateReady, true},
		{"r", StateReady, true},
		{"merged", StateMerged, true},
		{"m", StateMerged, true},
		{"all", StateAll, true},
		{"a", StateAll, true},
		{"invalid", 0, false},
	}
	for _, tt := range tests {
		got, ok := parsePRState(tt.input)
		require.Equal(t, tt.ok, ok, "parsePRState(%q) ok", tt.input)
		if ok {
			require.Equal(t, tt.want, got, "parsePRState(%q)", tt.input)
		}
	}
}

func TestRef(t *testing.T) {
	pr := PullRequest{
		Number: 42,
		Repository: Repository{
			Name:          "dockerfiles",
			NameWithOwner: "acme-corp/dockerfiles",
		},
	}

	// Default: includes owner
	refSingleOwner = ""
	require.Equal(t, "acme-corp/dockerfiles#42", pr.Ref())

	// Single owner: omits owner
	refSingleOwner = "acme-corp"
	require.Equal(t, "dockerfiles#42", pr.Ref())

	// Reset for other tests
	refSingleOwner = ""
}

func TestParseCIStatus(t *testing.T) {
	tests := []struct {
		input string
		want  CIStatus
		ok    bool
	}{
		{"success", CISuccess, true},
		{"s", CISuccess, true},
		{"pass", CISuccess, true},
		{"passed", CISuccess, true},
		{"failure", CIFailure, true},
		{"f", CIFailure, true},
		{"fail", CIFailure, true},
		{"failed", CIFailure, true},
		{"pending", CIPending, true},
		{"p", CIPending, true},
		{"invalid", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseCIStatus(tt.input)
		require.Equal(t, tt.ok, ok, "parseCIStatus(%q) ok", tt.input)
		if ok {
			require.Equal(t, tt.want, got, "parseCIStatus(%q)", tt.input)
		}
	}
}
