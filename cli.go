package main

import (
	"fmt"
	"strings"

	clib "github.com/gechr/clib/cli/kong"
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
	Organization    CSVFlag  `name:"owner"       help:"Limit to GitHub owner/organization"                           short:"O" aliases:"organization,org"                                                         clib:"terse='Owner/org',group='Filters/1'"`
	Repo            string   `name:"repo"        help:"Limit to specific repo"                                       short:"R" aliases:"repository"                                                               clib:"terse='Repository',complete='predictor=repo',group='Filters/1'"`
	Filter          []string `name:"filter"      help:"Search qualifier"                                             short:"f"                                                                                    clib:"terse='Search qualifier',group='Filters/2'"`
	Match           string   `name:"match"       help:"Restrict text search to field (title, body, comments)"                                                                  placeholder:"<field>"              clib:"terse='Search field',complete='values=title body comments',group='Filters/2',enum='title,body,comments',highlight='t,b,c',default='title'"`
	Author          *CSVFlag `name:"author"      help:"Filter by author"                                             short:"a"                                                 placeholder:"<user>"               clib:"terse='Author',complete='predictor=author',group='Filters/3'"`
	Commenter       CSVFlag  `name:"commenter"   help:"Filter by commenter"                                                                                                    placeholder:"<user>"               clib:"terse='Commenter',complete='predictor=author',group='Filters/3'"`
	NoBot           bool     `name:"no-bot"      help:"Exclude bot authors (may return fewer than --limit)"          short:"B"                                                                                    clib:"terse='Exclude bots',group='Filters/3'"`
	Team            CSVFlag  `name:"team"        help:"Filter by team authors"                                       short:"t"                                                 placeholder:"<slug>"               clib:"terse='Team',complete='predictor=team',group='Filters/3'"`
	Involves        CSVFlag  `name:"involves"    help:"Filter by involvement (author, assignee, mentions, comments)" short:"I"                                                 placeholder:"<user>"               clib:"terse='Involvement',complete='predictor=author',group='Filters/3'"`
	ReviewRequested CSVFlag  `name:"requested"   help:"Filter by requested reviewer"                                           aliases:"review-requested,request"              placeholder:"<user>"               clib:"terse='Requested reviewer',complete='predictor=author',group='Filters/3'"`
	ReviewedBy      CSVFlag  `name:"reviewed-by" help:"Filter by reviewer"                                                                                                     placeholder:"<user>"               clib:"terse='Reviewed by',complete='predictor=author',group='Filters/3'"`
	CI              string   `name:"ci"          help:"Filter by CI status"                                                                                                    placeholder:"<status>"             clib:"terse='CI status',complete='values=success failure pending',group='Filters/4',enum='success,failure,pending',highlight='s,f,p'"`
	Language        string   `name:"language"    help:"Filter by language"                                           short:"l"                                                 placeholder:"<lang>"               clib:"terse='Language',group='Filters/4'"`
	Review          string   `name:"review"      help:"Filter by review status"                                      short:"r"                                                 placeholder:"<status>"             clib:"terse='Review status',complete='values=none required approved changes_requested',group='Filters/4',enum='none,required,approved,changes_requested',highlight='n,r,a,c'"`
	State           string   `name:"state"       help:"Filter by state"                                              short:"s"                                                                                    clib:"terse='State',complete='values=open closed ready merged all',group='Filters/4',enum='open,closed,ready,merged,all',highlight='o,c,r,m,a',default='open'"`
	Topic           string   `name:"topic"       help:"Filter by repo topic"                                         short:"T"                                                                                    clib:"terse='Topic',complete='predictor=topic',group='Filters/4'"`
	Created         string   `name:"created"     help:"Filter by creation date"                                      short:"c"                                                 placeholder:"<duration>"           clib:"terse='Creation date',group='Filters/5'"`
	Since           string   `name:"since"       help:"Alias for --created"                                                                                                    placeholder:"<duration>" hidden:"" clib:"group='Filters/5'"`
	Drift           string   `name:"drift"       help:"Filter by duration between created/updated"                   short:"d"                                                 placeholder:"<duration>"           clib:"terse='Created/updated gap',group='Filters/5'"`
	Updated         string   `name:"updated"     help:"Filter by last updated date"                                  short:"u"                                                 placeholder:"<duration>"           clib:"terse='Updated date',group='Filters/5'"`
	Merged          string   `name:"merged"      help:"Filter by merged date"                                        short:"m" aliases:"merged-at"                             placeholder:"<duration>"           clib:"terse='Merged date',group='Filters/5'"`
	Archived        bool     `name:"archived"    help:"Include archived repos"                                                                                                                                    clib:"terse='Include archived',group='Filters/6'"`
	Draft           *bool    `name:"draft"       help:"Show only draft PRs"                                                                                       negatable:""                                    clib:"terse='Draft filter',group='Filters/6'"`

	// Interactive flags
	Interactive bool `name:"interactive" help:"Launch interactive TUI browser" short:"i" clib:"terse='TUI browser',group='Interactive/0'"`

	Approve      bool   `name:"approve"       help:"Approve each PR"                                                 clib:"terse='Approve PRs',group='Interactive/1'"`
	Close        bool   `name:"close"         help:"Close each PR"                                                   clib:"terse='Close PRs',group='Interactive/1'"`
	DeleteBranch bool   `name:"delete-branch" help:"Delete branch after close (requires --close)"                    clib:"terse='Delete branch',group='Interactive/1'"`
	Comment      string `name:"comment"       help:"Add a comment to each PR"                                        clib:"terse='Add comment',group='Interactive/1'"       placeholder:"<body>"`
	Edit         bool   `name:"edit"          help:"Edit title and body of each PR"                                  clib:"terse='Edit PR',group='Interactive/1'"                                short:"e"`
	MarkDraft    bool   `name:"mark-draft"    help:"Convert each PR to draft (only targets non-draft PRs)"           clib:"terse='Convert to draft',group='Interactive/1'"`
	MarkReady    bool   `name:"mark-ready"    help:"Mark each PR as ready for review (only targets draft PRs)"       clib:"terse='Mark as ready',group='Interactive/1'"`
	Merge        *bool  `name:"merge"         help:"Toggle auto-merge (squash) on each PR"                           clib:"terse='Auto-merge',group='Interactive/1'"                                       negatable:""`
	ForceMerge   bool   `name:"force-merge"   help:"Poll for checks, then force-merge (requires bypass permissions)" clib:"terse='Force-merge',group='Interactive/1'"                            short:"M"`
	Unsubscribe  bool   `name:"unsubscribe"   help:"Remove review request and unsubscribe from each PR"              clib:"terse='Unsubscribe',group='Interactive/1'"`
	Update       bool   `name:"update"        help:"Update each PR branch from base branch"                          clib:"terse='Update branch',group='Interactive/1'"`
	Yes          bool   `name:"yes"           help:"Skip interactive confirmation prompt"                            clib:"terse='Skip confirmation',group='Interactive/2'"                      short:"y"`

	// Action flags
	Clone bool `name:"clone" help:"Clone unique repos from results (parallel)" clib:"terse='Clone repos',group='Actions/1'"`
	Copy  bool `name:"copy"  help:"Copy output to clipboard"                   clib:"terse='Copy clipboard',group='Actions/1'"  short:"C"`
	Count bool `name:"count" help:"Print total result count"                   clib:"terse='Print count',group='Actions/1'"     short:"N"`
	Dry   bool `name:"dry"   help:"Show search query without executing"        clib:"terse='Dry run',group='Actions/1'"         short:"n" aliases:"dry-run,dryrun"`
	Open  bool `name:"open"  help:"Open each PR in browser"                    clib:"terse='Open in browser',group='Actions/1'" short:"P"`
	Web   bool `name:"web"   help:"Open GitHub search in browser"              clib:"terse='Web search',group='Actions/1'"      short:"w"`

	Send   bool   `name:"send"    help:"Send slack output to configured recipient(s)"      clib:"terse='Send to Slack',group='Actions/2'"`
	SendAt string `name:"send-at" help:"Schedule slack send (+5m, +2h, HH:MM, Unix ts)"    clib:"terse='Schedule Slack send',group='Actions/2'"                                           placeholder:"<time>"`
	SendTo string `name:"send-to" help:"Override Slack recipient (#channel, @user, email)" clib:"terse='Override Slack recipient',complete='predictor=slack-recipient',group='Actions/2'" placeholder:"<recipient>"`

	// Output flags
	Watch    bool    `name:"watch"   help:"Refresh output periodically"                                                                        short:"W"                                     clib:"terse='Watch mode',group='Output/0'"`
	ExitZero bool    `name:"exit-0"  help:"Exit immediately when there are no results"                                                         short:"0"                                     clib:"terse='Exit on no match',group='Output/0'"`
	Columns  CSVFlag `name:"columns" help:"Table columns [index, ref, repo, org, number, title, labels, author, state, created, updated, url]"           aliases:"col" placeholder:"<cols>"  clib:"terse='Table columns',complete='predictor=columns,comma',group='Output/1'"`
	Limit    *int    `name:"limit"   help:"Maximum results"                                                                                    short:"L"               placeholder:"<n>"     clib:"terse='Max results',group='Output/1'"`
	Output   *string `name:"output"  help:"Output format"                                                                                      short:"o"               placeholder:"<fmt>"   clib:"terse='Output format',complete='values=url bullet slack table json repo',group='Output/1',enum='url,bullet,slack,table,json,repo',highlight='u,b,s,t,j,r',default='table'"`
	Reverse  bool    `name:"reverse" help:"Show oldest first (top)"                                                                                                                          clib:"terse='Reverse display order',group='Output/1'"`
	Sort     *string `name:"sort"    help:"Sort by"                                                                                                                    placeholder:"<field>" clib:"terse='Sort field',complete='values=name created updated',group='Output/1',enum='name,created,updated',highlight='n,c,u',default='name'"`

	// Miscellaneous
	Init    bool   `name:"init"    help:"Initialize config with defaults"            clib:"terse='Initialize config',group='Miscellaneous/0'"`
	Color   string `name:"color"   help:"When to use color"                          clib:"terse='Color mode',complete='values=auto always never',group='Miscellaneous/1',enum='auto,always,never',default='auto'" default:"auto"`
	Debug   bool   `name:"debug"   help:"Log HTTP requests to stderr"                clib:"terse='Debug mode',group='Miscellaneous/1'"`
	Quick   bool   `name:"quick"   help:"Skip enrichment (merge status, auto-merge)" clib:"terse='Skip enrichment',group='Miscellaneous/1'"                                                                                       short:"Q"`
	Verbose bool   `name:"verbose" help:"Enable verbose logging"                     clib:"terse='Verbose',group='Miscellaneous/1'"                                                                                               short:"v"`

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
	sending := c.Send || c.SendTo != "" || c.SendAt != ""
	if sending && c.Clone {
		return fmt.Errorf("--send and --clone are mutually exclusive")
	}
	if sending && c.Open {
		return fmt.Errorf("--send and --open are mutually exclusive")
	}
	if sending && c.Web {
		return fmt.Errorf("--send and --web are mutually exclusive")
	}
	if c.Author != nil && len(c.Team.Values) > 0 {
		return fmt.Errorf("--author and --team are mutually exclusive")
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
				"invalid --output value %q (valid: table/t, url/u, bullet/b, slack/s, json/j, repo/r)",
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

	// Normalize org
	c.Organization.Values = normalizeCSV(c.Organization.Values)

	// --since is a hidden alias for --created
	if c.Since != "" && c.Created == "" {
		c.Created = c.Since
	}

	// Apply config defaults where CLI didn't set them
	if len(c.Organization.Values) == 0 && len(cfg.Default.Organizations) > 0 {
		c.Organization.Values = cfg.Default.Organizations
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
	if c.State == "" {
		c.State = cfg.Default.State
	}
	c.outputExplicit = c.Output != nil
	if c.Output == nil {
		c.Output = &cfg.Default.Output
	}

	// Author defaults
	// --requested implies --author=any (don't restrict to self)
	if c.Author == nil && len(c.ReviewRequested.Values) > 0 {
		c.Author = &CSVFlag{Values: []string{valueAll}}
	}
	if c.Author == nil {
		c.Author = &CSVFlag{Values: cfg.Default.Authors}
	}

	// Bots: config bots=false implies --no-bot
	if !cfg.Default.Bots && !c.NoBot {
		c.NoBot = true
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
	return c.Approve || c.Close || c.Comment != "" || c.Edit || c.ForceMerge || c.MarkDraft ||
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
	return c.Approve || c.Close || c.Comment != "" || c.Edit || c.ForceMerge || c.MarkDraft ||
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
// because GitHub search uses "-" only for qualifier negation (e.g. -org:foo),
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
	if c.HasAction() {
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

	// --send-at and --send-to imply --send
	if c.SendAt != "" {
		c.Send = true
	}
	if c.SendTo != "" {
		c.SendTo = normalizeSlackChannel(c.SendTo)
		c.Send = true
	}

	// --send: table for interactive selection; slack for non-interactive (--yes)
	if c.Send {
		if c.IsInteractive() {
			c.setOutput(valueTable)
		} else {
			c.setOutput("slack")
		}
	}

	// -o slack implies --copy
	if c.OutputFormat() == OutputSlack {
		c.Copy = true
	}
}
