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
	"github.com/gechr/clive"
	"github.com/gechr/clive/notify"
	"github.com/gechr/clive/updater/brew"
	"github.com/gechr/clog"
	cspinner "github.com/gechr/clog/fx/spinner"
	"github.com/gechr/conductor"
	cli "github.com/gechr/conductor/cli/kong"
	"github.com/gechr/primer/pick"
	xansi "github.com/gechr/x/ansi"
	xslices "github.com/gechr/x/slices"
	"github.com/gechr/x/terminal"
)

// Sentinel errors for controlled exits.
var (
	errOK    = errors.New("ok")    // caller handled it; exit 0
	errFatal = errors.New("fatal") // caller already logged; exit 1
)

func main() {
	app := conductor.New(conductor.App{
		Name:        "prl",
		Description: "Search, filter, display, and act on GitHub pull requests",
		Module:      "github.com/gechr/prl",
		HelpShort:   "Short help",
		HelpLong:    "Long help",
		Updater: brew.New(
			clive.Info{Module: "github.com/gechr/prl"},
			brew.WithFormula("prl"),
			brew.WithTap("gechr/tap"),
		),
		NotifyOptions: []notify.Option{notify.WithOutdatedHintCommand("prl --self-update")},
		ConfigureLog:  configureClog,
	})

	prl := New()

	cfg, err := loadConfig()
	if err != nil {
		clog.Fatal().Err(err).Msg("Failed to load config")
	}

	root := CLI{prl: prl, cfg: cfg}
	prog, err := cli.New(app, &root,
		// prl renders its own config-aware help, overriding conductor's
		// default help wiring.
		cli.WithKongOptions(kong.Help(prl.helpPrinter(cfg))),
		cli.WithCompletionHandler(func(shell, kind string, _ []string) {
			if completeErr := prl.handleComplete(shell, kind, cfg); completeErr != nil {
				clog.Error().Msg(completeErr.Error())
			}
		}),
		cli.WithExitCode(exitCode),
	)
	if err != nil {
		clog.Fatal().Err(err).Msg("Failed to build CLI")
	}
	os.Exit(prog.Run(os.Args[1:]))
}

// exitCode maps prl's sentinel errors to process exit codes; the Fatal branch
// prints and exits directly, preserving the pre-conductor output.
func exitCode(err error) int {
	switch {
	case errors.Is(err, errOK):
		return 0
	case errors.Is(err, errFatal):
		return 1
	default:
		clog.Fatal().Err(err).Send()
	}
	return 1
}

// configureClog layers prl's voice over conductor's defaults; conductor runs
// it via App.ConfigureLog.
func configureClog() {
	symbols := clog.DefaultSymbols()
	symbols[clog.LevelInfo] = "✅"
	clog.SetSymbols(symbols)
}

// Run implements the kong entry point: conductor dispatches here after
// completion preflight, parsing, standard-flag application and the passive
// update check.
func (c *CLI) Run(app *conductor.Runtime) error {
	prl, cfg, cli := c.prl, c.cfg, c

	if cli.Version {
		app.PrintVersion(false)
		return errOK
	}

	// Init mode: write default config and exit
	if cli.Init {
		return initConfig()
	}

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
	params, err := buildSearchQuery(cli, cfg)
	if err != nil {
		return err
	}

	// Dry run mode
	if cli.Dry {
		lipgloss.Println(prl.buildDryRunOutput(params, cli))
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
	clog.SetSpinnerDefaults(cspinner.WithFrames(s.frames...), cspinner.WithInterval(s.interval))

	// Watch mode: loop search+render with screen clear
	if cli.Watch {
		return runWatch(prl, rest, cli, cfg, tty, params, s)
	}

	// Interactive TUI browser
	if cli.Interactive {
		if !tty {
			return fmt.Errorf("--interactive requires a TTY")
		}
		cli.setOutput(valueTable)
		clog.SetLevel(clog.LevelFatal) // TUI manages its own notifications
		return runTui(prl, rest, cli, cfg, tty, params, s)
	}

	var output string
	if err := withSpinner(tty && !cli.Debug, s, func(stopSpinner func()) error {
		var runErr error
		output, runErr = runOnce(prl, rest, cli, cfg, tty, params, stopSpinner)
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

	fmt.Print(xansi.HideCursor)
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

func watchRefreshBaseDelay(n int, override *time.Duration) time.Duration {
	d := watchInterval(n)
	if override != nil && *override > d {
		return refreshCooldownDelay(*override)
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
		prs    []PullRequest
		err    error
	}
	type validationResult struct {
		changed bool
		err     error
	}
	cache := newListMetadataCache()
	var gql *api.GraphQLClient
	getGQL := func() (*api.GraphQLClient, error) {
		if gql == nil {
			var err error
			gql, err = newGraphQLClient(withDebug(cli.Debug))
			if err != nil {
				return nil, fmt.Errorf("creating GraphQL client: %w", err)
			}
		}
		return gql, nil
	}
	initialCLI := cli
	initialFetchDeferred := shouldDeferInitialWatchEnrichment(cli, tty)
	if initialFetchDeferred {
		initialCLI = cloneCLI(cli)
		initialCLI.Quick = true
	}
	cachedPRs, cachedOK, cachedErr := loadListResultCache(cli, params)
	if cachedErr != nil {
		clog.Debug().Err(cachedErr).Msg("list cache load failed")
	}

	var r fetchResult
	if cachedOK {
		out, renderErr := renderOutput(p, cli, cfg, tty, cachedPRs)
		r = fetchResult{output: out, prs: cachedPRs, err: renderErr}
	} else {
		r = withSpinner(tty && !cli.Debug, s, func(func()) fetchResult {
			out, prs, fErr := buildOutput(p, rest, initialCLI, cfg, tty, params, cache)
			return fetchResult{out, prs, fErr}
		})
		if r.err == nil && !initialFetchDeferred {
			if err := saveListResultCache(cli, params, r.prs); err != nil {
				clog.Debug().Err(err).Msg("list cache save failed")
			}
		}
	}
	if r.err != nil {
		return r.err
	}
	if !cachedOK && r.output == "" && cli.ExitZero {
		return errFatal
	}

	fmt.Print(xansi.EnterAltScreen + xansi.HideCursor)
	cleanup := func() { fmt.Print(xansi.ShowCursor + xansi.ExitAltScreen) }
	defer cleanup()

	// Restore terminal on interrupt.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup()
		os.Exit(0)
	}()

	// Re-render cached PRs on terminal resize (SIGWINCH on Unix; a no-op on
	// platforms without an equivalent, such as Windows).
	winch := make(chan os.Signal, 1)
	notifyResize(winch)

	results := make(chan fetchResult, 1)
	validations := make(chan validationResult, 1)
	fetch := func() {
		go func() {
			out, prs, fErr := buildOutput(p, rest, cli, cfg, tty, params, cache)
			if fErr == nil {
				if err := saveListResultCache(cli, params, prs); err != nil {
					clog.Debug().Err(err).Msg("list cache save failed")
				}
			}
			results <- fetchResult{out, prs, fErr}
		}()
	}
	validate := func(prs []PullRequest) {
		go func(prs []PullRequest) {
			g, err := getGQL()
			if err != nil {
				validations <- validationResult{err: err}
				return
			}
			changed, err := validateCachedHeads(g, prs, cache)
			validations <- validationResult{changed: changed, err: err}
		}(append([]PullRequest(nil), prs...))
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
	interval = watchRefreshBaseDelay(len(lastPRs), cli.Interval)
	nextFetchAt = time.Now().Add(interval)
	if initialFetchDeferred || cachedOK {
		fetching = true
		nextFetchAt = time.Time{}
		fetch()
	}

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
			interval = watchRefreshBaseDelay(len(lastPRs), cli.Interval)
			nextFetchAt = time.Now().Add(interval)
			if len(lastPRs) > 0 {
				validate(lastPRs)
			}
		case v := <-validations:
			if v.err != nil {
				clog.Debug().Err(v.err).Msg("Head validation failed")
				break
			}
			if v.changed && !fetching {
				fetching = true
				spinnerTick = 0
				fetch()
			}
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
		fmt.Print(xansi.EraseEntireScreen + xansi.CursorHomePosition)
		if lastOutput != "" {
			fmt.Println(lastOutput)
		}
		if fetching && lastOutput != "" {
			frame := s.frames[spinnerTick%len(s.frames)]
			fmt.Print(xansi.CursorHomePosition + frame)
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

func shouldDeferInitialWatchEnrichment(cli *CLI, tty bool) bool {
	if cli == nil || !tty {
		return false
	}
	if cli.Quick || cli.OutputFormat() != OutputTable {
		return false
	}
	if cli.PRState() == StateReady || cli.CIStatus() != CINone {
		return false
	}
	if len(cli.ClosedBy.Values) > 0 || len(cli.MergedBy.Values) > 0 {
		return false
	}
	return true
}

func applyListMetadata(
	cli *CLI,
	getGQL func() (*api.GraphQLClient, error),
	prs []PullRequest,
	needAutomerge bool,
	needMergeStatus bool,
	closedAllowed map[string]bool,
	mergedAllowed map[string]bool,
	cache *listMetadataCache,
) ([]PullRequest, error) {
	needTimeline := len(closedAllowed) > 0 || len(mergedAllowed) > 0
	needViewerApproval := cli.ReviewSelfRequired()
	if !needTimeline && !needMergeStatus && !needAutomerge && !needViewerApproval {
		return prs, nil
	}

	g, err := getGQL()
	if err != nil {
		if cli.Merge != nil || needTimeline || needViewerApproval {
			return nil, err
		}
		clog.Debug().Err(err).Msg("skipping list metadata hydration")
		return prs, nil
	}

	actors, err := hydrateListMetadataCached(g, prs, listMetadataRequest{
		automerge:      needAutomerge,
		mergeStatus:    needMergeStatus,
		timelineClosed: len(closedAllowed) > 0,
		timelineMerged: len(mergedAllowed) > 0,
		viewerApproval: needViewerApproval,
	}, cache)
	if err != nil {
		if cli.Merge != nil || needTimeline || needViewerApproval {
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
	if needViewerApproval {
		prs = filterByViewerApproval(prs)
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
	cache *listMetadataCache,
) (string, []PullRequest, error) {
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

	ready := cli.PRState() == StateReady
	ciFilter := cli.CIStatus()
	needsEnrich := ready || ciFilter != CINone
	// A --group breakdown only needs fields already present in search
	// results, so skip merge-status enrichment unless a filter (--ci,
	// --state=ready) genuinely requires it.
	needMergeStatus := (!cli.Quick || needsEnrich) &&
		((cli.OutputFormat() == OutputTable && !cli.GroupActive()) || needsEnrich)
	prs, searchHydrated, err := executeListSearch(rest, getGQL, params, needMergeStatus)
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

	// Resolve timeline filter logins before the shared metadata pass.
	closedAllowed, err := resolveTimelineLogins(rest, cli.ClosedBy.Values)
	if err != nil {
		return "", nil, err
	}
	mergedAllowed, err := resolveTimelineLogins(rest, cli.MergedBy.Values)
	if err != nil {
		return "", nil, err
	}

	needAutomerge := cli.Merge != nil || (!cli.Quick && cli.Send)
	prs, err = applyListMetadata(
		cli,
		getGQL,
		prs,
		needAutomerge && !allAutomergeLoaded(prs),
		needMergeStatus && !searchHydrated,
		closedAllowed,
		mergedAllowed,
		cache,
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

// groupCapSuffix returns a blank-line-separated notice to append after a
// --group breakdown when the match set exceeds GitHub's 1000-result search cap,
// so a recency-biased sample is not read as exhaustive. Returns "" when the
// results fit, an explicit --limit was set, or the true total can't be read.
func groupCapSuffix(
	rest *api.RESTClient,
	cli *CLI,
	params *SearchParams,
	rawFetched int,
	tty bool,
) string {
	if cli.limitExplicit || rawFetched < maxGroupResults {
		return ""
	}
	total, err := executeCount(rest, params)
	if err != nil || total <= rawFetched {
		return ""
	}
	return nl + nl + groupCapNotice(rawFetched, total, tty)
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

	ready := cli.PRState() == StateReady
	ciFilter := cli.CIStatus()
	needsEnrich := ready || ciFilter != CINone
	// A --group breakdown only needs fields already present in search
	// results, so skip merge-status enrichment unless a filter (--ci,
	// --state=ready) genuinely requires it.
	needMergeStatus := (!cli.Quick || needsEnrich) &&
		((cli.OutputFormat() == OutputTable && !cli.GroupActive()) || needsEnrich)
	prs, searchHydrated, err := executeListSearch(rest, getGQL, params, needMergeStatus)
	if err != nil {
		return "", err
	}
	rawFetched := len(prs)

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

	// Resolve timeline filter logins before the shared metadata pass.
	closedAllowed, err := resolveTimelineLogins(rest, cli.ClosedBy.Values)
	if err != nil {
		return "", err
	}
	mergedAllowed, err := resolveTimelineLogins(rest, cli.MergedBy.Values)
	if err != nil {
		return "", err
	}

	needAutomerge := cli.Merge != nil || (!cli.Quick && cli.Send)

	prs, err = applyListMetadata(
		cli,
		getGQL,
		prs,
		needAutomerge && !allAutomergeLoaded(prs),
		needMergeStatus && !searchHydrated,
		closedAllowed,
		mergedAllowed,
		nil,
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

	// Group-by breakdown: bucket the fetched results locally (no extra API
	// calls) instead of rendering the usual output.
	if cli.GroupActive() {
		keys, keyErr := cli.GroupKeys()
		if keyErr != nil {
			return "", keyErr
		}
		asJSON := cli.OutputFormat() == OutputJSON
		groupWidth, groupHeight := 0, 0
		if tty {
			groupWidth, groupHeight = terminal.Size(os.Stdout)
		}
		out, gErr := renderGroup(
			prs,
			keys,
			asJSON,
			tty,
			prl.AssignEntityColor,
			groupWidth,
			groupHeight,
		)
		if gErr != nil {
			return "", gErr
		}
		if !asJSON {
			out += groupCapSuffix(rest, cli, params, rawFetched, tty)
		}
		return out, nil
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
		xslices.SortNatural(urls)
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

	if cli.HasAction() || cli.Clone || cli.Send {
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
		return terminal.Is(os.Stdout)
	}
	return terminal.Is(os.Stdout)
}
