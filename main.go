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
	"github.com/charmbracelet/colorprofile"
	"github.com/cli/go-gh/v2/pkg/api"
	clib "github.com/gechr/clib/cli/kong"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clog"
	cspinner "github.com/gechr/clog/fx/spinner"
	"github.com/gechr/primer/pick"
	"github.com/gechr/primer/term"
)

var version = "dev"

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
	// Route all log output to stderr so stdout is reserved for machine-readable data.
	clog.SetOutputWriter(os.Stderr)

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
		kong.Help(prl.helpPrinter(cfg)),
	)

	_, err = parser.Parse(os.Args[1:])
	if err != nil {
		clog.Fatal().Msg(err.Error())
	}

	if cli.Version {
		fmt.Println(version)
		return errOK
	}

	// Handle completion (before validation/search logic)
	flags, flagsErr := clib.Reflect(&cli)
	if flagsErr != nil {
		clog.Fatal().Msg(flagsErr.Error())
	}
	gen := complete.NewGenerator("prl").FromFlags(flags)
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

	// Track which flags were explicitly set on CLI (before Normalize applies config defaults).
	// Bool flags: true iff --flag was passed. Strings: non-empty iff --flag was passed.
	// *bool: non-nil iff --flag was passed.
	cli.stateExplicit = cli.State != ""
	cli.draftExplicit = cli.Draft != nil
	cli.noBotExplicit = cli.NoBot
	cli.archivedExplicit = cli.Archived
	cli.ciExplicit = cli.CI != ""
	cli.reviewExplicit = cli.Review != ""

	// Normalize with config defaults
	cli.Normalize(cfg)

	// When a single owner is active, Ref() omits the owner prefix for brevity.
	refSingleOwner = singleOwner(cli.Owner.Values)

	// Apply output mode overrides based on action flags
	cli.ApplyOutputOverrides()

	// Build search query
	params, err := buildSearchQuery(&cli, cfg)
	if err != nil {
		return err
	}

	// Dry run mode
	if cli.Dry {
		lipgloss.Println(prl.buildDryRunOutput(params, &cli))
		return nil
	}

	// Web mode
	if cli.Web {
		return executeWebSearch(params)
	}

	// Ensure GitHub authentication before any API calls
	if err = ensureGHAuth(); err != nil {
		return err
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
	clog.SetSpinnerStyle(cspinner.Style{Frames: s.frames, Interval: s.interval})

	// Watch mode: loop search+render with screen clear
	if cli.Watch {
		return runWatch(prl, rest, &cli, cfg, tty, params, s)
	}

	// Interactive TUI browser
	if cli.Interactive {
		if !tty {
			return fmt.Errorf("--interactive requires a TTY")
		}
		cli.setOutput(valueTable)
		return runTui(prl, rest, &cli, cfg, tty, params, s)
	}

	var output string
	if err := withSpinner(tty && !cli.Debug, s, func(stopSpinner func()) error {
		var runErr error
		output, runErr = runOnce(prl, rest, &cli, cfg, tty, params, stopSpinner)
		return runErr
	}); err != nil {
		return err
	}
	if output != "" {
		lipgloss.Println(output)
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

	// Clear the spinner line before any clog write so output doesn't interleave.
	clog.AddHook(clog.HookBeforeWrite, func() { fmt.Print(ansiSpinnerClear) })
	defer clog.ClearHooks(clog.HookBeforeWrite)

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
var defaultSpinnerColors = []string{"218"}

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

var noResults = "\n  " + styleDraft.Bold(true).Render("[no results]")

// watchInterval returns a refresh duration scaled by result count:
// fewer results refresh faster, more results refresh slower to conserve API calls.
func watchInterval(n int) time.Duration {
	d := watchMinInterval + time.Duration(n)*watchScalePer
	if d > watchMaxInterval {
		return watchMaxInterval
	}
	return refreshCooldownDelay(d)
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
		prs    []PullRequest
		err    error
	}
	r := withSpinner(tty && !cli.Debug, s, func(func()) fetchResult {
		out, prs, fErr := buildOutput(p, rest, cli, cfg, tty, params)
		return fetchResult{out, prs, fErr}
	})

	if r.err != nil {
		return r.err
	}
	if r.output == "" && cli.ExitZero {
		return errFatal
	}

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

	// Re-render cached PRs on terminal resize (SIGWINCH).
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)

	results := make(chan fetchResult, 1)
	fetch := func() {
		go func() {
			out, prs, fErr := buildOutput(p, rest, cli, cfg, tty, params)
			results <- fetchResult{out, prs, fErr}
		}()
	}

	var (
		fetching    bool
		interval    time.Duration
		lastOutput  string
		lastPRs     []PullRequest
		nextFetchAt time.Time
		spinnerTick int
	)

	// Use the first fetch result.
	lastPRs = r.prs
	if r.output != "" {
		lastOutput = r.output
	} else {
		lastOutput = noResults
	}
	interval = watchInterval(len(lastPRs))
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
				lastPRs = r.prs
			case cli.ExitZero:
				return errFatal
			default:
				lastOutput = noResults
				lastPRs = nil
			}
			interval = watchInterval(len(lastPRs))
			nextFetchAt = time.Now().Add(interval)
		default:
		}

		// Re-render on terminal resize.
		select {
		case <-winch:
			if len(lastPRs) > 0 {
				if out, err := renderOutput(p, cli, cfg, tty, lastPRs); err == nil && out != "" {
					lastOutput = out
				}
			}
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

func applyListMetadata(
	cli *CLI,
	getGQL func() (*api.GraphQLClient, error),
	prs []PullRequest,
	needAutomerge bool,
	needMergeStatus bool,
	closedAllowed map[string]bool,
	mergedAllowed map[string]bool,
) ([]PullRequest, error) {
	needTimeline := len(closedAllowed) > 0 || len(mergedAllowed) > 0
	if !needTimeline && !needMergeStatus && !needAutomerge {
		return prs, nil
	}

	g, err := getGQL()
	if err != nil {
		if cli.Merge != nil || needTimeline {
			return nil, err
		}
		clog.Debug().Err(err).Msg("skipping list metadata hydration")
		return prs, nil
	}

	actors, err := hydrateListMetadata(g, prs, listMetadataRequest{
		automerge:      needAutomerge,
		mergeStatus:    needMergeStatus,
		timelineClosed: len(closedAllowed) > 0,
		timelineMerged: len(mergedAllowed) > 0,
	})
	if err != nil {
		if cli.Merge != nil || needTimeline {
			return nil, err
		}
		clog.Debug().Err(err).Msg("skipping list metadata hydration")
		return prs, nil
	}

	if cli.Merge != nil {
		prs = filterByAutomergeState(prs, !*cli.Merge)
	}
	if needTimeline {
		prs = filterByTimelineActorsLoaded(prs, closedAllowed, mergedAllowed, actors)
	}
	return prs, nil
}

// buildOutput runs the search+filter+enrich+render pipeline and returns the
// rendered output string, the PRs (for re-rendering on resize), and any error.
func buildOutput(
	p *prl,
	rest *api.RESTClient,
	cli *CLI,
	cfg *Config,
	tty bool,
	params *SearchParams,
) (string, []PullRequest, error) {
	// Execute search
	prs, err := executeSearch(rest, params)
	if err != nil {
		return "", nil, err
	}

	// Apply filters
	prs, err = applyFilters(cli, prs)
	if err != nil {
		return "", nil, err
	}
	if len(prs) == 0 {
		return "", nil, nil
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

	// Resolve timeline filter logins before the shared metadata pass.
	closedAllowed, err := resolveTimelineLogins(rest, cli.ClosedBy.Values)
	if err != nil {
		return "", nil, err
	}
	mergedAllowed, err := resolveTimelineLogins(rest, cli.MergedBy.Values)
	if err != nil {
		return "", nil, err
	}

	ready := cli.PRState() == StateReady
	ciFilter := cli.CIStatus()
	needsEnrich := ready || ciFilter != CINone
	needMergeStatus := (!cli.Quick || needsEnrich) &&
		(cli.OutputFormat() == OutputTable || needsEnrich)
	needAutomerge := cli.Merge != nil || (!cli.Quick && cli.Send)
	prs, err = applyListMetadata(
		cli,
		getGQL,
		prs,
		needAutomerge,
		needMergeStatus,
		closedAllowed,
		mergedAllowed,
	)
	if err != nil {
		return "", nil, err
	}
	if len(prs) == 0 {
		return "", nil, nil
	}

	// In quick mode, default open PRs to blocked so they render in blue instead of dim.
	if cli.Quick && !needMergeStatus {
		for i := range prs {
			if prs[i].State == valueOpen {
				prs[i].MergeStatus = MergeStatusBlocked
			}
		}
	}

	// Post-filter: --state=ready keeps only PRs that are ready to merge.
	if ready {
		prs = filterReady(prs)
		if len(prs) == 0 {
			return "", nil, nil
		}
	}

	// Post-filter: --ci keeps only PRs matching the requested CI status.
	if ciFilter != CINone {
		prs = filterByCI(prs, ciFilter)
		if len(prs) == 0 {
			return "", nil, nil
		}
	}

	// Render output
	out, rErr := renderOutput(p, cli, cfg, tty, prs)
	return out, prs, rErr
}

// renderOutput renders PRs in the requested output format.
func renderOutput(
	p *prl,
	cli *CLI,
	cfg *Config,
	tty bool,
	prs []PullRequest,
) (string, error) {
	switch cli.OutputFormat() {
	case OutputTable:
		resolver := NewAuthorResolver(cfg)
		ownerFilter := singleOwner(cli.Owner.Values)
		models := buildPRRowModels(prs, ownerFilter, resolver)
		renderer := p.NewTableRenderer(cli, tty)
		output := renderer.Render(models).String()
		return output, nil
	case OutputURL:
		return renderURLs(prs), nil
	case OutputBullet:
		return renderBullets(prs), nil
	case OutputJSON:
		return renderJSON(prs)
	case OutputRepo:
		return renderRepos(prs), nil
	default:
		return renderURLs(prs), nil
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

	// Resolve timeline filter logins before the shared metadata pass.
	closedAllowed, err := resolveTimelineLogins(rest, cli.ClosedBy.Values)
	if err != nil {
		return "", err
	}
	mergedAllowed, err := resolveTimelineLogins(rest, cli.MergedBy.Values)
	if err != nil {
		return "", err
	}

	ready := cli.PRState() == StateReady
	ciFilter := cli.CIStatus()
	needsEnrich := ready || ciFilter != CINone
	needMergeStatus := (!cli.Quick || needsEnrich) &&
		(cli.OutputFormat() == OutputTable || needsEnrich)
	needAutomerge := cli.Merge != nil || (!cli.Quick && cli.Send)

	prs, err = applyListMetadata(
		cli,
		getGQL,
		prs,
		needAutomerge,
		needMergeStatus,
		closedAllowed,
		mergedAllowed,
	)
	if err != nil {
		return "", err
	}
	if len(prs) == 0 {
		return "", nil
	}

	// In quick mode, default open PRs to blocked so they render in blue instead of dim.
	if cli.Quick && !needMergeStatus {
		for i := range prs {
			if prs[i].State == valueOpen {
				prs[i].MergeStatus = MergeStatusBlocked
			}
		}
	}

	// Post-filter: --state=ready keeps only PRs that are ready to merge.
	if ready {
		prs = filterReady(prs)
		if len(prs) == 0 {
			return "", nil
		}
	}

	// Post-filter: --ci keeps only PRs matching the requested CI status.
	if ciFilter != CINone {
		prs = filterByCI(prs, ciFilter)
		if len(prs) == 0 {
			return "", nil
		}
	}

	// Render output
	var output string
	var rows []TableRow

	switch cli.OutputFormat() {
	case OutputTable:
		resolver := NewAuthorResolver(cfg)
		ownerFilter := singleOwner(cli.Owner.Values)
		models := buildPRRowModels(prs, ownerFilter, resolver)
		renderer := prl.NewTableRenderer(cli, tty)
		rt := renderer.Render(models)
		output = rt.String()
		rows = rt.Rows
	case OutputURL:
		output = renderURLs(prs)
	case OutputBullet:
		output = renderBullets(prs)
	case OutputJSON:
		output, err = renderJSON(prs)
		if err != nil {
			return "", err
		}
	case OutputRepo:
		output = renderRepos(prs)
	default:
		output = renderURLs(prs)
	}

	if output == "" {
		return "", nil
	}

	// Clipboard copy (before interactive selection) - always copy plain URLs.
	if cli.Copy {
		urls := make([]string, len(prs))
		for i, pr := range prs {
			urls[i] = pr.URL
		}
		natsort(urls)
		if err := copyToClipboard(strings.Join(urls, nl)); err != nil {
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

	// Non-interactive clone: --clone --yes
	if cli.Clone {
		if stopSpinner != nil {
			stopSpinner()
		}
		return "", cloneRepos(rest, prs, cfg.VCS, cli.Debug)
	}

	// Non-interactive actions: pass PRs directly
	if stopSpinner != nil {
		stopSpinner()
	}
	if err := runActions(cli, rest, prs); err != nil {
		return "", err
	}

	// Open in browser
	if cli.Open {
		urls := make([]string, len(prs))
		for i, pr := range prs {
			urls[i] = pr.URL
		}
		return "", openBrowser(urls...)
	}

	// Send to Slack via plugin
	if cli.Send {
		if err := pluginSlackSend(cfg, cli.SendTo, prs); err != nil {
			return "", err
		}
	}

	if cli.HasAction() || cli.Clone {
		return "", nil
	}
	return output, nil
}

// runInteractive shows the multi-select prompt and dispatches to send or action runner.
func runInteractive(cli *CLI, rest *api.RESTClient, cfg *Config, rows []TableRow) error {
	selected, err := interactiveSelect(rows, buildActionHeader(cli))
	if errors.Is(err, pick.ErrCanceled) {
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
		selectedPRs[i] = row.Item.PR
	}

	// Clone repos
	if cli.Clone {
		return cloneRepos(rest, selectedPRs, cfg.VCS, cli.Debug)
	}

	// Run actions first (approve, merge, close, etc.)
	if err := runActions(cli, rest, selectedPRs); err != nil {
		return err
	}

	// Then send to Slack
	if cli.Send {
		return runInteractiveSend(cli, cfg, selectedPRs)
	}

	return nil
}

// runActions executes PR action flags (approve, edit, close, etc.) if any are set.
// After enabling automerge (--merge), it updates the in-memory PR structs so
// downstream consumers (e.g. --send Slack reactions) reflect the new state.
func runActions(cli *CLI, rest *api.RESTClient, prs []PullRequest) error {
	if !cli.HasAction() {
		return nil
	}
	actions, err := newActionRunner(cli, rest)
	if err != nil {
		return err
	}
	if cli.Edit {
		return interactiveEdit(actions, prs)
	}
	if err := actions.Execute(cli, prs); err != nil {
		return err
	}
	// Reflect automerge state change so --send picks it up.
	if cli.Merge != nil {
		for i := range prs {
			prs[i].Automerge = *cli.Merge
			prs[i].automergeLoaded = true
		}
	}
	return nil
}

// runInteractiveSend sends selected PRs to Slack via the plugin.
func runInteractiveSend(cli *CLI, cfg *Config, prs []PullRequest) error {
	return pluginSlackSend(cfg, cli.SendTo, prs)
}

// buildActionHeader creates the interactive selection header from active action flags.
func buildActionHeader(cli *CLI) string {
	var parts []string
	if cli.Approve {
		parts = append(parts, "Approve")
	}
	if cli.Clone {
		parts = append(parts, "Clone")
	}
	if cli.Close {
		parts = append(parts, "Close")
	}
	if cli.Copilot {
		parts = append(parts, "Copilot review")
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
	if cli.Unsubscribe {
		parts = append(parts, "Unsubscribe")
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
func applyColorMode(mode clog.ColorMode) bool {
	clog.SetColorMode(mode)
	switch mode {
	case clog.ColorAlways:
		lipgloss.Writer.Profile = colorprofile.TrueColor
		return true
	case clog.ColorNever:
		lipgloss.Writer.Profile = colorprofile.NoTTY
		return false
	case clog.ColorAuto:
		return term.Is(os.Stdout)
	}
	return term.Is(os.Stdout)
}
