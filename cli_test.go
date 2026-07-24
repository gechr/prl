package main

import (
	"testing"
	"time"

	clib "github.com/gechr/clib/cli/kong"
	"github.com/stretchr/testify/require"
)

func TestNormalize_NegatedTeamImpliesAuthorAny(t *testing.T) {
	c := &CLI{Team: CSVFlag{Values: []string{"!ops"}}}
	c.Normalize(&Config{})
	require.NotNil(t, c.Author)
	require.Equal(t, []string{valueAll}, c.Author.Values)
}

func TestNormalize_PositiveTeamOverridesDefaultAuthors(t *testing.T) {
	c := &CLI{Team: CSVFlag{Values: []string{"ops"}}}
	c.Normalize(&Config{Default: Defaults{Authors: []string{"@me"}}})
	require.NotNil(t, c.Author)
	require.Equal(t, []string{valueAll}, c.Author.Values)
}

func TestNormalize_MixedTeamsOverrideDefaultAuthors(t *testing.T) {
	c := &CLI{Team: CSVFlag{Values: []string{"ops", "!frontend"}}}
	c.Normalize(&Config{Default: Defaults{Authors: []string{"@me"}}})
	require.NotNil(t, c.Author)
	require.Equal(t, []string{valueAll}, c.Author.Values)
}

func TestNormalize_PreservesNegationWhenResolvingTeamAlias(t *testing.T) {
	c := &CLI{Team: CSVFlag{Values: []string{"!ops"}}}
	c.Normalize(&Config{TeamAliases: map[string]string{"ops": "acme/operations"}})
	require.Equal(t, []string{"!acme/operations"}, c.Team.Values)
}

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

func TestValidate_RejectsNegatedRequestedForUnsubscribe(t *testing.T) {
	tests := []string{"!alice", "team:!ops", "!team:ops"}
	for _, requested := range tests {
		t.Run(requested, func(t *testing.T) {
			cli := &CLI{
				Unsubscribe:     true,
				ReviewRequested: CSVFlag{Values: []string{requested}},
			}
			require.EqualError(
				t,
				cli.Validate(),
				"--unsubscribe cannot use negated --requested values",
			)
		})
	}
}

func TestValidate_GroupRejectsInvalidKey(t *testing.T) {
	cli := &CLI{Group: CSVFlag{Values: []string{"author", "bogus"}}}
	require.EqualError(
		t,
		cli.Validate(),
		`invalid --group value "bogus" (valid: author, repo, owner, state, draft, label)`,
	)
}

func TestGroupKeys_DeduplicatesInFirstSeenOrder(t *testing.T) {
	cli := &CLI{
		Group: CSVFlag{Values: []string{"author", "state", "repo", "repo", "author"}},
	}

	keys, err := cli.GroupKeys()

	require.NoError(t, err)
	require.Equal(t, []groupKey{groupAuthor, groupState, groupRepo}, keys)
}

func TestValidate_GroupMutualExclusion(t *testing.T) {
	tests := map[string]struct {
		mutate func(*CLI)
		want   string
	}{
		"interactive": {
			func(c *CLI) { c.Interactive = true },
			"--group and --interactive are mutually exclusive",
		},
		"watch": {
			func(c *CLI) { c.Watch = true },
			"--group and --watch are mutually exclusive",
		},
		"send": {
			func(c *CLI) { c.Send = true },
			"--group and --send are mutually exclusive",
		},
		"clone": {
			func(c *CLI) { c.Clone = true },
			"--group and --clone are mutually exclusive",
		},
		"action": {
			func(c *CLI) { c.Approve = true },
			"--group cannot be combined with action flags",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cli := &CLI{Group: CSVFlag{Values: []string{"author"}}}
			tt.mutate(cli)
			require.EqualError(t, cli.Validate(), tt.want)
		})
	}
}

func TestValidate_CountOverridesGroup(t *testing.T) {
	cli := &CLI{
		Group: CSVFlag{Values: []string{"author"}},
		Count: true,
	}
	require.NoError(t, cli.Validate())
	require.False(t, cli.GroupActive())
	require.True(t, cli.Count)
}

func TestValidate_AllowsGroupWithFilters(t *testing.T) {
	cli := &CLI{
		Group: CSVFlag{Values: []string{"author", "repo"}},
		State: valueAll,
		NoBot: true,
	}
	require.NoError(t, cli.Validate())
}

func TestNormalize_GroupRaisesLimitDefault(t *testing.T) {
	cfg := &Config{Default: Defaults{Limit: defaultLimit}}

	grouped := &CLI{Group: CSVFlag{Values: []string{"author"}}}
	grouped.Normalize(cfg)
	require.NotNil(t, grouped.Limit)
	require.Equal(t, maxGroupResults, *grouped.Limit)
	require.False(t, grouped.limitExplicit)

	plain := &CLI{}
	plain.Normalize(cfg)
	require.Equal(t, defaultLimit, *plain.Limit)

	explicit := 5
	override := &CLI{Group: CSVFlag{Values: []string{"author"}}, Limit: &explicit}
	override.Normalize(cfg)
	require.Equal(t, 5, *override.Limit)
	require.True(t, override.limitExplicit)
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

func TestValidate_NormalizesReviewFilterHyphens(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"changes-requested", valueReviewFilterChanges},
		{"changes_requested", valueReviewFilterChanges},
		{"self-required", valueReviewFilterSelfRequired},
		{"self_required", valueReviewFilterSelfRequired},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cli := &CLI{Review: tt.input}
			require.NoError(t, cli.Validate())
			require.Equal(t, tt.want, cli.Review)
		})
	}
}

func TestNormalizeSendToRecipient(t *testing.T) {
	tests := []struct {
		name      string
		recipient string
		want      string
	}{
		{name: "bare channel", recipient: "pull-requests", want: "#pull-requests"},
		{name: "channel", recipient: "#pull-requests", want: "#pull-requests"},
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
