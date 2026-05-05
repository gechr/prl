package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/x/shell"
	"github.com/stretchr/testify/require"
)

func TestCurrentAIReviewLauncher(t *testing.T) {
	if !isDarwin() {
		t.Setenv("TERM_PROGRAM", "ghostty")
		require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
		return
	}

	t.Setenv("TERM_PROGRAM", "ghostty")
	require.Equal(t, aiReviewLauncherGhostty, currentAIReviewLauncher())

	t.Setenv("TERM_PROGRAM", "iTerm.app")
	require.Equal(t, aiReviewLauncherITerm2, currentAIReviewLauncher())

	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
}

func TestBuildAIReviewAppleScriptGhosttyUsesNewTab(t *testing.T) {
	script, err := buildAIReviewAppleScript(aiReviewLauncherGhostty, "echo 'review'\n")

	require.NoError(t, err)
	require.Contains(t, script, `tell application "Ghostty"`)
	require.Contains(t, script, "set cfg to new surface configuration")
	require.Contains(t, script, `set initial input of cfg to "echo 'review'\n"`)
	require.Contains(t, script, "new tab in front window with configuration cfg")
	require.NotContains(t, script, "split focused terminal")
	require.NotContains(t, script, "display dialog")
}

func TestBuildAIReviewAppleScriptITerm2UsesNewTab(t *testing.T) {
	script, err := buildAIReviewAppleScript(aiReviewLauncherITerm2, "echo review")

	require.NoError(t, err)
	require.Contains(t, script, `tell application "iTerm2"`)
	require.Contains(t, script, `tell current window`)
	require.Contains(t, script, `set newTab to (create tab with default profile)`)
	require.Contains(t, script, `write text " " & "echo review"`)
	require.NotContains(t, script, "split horizontally")
	require.NotContains(t, script, "split vertically")
	require.NotContains(t, script, "display dialog")
}

func TestBuildAIReviewAppleScriptUnsupported(t *testing.T) {
	_, err := buildAIReviewAppleScript(aiReviewLauncherNone, "echo review")

	require.ErrorContains(t, err, "unsupported terminal")
}

func TestPrepareAIReviewConfirmUsesYesNo(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{confirmInput: newConfirmInput()}

	m = m.prepareAIReviewConfirm(pr, 0)

	require.Equal(t, "review", m.confirmAction)
	require.NotNil(t, m.confirmCmdFn)
	require.True(t, m.confirmState.Yes)
	require.True(t, m.confirmHasInput)
	require.Equal(t, "Prompt", m.confirmInputLabel)
	require.Len(t, m.confirmOptions, 3)
	require.Equal(t, reviewProviderOptionLabel, m.confirmOptions[0].label)
	require.Equal(t, reviewModelOptionLabel, m.confirmOptions[1].label)
	require.Equal(t, reviewEffortOptionLabel, m.confirmOptions[2].label)
	require.Equal(t, string(defaultReviewProvider), m.selectedConfirmOptionValue(0))
	require.Equal(
		t,
		defaultReviewModel(nil, defaultReviewProvider),
		m.selectedConfirmOptionValue(1),
	)
	require.Equal(
		t,
		defaultReviewEffort(
			nil,
			defaultReviewProvider,
			defaultReviewModel(nil, defaultReviewProvider),
		),
		m.selectedConfirmOptionValue(2),
	)
	require.Equal(t, tuiAIReviewConfirmInputWid, m.confirmInput.Width())
	require.True(t, m.confirmState.OptFocus)
	require.False(t, m.confirmInput.Focused())
	require.Equal(t, 0, m.confirmState.OptCursor)
	require.Equal(t, reviewPrompt(pr, nil, defaultReviewProvider), m.confirmInput.Value())
}

func TestUpdateConfirmOverlaySwitchesFocusBetweenPromptAndModel(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)

	require.True(t, m.confirmState.OptFocus)
	require.False(t, m.confirmInput.Focused())

	model, cmd := m.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Nil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.confirmState.OptFocus)
	require.False(t, bm.confirmInput.Focused())
	require.Equal(t, 1, bm.confirmState.OptCursor)

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Nil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, string(defaultReviewProvider), bm.selectedConfirmOptionValue(0))
	require.Equal(t, claudeReviewModelSonnet, bm.selectedConfirmOptionValue(1))

	model, cmd = bm.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Nil(t, cmd)

	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.confirmState.OptFocus)
	require.False(t, bm.confirmInput.Focused())
	require.Equal(t, 0, bm.confirmState.OptCursor)
}

func TestBuildAIReviewCommandUsesSelectedModel(t *testing.T) {
	pr := testReviewPullRequest()
	const promptFile = "/tmp/prl-prompt.txt"
	promptExpr := fmt.Sprintf(`"$(cat %s)"`, shell.Quote(promptFile))

	cmd := buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderClaude,
		claudeReviewModelSonnet,
		claudeReviewEffortHigh,
	)
	require.Equal(t, 1, strings.Count(cmd, "--model="+shell.Quote(claudeReviewModelSonnet)))
	require.Equal(t, 0, strings.Count(cmd, "--model="+shell.Quote(claudeReviewModelOpus)))
	require.Equal(t, 1, strings.Count(cmd, "--effort="+shell.Quote(claudeReviewEffortHigh)))
	require.Contains(t, cmd, promptExpr)

	cmd = buildAIReviewCommand(pr, promptFile, nil, reviewProviderClaude, "", "")
	require.Equal(t, 1, strings.Count(cmd, "--model="+shell.Quote(claudeReviewModelSonnet)))
	require.Equal(
		t,
		1,
		strings.Count(
			cmd,
			"--effort="+shell.Quote(claudeReviewEffortMedium),
		),
	)

	cmd = buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderCodex,
		codexReviewModel54Mini,
		codexReviewEffortXHigh,
	)
	require.Contains(
		t,
		cmd,
		fmt.Sprintf(
			"codex -m %s -c model_reasoning_effort=%s %s",
			shell.Quote(codexReviewModel54Mini),
			shell.Quote(codexReviewEffortXHigh),
			promptExpr,
		),
	)

	cmd = buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderGemini,
		geminiReviewModel3Pro,
		"",
	)
	require.Contains(
		t,
		cmd,
		fmt.Sprintf(
			"gemini --model %s --prompt-interactive %s",
			shell.Quote("prl-review"),
			promptExpr,
		),
	)
	require.Contains(t, cmd, `"thinkingLevel":"HIGH"`)
}

func TestBuildAIReviewCommandReadsPromptFromFile(t *testing.T) {
	pr := testReviewPullRequest()
	const promptFile = "/tmp/prl-prompt.txt"

	cmd := buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderCodex,
		codexReviewModel54,
		codexReviewEffortMedium,
	)

	// Prompt is loaded from a file at runtime - the typed shell command
	// must not embed the prompt body (which may contain newlines that
	// terminal initial-input automation would otherwise execute as
	// separate commands).
	require.Contains(t, cmd, fmt.Sprintf(`"$(cat %s)"`, shell.Quote(promptFile)))
	require.Contains(t, cmd, fmt.Sprintf("; rm -f %s", shell.Quote(promptFile)))
}

func TestWriteReviewPromptFilePreservesContent(t *testing.T) {
	prompt := "line one\n\nline two\nwith 'single quotes' and \"doubles\""
	path, err := writeReviewPromptFile(prompt)
	require.NoError(t, err)
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, prompt, string(got))
}

func TestDefaultAIReviewPromptUsesParagraphs(t *testing.T) {
	pr := testReviewPullRequest()

	prompt := reviewPrompt(pr, nil, reviewProviderClaude)
	require.Equal(
		t,
		fmt.Sprintf(
			`Perform a comprehensive code review of PR #%d in %s.

The PR branch is checked out.

First read the PR context with:
gh pr view %[1]d --repo %[2]s

Then get the diff with:
gh api repos/%[2]s/pulls/%[1]d -H 'Accept: application/vnd.github.v3.diff'

Focus on: correctness, edge cases, error handling, performance, readability, and style.

Be thorough but concise.`,
			pr.Number,
			pr.Repository.NameWithOwner,
		),
		prompt,
	)
}

func TestGeminiReviewHasEffortOptions(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, geminiReviewModel31Pro),
	)
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortOff, value: geminiReviewEffortOff},
			{label: geminiReviewEffort1024, value: geminiReviewEffort1024},
			{label: geminiReviewEffort8192, value: geminiReviewEffort8192},
			{label: geminiReviewEffort24576, value: geminiReviewEffort24576},
			{label: geminiReviewEffortDynamic, value: geminiReviewEffortDynamic},
		},
		reviewEffortChoices(nil, reviewProviderGemini, geminiReviewModelFlash),
	)
	require.True(t, reviewProviderHasEffort(nil, reviewProviderGemini, geminiReviewModel31Pro))
	require.True(t, reviewProviderHasEffort(nil, reviewProviderClaude, claudeReviewModelSonnet))
	require.True(t, reviewProviderHasEffort(nil, reviewProviderCodex, codexReviewModel54))
}

func TestGeminiPrepareAIReviewConfirmIncludesEffort(t *testing.T) {
	pr := testReviewPullRequest()
	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Default: TUIReviewDefaultConfig{
					Provider: string(reviewProviderGemini),
					Model:    geminiReviewModel31Pro,
				},
			},
		},
	}
	m := tuiModel{confirmInput: newConfirmInput(), cfg: cfg}

	m = m.prepareAIReviewConfirm(pr, 0)

	require.Len(t, m.confirmOptions, 3)
	require.Equal(t, reviewProviderOptionLabel, m.confirmOptions[0].label)
	require.Equal(t, reviewModelOptionLabel, m.confirmOptions[1].label)
	require.Equal(t, reviewEffortOptionLabel, m.confirmOptions[2].label)
	require.Len(t, m.confirmState.OptValues, 3)
}

func TestReviewPromptUsesConfigTemplate(t *testing.T) {
	pr := testReviewPullRequest()
	pr.Title = "Improve AI review prompts"

	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Providers: TUIReviewProvidersConfig{
					Claude: TUIReviewProviderConfig{
						Prompt: `Review PR {prNumber} in {ownerWithRepo}.
Repo: {repo}
Owner: {owner}
Ref: {prRef}
URL: {prURL}
Title: {title}`,
					},
				},
			},
		},
	}

	require.Equal(
		t,
		`Review PR 42 in owner/repo.
Repo: repo
Owner: owner
Ref: owner/repo#42
URL: https://github.com/owner/repo/pull/42
Title: Improve AI review prompts`,
		reviewPrompt(pr, cfg, reviewProviderClaude),
	)
}

func TestReviewConfigUsesConfiguredChoices(t *testing.T) {
	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Providers: TUIReviewProvidersConfig{
					Codex: TUIReviewProviderConfig{
						Models:  []string{"gpt-5.5", "gpt-5.5-mini"},
						Efforts: []string{"minimal", "deep"},
					},
				},
			},
		},
	}

	require.Equal(
		t,
		[]filterChoice{
			{label: "gpt-5.5", value: "gpt-5.5"},
			{label: "gpt-5.5-mini", value: "gpt-5.5-mini"},
		},
		reviewModelChoices(cfg, reviewProviderCodex),
	)
	require.Equal(t, "gpt-5.5", defaultReviewModel(cfg, reviewProviderCodex))
	require.Equal(
		t,
		[]filterChoice{
			{label: "minimal", value: "minimal"},
			{label: "deep", value: "deep"},
		},
		reviewEffortChoices(cfg, reviewProviderCodex, "gpt-5.5"),
	)
	require.Equal(t, "minimal", defaultReviewEffort(cfg, reviewProviderCodex, "gpt-5.5"))
}

func TestBuildAIReviewCommandUsesConfiguredFallbackChoices(t *testing.T) {
	pr := testReviewPullRequest()
	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Providers: TUIReviewProvidersConfig{
					Codex: TUIReviewProviderConfig{
						Models:  []string{"gpt-5.5"},
						Efforts: []string{"deep"},
					},
				},
			},
		},
	}

	cmd := buildAIReviewCommand(pr, "/tmp/prl-prompt.txt", cfg, reviewProviderCodex, "", "")

	require.Contains(t, cmd, "codex -m gpt-5.5 -c model_reasoning_effort=deep")
}

func TestBuildAIReviewCommandUsesGeminiBudgetFor25Flash(t *testing.T) {
	pr := testReviewPullRequest()

	cmd := buildAIReviewCommand(
		pr,
		"/tmp/prl-prompt.txt",
		nil,
		reviewProviderGemini,
		geminiReviewModelFlash,
		geminiReviewEffort1024,
	)

	require.Contains(t, cmd, `"model":"gemini-2.5-flash"`)
	require.Contains(t, cmd, `"thinkingBudget":1024`)
}

func TestMatchesPatternTreatsPlainStringsAsExact(t *testing.T) {
	require.True(t, matchesPattern("sonnet", "sonnet"))
	require.False(t, matchesPattern("sonnet", "sonnet-4"))
}

func TestMatchesPatternTreatsWildcardsAsGlobs(t *testing.T) {
	require.True(t, matchesPattern("gemini-3*", "gemini-3-pro"))
	require.True(t, matchesPattern("gemini-*", "gemini-2.5-flash"))
	require.False(t, matchesPattern("gemini-3*", "gemini-2.5-flash"))
}

func TestClaudeEffortRulesUseGlobFallback(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
			{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
			{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
			{label: claudeReviewEffortXHigh, value: claudeReviewEffortXHigh},
			{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
			{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
		},
		reviewEffortChoices(nil, reviewProviderClaude, "claude-3.7-sonnet"),
	)
	require.Equal(
		t,
		claudeReviewEffortMedium,
		defaultReviewEffort(nil, reviewProviderClaude, "claude-3.7-sonnet"),
	)
}

func TestCodexEffortRulesUseGlobFallback(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
		},
		reviewEffortChoices(nil, reviewProviderCodex, "gpt-5.5"),
	)
	require.Equal(
		t,
		codexReviewEffortMedium,
		defaultReviewEffort(nil, reviewProviderCodex, "gpt-5.5"),
	)
}

func TestGeminiEffortRulesPreferSpecificGlobBeforeCatchAll(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortOff, value: geminiReviewEffortOff},
			{label: geminiReviewEffort1024, value: geminiReviewEffort1024},
			{label: geminiReviewEffort8192, value: geminiReviewEffort8192},
			{label: geminiReviewEffort24576, value: geminiReviewEffort24576},
			{label: geminiReviewEffortDynamic, value: geminiReviewEffortDynamic},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-2.5-flash-preview"),
	)
	require.Equal(
		t,
		geminiReviewEffortDynamic,
		defaultReviewEffort(nil, reviewProviderGemini, "gemini-2.5-flash-preview"),
	)
}

func TestGeminiEffortRulesUseCatchAllGlob(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-2.0-pro"),
	)
	require.Equal(
		t,
		geminiReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderGemini, "gemini-2.0-pro"),
	)
}

func TestGeminiEffortRulesUseExactMatchForBareGemini(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini"),
	)
	require.Equal(
		t,
		geminiReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderGemini, "gemini"),
	)
	require.True(t, reviewProviderHasEffort(nil, reviewProviderGemini, "gemini"))
}
