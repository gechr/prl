package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
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

	geminiReviewEffortMinimal = "minimal"
	geminiReviewEffortLow     = "low"
	geminiReviewEffortMedium  = "medium"
	geminiReviewEffortHigh    = "high"
	geminiReviewEffortOff     = "0"
	geminiReviewEffort1024    = "1024"
	geminiReviewEffort8192    = "8192"
	geminiReviewEffort24576   = "24576"
	geminiReviewEffortDynamic = "dynamic"

	geminiReviewModel31Pro = "gemini-3.1-pro"
	geminiReviewModel3Pro  = "gemini-3-pro"
	geminiReviewModelFlash = "gemini-2.5-flash"

	geminiModelPatternExact   = "gemini"
	geminiModelPatternAll     = "gemini-*"
	geminiModelPattern3       = "gemini-3*"
	geminiModelPattern25Flash = "gemini-2.5-flash*"
)

var builtInReviewProviderChoices = []filterChoice{
	{label: string(reviewProviderClaude), value: string(reviewProviderClaude)},
	{label: string(reviewProviderCodex), value: string(reviewProviderCodex)},
	{label: string(reviewProviderGemini), value: string(reviewProviderGemini)},
}

type reviewProviderConfig struct {
	models       []filterChoice
	defaultModel string
}

var claudeReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: claudeReviewModelSonnet, value: claudeReviewModelSonnet},
		{label: claudeReviewModelOpus, value: claudeReviewModelOpus},
	},
	defaultModel: claudeReviewModelSonnet,
}

var codexReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: codexReviewModel54, value: codexReviewModel54},
		{label: codexReviewModel54Mini, value: codexReviewModel54Mini},
		{label: codexReviewModel53Codex, value: codexReviewModel53Codex},
	},
	defaultModel: codexReviewModel54,
}

var geminiReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: geminiReviewModel31Pro, value: geminiReviewModel31Pro},
		{label: geminiReviewModel3Pro, value: geminiReviewModel3Pro},
		{label: geminiReviewModelFlash, value: geminiReviewModelFlash},
	},
	defaultModel: geminiReviewModel31Pro,
}

func reviewConfig(cfg *Config, provider reviewProvider) reviewProviderConfig {
	base := builtInReviewConfig(provider)
	if cfg == nil {
		return base
	}

	override := cfg.TUI.Review.providerConfig(provider)
	if len(override.Models) > 0 {
		base.models = reviewChoices(override.Models)
	}

	if !isChoiceValue(base.models, base.defaultModel) && len(base.models) > 0 {
		base.defaultModel = base.models[0].value
	}

	return base
}

func builtInReviewConfig(provider reviewProvider) reviewProviderConfig {
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

func reviewChoices(values []string) []filterChoice {
	choices := make([]filterChoice, len(values))
	for i, value := range values {
		choices[i] = filterChoice{label: value, value: value}
	}
	return choices
}

func isChoiceValue(choices []filterChoice, value string) bool {
	for _, choice := range choices {
		if choice.value == value {
			return true
		}
	}
	return false
}

func reviewProviderChoices(cfg *Config) []filterChoice {
	if cfg == nil || len(cfg.TUI.Review.Enabled) == 0 {
		return builtInReviewProviderChoices
	}
	return reviewChoices(cfg.TUI.Review.Enabled)
}

func reviewModelChoices(cfg *Config, provider reviewProvider) []filterChoice {
	return reviewConfig(cfg, provider).models
}

func defaultReviewModel(cfg *Config, provider reviewProvider) string {
	return reviewConfig(cfg, provider).defaultModel
}

func reviewEffortChoices(cfg *Config, provider reviewProvider, model string) []filterChoice {
	if model == "" {
		model = defaultReviewModel(cfg, provider)
	}
	return reviewEffortChoicesForModel(cfg, provider, model)
}

func defaultReviewEffort(cfg *Config, provider reviewProvider, model string) string {
	if model == "" {
		model = defaultReviewModel(cfg, provider)
	}
	return defaultReviewEffortForModel(cfg, provider, model)
}

func reviewEffortChoicesForModel(
	cfg *Config,
	provider reviewProvider,
	model string,
) []filterChoice {
	if cfg != nil {
		override := cfg.TUI.Review.providerConfig(provider)
		if len(override.Efforts) > 0 {
			return reviewChoices(override.Efforts)
		}
	}
	switch provider {
	case reviewProviderCodex:
		return matchingReviewEffortRule(codexEffortRules, model).choices
	case reviewProviderGemini:
		return matchingReviewEffortRule(geminiEffortRules, model).choices
	case reviewProviderClaude, reviewProviderUnknown:
		return matchingReviewEffortRule(claudeEffortRules, model).choices
	}
	return nil
}

func defaultReviewEffortForModel(cfg *Config, provider reviewProvider, model string) string {
	if cfg != nil {
		override := cfg.TUI.Review.providerConfig(provider)
		if len(override.Efforts) > 0 {
			return override.Efforts[0]
		}
	}
	switch provider {
	case reviewProviderCodex:
		return matchingReviewEffortRule(codexEffortRules, model).def
	case reviewProviderGemini:
		return matchingReviewEffortRule(geminiEffortRules, model).def
	case reviewProviderClaude, reviewProviderUnknown:
		return matchingReviewEffortRule(claudeEffortRules, model).def
	}
	return ""
}

type geminiThinkingMode string

const (
	geminiEffortModeNone           geminiThinkingMode = ""
	geminiEffortModeThinkingLevel  geminiThinkingMode = "thinking_level"
	geminiEffortModeThinkingBudget geminiThinkingMode = "thinking_budget"
)

type reviewEffortRule struct {
	pattern string
	choices []filterChoice
	def     string
	mode    geminiThinkingMode
}

var claudeEffortRules = []reviewEffortRule{
	{
		pattern: "*",
		choices: []filterChoice{
			{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
			{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
			{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
			{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
			{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
		},
		def: claudeReviewEffortMedium,
	},
}

var codexEffortRules = []reviewEffortRule{
	{
		pattern: "*",
		choices: []filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
		},
		def: codexReviewEffortMedium,
	},
}

var geminiEffortRules = []reviewEffortRule{
	{
		pattern: geminiModelPattern25Flash,
		choices: []filterChoice{
			{label: geminiReviewEffortOff, value: geminiReviewEffortOff},
			{label: geminiReviewEffort1024, value: geminiReviewEffort1024},
			{label: geminiReviewEffort8192, value: geminiReviewEffort8192},
			{label: geminiReviewEffort24576, value: geminiReviewEffort24576},
			{label: geminiReviewEffortDynamic, value: geminiReviewEffortDynamic},
		},
		def:  geminiReviewEffortDynamic,
		mode: geminiEffortModeThinkingBudget,
	},
	{
		pattern: geminiModelPattern3,
		choices: []filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		def:  geminiReviewEffortHigh,
		mode: geminiEffortModeThinkingLevel,
	},
	{
		pattern: geminiModelPatternExact,
		choices: []filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		def:  geminiReviewEffortHigh,
		mode: geminiEffortModeThinkingLevel,
	},
	{
		pattern: geminiModelPatternAll,
		choices: []filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		def:  geminiReviewEffortHigh,
		mode: geminiEffortModeThinkingLevel,
	},
}

func geminiEffortMode(model string) geminiThinkingMode {
	return matchingReviewEffortRule(geminiEffortRules, model).mode
}

func matchingReviewEffortRule(rules []reviewEffortRule, model string) reviewEffortRule {
	for _, rule := range rules {
		if matchesPattern(rule.pattern, model) {
			return rule
		}
	}
	return reviewEffortRule{}
}

func matchesPattern(pattern, value string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == value
	}
	match, err := path.Match(pattern, value)
	return err == nil && match
}

func isValidReviewModel(cfg *Config, provider reviewProvider, model string) bool {
	for _, choice := range reviewModelChoices(cfg, provider) {
		if choice.value == model {
			return true
		}
	}
	return false
}

func isValidReviewEffort(cfg *Config, provider reviewProvider, model, effort string) bool {
	for _, choice := range reviewEffortChoicesForModel(cfg, provider, model) {
		if choice.value == effort {
			return true
		}
	}
	return false
}

func normalizeReviewModel(cfg *Config, provider reviewProvider, model string) string {
	if isValidReviewModel(cfg, provider, model) {
		return model
	}
	return defaultReviewModel(cfg, provider)
}

func normalizeReviewEffort(cfg *Config, provider reviewProvider, model, effort string) string {
	if isValidReviewEffort(cfg, provider, model, effort) {
		return effort
	}
	return defaultReviewEffort(cfg, provider, model)
}

func configuredReviewProvider(cfg *Config) reviewProvider {
	if cfg == nil {
		return defaultReviewProvider
	}
	if provider := normalizeReviewProvider(
		cfg.TUI.Review.Default.Provider,
	); provider != reviewProviderUnknown &&
		isChoiceValue(reviewProviderChoices(cfg), string(provider)) {
		return provider
	}
	if isChoiceValue(reviewProviderChoices(cfg), string(defaultReviewProvider)) {
		return defaultReviewProvider
	}
	if choices := reviewProviderChoices(cfg); len(choices) > 0 {
		return normalizeReviewProvider(choices[0].value)
	}
	return defaultReviewProvider
}

func configuredReviewModel(cfg *Config, provider reviewProvider) string {
	if cfg == nil {
		return defaultReviewModel(nil, provider)
	}
	return normalizeReviewModel(cfg, provider, cfg.TUI.Review.Default.Model)
}

func configuredReviewEffort(cfg *Config, provider reviewProvider, model string) string {
	if cfg == nil {
		return defaultReviewEffort(nil, provider, model)
	}
	return normalizeReviewEffort(cfg, provider, model, cfg.TUI.Review.Default.Effort)
}

func (m tuiModel) selectedReviewProvider() reviewProvider {
	provider := normalizeReviewProvider(m.selectedConfirmOptionValue(0))
	if provider != reviewProviderUnknown {
		return provider
	}
	return configuredReviewProvider(m.cfg)
}

func reviewProviderHasEffort(cfg *Config, provider reviewProvider, model string) bool {
	model = normalizeReviewModel(cfg, provider, model)
	if model == "" {
		model = defaultReviewModel(cfg, provider)
	}
	return len(reviewEffortChoicesForModel(cfg, provider, model)) > 0
}

func reviewConfirmOptions(cfg *Config, provider reviewProvider, model string) []filterOptionDef {
	model = normalizeReviewModel(cfg, provider, model)
	opts := []filterOptionDef{
		{
			label:   reviewProviderOptionLabel,
			choices: reviewProviderChoices(cfg),
		},
		{
			label:   reviewModelOptionLabel,
			choices: reviewModelChoices(cfg, provider),
		},
	}
	if reviewProviderHasEffort(cfg, provider, model) {
		opts = append(opts, filterOptionDef{
			label:   reviewEffortOptionLabel,
			choices: reviewEffortChoicesForModel(cfg, provider, model),
		})
	}
	return opts
}

func reviewConfirmOptValues(cfg *Config, provider reviewProvider, model, effort string) []int {
	model = normalizeReviewModel(cfg, provider, model)
	effort = normalizeReviewEffort(cfg, provider, model, effort)
	vals := []int{
		choiceIndex(reviewProviderChoices(cfg), string(provider)),
		choiceIndex(reviewModelChoices(cfg, provider), model),
	}
	if reviewProviderHasEffort(cfg, provider, model) {
		vals = append(vals, choiceIndex(reviewEffortChoicesForModel(cfg, provider, model), effort))
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
	if reviewProviderHasEffort(m.cfg, previousProvider, currentModel) &&
		len(m.confirmOptions) > reviewEffortOptionRow {
		currentEffort = m.selectedConfirmOptionValue(reviewEffortOptionRow)
	}

	m.confirmOptions = reviewConfirmOptions(m.cfg, currentProvider, currentModel)
	m.confirmState.OptValues = reviewConfirmOptValues(
		m.cfg,
		currentProvider,
		normalizeReviewModel(m.cfg, currentProvider, currentModel),
		normalizeReviewEffort(m.cfg, currentProvider, currentModel, currentEffort),
	)

	// Clamp cursor to new option count.
	if m.confirmState.OptCursor >= len(m.confirmOptions) {
		m.confirmState.OptCursor = len(m.confirmOptions) - 1
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
	m.confirmState.Yes = true
	m.confirmHasInput = true
	m = m.prepareConfirmInput()
	m.confirmInputLabel = "Prompt"
	m.confirmOptions = reviewConfirmOptions(m.cfg, provider, model)
	m.confirmState.OptValues = reviewConfirmOptValues(m.cfg, provider, model, effort)
	m.confirmState.OptCursor = 0
	m.confirmState.OptFocus = true
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
		model := normalizeReviewModel(m.cfg, provider, submission.Option(reviewModelOptionLabel))
		effort := ""
		if reviewProviderHasEffort(m.cfg, provider, model) {
			effort = normalizeReviewEffort(
				m.cfg,
				provider,
				model,
				submission.Option(reviewEffortOptionLabel),
			)
		}
		return func() tea.Msg {
			err := launchAIReview(prCopy, prompt, m.cfg, provider, model, effort)
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
	cfg *Config,
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
		buildAIReviewCommand(pr, prompt, cfg, provider, model, effort),
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
	cfg *Config,
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
	cmdModel := normalizeReviewModel(cfg, provider, model)
	cmdEffort := normalizeReviewEffort(cfg, provider, cmdModel, effort)
	switch provider {
	case reviewProviderCodex:
		return baseCmd + fmt.Sprintf(
			"codex -m %s -c model_reasoning_effort=%s %s",
			shellescape.Quote(cmdModel),
			shellescape.Quote(cmdEffort),
			shellescape.Quote(prompt),
		)
	case reviewProviderGemini:
		return baseCmd + buildGeminiReviewCommand(reviewDir, cmdModel, cmdEffort, prompt)
	case reviewProviderUnknown:
		return baseCmd + fmt.Sprintf(
			"claude --model=%s --effort=%s --allowedTools 'Bash(gh:*)' --system-prompt %s %s",
			shellescape.Quote(cmdModel),
			shellescape.Quote(cmdEffort),
			shellescape.Quote(
				"You are an expert code reviewer. Be thorough, precise, and actionable.",
			),
			shellescape.Quote(prompt),
		)
	case reviewProviderClaude:
		return baseCmd + fmt.Sprintf(
			"claude --model=%s --effort=%s --allowedTools 'Bash(gh:*)' --system-prompt %s %s",
			shellescape.Quote(cmdModel),
			shellescape.Quote(cmdEffort),
			shellescape.Quote(
				"You are an expert code reviewer. Be thorough, precise, and actionable.",
			),
			shellescape.Quote(prompt),
		)
	}
	return baseCmd + fmt.Sprintf(
		"claude --model=%s --effort=%s --allowedTools 'Bash(gh:*)' --system-prompt %s %s",
		shellescape.Quote(cmdModel),
		shellescape.Quote(cmdEffort),
		shellescape.Quote("You are an expert code reviewer. Be thorough, precise, and actionable."),
		shellescape.Quote(prompt),
	)
}

func buildGeminiReviewCommand(reviewDir, model, effort, prompt string) string {
	settingsJSON, err := json.Marshal(geminiReviewSettings(model, effort))
	if err != nil {
		return fmt.Sprintf(
			"gemini --model %s --prompt-interactive %s",
			shellescape.Quote(model),
			shellescape.Quote(prompt),
		)
	}
	return fmt.Sprintf(
		"/bin/mkdir -p %s/.gemini && printf '%%s' %s > %s/.gemini/settings.json && gemini --model %s --prompt-interactive %s",
		shellescape.Quote(reviewDir),
		shellescape.Quote(string(settingsJSON)),
		shellescape.Quote(reviewDir),
		shellescape.Quote("prl-review"),
		shellescape.Quote(prompt),
	)
}

func geminiReviewSettings(model, effort string) map[string]any {
	modelConfig := map[string]any{"model": model}
	if thinkingConfig := geminiThinkingConfig(model, effort); len(thinkingConfig) > 0 {
		modelConfig["generateContentConfig"] = map[string]any{
			"thinkingConfig": thinkingConfig,
		}
	}
	return map[string]any{
		"modelConfigs": map[string]any{
			"customAliases": map[string]any{
				"prl-review": map[string]any{
					"modelConfig": modelConfig,
				},
			},
		},
	}
}

func geminiThinkingConfig(model, effort string) map[string]any {
	if effort == "" {
		return nil
	}
	switch geminiEffortMode(model) {
	case geminiEffortModeThinkingLevel:
		return map[string]any{
			"thinkingLevel": strings.ToUpper(effort),
		}
	case geminiEffortModeThinkingBudget:
		if effort == geminiReviewEffortDynamic {
			return map[string]any{"thinkingBudget": -1}
		}
		budget, err := strconv.Atoi(effort)
		if err != nil {
			return nil
		}
		return map[string]any{"thinkingBudget": budget}
	case geminiEffortModeNone:
		return nil
	}
	return nil
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
