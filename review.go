package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"al.essio.dev/pkg/shellescape"
	tea "charm.land/bubbletea/v2"
)

type aiReviewLauncher string

const (
	aiReviewLauncherNone    aiReviewLauncher = ""
	aiReviewLauncherGhostty aiReviewLauncher = "ghostty"
	aiReviewLauncherITerm2  aiReviewLauncher = "iterm2"
)

func currentAIReviewLauncher() aiReviewLauncher {
	if !isDarwin() {
		return aiReviewLauncherNone
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty":
		return aiReviewLauncherGhostty
	case "iTerm.app":
		return aiReviewLauncherITerm2
	default:
		return aiReviewLauncherNone
	}
}

func hasAIReviewLauncher() bool { return currentAIReviewLauncher() != aiReviewLauncherNone }

const (
	reviewProviderOptionLabel = "Provider"
	reviewModelOptionLabel    = "Model"
	reviewEffortOptionLabel   = "Effort"

	reviewProviderOptionRow = 0
	reviewModelOptionRow    = 1
	reviewEffortOptionRow   = 2

	claudeReviewModelSonnet = "sonnet"
	claudeReviewModelOpus   = "opus"
	codexReviewModel54      = "gpt-5.4"
	codexReviewModel54Mini  = "gpt-5.4-mini"
	codexReviewModel53Codex = "gpt-5.3-codex"

	claudeReviewEffortLow    = "low"
	claudeReviewEffortMedium = "medium"
	claudeReviewEffortHigh   = "high"
	claudeReviewEffortMax    = "max"
	claudeReviewEffortAuto   = "auto"
	codexReviewEffortLow     = "low"
	codexReviewEffortMedium  = "medium"
	codexReviewEffortHigh    = "high"
	codexReviewEffortXHigh   = "xhigh"

	geminiReviewModel31Pro = "gemini-3.1-pro"
	geminiReviewModel3Pro  = "gemini-3-pro"
	geminiReviewModelFlash = "gemini-2.5-flash"
)

var reviewProviderChoices = []filterChoice{
	{label: string(reviewProviderClaude), value: string(reviewProviderClaude)},
	{label: string(reviewProviderCodex), value: string(reviewProviderCodex)},
	{label: string(reviewProviderGemini), value: string(reviewProviderGemini)},
}

type reviewProviderConfig struct {
	models        []filterChoice
	defaultModel  string
	efforts       []filterChoice
	defaultEffort string
}

var claudeReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: claudeReviewModelSonnet, value: claudeReviewModelSonnet},
		{label: claudeReviewModelOpus, value: claudeReviewModelOpus},
	},
	defaultModel: claudeReviewModelOpus,
	efforts: []filterChoice{
		{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
		{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
		{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
		{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
		{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
	},
	defaultEffort: claudeReviewEffortMedium,
}

var codexReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: codexReviewModel54, value: codexReviewModel54},
		{label: codexReviewModel54Mini, value: codexReviewModel54Mini},
		{label: codexReviewModel53Codex, value: codexReviewModel53Codex},
	},
	defaultModel: codexReviewModel54,
	efforts: []filterChoice{
		{label: codexReviewEffortLow, value: codexReviewEffortLow},
		{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
		{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
		{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
	},
	defaultEffort: codexReviewEffortMedium,
}

var geminiReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: geminiReviewModel31Pro, value: geminiReviewModel31Pro},
		{label: geminiReviewModel3Pro, value: geminiReviewModel3Pro},
		{label: geminiReviewModelFlash, value: geminiReviewModelFlash},
	},
	defaultModel:  geminiReviewModel31Pro,
	efforts:       nil,
	defaultEffort: "",
}

func reviewConfig(provider reviewProvider) reviewProviderConfig {
	switch provider {
	case reviewProviderCodex:
		return codexReviewConfig
	case reviewProviderGemini:
		return geminiReviewConfig
	case reviewProviderClaude, reviewProviderUnknown:
		return claudeReviewConfig
	}
	return claudeReviewConfig
}

func reviewModelChoices(provider reviewProvider) []filterChoice {
	return reviewConfig(provider).models
}

func defaultReviewModel(provider reviewProvider) string {
	return reviewConfig(provider).defaultModel
}

func reviewEffortChoices(provider reviewProvider, _ string) []filterChoice {
	return reviewConfig(provider).efforts
}

func defaultReviewEffort(provider reviewProvider, _ string) string {
	return reviewConfig(provider).defaultEffort
}

func isValidReviewModel(provider reviewProvider, model string) bool {
	for _, choice := range reviewModelChoices(provider) {
		if choice.value == model {
			return true
		}
	}
	return false
}

func isValidReviewEffort(provider reviewProvider, model, effort string) bool {
	for _, choice := range reviewEffortChoices(provider, model) {
		if choice.value == effort {
			return true
		}
	}
	return false
}

func normalizeReviewModel(provider reviewProvider, model string) string {
	if isValidReviewModel(provider, model) {
		return model
	}
	return defaultReviewModel(provider)
}

func normalizeReviewEffort(provider reviewProvider, model, effort string) string {
	if isValidReviewEffort(provider, model, effort) {
		return effort
	}
	return defaultReviewEffort(provider, model)
}

func configuredReviewProvider(cfg *Config) reviewProvider {
	if cfg == nil {
		return defaultReviewProvider
	}
	if provider := normalizeReviewProvider(
		cfg.TUI.Review.Default.Provider,
	); provider != reviewProviderUnknown {
		return provider
	}
	return defaultReviewProvider
}

func configuredReviewModel(cfg *Config, provider reviewProvider) string {
	if cfg == nil {
		return defaultReviewModel(provider)
	}
	return normalizeReviewModel(provider, cfg.TUI.Review.Default.Model)
}

func configuredReviewEffort(cfg *Config, provider reviewProvider, model string) string {
	if cfg == nil {
		return defaultReviewEffort(provider, model)
	}
	return normalizeReviewEffort(provider, model, cfg.TUI.Review.Default.Effort)
}

func (m tuiModel) selectedReviewProvider() reviewProvider {
	provider := normalizeReviewProvider(m.selectedConfirmOptionValue(0))
	if provider != reviewProviderUnknown {
		return provider
	}
	return configuredReviewProvider(m.cfg)
}

func reviewProviderHasEffort(provider reviewProvider) bool {
	return len(reviewConfig(provider).efforts) > 0
}

func reviewConfirmOptions(provider reviewProvider, model string) []filterOptionDef {
	opts := []filterOptionDef{
		{
			label:   reviewProviderOptionLabel,
			choices: reviewProviderChoices,
		},
		{
			label:   reviewModelOptionLabel,
			choices: reviewModelChoices(provider),
		},
	}
	if reviewProviderHasEffort(provider) {
		opts = append(opts, filterOptionDef{
			label:   reviewEffortOptionLabel,
			choices: reviewEffortChoices(provider, model),
		})
	}
	return opts
}

func reviewConfirmOptValues(provider reviewProvider, model, effort string) []int {
	vals := []int{
		choiceIndex(reviewProviderChoices, string(provider)),
		choiceIndex(reviewModelChoices(provider), model),
	}
	if reviewProviderHasEffort(provider) {
		vals = append(vals, choiceIndex(reviewEffortChoices(provider, model), effort))
	}
	return vals
}

func (m tuiModel) syncReviewConfirmOptions(previousProvider reviewProvider) tuiModel {
	if m.confirmAction != tuiActionReview || len(m.confirmOptions) < 2 {
		return m
	}

	currentProvider := m.selectedReviewProvider()
	currentModel := m.selectedConfirmOptionValue(reviewModelOptionRow)
	currentEffort := ""
	if reviewProviderHasEffort(previousProvider) && len(m.confirmOptions) > reviewEffortOptionRow {
		currentEffort = m.selectedConfirmOptionValue(reviewEffortOptionRow)
	}

	m.confirmOptions = reviewConfirmOptions(currentProvider, currentModel)
	m.confirmOptValues = reviewConfirmOptValues(
		currentProvider,
		normalizeReviewModel(currentProvider, currentModel),
		normalizeReviewEffort(currentProvider, currentModel, currentEffort),
	)

	// Clamp cursor to new option count.
	if m.confirmOptCursor >= len(m.confirmOptions) {
		m.confirmOptCursor = len(m.confirmOptions) - 1
	}

	if m.confirmReviewPR != nil && previousProvider != reviewProviderUnknown &&
		previousProvider != currentProvider {
		oldPrompt := reviewPrompt(*m.confirmReviewPR, m.cfg, previousProvider)
		if m.confirmInput.Value() == oldPrompt {
			m.confirmInput.SetValue(reviewPrompt(*m.confirmReviewPR, m.cfg, currentProvider))
		}
	}

	return m
}

func (m tuiModel) prepareAIReviewConfirm(pr PullRequest, idx int) tuiModel {
	prCopy := pr
	provider := configuredReviewProvider(m.cfg)
	model := configuredReviewModel(m.cfg, provider)
	effort := configuredReviewEffort(m.cfg, provider, model)
	m.confirmAction = tuiActionReview
	m.confirmYes = true
	m.confirmHasInput = true
	m = m.prepareConfirmInput()
	m.confirmInputLabel = "Prompt"
	m.confirmOptions = reviewConfirmOptions(provider, model)
	m.confirmOptValues = reviewConfirmOptValues(provider, model, effort)
	m.confirmOptCursor = 0
	m.confirmOptFocus = true
	m.confirmReviewPR = &prCopy
	m = m.setConfirmInputPlaceholder("Leave blank to use the default prompt")
	m.confirmInput.Blur()
	m.confirmInput.SetValue(reviewPrompt(pr, m.cfg, provider))
	m.confirmPrompt = "Launch AI review for " + styledRef(&prCopy) + "?"
	m.confirmCmdFn = func(submission confirmSubmission) tea.Cmd {
		prompt := submission.Input
		provider := normalizeReviewProvider(submission.Option(reviewProviderOptionLabel))
		if provider == reviewProviderUnknown {
			provider = configuredReviewProvider(m.cfg)
		}
		model := normalizeReviewModel(provider, submission.Option(reviewModelOptionLabel))
		effort := ""
		if reviewProviderHasEffort(provider) {
			effort = normalizeReviewEffort(
				provider,
				model,
				submission.Option(reviewEffortOptionLabel),
			)
		}
		return func() tea.Msg {
			err := launchAIReview(prCopy, prompt, provider, model, effort)
			return aiReviewMsg{index: idx, key: makePRKey(prCopy), err: err}
		}
	}
	return m
}

// launchAIReview opens a new terminal tab, clones the PR there, and
// launches an AI review session in that tab. Cloning happens in the new tab
// so SSH prompts and progress are visible to the user.
func launchAIReview(
	pr PullRequest,
	prompt string,
	provider reviewProvider,
	model string,
	effort string,
) error {
	launcher := currentAIReviewLauncher()
	if launcher == aiReviewLauncherNone {
		return fmt.Errorf("unsupported terminal %q", os.Getenv("TERM_PROGRAM"))
	}

	script, err := buildAIReviewAppleScript(
		launcher,
		buildAIReviewCommand(pr, prompt, provider, model, effort),
	)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(
		context.Background(),
		"osascript",
		"-e",
		script,
	)
	if output, asErr := cmd.CombinedOutput(); asErr != nil {
		return fmt.Errorf("osascript: %w: %s", asErr, strings.TrimSpace(string(output)))
	}
	return nil
}

func buildAIReviewCommand(
	pr PullRequest,
	prompt string,
	provider reviewProvider,
	model string,
	effort string,
) string {
	nwo := pr.Repository.NameWithOwner

	// Clone repo and checkout the PR ref in the new tab so the user sees
	// progress and any SSH/auth prompts. Fetches refs/pull/N/head which
	// works for open, closed, and fork PRs alike.
	remote := "git@github.com:" + nwo
	// Use a fixed review directory so the user only has to trust it once.
	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		cacheHome = os.Getenv("HOME") + "/.cache"
	}
	reviewDir := fmt.Sprintf("%s/prl/reviews/%s/%d", cacheHome, pr.Repository.Name, pr.Number)
	baseCmd := fmt.Sprintf(
		"/usr/bin/trash %s 2>/dev/null; /bin/mkdir -p %s && cd %s && git clone --quiet --depth 1 %s . && git fetch origin refs/pull/%d/head:pr-%d --no-tags && git checkout pr-%d && ",
		shellescape.Quote(reviewDir),
		shellescape.Quote(reviewDir),
		shellescape.Quote(reviewDir),
		shellescape.Quote(remote),
		pr.Number,
		pr.Number,
		pr.Number,
	)
	switch provider {
	case reviewProviderCodex:
		return baseCmd + fmt.Sprintf(
			"codex -m %s -c model_reasoning_effort=%s %s",
			shellescape.Quote(normalizeReviewModel(provider, model)),
			shellescape.Quote(normalizeReviewEffort(provider, model, effort)),
			shellescape.Quote(prompt),
		)
	case reviewProviderGemini:
		return baseCmd + fmt.Sprintf(
			"gemini --model %s --prompt-interactive %s",
			shellescape.Quote(normalizeReviewModel(provider, model)),
			shellescape.Quote(prompt),
		)
	case reviewProviderUnknown:
		return baseCmd + fmt.Sprintf(
			"claude --model=%s --effort=%s --allowedTools 'Bash(gh:*)' --system-prompt %s %s",
			shellescape.Quote(normalizeReviewModel(provider, model)),
			shellescape.Quote(normalizeReviewEffort(provider, model, effort)),
			shellescape.Quote(
				"You are an expert code reviewer. Be thorough, precise, and actionable.",
			),
			shellescape.Quote(prompt),
		)
	case reviewProviderClaude:
		return baseCmd + fmt.Sprintf(
			"claude --model=%s --effort=%s --allowedTools 'Bash(gh:*)' --system-prompt %s %s",
			shellescape.Quote(normalizeReviewModel(provider, model)),
			shellescape.Quote(normalizeReviewEffort(provider, model, effort)),
			shellescape.Quote(
				"You are an expert code reviewer. Be thorough, precise, and actionable.",
			),
			shellescape.Quote(prompt),
		)
	}
	return baseCmd + fmt.Sprintf(
		"claude --model=%s --effort=%s --allowedTools 'Bash(gh:*)' --system-prompt %s %s",
		shellescape.Quote(normalizeReviewModel(provider, model)),
		shellescape.Quote(normalizeReviewEffort(provider, model, effort)),
		shellescape.Quote("You are an expert code reviewer. Be thorough, precise, and actionable."),
		shellescape.Quote(prompt),
	)
}

func buildAIReviewAppleScript(launcher aiReviewLauncher, shellCmd string) (string, error) {
	switch launcher {
	case aiReviewLauncherNone:
		return "", fmt.Errorf("unsupported terminal %q", launcher)
	case aiReviewLauncherGhostty:
		return fmt.Sprintf(`tell application "Ghostty"
	tell application "System Events" to tell process "Ghostty" to set frontmost to true
	set cfg to new surface configuration
	set initial input of cfg to %q
	new tab in front window with configuration cfg
end tell`, shellCmd), nil
	case aiReviewLauncherITerm2:
		return fmt.Sprintf(`tell application "iTerm2"
	activate
	tell current window
		set newTab to (create tab with default profile)
		tell current session of newTab
			write text " " & %q
		end tell
	end tell
end tell`, shellCmd), nil
	}
	return "", fmt.Errorf("unsupported terminal %q", launcher)
}
