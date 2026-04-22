package main

import (
	"testing"
	"time"

	clib "github.com/gechr/clib/cli/kong"
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

func TestValidate_AllowsAuthorAndTeamTogether(t *testing.T) {
	author := clib.CSVFlag{Values: []string{"user-1"}}
	cli := &CLI{
		Author: &author,
		Team:   clib.CSVFlag{Values: []string{"ops"}},
	}

	require.NoError(t, cli.Validate())
}

func TestValidate_IntervalRequiresInteractiveOrWatch(t *testing.T) {
	interval := 30 * time.Second
	cli := &CLI{Interval: &interval}

	require.EqualError(t, cli.Validate(), "--interval requires --interactive or --watch")
}

func TestValidate_IntervalMustBePositive(t *testing.T) {
	interval := time.Duration(0)
	cli := &CLI{Interactive: true, Interval: &interval}

	require.EqualError(t, cli.Validate(), "--interval must be greater than 0")
}

func TestValidate_AllowsInteractiveInterval(t *testing.T) {
	interval := 30 * time.Second
	cli := &CLI{Interactive: true, Interval: &interval}

	require.NoError(t, cli.Validate())
}

func TestValidate_AllowsWatchInterval(t *testing.T) {
	interval := 30 * time.Second
	cli := &CLI{Watch: true, Interval: &interval}

	require.NoError(t, cli.Validate())
}

func TestNormalizeSendToRecipient(t *testing.T) {
	tests := []struct {
		name      string
		recipient string
		want      string
	}{
		{name: "bare channel", recipient: "eng-prs", want: "#eng-prs"},
		{name: "channel", recipient: "#eng-prs", want: "#eng-prs"},
		{name: "user handle", recipient: "@alice", want: "@alice"},
		{name: "email", recipient: "alice@example.com", want: "alice@example.com"},
		{name: "channel id", recipient: "C123456", want: "C123456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeSendToRecipient(tt.recipient))
		})
	}
}
