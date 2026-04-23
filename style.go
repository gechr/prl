package main

import (
	"image/color"
	"strings"
	"sync"

	lg "charm.land/lipgloss/v2"
	"github.com/gechr/clib/theme"
)

// Primary interactive palette.
var (
	colorAccent    = lg.Color("198")     // cursor, help keys, separators, borders (hot pink)
	colorRef       = lg.Color("117")     // PR refs, hyperlinks (light blue)
	colorHighlight = lg.Color("118")     // selected index, on-state, additions (bright green)
	colorOK        = lg.Color("48")      // success, selected items, focused text (spring green)
	colorDanger    = lg.Color("196")     // errors, confirm No (bright red)
	colorOff       = lg.Color("197")     // off-state, closed, CI failed, deletions (rose)
	colorWarning   = lg.Color("214")     // pending, CI pending, needs review (amber)
	colorHeading   = lg.Color("208")     // headers, filter slash, syntax keys (orange)
	colorTitle     = lg.Color("218")     // titles, selected choices, labels (pink)
	colorLabel     = lg.Color("212")     // edit label, diff header, select cursor (orchid)
	colorHelpText  = lg.Color("175")     // help text, detail section headers (mauve)
	colorFilter    = lg.Color("216")     // filter input, syntax desc (peach)
	colorDim       = lg.Color("240")     // dim/unavailable text (medium gray)
	colorSubtle    = lg.Color("242")     // placeholders, blurred text (gray)
	colorText      = lg.Color("255")     // normal value text (white)
	colorBlack     = lg.Color("#000000") // button text on colored bg
)

// Basic ANSI palette.
var (
	colorRed     = lg.Color("1")
	colorGreen   = lg.Color("2")
	colorYellow  = lg.Color("3")
	colorMagenta = lg.Color("5")
)

// Niche colors - single/low-use but named for palette auditing.
var (
	colorDraft         = lg.Color("8")   // draft dim, noResults
	colorMerged        = lg.Color("141") // merged status (purple)
	colorDraftLabel    = lg.Color("250") // "Draft" label (light gray)
	colorDirty         = lg.Color("226") // dirty indicator (bright yellow)
	colorDismiss       = lg.Color("210") // help dismiss (salmon)
	colorFilterTag     = lg.Color("116") // filter tag (cyan)
	colorDefault       = lg.Color("75")  // default choice (faint blue)
	colorCheckDuration = lg.Color("245") // check duration (lighter gray)
	colorHelpKeyDim    = lg.Color("248") // edit help key (near-white)
)

// Raw ANSI escape backgrounds for cursor-line highlighting.
const (
	cursorLineBG         = "\x1b[48;2;40;10;30m"
	cursorLineSelectedBG = "\x1b[48;2;10;30;15m"
)

// Base styles - add .Bold(true), .Faint(true), etc. at call sites as needed.
var (
	styleAccent     = lg.NewStyle().Foreground(colorAccent)
	styleRef        = lg.NewStyle().Foreground(colorRef)
	styleHighlight  = lg.NewStyle().Foreground(colorHighlight)
	styleOK         = lg.NewStyle().Foreground(colorOK)
	styleDanger     = lg.NewStyle().Foreground(colorDanger)
	styleClosed     = lg.NewStyle().Foreground(colorOff)
	styleWarning    = lg.NewStyle().Foreground(colorWarning)
	styleHeading    = lg.NewStyle().Foreground(colorHeading)
	styleTitle      = lg.NewStyle().Foreground(colorTitle)
	styleLabel      = lg.NewStyle().Foreground(colorLabel)
	styleHelp       = lg.NewStyle().Foreground(colorHelpText)
	styleFilter     = lg.NewStyle().Foreground(colorFilter)
	styleDim        = lg.NewStyle().Foreground(colorDim)
	styleSubtle     = lg.NewStyle().Foreground(colorSubtle)
	styleText       = lg.NewStyle().Foreground(colorText)
	styleGreen      = lg.NewStyle().Foreground(colorGreen)
	styleRed        = lg.NewStyle().Foreground(colorRed)
	styleYellow     = lg.NewStyle().Foreground(colorYellow)
	styleMagenta    = lg.NewStyle().Foreground(colorMagenta)
	styleDraft      = lg.NewStyle().Foreground(colorDraft)
	styleMerged     = lg.NewStyle().Foreground(colorMerged)
	styleAdd        = lg.NewStyle().Foreground(colorHighlight)
	styleDelete     = lg.NewStyle().Foreground(colorOff)
	styleDraftLbl   = lg.NewStyle().Foreground(colorDraftLabel)
	styleDirty      = lg.NewStyle().Foreground(colorDirty)
	styleDismiss    = lg.NewStyle().Foreground(colorDismiss)
	styleFilterTag  = lg.NewStyle().Foreground(colorFilterTag)
	styleDefault    = lg.NewStyle().Foreground(colorDefault)
	styleCheckDur   = lg.NewStyle().Foreground(colorCheckDuration)
	styleHelpKeyDim = lg.NewStyle().Foreground(colorHelpKeyDim)
)

// Pre-rendered styled strings.
var (
	styledOn  = styleHighlight.Render("on")
	styledOff = styleClosed.Render("off")
)

// prl holds shared dependencies for the application.
type prl struct {
	theme *theme.Theme

	entityColorMu sync.Mutex
	entityColors  map[string]int
	nextColor     int
}

// New creates a new prl with the configured theme.
func New() *prl {
	return &prl{
		theme: theme.Default().With(
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
	case MergeStatusBlocked:
		return valueBlocked
	case MergeStatusUnknown:
		return valueUnknown
	}
	return ""
}
