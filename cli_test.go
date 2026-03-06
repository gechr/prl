package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryString(t *testing.T) {
	tests := []struct {
		name  string
		query []string
		want  string
	}{
		{"plain terms", []string{"branch"}, "branch"},
		{"multiple terms", []string{"fix", "bug"}, "fix bug"},
		{"dash negation", []string{"-branch"}, "NOT branch"},
		{"bang negation", []string{"!branch"}, "NOT branch"},
		{"mixed", []string{"fix", "-branch", "!draft"}, "fix NOT branch NOT draft"},
		{"multi-word negation", []string{"-foo bar"}, `NOT "foo bar"`},
		{"multi-word positive", []string{"foo bar"}, `"foo bar"`},
		{"multi-word mixed", []string{"-branch", "-foo bar"}, `NOT branch NOT "foo bar"`},
		{"bare dash", []string{"-"}, "-"},
		{"bare bang", []string{"!"}, "!"},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &CLI{Query: tt.query}
			require.Equal(t, tt.want, cli.QueryString())
		})
	}
}
