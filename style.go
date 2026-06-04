package main

import (
	"image/color"
	"strings"
	"sync"

	lg "charm.land/lipgloss/v2"
	"github.com/gechr/clib/theme"
)

// Color palette. Each entry adapts to the terminal background and is populated
// by initPalette, which is seeded from the same background detection that
// clib's theme.Auto uses, so prl's own palette and the clib theme stay in sync.
var (
	// Primary interactive palette.
	colorAccent    color.Color // cursor, help keys, separators, borders
	colorRef       color.Color // PR refs, hyperlinks
	colorHighlight color.Color // selected index, on-state, additions
	colorOK        color.Color // success, selected items, focused text
	colorDanger    color.Color // errors, confirm No
	colorOff       color.Color // off-state, closed, CI failed, deletions
	colorWarning   color.Color // pending, CI pending, needs review
	colorHeading   color.Color // headers, filter slash, syntax keys
	colorTitle     color.Color // titles, selected choices, labels
	colorLabel     color.Color // edit label, diff header, select cursor
	colorHelpText  color.Color // help text, detail section headers
	colorFilter    color.Color // filter input, syntax desc
	colorDim       color.Color // dim/unavailable text
	colorSubtle    color.Color // placeholders, blurred text
	colorText      color.Color // normal value text
	colorBlack     color.Color // button text on colored bg

	// Basic ANSI palette (terminal-themed; identical in both modes).
	colorRed     color.Color
	colorGreen   color.Color
	colorYellow  color.Color
	colorMagenta color.Color

	// Niche colors - single/low-use but named for palette auditing.
	colorDraft         color.Color // draft dim, noResults
	colorMerged        color.Color // merged status
	colorDraftLabel    color.Color // "Draft" label
	colorDirty         color.Color // dirty indicator
	colorDismiss       color.Color // help dismiss
	colorFilterTag     color.Color // filter tag
	colorDefault       color.Color // default choice
	colorCheckDuration color.Color // check duration
	colorHelpKeyDim    color.Color // edit help key
)

// Raw ANSI escape backgrounds for cursor-line highlighting (background-adaptive).
var (
	cursorLineBG         string
	cursorLineSelectedBG string
)

// Base styles - add .Bold(true), .Faint(true), etc. at call sites as needed.
var (
	styleAccent     lg.Style
	styleRef        lg.Style
	styleHighlight  lg.Style
	styleOK         lg.Style
	styleDanger     lg.Style
	styleClosed     lg.Style
	styleWarning    lg.Style
	styleHeading    lg.Style
	styleTitle      lg.Style
	styleLabel      lg.Style
	styleHelp       lg.Style
	styleFilter     lg.Style
	styleDim        lg.Style
	styleSubtle     lg.Style
	styleText       lg.Style
	styleGreen      lg.Style
	styleRed        lg.Style
	styleYellow     lg.Style
	styleMagenta    lg.Style
	styleDraft      lg.Style
	styleMerged     lg.Style
	styleAdd        lg.Style
	styleDelete     lg.Style
	styleDraftLbl   lg.Style
	styleDirty      lg.Style
	styleDismiss    lg.Style
	styleFilterTag  lg.Style
	styleDefault    lg.Style
	styleCheckDur   lg.Style
	styleHelpKeyDim lg.Style
)

// Pre-rendered styled strings.
var (
	styledOn  string
	styledOff string
)

// init seeds the palette with the dark variant so package-level styles are
// always usable (e.g. in tests). New re-runs initPalette once the terminal
// background is known.
//
//nolint:gochecknoinits // palette must be populated before any rendering.
func init() { initPalette(false) }

// initPalette (re)builds the color palette, derived styles, and pre-rendered
// strings for the given background. When isLight is true, light-background
// variants are selected; otherwise the dark variants are used.
func initPalette(isLight bool) {
	// lg.LightDark(isDark) returns a picker: ld(lightValue, darkValue).
	ld := lg.LightDark(!isLight)

	// Primary interactive palette.
	colorAccent = ld(lg.Color("162"), lg.Color("198"))   // deep/hot pink
	colorRef = ld(lg.Color("26"), lg.Color("117"))       // blue
	colorHighlight = ld(lg.Color("28"), lg.Color("118")) // green
	colorOK = ld(lg.Color("29"), lg.Color("48"))         // sea/spring green
	colorDanger = ld(lg.Color("160"), lg.Color("196"))   // red
	colorOff = ld(lg.Color("161"), lg.Color("197"))      // rose
	colorWarning = ld(lg.Color("130"), lg.Color("214"))  // amber
	colorHeading = ld(lg.Color("130"), lg.Color("208"))  // orange
	colorTitle = ld(lg.Color("168"), lg.Color("218"))    // pink
	colorLabel = ld(lg.Color("133"), lg.Color("212"))    // orchid
	colorHelpText = ld(lg.Color("96"), lg.Color("175"))  // mauve
	colorFilter = ld(lg.Color("130"), lg.Color("216"))   // peach
	colorDim = ld(lg.Color("245"), lg.Color("240"))      // dim gray
	colorSubtle = ld(lg.Color("247"), lg.Color("242"))   // gray
	colorText = ld(lg.Color("235"), lg.Color("255"))     // normal text
	colorBlack = lg.Color("#000000")                     // button text on colored bg

	// Basic ANSI palette.
	colorRed = lg.Color("1")
	colorGreen = lg.Color("2")
	colorYellow = lg.Color("3")
	colorMagenta = lg.Color("5")

	// Niche colors.
	colorDraft = ld(lg.Color("244"), lg.Color("8"))
	colorMerged = ld(lg.Color("91"), lg.Color("141"))
	colorDraftLabel = ld(lg.Color("244"), lg.Color("250"))
	colorDirty = ld(lg.Color("136"), lg.Color("226"))
	colorDismiss = ld(lg.Color("167"), lg.Color("210"))
	colorFilterTag = ld(lg.Color("30"), lg.Color("116"))
	colorDefault = ld(lg.Color("26"), lg.Color("75"))
	colorCheckDuration = ld(lg.Color("244"), lg.Color("245"))
	colorHelpKeyDim = ld(lg.Color("243"), lg.Color("248"))

	// Cursor-line highlight backgrounds: pale tints on light, dark tints on dark.
	if isLight {
		cursorLineBG = "\x1b[48;2;255;225;238m"
		cursorLineSelectedBG = "\x1b[48;2;225;245;230m"
	} else {
		cursorLineBG = "\x1b[48;2;40;10;30m"
		cursorLineSelectedBG = "\x1b[48;2;10;30;15m"
	}

	// Derived styles.
	styleAccent = lg.NewStyle().Foreground(colorAccent)
	styleRef = lg.NewStyle().Foreground(colorRef)
	styleHighlight = lg.NewStyle().Foreground(colorHighlight)
	styleOK = lg.NewStyle().Foreground(colorOK)
	styleDanger = lg.NewStyle().Foreground(colorDanger)
	styleClosed = lg.NewStyle().Foreground(colorOff)
	styleWarning = lg.NewStyle().Foreground(colorWarning)
	styleHeading = lg.NewStyle().Foreground(colorHeading)
	styleTitle = lg.NewStyle().Foreground(colorTitle)
	styleLabel = lg.NewStyle().Foreground(colorLabel)
	styleHelp = lg.NewStyle().Foreground(colorHelpText)
	styleFilter = lg.NewStyle().Foreground(colorFilter)
	styleDim = lg.NewStyle().Foreground(colorDim)
	styleSubtle = lg.NewStyle().Foreground(colorSubtle)
	styleText = lg.NewStyle().Foreground(colorText)
	styleGreen = lg.NewStyle().Foreground(colorGreen)
	styleRed = lg.NewStyle().Foreground(colorRed)
	styleYellow = lg.NewStyle().Foreground(colorYellow)
	styleMagenta = lg.NewStyle().Foreground(colorMagenta)
	styleDraft = lg.NewStyle().Foreground(colorDraft)
	styleMerged = lg.NewStyle().Foreground(colorMerged)
	styleAdd = lg.NewStyle().Foreground(colorHighlight)
	styleDelete = lg.NewStyle().Foreground(colorOff)
	styleDraftLbl = lg.NewStyle().Foreground(colorDraftLabel)
	styleDirty = lg.NewStyle().Foreground(colorDirty)
	styleDismiss = lg.NewStyle().Foreground(colorDismiss)
	styleFilterTag = lg.NewStyle().Foreground(colorFilterTag)
	styleDefault = lg.NewStyle().Foreground(colorDefault)
	styleCheckDur = lg.NewStyle().Foreground(colorCheckDuration)
	styleHelpKeyDim = lg.NewStyle().Foreground(colorHelpKeyDim)

	// Pre-rendered styled strings.
	styledOn = styleHighlight.Render("on")
	styledOff = styleClosed.Render("off")
}

// prl holds shared dependencies for the application.
type prl struct {
	theme *theme.Theme

	entityColorMu sync.Mutex
	entityColors  map[string]int
	nextColor     int
}

// New creates a new prl with a background-adaptive theme. theme.Auto detects
// the terminal background (falling back to dark) and selects clib's matching
// preset; initPalette aligns prl's own palette to the same background.
func New() *prl {
	th := theme.Auto()
	initPalette(th.Background == theme.BackgroundLight)
	return &prl{
		theme: th.With(
			theme.WithEnumStyle(theme.EnumStyleHighlightBoth),
		),
		entityColors: make(map[string]int),
	}
}

// RenderBold renders text in bold using the theme.
func (p *prl) RenderBold(s string) string { return p.theme.Bold.Render(s) }

// RenderDim renders text in dim using the theme.
func (p *prl) RenderDim(s string) string { return p.theme.Dim.Render(s) }

// EntityColors returns the theme's entity color palette.
func (p *prl) EntityColors() []color.Color { return p.theme.EntityColors }

// AssignEntityColor returns a stable session-scoped color for the given key.
func (p *prl) AssignEntityColor(key string) color.Color {
	colors := p.EntityColors()
	if len(colors) == 0 {
		return nil
	}

	key = strings.ToLower(key)

	p.entityColorMu.Lock()
	defer p.entityColorMu.Unlock()

	if idx, ok := p.entityColors[key]; ok {
		return colors[idx]
	}

	idx := p.nextColor % len(colors)
	p.entityColors[key] = idx
	p.nextColor++
	return colors[idx]
}

// prMergeStyle returns the lipgloss style for an open PR based on its merge readiness.
func (p *prl) prMergeStyle(pr PullRequest) lg.Style {
	switch resolvePRStatus(pr) {
	case resolvedClosed:
		return *p.theme.Red
	case resolvedMerged:
		return *p.theme.Magenta
	case resolvedDraftCIFail:
		return styleRed.Faint(true)
	case resolvedDraft:
		return styleDraft
	case resolvedReady:
		return *p.theme.BoldGreen
	case resolvedCIPending:
		return *p.theme.Yellow
	case resolvedCIFailed:
		return p.theme.Red.Bold(true)
	case resolvedBlocked:
		return *p.theme.Blue
	case resolvedConflict:
		return styleHeading
	case resolvedUnknown:
		return *p.theme.Dim
	}
	return *p.theme.Blue
}

// renderMergeStatus returns a plain text label for the PR's CI/review status.
// Used in non-TTY output where colors are unavailable.
func (p *prl) renderMergeStatus(pr PullRequest) string {
	state := strings.ToLower(pr.State)
	if state == valueClosed {
		return valueClosed
	}
	if state == valueMerged {
		return valueClosed
	}
	if state != valueOpen {
		return valueUnknown
	}
	switch pr.MergeStatus {
	case MergeStatusReady:
		return valueReady
	case MergeStatusCIPending:
		return valueBlocked
	case MergeStatusCIFailed:
		return valueBlocked
	case MergeStatusBlocked, MergeStatusConflict:
		return valueBlocked
	case MergeStatusUnknown:
		return valueUnknown
	}
	return ""
}
