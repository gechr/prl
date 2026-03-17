package main

import (
	"fmt"
	"strings"
	"time"
)

// PRRowModel holds enriched, typed table data for a pull request before rendering.
type PRRowModel struct {
	PR          PullRequest
	Ref         string
	Repo        string
	RepoNWO     string
	Number      int
	Title       string
	Labels      []string
	Author      AuthorModel
	State       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	URL         string
	MergeStatus MergeStatus
	MergeReason string
}

// AuthorModel holds resolved author information for display.
type AuthorModel struct {
	Login      string
	Display    string
	URL        string
	IsBot      bool
	IsDeparted bool
}

// buildPRRowModels creates PRRowModel values from raw PullRequests.
func buildPRRowModels(
	prs []PullRequest,
	orgFilter string,
	resolver *AuthorResolver,
) []PRRowModel {
	models := make([]PRRowModel, len(prs))
	for i, pr := range prs {
		// Build ref text.
		name := pr.Repository.NameWithOwner
		if orgFilter != "" && orgFilter != valueAll {
			name = pr.Repository.Name
		}
		ref := fmt.Sprintf("%s#%d", name, pr.Number)

		// Build labels.
		labels := make([]string, len(pr.Labels))
		for j, l := range pr.Labels {
			labels[j] = l.Name
		}

		models[i] = PRRowModel{
			PR:          pr,
			Ref:         ref,
			Repo:        pr.Repository.Name,
			RepoNWO:     pr.Repository.NameWithOwner,
			Number:      pr.Number,
			Title:       pr.Title,
			Labels:      labels,
			Author:      buildAuthorModel(pr, resolver),
			State:       pr.State,
			CreatedAt:   pr.CreatedAt,
			UpdatedAt:   pr.UpdatedAt,
			URL:         pr.URL,
			MergeStatus: pr.MergeStatus,
			MergeReason: deriveMergeReason(pr),
		}
	}
	return models
}

// buildAuthorModel resolves author information from a PullRequest.
func buildAuthorModel(pr PullRequest, resolver *AuthorResolver) AuthorModel {
	login := pr.Author.Login
	isBot := strings.HasSuffix(strings.ToLower(login), BotSuffix)

	am := AuthorModel{Login: login, IsBot: isBot}
	am.URL = authorURL(login, isBot)

	if resolver == nil {
		am.Display = login
		return am
	}

	am.Display = resolver.Resolve(login)

	if isBot {
		am.Display = resolveBotDisplay(am.Display, resolver)
	} else {
		am.IsDeparted = resolver.IsKnown(login) && !resolver.IsHCL(login)
	}

	return am
}

// authorURL returns the GitHub URL for the given login.
func authorURL(login string, isBot bool) string {
	if isBot {
		return "https://github.com/apps/" + strings.TrimSuffix(login, BotSuffix)
	}
	return "https://github.com/" + login
}

// resolveBotDisplay strips [bot] suffix from a resolved display name.
func resolveBotDisplay(display string, resolver *AuthorResolver) string {
	before, ok := strings.CutSuffix(display, BotSuffix)
	if !ok {
		return display
	}
	if resolved := resolver.Resolve(before); resolved != before {
		return resolved
	}
	return before
}

// deriveMergeReason returns a human-readable reason for the PR's current status.
func deriveMergeReason(pr PullRequest) string {
	state := strings.ToLower(pr.State)
	if state == valueClosed {
		return valueRejected
	}
	if state == valueMerged {
		return valueMerged
	}
	if state != valueOpen {
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
