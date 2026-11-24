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
		Name:        programName,
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
	mode, _ := parseIconMode(cfg.Icons)
	useIcons(iconsFor(resolveIconMode(mode)))

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
	code := prog.Run(os.Args[1:])
	if code != 0 {
		reportProgramStatus(clog.ProgramStateError, "")
	}
	os.Exit(code)
}

// exitCode maps prl's sentinel errors to process exit codes; the Fatal branch
// prints and exits directly, preserving the pre-conductor output.
func exitCode(err error) int {
	switch {
	case errors.Is(err, errOK), errors.Is(err, errCanceled):
		return 0
	case errors.Is(err, errFatal):
		return 1
	default:
		clog.Fatal().Err(withGitHubErrorDetail(err)).Send()
	}
	return 1
}

// configureClog layers prl's voice over conductor's defaults; conductor runs
// it via App.ConfigureLog.
func configureClog() {
	clog.ApplyPreset(clog.TersePreset())
	clog.SetOmitEmpty(true)
}

// Run implements the kong entry point: conductor dispatches here after
// completion preflight, parsing, standard-flag application and the passive
// update check.
func (c *CLI) Run(app *conductor.Runtime) error {
	if c.Version {
		return c.run(app)
	}
	reportProgramStatus(clog.ProgramStateWorking, "")
	err := c.run(app)
	state := completedProgramState(err)
	reportProgramStatus(state, "")
	if state == clog.ProgramStateIdle {
		return errCanceled
	}
	return err
}

func (c *CLI) run(app *conductor.Runtime) error {
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

	// Watch mode and the TUI handle interrupts themselves.
	stopInterrupt := idleOnInterrupt()
	defer stopInterrupt()

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

// Default spinner colors (256-color palette), per background.
var (
	defaultSpinnerColorsDark  = []string{"218"}
	defaultSpinnerColorsLight = []string{"168"}
)

// defaultSpinnerColors returns the default spinner colors for the active
// background.
func defaultSpinnerColors() []string {
	if paletteIsLight {
		return defaultSpinnerColorsLight
	}
	return defaultSpinnerColorsDark
}

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
		colors = defaultSpinnerColors()
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

// runWatch refreshes search membership periodically and updates streamed PRs by reference.
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
		events prEvents
		err    error
	}
	type validationResult struct {
		changed bool
		err     error
	}
	cache := newListMetadataCache()
	read := func(readCLI *CLI, previous []PullRequest, changed prEvents) fetchResult {
		if len(changed) == 0 {
			out, prs, err := buildOutput(p, rest, readCLI, cfg, tty, params, cache)
			return fetchResult{output: out, prs: prs, err: err}
		}
		gql, err := newGraphQLClient(withDebug(readCLI.Debug))
		if err != nil {
			return fetchResult{events: changed, err: err}
		}
		prs, err := refreshWatchEvents(gql, readCLI, params, previous, changed, cache)
		var out string
		if err == nil && len(prs) > 0 {
			out, err = renderOutput(p, readCLI, cfg, tty, prs)
		}
		return fetchResult{output: out, prs: prs, events: changed, err: err}
	}
	initialCLI := cli
	initialFetchDeferred := shouldDeferInitialWatchEnrichment(cli, tty)
	if initialFetchDeferred {
		initialCLI = cloneCLI(cli)
		initialCLI.Quick = true
	}
	// Watch mode keeps its previous startup behaviour and can render any saved
	// result immediately while it refreshes in the background.
	cachedPRs, membershipAt, cachedErr := loadListResultSnapshot(cli, params, 0)
	cachedOK := !membershipAt.IsZero()
	if cachedErr != nil {
		clog.Debug().Err(cachedErr).Msg("list cache load failed")
	}

	var r fetchResult
	if cachedOK {
		out, renderErr := renderOutput(p, cli, cfg, tty, cachedPRs)
		r = fetchResult{output: out, prs: cachedPRs, err: renderErr}
	} else {
		r = withSpinner(tty && !cli.Debug, s, func(func()) fetchResult {
			return read(initialCLI, nil, nil)
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
	defer signal.Stop(sig)

	// Re-render cached PRs on terminal resize (SIGWINCH on Unix; a no-op on
	// platforms without an equivalent, such as Windows).
	winch := make(chan os.Signal, 1)
	notifyResize(winch)
	defer signal.Stop(winch)

	results := make(chan fetchResult, 1)
	validations := make(chan validationResult, 1)
	validating := 0
	refreshFailed := false
	fetch := func(previous []PullRequest, changed prEvents) {
		refreshFailed = false
		go func() { results <- read(cli, previous, changed) }()
	}
	var validationClient *api.GraphQLClient
	validate := func(prs []PullRequest) {
		if validationClient == nil {
			var err error
			validationClient, err = newGraphQLClient(withDebug(cli.Debug))
			if err != nil {
				clog.Debug().Err(err).Msg("Head validation unavailable")
				return
			}
		}
		validating++
		go func(prs []PullRequest, cache *listMetadataCache) {
			changed, err := validateCachedHeads(validationClient, prs, cache)
			validations <- validationResult{changed: changed, err: err}
		}(append([]PullRequest(nil), prs...), cache)
	}

	var (
		fetching    bool
		interval    time.Duration
		lastOutput  string
		lastPRs     []PullRequest
		nextFetchAt time.Time
		spinnerTick int
	)

	streams := newEventStreams(cfg.Features.EventStreams)
	defer streams.close()
	streams.sync(r.prs)

	// Use the first fetch result.
	lastPRs = r.prs
	if r.output != "" {
		lastOutput = r.output
	} else {
		lastOutput = noResults
	}
	interval = watchRefreshBaseDelay(len(lastPRs), cli.Interval)
	nextFetchAt = time.Now().Add(interval)
	if !cachedOK {
		membershipAt = time.Now()
	}
	if initialFetchDeferred || cachedOK {
		fetching = true
		nextFetchAt = time.Time{}
		fetch(nil, nil)
	}

	lastStatus := watchProgramStatus(fetching, validating, refreshFailed)
	reportProgramStatus(lastStatus, "")
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-sig:
			reportProgramStatus(clog.ProgramStateIdle, "")
			return nil
		case <-ticker.C:
		}

		// Check for fetch completion (non-blocking).
		select {
		case r := <-results:
			fetching = false
			refreshFailed = r.err != nil
			switch {
			case r.err != nil:
				clog.Error().Err(withGitHubErrorDetail(r.err)).Msg("Refresh failed")
			case r.output != "":
				lastOutput = r.output
				lastPRs = r.prs
			case cli.ExitZero && r.events == nil:
				return errFatal
			default:
				lastOutput = noResults
				lastPRs = nil
			}
			streams.sync(lastPRs)
			if r.events != nil && r.err == nil && needsListMergeStatus(cli) {
				for _, pr := range r.prs {
					streams.followUpCI(pr, r.events[makePRKey(pr)])
				}
			}
			if r.events == nil {
				interval = watchRefreshBaseDelay(len(lastPRs), cli.Interval)
				nextFetchAt = time.Now().Add(interval)
				if r.err == nil {
					membershipAt = time.Now()
				}
				if len(lastPRs) > 0 {
					validate(lastPRs)
				}
			}
			if r.err == nil && !membershipAt.IsZero() {
				if err := saveListResultCacheAt(cli, params, lastPRs, membershipAt); err != nil {
					clog.Debug().Err(err).Msg("list cache save failed")
				}
			}
		case v := <-validations:
			validating--
			if v.err != nil {
				clog.Debug().Err(v.err).Msg("Head validation failed")
				break
			}
			if v.changed {
				// A targeted read may already be in flight. Keep the membership
				// check due until the loop can start it.
				nextFetchAt = time.Now()
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

		// A due membership check takes priority. Events stay queued through
		// in-flight reads and never advance the polling deadline.
		if !fetching {
			switch {
			case !nextFetchAt.IsZero() && !time.Now().Before(nextFetchAt):
				fetching = true
				spinnerTick = 0
				fetch(nil, nil)
			default:
				if changed := streams.takeChanges(); len(changed) > 0 {
					fetching = true
					spinnerTick = 0
					fetch(lastPRs, changed)
				}
			}
		}
		state := watchProgramStatus(fetching, validating, refreshFailed)
		if state != lastStatus {
			reportProgramStatus(state, "")
			lastStatus = state
		}
	}
}

func shouldDeferInitialWatchEnrichment(cli *CLI, tty bool) bool {
	if cli == nil || !tty {
		return false
	}
	if cli.Quick || cli.OutputFormat() != OutputTable {
		return false
	}
	if cli.PRState() == StateReady || cli.CIFilter().Active() {
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
	if !needTimeline && !needMergeStatus && !needAutomerge && !needViewerApproval &&
		!cli.IsInteractive() {
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

	if cli.IsInteractive() {
		if stackErr := hydrateStackMetadata(g, prs); stackErr != nil {
			clog.Debug().Err(stackErr).Msg("skipping stack metadata hydration")
		}
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

// Grouped output uses search fields unless a readiness or CI filter needs enrichment.
func needsListMergeStatus(cli *CLI) bool {
	needsEnrich := cli.PRState() == StateReady || cli.CIFilter().Active()
	return needsEnrich || !cli.Quick && cli.OutputFormat() == OutputTable && !cli.GroupActive()
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
	ciFilter := cli.CIFilter()
	needMergeStatus := needsListMergeStatus(cli)
	prs, err := executeListSearch(rest, getGQL, params, needMergeStatus)
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
		needMergeStatus,
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
	if ciFilter.Active() {
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
	p = p.forTerminal(tty)
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
	prl = prl.forTerminal(tty)

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
	ciFilter := cli.CIFilter()
	needMergeStatus := needsListMergeStatus(cli)
	prs, err := executeListSearch(rest, getGQL, params, needMergeStatus)
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
		needMergeStatus,
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
	if ciFilter.Active() {
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
			groupAuthorResolver(keys, cfg),
			params,
			shouldStripGroupRepoOwner(prs, cli.Owner.Values),
			prl.widthMethod(),
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
		return errCanceled
	}
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return errCanceled
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
