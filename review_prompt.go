package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gechr/clog"
)

var aiReviewPromptPlaceholderPattern = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9]*)\}`)

const aiReviewPromptSubmatchCount = 2

type reviewProvider string

const (
	reviewProviderUnknown reviewProvider = ""
	reviewProviderClaude  reviewProvider = "claude"
	reviewProviderCodex   reviewProvider = "codex"
	reviewProviderGemini  reviewProvider = "gemini"
	defaultReviewProvider                = reviewProviderClaude
)

func normalizeReviewProvider(provider string) reviewProvider {
	normalized := reviewProvider(strings.ToLower(provider))
	switch normalized {
	case reviewProviderClaude, reviewProviderCodex, reviewProviderGemini:
		return normalized
	case reviewProviderUnknown:
		return reviewProviderUnknown
	default:
		return reviewProviderUnknown
	}
}

func reviewPromptPlaceholderNames() []string {
	return []string{
		"prNumber",
		"repo",
		"owner",
		"ownerWithRepo",
		"prURL",
		"prRef",
		"title",
	}
}

func defaultReviewPromptTemplate(_ reviewProvider) string {
	return `Perform a comprehensive code review of PR #{prNumber} in {ownerWithRepo}.

The PR branch is checked out.

First read the PR context with:
gh pr view {prNumber} --repo {ownerWithRepo}

Then get the diff with:
gh api repos/{ownerWithRepo}/pulls/{prNumber} -H 'Accept: application/vnd.github.v3.diff'

Focus on: correctness, edge cases, error handling, performance, readability, and style.

Be thorough but concise.`
}

func reviewPromptTemplate(cfg *Config, provider reviewProvider) string {
	if cfg != nil {
		switch provider {
		case reviewProviderClaude:
			if cfg.TUI.Review.Providers.Claude.Prompt != "" {
				return cfg.TUI.Review.Providers.Claude.Prompt
			}
		case reviewProviderCodex:
			if cfg.TUI.Review.Providers.Codex.Prompt != "" {
				return cfg.TUI.Review.Providers.Codex.Prompt
			}
		case reviewProviderGemini:
			if cfg.TUI.Review.Providers.Gemini.Prompt != "" {
				return cfg.TUI.Review.Providers.Gemini.Prompt
			}
		case reviewProviderUnknown:
			return defaultReviewPromptTemplate(defaultReviewProvider)
		}
	}
	return defaultReviewPromptTemplate(provider)
}

func reviewPrompt(pr PullRequest, cfg *Config, provider reviewProvider) string {
	prompt, err := renderReviewPrompt(reviewPromptTemplate(cfg, provider), pr)
	if err == nil {
		return prompt
	}

	clog.Warn().
		Err(err).
		Str("provider", string(provider)).
		Msg("Invalid AI review prompt template; falling back to default")
	prompt, _ = renderReviewPrompt(defaultReviewPromptTemplate(provider), pr)
	return prompt
}

func validateReviewPromptTemplate(template string) error {
	allowed := make(map[string]struct{}, len(reviewPromptPlaceholderNames()))
	for _, name := range reviewPromptPlaceholderNames() {
		allowed[name] = struct{}{}
	}

	unknown := make(map[string]struct{})
	for _, match := range aiReviewPromptPlaceholderPattern.FindAllStringSubmatch(template, -1) {
		if len(match) < aiReviewPromptSubmatchCount {
			continue
		}
		if _, ok := allowed[match[1]]; !ok {
			unknown[match[1]] = struct{}{}
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	names := make([]string, 0, len(unknown))
	for name := range unknown {
		names = append(names, name)
	}
	sort.Strings(names)

	return fmt.Errorf(
		"unknown placeholder(s): %s (available: %s)",
		strings.Join(names, ", "),
		strings.Join(reviewPromptPlaceholderNames(), ", "),
	)
}

func renderReviewPrompt(template string, pr PullRequest) (string, error) {
	if err := validateReviewPromptTemplate(template); err != nil {
		return "", err
	}

	owner, repo := prOwnerRepo(pr)
	values := map[string]string{
		"prNumber":      fmt.Sprintf("%d", pr.Number),
		"repo":          repo,
		"owner":         owner,
		"ownerWithRepo": pr.Repository.NameWithOwner,
		"prURL":         pr.URL,
		"prRef":         pr.Ref(),
		"title":         pr.Title,
	}

	rendered := aiReviewPromptPlaceholderPattern.ReplaceAllStringFunc(
		template,
		func(match string) string {
			parts := aiReviewPromptPlaceholderPattern.FindStringSubmatch(match)
			if len(parts) < aiReviewPromptSubmatchCount {
				return match
			}
			return values[parts[1]]
		},
	)
	return rendered, nil
}
