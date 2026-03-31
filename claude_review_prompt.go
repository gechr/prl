package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gechr/clog"
)

var claudeReviewPromptPlaceholderPattern = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9]*)\}`)

const claudeReviewPromptSubmatchCount = 2

func claudeReviewPromptPlaceholderNames() []string {
	return []string{
		"prNumber",
		"repo",
		"org",
		"orgWithRepo",
		"prURL",
		"prRef",
		"title",
	}
}

func defaultClaudeReviewPromptTemplate() string {
	return "Perform a comprehensive code review of PR #{prNumber} in {orgWithRepo}.\n\n" +
		"The PR branch is checked out.\n\n" +
		"First read the PR context with:\n" +
		"gh pr view {prNumber} --repo {orgWithRepo}\n\n" +
		"Then get the diff with:\n" +
		"gh api repos/{orgWithRepo}/pulls/{prNumber} -H 'Accept: application/vnd.github.v3.diff'\n\n" +
		"Focus on: correctness, edge cases, error handling, performance, readability, and style.\n\n" +
		"Be thorough but concise."
}

func claudeReviewPromptTemplate(cfg *Config) string {
	if cfg != nil && cfg.TUI.Review.Claude.Prompt != "" {
		return cfg.TUI.Review.Claude.Prompt
	}
	return defaultClaudeReviewPromptTemplate()
}

func claudeReviewPrompt(pr PullRequest, cfg *Config) string {
	prompt, err := renderClaudeReviewPrompt(claudeReviewPromptTemplate(cfg), pr)
	if err == nil {
		return prompt
	}

	clog.Warn().Err(err).Msg("Invalid Claude review prompt template; falling back to default")
	prompt, _ = renderClaudeReviewPrompt(defaultClaudeReviewPromptTemplate(), pr)
	return prompt
}

func validateClaudeReviewPromptTemplate(template string) error {
	allowed := make(map[string]struct{}, len(claudeReviewPromptPlaceholderNames()))
	for _, name := range claudeReviewPromptPlaceholderNames() {
		allowed[name] = struct{}{}
	}

	unknown := make(map[string]struct{})
	for _, match := range claudeReviewPromptPlaceholderPattern.FindAllStringSubmatch(template, -1) {
		if len(match) < claudeReviewPromptSubmatchCount {
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
		strings.Join(claudeReviewPromptPlaceholderNames(), ", "),
	)
}

func renderClaudeReviewPrompt(template string, pr PullRequest) (string, error) {
	if err := validateClaudeReviewPromptTemplate(template); err != nil {
		return "", err
	}

	owner, repo := prOwnerRepo(pr)
	values := map[string]string{
		"prNumber":    fmt.Sprintf("%d", pr.Number),
		"repo":        repo,
		"org":         owner,
		"orgWithRepo": pr.Repository.NameWithOwner,
		"prURL":       pr.URL,
		"prRef":       pr.Ref(),
		"title":       pr.Title,
	}

	rendered := claudeReviewPromptPlaceholderPattern.ReplaceAllStringFunc(
		template,
		func(match string) string {
			parts := claudeReviewPromptPlaceholderPattern.FindStringSubmatch(match)
			if len(parts) < claudeReviewPromptSubmatchCount {
				return match
			}
			return values[parts[1]]
		},
	)
	return rendered, nil
}
