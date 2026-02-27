package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
			theme.WithEnumStyle(theme.EnumStyleHighlightPrefix),
		),
	}
}

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
	if strings.ToLower(pr.State) != valueOpen {
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

// renderMergeReason returns a human-readable reason for the PR's current status.
// Used in non-TTY output where colors are unavailable.
func (p *prl) renderMergeReason(pr PullRequest) string {
	if strings.ToLower(pr.State) != valueOpen {
		return valueUnknown
	}
	switch pr.MergeStatus {
	case MergeStatusReady:
		return "ready_to_merge"
	case MergeStatusCIPending:
		return "ci_pending"
	case MergeStatusCIFailed:
		return "ci_fail"
	case MergeStatusBlocked:
		return "needs_review"
	case MergeStatusUnknown:
		return valueUnknown
	}
	return ""
}
