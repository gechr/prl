package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	xslices "github.com/gechr/x/slices"
	"github.com/gechr/x/terminal/emulator"
)

type aiReviewLauncher string

const (
	aiReviewLauncherNone    aiReviewLauncher = ""
	aiReviewLauncherGhostty aiReviewLauncher = "ghostty"
	aiReviewLauncherITerm2  aiReviewLauncher = "iterm2"
	aiReviewLauncherKitty   aiReviewLauncher = "kitty"
)

func currentAIReviewLauncher() aiReviewLauncher {
	if !xos.IsDarwin() {
		return aiReviewLauncherNone
	}
	switch emulator.Detect() {
	case emulator.Kitty:
		if _, err := exec.LookPath("kitty"); err == nil {
			return aiReviewLauncherKitty
		}
	case emulator.Ghostty:
		return aiReviewLauncherGhostty
	case emulator.ITerm2:
		return aiReviewLauncherITerm2
	}
	return aiReviewLauncherNone
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
	claudeReviewModelFable  = "fable"
	codexReviewModel56Sol   = "gpt-5.6-sol"
	codexReviewModel56Terra = "gpt-5.6-terra"
	codexReviewModel56Luna  = "gpt-5.6-luna"
	codexReviewModel55      = "gpt-5.5"
	codexReviewModel54      = "gpt-5.4"
	codexReviewModel54Mini  = "gpt-5.4-mini"

	claudeReviewEffortLow    = "low"
	claudeReviewEffortMedium = "medium"
	claudeReviewEffortHigh   = "high"
	claudeReviewEffortXHigh  = "xhigh"
	claudeReviewEffortMax    = "max"
	claudeReviewEffortAuto   = "auto"
	codexReviewEffortLow     = "low"
	codexReviewEffortMedium  = "medium"
	codexReviewEffortHigh    = "high"
	codexReviewEffortXHigh   = "xhigh"
	codexReviewEffortMax     = "max"

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
	geminiReviewModelFlash = "gemini-2.5-flash"

	geminiModelPatternExact   = "gemini"
	geminiModelPatternAll     = "gemini-*"
	geminiModelPattern31Pro   = "gemini-3.1-pro*"
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
		{label: claudeReviewModelFable, value: claudeReviewModelFable},
	},
	defaultModel: claudeReviewModelOpus,
}

var codexReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: codexReviewModel56Sol, value: codexReviewModel56Sol},
		{label: codexReviewModel56Terra, value: codexReviewModel56Terra},
		{label: codexReviewModel56Luna, value: codexReviewModel56Luna},
		{label: codexReviewModel55, value: codexReviewModel55},
		{label: codexReviewModel54, value: codexReviewModel54},
		{label: codexReviewModel54Mini, value: codexReviewModel54Mini},
	},
	defaultModel: codexReviewModel56Sol,
}

var geminiReviewConfig = reviewProviderConfig{
	models: []filterChoice{
		{label: geminiReviewModel31Pro, value: geminiReviewModel31Pro},
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
	return xslices.Map(values, func(value string) filterChoice {
		return filterChoice{label: value, value: value}
	})
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
		pattern: claudeReviewModelOpus,
		choices: []filterChoice{
			{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
			{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
			{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
			{label: claudeReviewEffortXHigh, value: claudeReviewEffortXHigh},
			{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
			{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
		},
		def: claudeReviewEffortHigh,
	},
	{
		pattern: claudeReviewModelSonnet,
		choices: []filterChoice{
			{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
			{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
			{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
			{label: claudeReviewEffortXHigh, value: claudeReviewEffortXHigh},
			{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
			{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
		},
		def: claudeReviewEffortMedium,
	},
	{
		pattern: claudeReviewModelFable,
		choices: []filterChoice{
			{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
			{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
			{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
			{label: claudeReviewEffortXHigh, value: claudeReviewEffortXHigh},
			{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
			{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
		},
		def: claudeReviewEffortMedium,
	},
	{
		pattern: "*",
		choices: []filterChoice{
			{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
			{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
			{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
			{label: claudeReviewEffortXHigh, value: claudeReviewEffortXHigh},
			{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
			{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
		},
		def: claudeReviewEffortHigh,
	},
}

var codexEffortRules = []reviewEffortRule{
	{
		pattern: "gpt-5.6*",
		choices: []filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
			{label: codexReviewEffortMax, value: codexReviewEffortMax},
		},
		def: codexReviewEffortHigh,
	},
	{
		pattern: "*",
		choices: []filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
		},
		def: codexReviewEffortXHigh,
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
		pattern: geminiModelPattern31Pro,
		choices: []filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		def:  geminiReviewEffortHigh,
		mode: geminiEffortModeThinkingLevel,
	},
	{
		pattern: geminiModelPattern3,
		choices: []filterChoice{
			{label: geminiReviewEffortMinimal, value: geminiReviewEffortMinimal},
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
	ctx := context.Background()
	launcher := currentAIReviewLauncher()
	if launcher == aiReviewLauncherNone {
		return fmt.Errorf("unsupported terminal %q", os.Getenv("TERM_PROGRAM"))
	}

	promptFile, err := writeReviewPromptFile(prompt)
	if err != nil {
		return err
	}
	dispatched := false
	launchFile := ""
	defer func() {
		if !dispatched {
			_ = os.Remove(promptFile)
			if launchFile != "" {
				_ = os.Remove(launchFile)
			}
		}
	}()

	shellCmd := buildAIReviewCommand(pr, promptFile, cfg, provider, model, effort)
	launchFile, err = writeReviewLaunchFile(shellCmd, promptFile)
	if err != nil {
		return err
	}
	launchCmd := "/bin/sh " + shell.Quote(launchFile)

	if launcher == aiReviewLauncherKitty {
		tabTitle := fmt.Sprintf("%s#%d", pr.Repository.Name, pr.Number)
		if kittyErr := launchAIReviewKitty(ctx, launchCmd, tabTitle); kittyErr != nil {
			return kittyErr
		}
		dispatched = true
		return nil
	}

	script, err := buildAIReviewAppleScript(launcher)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "osascript", "-e", script, "--", launchCmd)
	if output, asErr := cmd.CombinedOutput(); asErr != nil {
		return fmt.Errorf("osascript: %w: %s", asErr, strings.TrimSpace(string(output)))
	}
	dispatched = true
	return nil
}

// writeReviewPromptFile writes the prompt to a temp file so the
// AppleScript-typed shell command can reference it by path. Inlining the
// prompt as a quoted argv fails when the prompt contains newlines:
// terminal "initial input" / "write text" automation interprets each \n
// as Enter, executing prompt lines as separate shell commands before the
// AI tool ever runs.
func writeReviewPromptFile(prompt string) (string, error) {
	f, err := os.CreateTemp("", "prl-review-*.txt")
	if err != nil {
		return "", fmt.Errorf("write prompt file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(prompt); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write prompt file: %w", err)
	}
	return f.Name(), nil
}

func writeReviewLaunchFile(shellCmd, promptFile string) (string, error) {
	f, err := os.CreateTemp("", "prl-review-*.sh")
	if err != nil {
		return "", fmt.Errorf("write launch file: %w", err)
	}
	cleanup := "/bin/rm -f " + shell.Quote(promptFile) + " " + shell.Quote(f.Name())
	contents := "#!/bin/sh\ntrap " + shell.Quote(cleanup) + " EXIT HUP INT TERM\n" + shellCmd + "\n"
	if _, err := f.WriteString(contents); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write launch file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write launch file: %w", err)
	}
	return f.Name(), nil
}

// promptArg returns a shell expression that expands to the prompt
// contents at runtime, plus a cleanup snippet for the temp file. The
// expression must not be further shell-quoted.
func promptArg(promptFile string) (string, string) {
	q := shell.Quote(promptFile)
	return fmt.Sprintf(`"$(/bin/cat %s)"`, q), fmt.Sprintf("; rm -f %s", q)
}

// launchAIReviewKitty opens a new Kitty tab using Kitty's remote control
// protocol. Requires `allow_remote_control yes` in kitty.conf.
func launchAIReviewKitty(ctx context.Context, shellCmd, tabTitle string) error {
	// Open a new tab without specifying a command so Kitty starts the user's
	// configured shell, giving the full login environment (API keys, direnv, etc.).
	// The window ID printed to stdout lets us target send-text precisely.
	launchCmd := exec.CommandContext( //nolint:gosec // tabTitle is built from PR metadata
		ctx, "kitty", "@", "launch", "--type=tab", "--tab-title="+tabTitle,
	)
	out, err := launchCmd.Output()
	if err != nil {
		return fmt.Errorf("kitty remote control: %w", err)
	}
	windowID := strings.TrimSpace(string(out))

	// Send the command into that specific window.
	cmd := exec.CommandContext( //nolint:gosec // shellCmd is built internally
		ctx,
		"kitty",
		"@",
		"send-text",
		"--match",
		"id:"+windowID,
		shellCmd+"\n",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kitty send-text: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func buildAIReviewCommand(
	pr PullRequest,
	promptFile string,
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
	reviewDir := aiReviewDir(pr, promptFile)
	headGuard := ""
	if pr.HeadSHA != "" && safeReviewPathComponent(pr.HeadSHA, "") == pr.HeadSHA {
		headGuard = fmt.Sprintf(
			`test "$(git rev-parse HEAD)" = %s && `,
			shell.Quote(pr.HeadSHA),
		)
	}
	baseCmd := fmt.Sprintf(
		"/usr/bin/trash %s 2>/dev/null; /bin/mkdir -p %s && cd %s && git clone --quiet --depth 1 %s . && git fetch origin refs/pull/%d/head:pr-%d --no-tags && git checkout pr-%d && %s",
		shell.Quote(reviewDir),
		shell.Quote(reviewDir),
		shell.Quote(reviewDir),
		shell.Quote(remote),
		pr.Number,
		pr.Number,
		pr.Number,
		headGuard,
	)
	cmdModel := normalizeReviewModel(cfg, provider, model)
	cmdEffort := normalizeReviewEffort(cfg, provider, cmdModel, effort)
	prompt, cleanup := promptArg(promptFile)
	switch provider {
	case reviewProviderCodex:
		return baseCmd + fmt.Sprintf(
			"codex --sandbox read-only -m %s -c model_reasoning_effort=%s %s%s",
			shell.Quote(cmdModel),
			shell.Quote(cmdEffort),
			prompt,
			cleanup,
		)
	case reviewProviderGemini:
		return baseCmd + buildGeminiReviewCommand(reviewDir, cmdModel, cmdEffort, prompt) + cleanup
	case reviewProviderUnknown, reviewProviderClaude:
		return baseCmd + fmt.Sprintf(
			"claude --permission-mode plan --model=%s %s--system-prompt %s %s%s",
			shell.Quote(cmdModel),
			claudeEffortArg(cmdEffort),
			shell.Quote(
				"You are an expert code reviewer. Be thorough, precise, and actionable.",
			),
			prompt,
			cleanup,
		)
	}
	return baseCmd + fmt.Sprintf(
		"claude --permission-mode plan --model=%s %s--system-prompt %s %s%s",
		shell.Quote(cmdModel),
		claudeEffortArg(cmdEffort),
		shell.Quote("You are an expert code reviewer. Be thorough, precise, and actionable."),
		prompt,
		cleanup,
	)
}

func aiReviewDir(pr PullRequest, promptFile string) string {
	cacheHome, err := shell.CacheDir()
	if err != nil {
		cacheHome = filepath.Join(os.TempDir(), ".cache")
	}
	owner, repo, ok := strings.Cut(pr.Repository.NameWithOwner, "/")
	if !ok {
		owner, repo = "unknown", pr.Repository.Name
	}
	owner = safeReviewPathComponent(owner, "unknown")
	repo = safeReviewPathComponent(repo, safeReviewPathComponent(pr.Repository.Name, "repository"))
	revisionFallback := strings.TrimSuffix(filepath.Base(promptFile), filepath.Ext(promptFile))
	revision := safeReviewPathComponent(pr.HeadSHA, revisionFallback)
	return filepath.Join(
		cacheHome, "prl", "reviews", owner, repo, strconv.Itoa(pr.Number), revision,
	)
}

func safeReviewPathComponent(value, fallback string) string {
	if value == "" || value == "." || value == ".." {
		return fallback
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return fallback
		}
	}
	return value
}

func claudeEffortArg(effort string) string {
	if effort == claudeReviewEffortAuto {
		return ""
	}
	return fmt.Sprintf("--effort=%s ", shell.Quote(effort))
}

// buildGeminiReviewCommand expects promptExpr to be an already shell-safe
// expression (e.g. "$(/bin/cat /path)"); it must not be further quoted.
func buildGeminiReviewCommand(reviewDir, model, effort, promptExpr string) string {
	settingsJSON, err := json.Marshal(geminiReviewSettings(model, effort))
	if err != nil {
		return fmt.Sprintf(
			"gemini --model %s --prompt-interactive %s",
			shell.Quote(model),
			promptExpr,
		)
	}
	return fmt.Sprintf(
		"/bin/rm -rf %s/.gemini && /bin/mkdir -p %s/.gemini && printf '%%s' %s > %s/.gemini/settings.json && gemini --sandbox --approval-mode plan --model %s --prompt-interactive %s",
		shell.Quote(reviewDir),
		shell.Quote(reviewDir),
		shell.Quote(string(settingsJSON)),
		shell.Quote(reviewDir),
		shell.Quote("prl-review"),
		promptExpr,
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

func buildAIReviewAppleScript(launcher aiReviewLauncher) (string, error) {
	switch launcher {
	case aiReviewLauncherNone:
		return "", fmt.Errorf("unsupported terminal %q", launcher)
	case aiReviewLauncherGhostty:
		return `on run argv
	set shellCmd to item 1 of argv
	tell application "Ghostty"
	tell application "System Events" to tell process "Ghostty" to set frontmost to true
	set cfg to new surface configuration
	set initial input of cfg to shellCmd
	new tab in front window with configuration cfg
	end tell
end run`, nil
	case aiReviewLauncherITerm2:
		return `on run argv
	set shellCmd to item 1 of argv
	tell application "iTerm2"
	activate
	tell current window
		set newTab to (create tab with default profile)
		tell current session of newTab
			write text " " & shellCmd
		end tell
	end tell
	end tell
end run`, nil
	case aiReviewLauncherKitty:
		// unreachable: Kitty is dispatched before AppleScript in launchAIReview.
		return "", fmt.Errorf("kitty does not use AppleScript")
	}
	return "", fmt.Errorf("unsupported terminal %q", launcher)
}
