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
