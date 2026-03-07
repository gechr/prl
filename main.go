package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/kong"
	"github.com/cli/go-gh/v2/pkg/api"
	clib "github.com/gechr/clib/cli/kong"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clib/prompt"
	"github.com/gechr/clib/terminal"
	"github.com/gechr/clog"
)

// Sentinel errors for controlled exits.
var (
	errOK    = errors.New("ok")    // caller handled it; exit 0
	errFatal = errors.New("fatal") // caller already logged; exit 1
)

func main() {
	if err := run(); err != nil {
		switch {
		case errors.Is(err, errOK):
			return
		case errors.Is(err, errFatal):
			os.Exit(1)
		default:
			clog.Fatal().Err(err).Send()
		}
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

	// Init mode: write default config and exit
	if cli.Init {
		return initConfig()
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

	// Count mode: use API total_count (single lightweight request)
	if cli.Count {
		count, cErr := executeCount(rest, params)
		if cErr != nil {
			return cErr
		}
		fmt.Println(count)
		return nil
	}

	s := buildSpinner(cfg.Spinner)

	// Watch mode: loop search+render with screen clear
	if cli.Watch {
		return runWatch(prl, rest, &cli, cfg, tty, params, s)
	}

	var output string
	if err := withSpinner(tty, s, func(stopSpinner func()) error {
		var runErr error
		output, runErr = runOnce(prl, rest, &cli, cfg, tty, params, stopSpinner)
		return runErr
	}); err != nil {
		return err
	}
	if output != "" {
		fmt.Println(output)
	}
	return nil
}

// withSpinner runs fn in a goroutine while displaying an inline spinner on a TTY.
// Returns fn's result. On non-TTY it just runs fn directly.
func withSpinner[T any](tty bool, s spinner, fn func(stop func()) T) T {
	if !tty {
		return fn(func() {})
	}

	stopReq := make(chan struct{})
	stopAck := make(chan struct{})
	done := make(chan T, 1)
	go func() {
		var once sync.Once
		stop := func() {
			once.Do(func() {
				stopReq <- struct{}{}
				<-stopAck
			})
		}
		done <- fn(stop)
	}()

	fmt.Print(ansiHideCursor)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	i := 0
	for {
		select {
		case result := <-done:
			fmt.Print(ansiSpinnerClear)
			return result
		case <-stopReq:
			fmt.Print(ansiSpinnerClear)
			stopAck <- struct{}{}
			return <-done
		case <-ticker.C:
			fmt.Print("\r" + s.frames[i%len(s.frames)])
			i++
		}
	}
}

// Spinner style names.
const (
	spinnerDots  = "dots"
	spinnerStars = "stars"

	defaultSpinner = spinnerDots
)

// Default spinner colors (256-color palette).
var defaultSpinnerColors = []string{"210", "211", "212", "217", "218", "225"}

type spinner struct {
	frames   []string
	interval time.Duration
}

// spinnerStyle defines the raw glyphs and tick rate for a spinner style.
type spinnerStyle struct {
	glyphs   []string
	interval time.Duration
}

var spinnerStyles = map[string]spinnerStyle{
	spinnerDots: {
		glyphs:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		interval: 80 * time.Millisecond, //nolint:mnd // spinner tick rate
	},
	spinnerStars: {
		glyphs:   []string{"·", "✢", "✳", "✶", "✻", "✽"},
		interval: 150 * time.Millisecond, //nolint:mnd // spinner tick rate
	},
}

func buildSpinner(cfg SpinnerConfig) spinner {
	style, ok := spinnerStyles[cfg.Style]
	if !ok {
		clog.Warn().
			Msgf("Invalid spinner '%s' defined in config - falling back to '%s'", cfg.Style, defaultSpinner)
		style = spinnerStyles[defaultSpinner]
	}

	colors := cfg.Colors
	if len(colors) == 0 {
		colors = defaultSpinnerColors
	}

	frames := make([]string, len(style.glyphs))
	for i, glyph := range style.glyphs {
		c := colors[i%len(colors)]
		frames[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(glyph)
	}

	return spinner{frames: frames, interval: style.interval}
}

var noResults = "\n  " + lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("8")).
	Render("[no results]")

// watchInterval returns a refresh duration scaled by result count:
// fewer results refresh faster, more results refresh slower to conserve API calls.
func watchInterval(n int) time.Duration {
	d := watchMinInterval + time.Duration(n)*watchScalePer
	if d > watchMaxInterval {
		return watchMaxInterval
	}
	return d
}

// runWatch loops buildOutput every watchInterval, clearing the screen between refreshes.
func runWatch(
	p *prl,
	rest *api.RESTClient,
	cli *CLI,
	cfg *Config,
	tty bool,
	params *SearchParams,
	s spinner,
) error {
	// First fetch with spinner before entering the alternate screen.
	type fetchResult struct {
		output string
		count  int
		err    error
	}
	r := withSpinner(tty, s, func(func()) fetchResult {
		out, n, fErr := buildOutput(p, rest, cli, cfg, tty, params)
		return fetchResult{out, n, fErr}
	})

	if r.err != nil {
		return r.err
	}
	if r.output == "" && cli.ExitZero {
		return errFatal
	}

	output, count := r.output, r.count

	fmt.Print(ansiAltScreenOn + ansiHideCursor)
	cleanup := func() { fmt.Print(ansiShowCursor + ansiAltScreenOff) }
	defer cleanup()

	// Restore terminal on interrupt.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup()
		os.Exit(0)
	}()

	results := make(chan fetchResult, 1)
	fetch := func() {
		go func() {
			out, n, fErr := buildOutput(p, rest, cli, cfg, tty, params)
			results <- fetchResult{out, n, fErr}
		}()
	}

	var (
		fetching    bool
		interval    time.Duration
		lastOutput  string
		nextFetchAt time.Time
		spinnerTick int
	)

	// Use the first fetch result.
	if output != "" {
		lastOutput = output
	} else {
		lastOutput = noResults
	}
	interval = watchInterval(count)
	nextFetchAt = time.Now().Add(interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for range ticker.C {
		// Check for fetch completion (non-blocking).
		select {
		case r := <-results:
			fetching = false
			switch {
			case r.err != nil:
				clog.Error().Err(r.err).Msg("Refresh failed")
			case r.output != "":
				lastOutput = r.output
			case cli.ExitZero:
				return errFatal
			default:
				lastOutput = noResults
			}
			interval = watchInterval(r.count)
			nextFetchAt = time.Now().Add(interval)
		default:
		}

		// Repaint.
		fmt.Print(ansiClearScreen)
		if lastOutput != "" {
			fmt.Println(lastOutput)
		}
		if fetching && lastOutput != "" {
			frame := s.frames[spinnerTick%len(s.frames)]
			fmt.Print(ansiMoveTo1x1 + frame)
			spinnerTick++
		}

		// Schedule next fetch when due.
		if !fetching && !nextFetchAt.IsZero() && time.Now().After(nextFetchAt) {
			fetching = true
			spinnerTick = 0
			fetch()
		}
	}

	return nil
}

// buildOutput runs the search+filter+enrich+render pipeline and returns the
// rendered output string, the number of PRs, and any error.
func buildOutput(
	p *prl,
	rest *api.RESTClient,
	cli *CLI,
	cfg *Config,
	tty bool,
	params *SearchParams,
) (string, int, error) {
	// Execute search
	prs, err := executeSearch(rest, params)
	if err != nil {
		return "", 0, err
	}

	// Apply filters
	prs, err = applyFilters(cli, prs)
	if err != nil {
		return "", 0, err
	}
	if len(prs) == 0 {
		return "", 0, nil
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
			return "", 0, gqlErr
		}
		prs, err = filterByAutoMerge(g, prs, !*cli.Merge)
		if err != nil {
			return "", 0, err
		}
		if len(prs) == 0 {
			return "", 0, nil
		}
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
	n := len(prs)
	switch cli.OutputFormat() {
	case OutputTable:
		resolver := NewAuthorResolver(cfg)
		renderer := p.NewTableRenderer(cli, tty, resolver)
		output, _ := renderer.Render(prs)
		return output, n, nil
	case OutputURL:
		return renderURLs(prs), n, nil
	case OutputBullet:
		return renderBullets(prs), n, nil
	case OutputJSON:
		out, err := renderJSON(prs)
		return out, n, err
	case OutputSlack:
		if cli.Send && cli.SendTo == "" {
			return renderSlackDisplay(prs, cfg), n, nil
		}
		output, _ := renderSlack(prs, cfg)
		return output, n, nil
	case OutputRepo:
		return renderRepos(prs), n, nil
	default:
		return renderURLs(prs), n, nil
	}
}

// runOnce executes a single search+render cycle. It returns the output string
// to print (if any) separately from the error, so the caller can print it
// after clearing the spinner.
func runOnce(
	prl *prl,
	rest *api.RESTClient,
	cli *CLI,
	cfg *Config,
	tty bool,
	params *SearchParams,
	stopSpinner func(),
) (string, error) {
	// Execute search
	prs, err := executeSearch(rest, params)
	if err != nil {
		return "", err
	}

	// Apply filters
	prs, err = applyFilters(cli, prs)
	if err != nil {
		return "", err
	}
	if len(prs) == 0 {
		if cli.ExitZero {
			return "", errFatal
		}
		return "", nil
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
			return "", gqlErr
		}
		prs, err = filterByAutoMerge(g, prs, !*cli.Merge)
		if err != nil {
			return "", err
		}
		if len(prs) == 0 {
			return "", nil
		}
	}

	// Clone mode: clone unique repos and exit
	if cli.Clone {
		return "", cloneRepos(rest, prs, cfg.VCS)
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
		renderer := prl.NewTableRenderer(cli, tty, resolver)
		output, rows = renderer.Render(prs)
	case OutputURL:
		output = renderURLs(prs)
	case OutputBullet:
		output = renderBullets(prs)
	case OutputJSON:
		output, err = renderJSON(prs)
		if err != nil {
			return "", err
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
		return "", nil
	}

	// Clipboard copy (before interactive selection)
	if cli.Copy {
		if err := copyToClipboard(output); err != nil {
			clog.Warn().Err(err).Msg("Clipboard copy failed")
		}
	}

	// Interactive selection (only for table output with action flags)
	if cli.IsInteractive() && rows != nil {
		if stopSpinner != nil {
			stopSpinner()
		}
		return "", runInteractive(cli, rest, cfg, rows)
	}

	// Non-interactive actions: pass PRs directly
	if cli.HasAction() {
		actions, aErr := newActionRunner(cli, rest)
		if aErr != nil {
			return "", aErr
		}
		return "", actions.Execute(cli, prs)
	}

	// Open in browser
	if cli.Open {
		urls := make([]string, len(prs))
		for i, pr := range prs {
			urls[i] = pr.URL
		}
		return "", openBrowser(urls...)
	}

	// Send to Slack
	if cli.Send {
		if err := sendSlack(prs, cli, cfg); err != nil {
			return "", err
		}
	}

	return output, nil
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
