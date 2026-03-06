package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/cli/go-gh/v2/pkg/api"
	clib "github.com/gechr/clib/cli/kong"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clib/prompt"
	"github.com/gechr/clib/terminal"
	"github.com/gechr/clog"
)

func main() {
	if err := run(); err != nil {
		clog.Fatal().Err(err).Send()
	}
}

func run() error {
	prl := New()

	// Load configuration
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Parse CLI arguments
	var cli CLI
	parser := kong.Must(&cli,
		kong.Name("prl"),
		kong.Description("Search, filter, display, and act on GitHub pull requests"),
		kong.UsageOnError(),
		kong.Help(prl.helpPrinter(cfg)),
	)

	_, err = parser.Parse(os.Args[1:])
	if err != nil {
		parser.FatalIfErrorf(err)
	}

	// Handle completion (before validation/search logic)
	gen := complete.NewGenerator("prl").FromFlags(clib.Reflect(&cli))
	gen.Specs = append(gen.Specs,
		complete.Spec{ShortFlag: "h", Terse: "Short help"},
		complete.Spec{LongFlag: "help", Terse: "Long help"},
	)
	var completeErr error
	handled, hErr := cli.Handle(
		gen,
		func(shell, kind string, _ []string) {
			completeErr = prl.handleComplete(shell, kind, cfg)
		},
	)
	if hErr != nil {
		return hErr
	}
	if completeErr != nil {
		return completeErr
	}
	if handled {
		return nil
	}

	// Configure logging and color
	clog.SetVerbose(cli.Verbose)
	symbols := clog.DefaultSymbols()
	symbols[clog.LevelInfo] = "✅"
	clog.SetSymbols(symbols)
	tty := applyColorMode(cli.Color)

	// Validate
	if vErr := cli.Validate(); vErr != nil {
		return vErr
	}

	// Normalize with config defaults
	cli.Normalize(cfg)

	// When a single org is active, Ref() omits the org prefix for brevity.
	refSingleOrg = singleOrg(cli.Organization.Values)

	// Apply output mode overrides based on action flags
	cli.ApplyOutputOverrides()

	// Build search query
	params, err := buildSearchQuery(&cli, cfg)
	if err != nil {
		return err
	}

	// Dry run mode
	if cli.Dry {
		fmt.Println(prl.buildDryRunOutput(params, &cli, cfg))
		return nil
	}

	// Web mode
	if cli.Web {
		return executeWebSearch(params)
	}

	// Create REST client
	rest, err := newRESTClient(withDebug(cli.Debug))
	if err != nil {
		return fmt.Errorf("creating REST client: %w", err)
	}

	// --send is restricted to the authenticated user's own PRs
	if cli.Send {
		if len(cli.Team.Values) > 0 {
			return fmt.Errorf("--send is only allowed for your own PRs (cannot use --team)")
		}
		if sendErr := requireOwnAuthor(rest, cli.Author.Values); sendErr != nil {
			return sendErr
		}
	}

	// Execute search
	prs, err := executeSearch(rest, params)
	if err != nil {
		return err
	}

	// Apply filters
	prs, err = applyFilters(&cli, prs)
	if err != nil {
		return err
	}
	if len(prs) == 0 {
		return nil
	}

	// Force-merge: filter to repos where user has admin/bypass permissions.
	if cli.ForceMerge {
		prs = filterByAdminAccess(rest, prs)
		if len(prs) == 0 {
			return nil
		}
	}

	// Lazy GraphQL client (shared by automerge filter and merge status enrichment).
	var gql *api.GraphQLClient
	getGQL := func() (*api.GraphQLClient, error) {
		if gql == nil {
			var gqlErr error
			gql, gqlErr = newGraphQLClient(withDebug(cli.Debug))
			if gqlErr != nil {
				return nil, fmt.Errorf("creating GraphQL client: %w", gqlErr)
			}
		}
		return gql, nil
	}

	// Filter by automerge status: --merge shows PRs without automerge,
	// --no-merge shows PRs with automerge.
	if cli.Merge != nil {
		g, gqlErr := getGQL()
		if gqlErr != nil {
			return gqlErr
		}
		prs, err = filterByAutoMerge(g, prs, !*cli.Merge)
		if err != nil {
			return err
		}
		if len(prs) == 0 {
			return nil
		}
	}

	// Clone mode: clone unique repos and exit
	if cli.Clone {
		return cloneRepos(rest, prs, cfg.VCS)
	}

	// In quick mode, default open PRs to blocked so they render in blue instead of dim.
	if cli.Quick {
		for i := range prs {
			if prs[i].State == valueOpen {
				prs[i].MergeStatus = MergeStatusBlocked
			}
		}
	}

	// Enrich open PRs with CI/review status for table coloring and status column.
	if !cli.Quick && cli.OutputFormat() == OutputTable {
		if g, gqlErr := getGQL(); gqlErr == nil {
			enrichMergeStatus(g, prs)
		} else {
			clog.Debug().Err(gqlErr).Msg("skipping merge status enrichment")
		}
	}

	// Enrich auto-merge status for Slack reactions (only when actually sending).
	if !cli.Quick && cli.Send {
		if g, gqlErr := getGQL(); gqlErr == nil {
			if amErr := enrichAutoMerge(g, prs); amErr != nil {
				clog.Debug().Err(amErr).Msg("Failed to enrich auto-merge status")
			}
		} else {
			clog.Debug().Err(gqlErr).Msg("Skipping auto-merge enrichment")
		}
	}

	// Render output
	var output string
	var rows []TableRow

	switch cli.OutputFormat() {
	case OutputTable:
		resolver := NewAuthorResolver(cfg)
		renderer := prl.NewTableRenderer(&cli, tty, resolver)
		output, rows = renderer.Render(prs)
	case OutputURL:
		output = renderURLs(prs)
	case OutputBullet:
		output = renderBullets(prs)
	case OutputJSON:
		output, err = renderJSON(prs)
		if err != nil {
			return err
		}
	case OutputSlack:
		if cli.Send && cli.SendTo == "" {
			output = renderSlackDisplay(prs, cfg)
		} else {
			output, _ = renderSlack(prs, cfg)
		}
	case OutputRepo:
		output = renderRepos(prs)
	default:
		output = renderURLs(prs)
	}

	if output == "" {
		return nil
	}

	// Clipboard copy (before interactive selection)
	if cli.Copy {
		if err := copyToClipboard(output); err != nil {
			clog.Warn().Err(err).Msg("Clipboard copy failed")
		}
	}

	// Interactive selection (only for table output with action flags)
	if cli.IsInteractive() && rows != nil {
		return runInteractive(&cli, rest, cfg, rows)
	}

	// Non-interactive actions: pass PRs directly
	if cli.HasAction() {
		actions, aErr := newActionRunner(&cli, rest)
		if aErr != nil {
			return aErr
		}
		return actions.Execute(&cli, prs)
	}

	// Open in browser
	if cli.Open {
		urls := make([]string, len(prs))
		for i, pr := range prs {
			urls[i] = pr.URL
		}
		return openBrowser(urls...)
	}

	// Print output
	fmt.Println(output)

	// Send to Slack
	if cli.Send {
		if err := sendSlack(prs, &cli, cfg); err != nil {
			return err
		}
	}

	return nil
}

// runInteractive shows the multi-select prompt and dispatches to send or action runner.
func runInteractive(cli *CLI, rest *api.RESTClient, cfg *Config, rows []TableRow) error {
	selected, err := interactiveSelect(rows, buildActionHeader(cli))
	if errors.Is(err, prompt.ErrCancelled) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return nil
	}

	selectedPRs := make([]PullRequest, len(selected))
	for i, row := range selected {
		selectedPRs[i] = row.Item
	}

	if cli.Send {
		return runInteractiveSend(cli, cfg, selectedPRs)
	}

	actions, aErr := newActionRunner(cli, rest)
	if aErr != nil {
		return aErr
	}

	if cli.Edit {
		return interactiveEdit(actions, selectedPRs)
	}

	return actions.Execute(cli, selectedPRs)
}

// runInteractiveSend renders and sends selected PRs to Slack after interactive selection.
func runInteractiveSend(cli *CLI, cfg *Config, prs []PullRequest) error {
	var slackOutput string
	if cli.SendTo == "" {
		slackOutput = renderSlackDisplay(prs, cfg)
	} else {
		slackOutput, _ = renderSlack(prs, cfg)
	}
	if slackOutput == "" {
		return nil
	}
	if err := copyToClipboard(slackOutput); err != nil {
		clog.Warn().Err(err).Msg("Clipboard copy failed")
	}
	fmt.Println(slackOutput)
	return sendSlack(prs, cli, cfg)
}

// buildActionHeader creates the interactive selection header from active action flags.
func buildActionHeader(cli *CLI) string {
	var parts []string
	if cli.Approve {
		parts = append(parts, "Approve")
	}
	if cli.Close {
		parts = append(parts, "Close")
	}
	if cli.Comment != "" {
		parts = append(parts, "Comment")
	}
	if cli.Edit {
		parts = append(parts, "Edit")
	}
	if cli.ForceMerge {
		parts = append(parts, "Force-merge")
	}
	if cli.MarkDraft {
		parts = append(parts, "Mark draft")
	}
	if cli.MarkReady {
		parts = append(parts, "Mark ready")
	}
	if cli.Merge != nil && *cli.Merge {
		parts = append(parts, "Merge")
	}
	if cli.Update {
		parts = append(parts, "Update")
	}
	if cli.Send {
		parts = append(parts, "Send")
	}
	if len(parts) == 0 {
		return "Select PRs:"
	}
	return strings.Join(parts, " / ") + ":"
}

// applyColorMode configures global color settings based on --color and returns
// whether stdout should be treated as a terminal for ANSI sequences.
func applyColorMode(color string) bool {
	switch color {
	case "always":
		clog.SetColorMode(clog.ColorAlways)
		return true
	case "never":
		clog.SetColorMode(clog.ColorNever)
		return false
	default: // "auto"
		return terminal.Is(os.Stdout)
	}
}
