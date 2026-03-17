package main

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clib/theme"
)

// prl holds shared dependencies for the application.
type prl struct {
	theme *theme.Theme
}

// New creates a new prl with the configured theme.
func New() *prl {
	return &prl{
		theme: theme.New(
			theme.WithEnumStyle(theme.EnumStyleHighlightBoth),
		),
	}
}

// SetPlain switches the theme to plain (no ANSI) mode for non-TTY output.
func (p *prl) SetPlain() { p.theme = p.theme.Plain() }

// RenderBold renders text in bold using the theme.
func (p *prl) RenderBold(s string) string { return p.theme.Bold.Render(s) }

// RenderDim renders text in dim using the theme.
func (p *prl) RenderDim(s string) string { return p.theme.Dim.Render(s) }

// EntityColors returns the theme's entity color palette.
func (p *prl) EntityColors() []color.Color { return p.theme.EntityColors }

// prStateStyle returns the lipgloss style for a PR state.
func (p *prl) prStateStyle(state string) lipgloss.Style {
	switch strings.ToLower(state) {
	case valueOpen:
		return *p.theme.Blue
	case valueMerged:
		return *p.theme.Magenta
	case valueClosed:
		return *p.theme.Red
	default:
		return lipgloss.NewStyle()
	}
}

// prMergeStyle returns the lipgloss style for an open PR based on its merge readiness.
func (p *prl) prMergeStyle(pr PullRequest) lipgloss.Style {
	if strings.ToLower(pr.State) != valueOpen {
		return p.prStateStyle(pr.State)
	}
	if pr.IsDraft {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	}
	switch pr.MergeStatus {
	case MergeStatusReady:
		return *p.theme.BoldGreen
	case MergeStatusCIPending:
		return *p.theme.Yellow
	case MergeStatusCIFailed:
		return p.theme.Red.Bold(true)
	case MergeStatusBlocked:
		return *p.theme.Blue
	case MergeStatusUnknown:
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
