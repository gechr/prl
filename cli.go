package main

import (
	"fmt"
	"strings"
	"time"

	clib "github.com/gechr/clib/cli/kong"
	"github.com/gechr/clog"
)

// CSVFlag is a comma-separated value flag backed by clib.
type CSVFlag = clib.CSVFlag

// formatCSV joins string values with commas.
func formatCSV(values []string) string {
	return strings.Join(values, ",")
}

// CLI represents the command-line interface.
type CLI struct {
	// Embedded flags (hidden)
	clib.CompletionFlags

	// Positional
	Query []string `help:"Search term(s) to filter pull requests" arg:"" optional:""`

	// Filter flags
	Owner           CSVFlag  `name:"owner"       help:"Limit to GitHub owner"                                        short:"O" clib:"terse='Owner',group='Filters/1'"`
	Repo            CSVFlag  `name:"repo"        help:"Limit to specific repo(s)"                                    short:"R" clib:"terse='Repository',complete='predictor=repo,comma',group='Filters/1'"                                                                                                   aliases:"repository"`
	Filter          []string `name:"filter"      help:"Search qualifier"                                             short:"f" clib:"terse='Search qualifier',group='Filters/2'"`
	Match           string   `name:"match"       help:"Restrict text search to field"                                          clib:"terse='Search field',complete='values=title body comments',group='Filters/2',enum='title,body,comments',highlight='t,b,c',default='title'"                                                                 placeholder:"<field>"`
	Author          *CSVFlag `name:"author"      help:"Filter by author"                                             short:"a" clib:"terse='Author',complete='predictor=author',order=keep,group='Filters/3'"                                                                                                                                   placeholder:"<user>"`
	Commenter       CSVFlag  `name:"commenter"   help:"Filter by commenter"                                                    clib:"terse='Commenter',complete='predictor=author',group='Filters/3'"                                                                                                                                           placeholder:"<user>"`
	NoBot           bool     "name:\"no-bot\"    help:\"Exclude bot authors (may return fewer than `--limit`)\"      short:\"B\"                                                                                      clib:\"terse='Exclude bots',group='Filters/3'\""
	Team            CSVFlag  `name:"team"        help:"Filter by team authors"                                       short:"t" clib:"terse='Team',complete='predictor=team',group='Filters/3'"                                                                                                                                                  placeholder:"<slug>"`
	Involves        CSVFlag  `name:"involves"    help:"Filter by involvement (author, assignee, mentions, comments)" short:"I" clib:"terse='Involvement',complete='predictor=author',group='Filters/3'"                                                                                                                                         placeholder:"<user>"`
	ReviewRequested CSVFlag  `name:"requested"   help:"Filter by requested reviewer"                                           clib:"terse='Requested reviewer',complete='predictor=author',group='Filters/3'"                                                                                               aliases:"review-requested,request" placeholder:"<user>"`
	ClosedBy        CSVFlag  `name:"closed-by"   help:"Filter by who closed the PR (post-fetch)"                               clib:"terse='Closed by',complete='predictor=author',group='Filters/3'"                                                                                                                                           placeholder:"<user>"`
	MergedBy        CSVFlag  `name:"merged-by"   help:"Filter by who merged the PR (post-fetch)"                               clib:"terse='Merged by',complete='predictor=author',group='Filters/3'"                                                                                                                                           placeholder:"<user>"`
	ReviewedBy      CSVFlag  `name:"reviewed-by" help:"Filter by reviewer"                                                     clib:"terse='Reviewed by',complete='predictor=author',group='Filters/3'"                                                                                                                                         placeholder:"<user>"`
	CI              string   `name:"ci"          help:"Filter by CI status"                                                    clib:"terse='CI status',complete='values=success failure pending',group='Filters/4',enum='success,failure,pending',highlight='s,f,p'"                                                                            placeholder:"<status>"`
	Comments        string   `name:"comments"    help:"Filter by comment count (>5, 10..20)"                                   clib:"terse='Comment count',group='Filters/4'"                                                                                                                                                                   placeholder:"<range>"`
	Language        string   `name:"language"    help:"Filter by language"                                           short:"l" clib:"terse='Language',group='Filters/4'"                                                                                                                                                                        placeholder:"<lang>"`
	Review          string   `name:"review"      help:"Filter by review status"                                      short:"r" clib:"terse='Review status',complete='values=none required approved changes_requested',group='Filters/4',enum='none,required,approved,changes_requested',highlight='n,r,a,c'"                                    placeholder:"<status>"`
	State           string   `name:"state"       help:"Filter by state"                                              short:"s" clib:"terse='State',complete='values=open closed ready merged all',group='Filters/4',enum='open,closed,ready,merged,all',highlight='o,c,r,m,a',default='open'"`
	Topic           string   `name:"topic"       help:"Filter by repo topic"                                         short:"T" clib:"terse='Topic',complete='predictor=topic',group='Filters/4'"`
	Created         string   `name:"created"     help:"Filter by creation date"                                      short:"c" clib:"terse='Creation date',group='Filters/5'"                                                                                                                                aliases:"since"                    placeholder:"<duration>"`
	Drift           string   `name:"drift"       help:"Filter by duration between created/updated"                   short:"d" clib:"terse='Created/updated gap',group='Filters/5'"                                                                                                                                                             placeholder:"<duration>"`
	Updated         string   `name:"updated"     help:"Filter by last updated date"                                  short:"u" clib:"terse='Updated date',group='Filters/5'"                                                                                                                                                                    placeholder:"<duration>"`
	Merged          string   `name:"merged"      help:"Filter by merged date"                                        short:"m" clib:"terse='Merged date',group='Filters/5'"                                                                                                                                  aliases:"merged-at"                placeholder:"<duration>"`
	Archived        bool     `name:"archived"    help:"Include archived repos"                                                 clib:"terse='Include archived',group='Filters/6'"`
	Draft           *bool    `name:"draft"       help:"Show only draft PRs"                                                    clib:"terse='Draft filter',group='Filters/6'"                                                                                                                                                                                             negatable:""`

	// Interactive flags
	Interactive bool           `name:"interactive" help:"Launch interactive TUI browser" short:"i" clib:"terse='TUI browser',group='Interactive/0'"`
	Interval    *time.Duration "help:\"Override TUI auto-refresh interval (requires `--interactive`)\" clib:\"terse='Refresh interval',group='Interactive/0'\" placeholder:\"<duration>\""

	Approve      bool   `name:"approve"     help:"Approve each PR"                                                 clib:"terse='Approve PRs',group='Interactive/1'"`
	Close        bool   `name:"close"       help:"Close each PR"                                                   clib:"terse='Close PRs',group='Interactive/1'"`
	Copilot      bool   `name:"copilot"     help:"Request Copilot review on each PR"                               clib:"terse='Copilot review',group='Interactive/1'"`
	DeleteBranch bool   "name:\"delete-branch\" help:\"Delete branch after close (requires `--close`)\"                                                            clib:\"terse='Delete branch',group='Interactive/1'\""
	Comment      string `name:"comment"     help:"Add a comment to each PR"                                        clib:"terse='Add comment',group='Interactive/1'"       placeholder:"<body>"`
	Edit         bool   `name:"edit"        help:"Edit title and body of each PR"                                  clib:"terse='Edit PR',group='Interactive/1'"                                short:"e"`
	MarkDraft    bool   `name:"mark-draft"  help:"Convert each PR to draft (only targets non-draft PRs)"           clib:"terse='Convert to draft',group='Interactive/1'"`
	MarkReady    bool   `name:"mark-ready"  help:"Mark each PR as ready for review (only targets draft PRs)"       clib:"terse='Mark as ready',group='Interactive/1'"`
	Merge        *bool  `name:"merge"       help:"Toggle auto-merge (squash) on each PR"                           clib:"terse='Auto-merge',group='Interactive/1'"                                       negatable:""`
	ForceMerge   bool   `name:"force-merge" help:"Poll for checks, then force-merge (requires bypass permissions)" clib:"terse='Force-merge',group='Interactive/1'"                            short:"M"`
	Unsubscribe  bool   `name:"unsubscribe" help:"Remove review request and unsubscribe from each PR"              clib:"terse='Unsubscribe',group='Interactive/1'"                            short:"U"`
	Update       bool   `name:"update"      help:"Update each PR branch from base branch"                          clib:"terse='Update branch',group='Interactive/1'"`
	Yes          bool   `name:"yes"         help:"Skip interactive confirmation prompt"                            clib:"terse='Skip confirmation',group='Interactive/2'"                      short:"y"`

	// Action flags
	Clone bool `name:"clone" help:"Clone unique repos from results (parallel)" clib:"terse='Clone repos',group='Actions/1'"`
	Copy  bool `name:"copy"  help:"Copy output to clipboard"                   clib:"terse='Copy clipboard',group='Actions/1'"  short:"C"`
	Count bool `name:"count" help:"Print total result count"                   clib:"terse='Print count',group='Actions/1'"     short:"N"`
	Dry   bool `name:"dry"   help:"Show search query without executing"        clib:"terse='Dry run',group='Actions/1'"         short:"n" aliases:"dry-run,dryrun"`
	Open  bool `name:"open"  help:"Open each PR in browser"                    clib:"terse='Open in browser',group='Actions/1'" short:"P"`
	Web   bool `name:"web"   help:"Open GitHub search in browser"              clib:"terse='Web search',group='Actions/1'"      short:"w"`

	Send   bool   `name:"send"    help:"Send PRs to Slack via plugin"                      clib:"terse='Send to Slack',group='Actions/2'"`
	SendTo string `name:"send-to" help:"Override Slack recipient (#channel, @user, email)" clib:"terse='Override Slack recipient',complete='predictor=slack-recipient',group='Actions/2'" placeholder:"<recipient>"`

	// Output flags
	Watch    bool    `name:"watch"   help:"Refresh output periodically"                                                                          short:"W" clib:"terse='Watch mode',group='Output/0'"`
	ExitZero bool    `name:"exit-0"  help:"Exit immediately when there are no results"                                                           short:"0" clib:"terse='Exit on no match',group='Output/0'"`
	Columns  CSVFlag `name:"columns" help:"Table columns [index, ref, repo, owner, number, title, labels, author, state, created, updated, url]"           clib:"terse='Table columns',complete='predictor=columns,comma',group='Output/1'"                                                                                   aliases:"col" placeholder:"<cols>"`
	Limit    *int    `name:"limit"   help:"Maximum results"                                                                                      short:"L" clib:"terse='Max results',group='Output/1'"                                                                                                                                      placeholder:"<n>"`
	Output   *string `name:"output"  help:"Output format"                                                                                        short:"o" clib:"terse='Output format',complete='values=url bullet table json repo',group='Output/1',enum='url,bullet,table,json,repo',highlight='u,b,t,j,r',default='table'"               placeholder:"<fmt>"`
	Reverse  bool    `name:"reverse" help:"Show oldest first (top)"                                                                                        clib:"terse='Reverse display order',group='Output/1'"`
	Sort     *string `name:"sort"    help:"Sort by"                                                                                                        clib:"terse='Sort field',complete='values=name created updated',group='Output/1',enum='name,created,updated',highlight='n,c,u',default='name'"                                   placeholder:"<field>"`

	// Miscellaneous
	Init    bool           `name:"init"    help:"Initialize config with defaults"            clib:"terse='Initialize config',group='Miscellaneous/0'"`
	Color   clog.ColorMode `name:"color"   help:"When to use color"                          clib:"terse='Color mode',group='Miscellaneous/1'"        default:"auto" enum:"auto,always,never"`
	Debug   bool           `name:"debug"   help:"Log HTTP requests to stderr"                clib:"terse='Debug mode',group='Miscellaneous/1'"`
	Quick   bool           `name:"quick"   help:"Skip enrichment (merge status, auto-merge)" clib:"terse='Skip enrichment',group='Miscellaneous/1'"                                           short:"Q"`
	Verbose bool           `name:"verbose" help:"Enable verbose logging"                     clib:"terse='Verbose',group='Miscellaneous/1'"                                                   short:"v"`
	Version bool           `name:"version" help:"Print version"                              clib:"terse='Version',group='Miscellaneous/2'"                                                   short:"V"`

	stateExplicit    bool `kong:"-"`
	draftExplicit    bool `kong:"-"`
	noBotExplicit    bool `kong:"-"`
	archivedExplicit bool `kong:"-"`
	ciExplicit       bool `kong:"-"`
	reviewExplicit   bool `kong:"-"`
	sortExplicit     bool `kong:"-"`
	outputExplicit   bool `kong:"-"`
}

// Validate checks for mutually exclusive options.
func (c *CLI) Validate() error {
	if c.Close && c.Approve {
		return fmt.Errorf("--close and --approve are mutually exclusive")
	}
	if c.Close && c.Merge != nil && *c.Merge {
		return fmt.Errorf("--close and --merge are mutually exclusive")
	}
	if c.Close && c.Update {
		return fmt.Errorf("--close and --update are mutually exclusive")
	}
	if c.Copilot && c.Close {
		return fmt.Errorf("--copilot and --close are mutually exclusive")
	}
	if c.MarkDraft && c.MarkReady {
		return fmt.Errorf("--mark-draft and --mark-ready are mutually exclusive")
	}
	if c.MarkDraft && c.Close {
		return fmt.Errorf("--mark-draft and --close are mutually exclusive")
	}
	if c.MarkDraft && c.Merge != nil && *c.Merge {
		return fmt.Errorf("--mark-draft and --merge are mutually exclusive")
	}
	if c.ForceMerge && c.Close {
		return fmt.Errorf("--force-merge and --close are mutually exclusive")
	}
	if c.ForceMerge && c.Merge != nil {
		return fmt.Errorf("--force-merge and --merge are mutually exclusive")
	}
	if c.ForceMerge && c.MarkDraft {
		return fmt.Errorf("--force-merge and --mark-draft are mutually exclusive")
	}
	if c.Edit && c.Yes {
		return fmt.Errorf("--edit requires interactive mode (cannot use --yes)")
	}
	if c.Edit && c.Close {
		return fmt.Errorf("--edit and --close are mutually exclusive")
	}
	if c.Edit && c.ForceMerge {
		return fmt.Errorf("--edit and --force-merge are mutually exclusive")
	}
	if c.DeleteBranch && !c.Close {
		return fmt.Errorf("--delete-branch requires --close")
	}
	if c.Clone && c.HasAction() {
		return fmt.Errorf("--clone cannot be combined with PR action flags")
	}
	sending := c.Send || c.SendTo != ""
	if sending && c.Clone {
		return fmt.Errorf("--send and --clone are mutually exclusive")
	}
	if sending && c.Open {
		return fmt.Errorf("--send and --open are mutually exclusive")
	}
	if sending && c.Web {
		return fmt.Errorf("--send and --web are mutually exclusive")
	}
	if c.Interactive && c.HasAction() {
		return fmt.Errorf("--interactive cannot be combined with action flags")
	}
	if c.Interactive && c.Yes {
		return fmt.Errorf("--interactive and --yes are mutually exclusive")
	}
	if c.Interactive && c.Watch {
		return fmt.Errorf("--interactive and --watch are mutually exclusive")
	}
	if c.Interactive && c.Send {
		return fmt.Errorf("--interactive and --send are mutually exclusive")
	}
	if c.Interactive && c.Clone {
		return fmt.Errorf("--interactive and --clone are mutually exclusive")
	}
	if c.Interactive && c.Web {
		return fmt.Errorf("--interactive and --web are mutually exclusive")
	}
	if c.Interactive && c.Open {
		return fmt.Errorf("--interactive and --open are mutually exclusive")
	}
	if c.Interactive && c.Count {
		return fmt.Errorf("--interactive and --count are mutually exclusive")
	}
	if c.Interval != nil && !c.Interactive {
		return fmt.Errorf("--interval requires --interactive")
	}
	if c.Interval != nil && *c.Interval <= 0 {
		return fmt.Errorf("--interval must be greater than 0")
	}
	if c.Watch && c.Count {
		return fmt.Errorf("--watch and --count are mutually exclusive")
	}
	if c.Watch && c.HasAction() {
		return fmt.Errorf("--watch cannot be combined with action flags")
	}
	if c.Watch && c.Send {
		return fmt.Errorf("--watch and --send are mutually exclusive")
	}
	if c.Watch && c.Clone {
		return fmt.Errorf("--watch and --clone are mutually exclusive")
	}
	if c.Watch && c.Open {
		return fmt.Errorf("--watch and --open are mutually exclusive")
	}
	if c.Watch && c.Web {
		return fmt.Errorf("--watch and --web are mutually exclusive")
	}

	for _, f := range c.Filter {
		lower := strings.ToLower(f)
		if strings.HasPrefix(lower, "type:") || strings.HasPrefix(lower, "-type:") {
			return fmt.Errorf("--filter %q conflicts with the implicit type:pr qualifier", f)
		}
	}

	// Validate limit
	if c.Limit != nil && *c.Limit <= 0 {
		return fmt.Errorf("--limit must be greater than 0")
	}

	// Validate drift
	if c.Drift != "" {
		if _, _, err := parseDrift(c.Drift); err != nil {
			return fmt.Errorf("invalid --drift value: %w", err)
		}
	}

	// Validate enum values
	if c.Output != nil {
		if _, ok := parseOutputFormat(*c.Output); !ok {
			return fmt.Errorf(
				"invalid --output value %q (valid: table/t, url/u, bullet/b, json/j, repo/r)",
				*c.Output,
			)
		}
	}
	if c.Sort != nil {
		if _, ok := parseSortField(*c.Sort); !ok {
			return fmt.Errorf(
				"invalid --sort value %q (valid: name/n, created/c, updated/u)",
				*c.Sort,
			)
		}
	}
	if c.State != "" {
		if _, ok := parsePRState(c.State); !ok {
			return fmt.Errorf(
				"invalid --state value %q (valid: open/o, closed/c, ready/r, merged/m, all/a)",
				c.State,
			)
		}
	}
	if c.CI != "" {
		if _, ok := parseCIStatus(c.CI); !ok {
			return fmt.Errorf(
				"invalid --ci value %q (valid: success/pass/passed/s, failure/fail/failed/f, pending/p)",
				c.CI,
			)
		}
	}
	if c.Review != "" {
		switch c.Review {
		case "none", "required", "approved", "changes_requested":
			// valid
		default:
			return fmt.Errorf(
				"invalid --review value %q (valid: none, required, approved, changes_requested)",
				c.Review,
			)
		}
	}
	if c.Match != "" {
		switch c.Match {
		case "title", "body", "comments":
			// valid
		default:
			return fmt.Errorf(
				"invalid --match value %q (valid: title, body, comments)",
				c.Match,
			)
		}
	}
	return nil
}

// Normalize applies post-parse normalization.
func (c *CLI) Normalize(cfg *Config) {
	// Normalize "any" → "all" for author
	if c.Author != nil {
		for i, v := range c.Author.Values {
			if strings.ToLower(v) == valueAny {
				c.Author.Values[i] = valueAll
			}
		}
	}
	// Normalize other CSV flags
	normalizeCSV := func(values []string) []string {
		for i, v := range values {
			if strings.ToLower(v) == valueAny {
				values[i] = valueAll
			}
		}
		return values
	}
	c.Commenter.Values = normalizeCSV(c.Commenter.Values)
	c.Involves.Values = normalizeCSV(c.Involves.Values)
	c.ReviewRequested.Values = normalizeCSV(c.ReviewRequested.Values)
	c.ReviewedBy.Values = normalizeCSV(c.ReviewedBy.Values)

	// Normalize owner
	c.Owner.Values = normalizeCSV(c.Owner.Values)

	c.Comment = strings.TrimSpace(c.Comment)

	// Apply config defaults where CLI didn't set them
	if len(c.Owner.Values) == 0 && len(cfg.Default.Owners) > 0 {
		c.Owner.Values = cfg.Default.Owners
	}
	if c.Limit == nil {
		c.Limit = &cfg.Default.Limit
	}
	if c.Match == "" {
		c.Match = cfg.Default.Match
	}
	c.sortExplicit = c.Sort != nil
	if c.Sort == nil {
		c.Sort = &cfg.Default.Sort
	}
	// --closed-by / --merged-by imply state and sort-by-updated
	if len(c.ClosedBy.Values) > 0 && !c.stateExplicit {
		c.State = valueClosed
		clog.Debug().Msgf("--closed-by implied --state=%s", valueClosed)
	}
	if len(c.MergedBy.Values) > 0 && !c.stateExplicit {
		c.State = valueMerged
		clog.Debug().Msgf("--merged-by implied --state=%s", valueMerged)
	}
	if (len(c.ClosedBy.Values) > 0 || len(c.MergedBy.Values) > 0) && !c.sortExplicit {
		updated := valueUpdated
		c.Sort = &updated
		if len(c.ClosedBy.Values) > 0 {
			clog.Debug().Msg("--closed-by implied --sort=updated")
		} else {
			clog.Debug().Msg("--merged-by implied --sort=updated")
		}
	}
	if c.State == "" {
		c.State = cfg.Default.State
	}
	c.outputExplicit = c.Output != nil
	if c.Output == nil {
		c.Output = &cfg.Default.Output
	}

	// Author defaults
	// User-oriented filters imply --author=any (don't restrict to self)
	hasUserFilter := len(c.ReviewRequested.Values) > 0 ||
		len(c.Involves.Values) > 0 ||
		len(c.Commenter.Values) > 0 ||
		len(c.ReviewedBy.Values) > 0 ||
		len(c.ClosedBy.Values) > 0 ||
		len(c.MergedBy.Values) > 0
	if c.Author == nil && hasUserFilter {
		c.Author = &CSVFlag{Values: []string{valueAll}}
		clog.Debug().Msg("user-oriented filter implied --author=any")
	}
	if c.Author == nil {
		c.Author = &CSVFlag{Values: cfg.Default.Authors}
	}

	// Bots: config bots=false implies --no-bot
	if !cfg.Default.Bots && !c.NoBot {
		c.NoBot = true
		clog.Debug().Msg("config bots=false implied --no-bot")
	}

	// Reverse: XOR with config default so --reverse toggles the configured direction
	c.Reverse = cfg.Default.Reverse != c.Reverse

	// --columns defaults to table output when --output is not explicit
	if len(c.Columns.Values) > 0 && !c.outputExplicit {
		c.setOutput(valueTable)
	}

	// Team alias resolution
	for i, t := range c.Team.Values {
		c.Team.Values[i] = cfg.resolveTeamAlias(t)
	}
}

// HasAction returns true if any action flag is set.
func (c *CLI) HasAction() bool {
	return c.Approve || c.Close || c.Comment != "" || c.Copilot || c.Edit || c.ForceMerge ||
		c.MarkDraft ||
		c.MarkReady ||
		c.Merge != nil ||
		c.Unsubscribe ||
		c.Update
}

// IsInteractive returns true if interactive selection should be shown.
// Note: --no-merge does NOT trigger interactive mode.
func (c *CLI) IsInteractive() bool {
	if c.Yes {
		return false
	}
	return c.Approve || c.Clone || c.Close || c.Comment != "" || c.Copilot || c.Edit ||
		c.ForceMerge ||
		c.MarkDraft ||
		c.MarkReady ||
		(c.Merge != nil && *c.Merge) ||
		c.Unsubscribe ||
		c.Update ||
		c.Send
}

// setOutput sets the output format string.
func (c *CLI) setOutput(s string) {
	c.Output = &s
}

func normalizeSendToRecipient(recipient string) string {
	if recipient == "" ||
		strings.HasPrefix(recipient, "#") ||
		strings.HasPrefix(recipient, "@") ||
		strings.Contains(recipient, ",") ||
		strings.Contains(recipient, "@") ||
		looksLikeSlackRecipientID(recipient) {
		return recipient
	}
	return "#" + recipient
}

func looksLikeSlackRecipientID(recipient string) bool {
	if len(recipient) <= 1 {
		return false
	}

	switch recipient[0] {
	case 'C', 'D', 'G', 'U', 'W':
		return true
	default:
		return false
	}
}

// OutputFormat returns the parsed output format.
func (c *CLI) OutputFormat() OutputFormat {
	if c.Output == nil {
		return OutputTable
	}
	f, ok := parseOutputFormat(*c.Output)
	if !ok {
		return OutputTable
	}
	return f
}

// SortField returns the parsed sort field.
func (c *CLI) SortField() SortField {
	if c.Sort == nil {
		return SortName
	}
	f, ok := parseSortField(*c.Sort)
	if !ok {
		return SortName
	}
	return f
}

// SortExplicit returns true if --sort was explicitly provided on the CLI.
func (c *CLI) SortExplicit() bool {
	return c.sortExplicit
}

// PRState returns the parsed PR state.
func (c *CLI) PRState() PRState {
	s, ok := parsePRState(c.State)
	if !ok {
		return StateOpen
	}
	return s
}

// CIStatus returns the parsed CI status.
func (c *CLI) CIStatus() CIStatus {
	if c.CI == "" {
		return CINone
	}
	s, ok := parseCIStatus(c.CI)
	if !ok {
		return CINone
	}
	return s
}

// LimitValue returns the effective limit value.
func (c *CLI) LimitValue() int {
	if c.Limit == nil {
		return defaultLimit
	}
	return *c.Limit
}

// QueryString joins positional arguments into a search query.
// A leading "-" or "!" on any term is converted to the GitHub "NOT" keyword
// because GitHub search uses "-" only for qualifier negation (e.g. -author:foo),
// not for free-text negation. Multi-word terms are quoted so the phrase is
// treated as a unit (e.g. -"foo bar" → NOT "foo bar").
func (c *CLI) QueryString() string {
	parts := make([]string, len(c.Query))
	for i, q := range c.Query {
		if rest := strings.TrimLeft(q, "-!"); rest != "" && rest != q {
			parts[i] = "NOT " + quoteIfNeeded(rest)
		} else {
			parts[i] = quoteIfNeeded(q)
		}
	}
	return strings.Join(parts, " ")
}

// quoteIfNeeded wraps s in double quotes if it contains spaces.
func quoteIfNeeded(s string) string {
	if strings.Contains(s, " ") {
		return `"` + s + `"`
	}
	return s
}

// ApplyOutputOverrides adjusts output mode based on action flags.
func (c *CLI) ApplyOutputOverrides() {
	if c.Open {
		c.setOutput(valueURL)
	}
	if c.HasAction() || c.Clone {
		if c.IsInteractive() {
			c.setOutput(valueTable)
		} else {
			c.setOutput(valueURL)
		}
	}
	// --columns defaults to table output (re-check after overrides)
	if len(c.Columns.Values) > 0 && !c.outputExplicit {
		c.setOutput(valueTable)
	}

	// --send-to implies --send
	if c.SendTo != "" {
		c.SendTo = normalizeSendToRecipient(c.SendTo)
		c.Send = true
		clog.Debug().Msg("--send-to implied --send")
	}

	// --send: table for interactive selection; url for non-interactive (--yes)
	if c.Send {
		if c.IsInteractive() {
			c.setOutput(valueTable)
		} else {
			c.setOutput(valueURL)
		}
		// --send implies --no-draft unless draft was explicitly set
		if !c.draftExplicit {
			c.Draft = new(false)
			clog.Debug().Msg("--send implied --no-draft")
		}
	}
}
