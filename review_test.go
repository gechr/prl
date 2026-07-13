package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/stretchr/testify/require"
)

func TestCurrentAIReviewLauncher(t *testing.T) {
	if !xos.IsDarwin() {
		t.Run("non-darwin always returns none", func(t *testing.T) {
			t.Setenv("KITTY_WINDOW_ID", "1")
			t.Setenv("TERM_PROGRAM", "ghostty")
			require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
		})
		return
	}

	t.Run("kitty via KITTY_WINDOW_ID", func(t *testing.T) {
		if _, err := exec.LookPath("kitty"); err != nil {
			t.Skip("kitty not in PATH")
		}
		t.Setenv("KITTY_WINDOW_ID", "1")
		t.Setenv("TERM_PROGRAM", "")
		require.Equal(t, aiReviewLauncherKitty, currentAIReviewLauncher())
	})
	t.Run("kitty takes precedence over TERM_PROGRAM", func(t *testing.T) {
		if _, err := exec.LookPath("kitty"); err != nil {
			t.Skip("kitty not in PATH")
		}
		t.Setenv("KITTY_WINDOW_ID", "2")
		t.Setenv("TERM_PROGRAM", "ghostty")
		require.Equal(t, aiReviewLauncherKitty, currentAIReviewLauncher())
	})

	t.Setenv("KITTY_WINDOW_ID", "") // ensure kitty not detected for remaining cases
	t.Setenv("TERM_PROGRAM", "ghostty")
	require.Equal(t, aiReviewLauncherGhostty, currentAIReviewLauncher())

	t.Setenv("TERM_PROGRAM", "iTerm.app")
	require.Equal(t, aiReviewLauncherITerm2, currentAIReviewLauncher())

	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
}

func TestBuildAIReviewAppleScriptGhosttyUsesNewTab(t *testing.T) {
	script, err := buildAIReviewAppleScript(aiReviewLauncherGhostty)

	require.NoError(t, err)
	require.Equal(t, `on run argv
	set shellCmd to item 1 of argv
	tell application "Ghostty"
	tell application "System Events" to tell process "Ghostty" to set frontmost to true
	set cfg to new surface configuration
	set initial input of cfg to shellCmd
	new tab in front window with configuration cfg
	end tell
end run`, script)
}

func TestBuildAIReviewAppleScriptITerm2UsesNewTab(t *testing.T) {
	script, err := buildAIReviewAppleScript(aiReviewLauncherITerm2)

	require.NoError(t, err)
	require.Equal(t, `on run argv
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
end run`, script)
}

func TestBuildAIReviewAppleScriptUnsupported(t *testing.T) {
	_, err := buildAIReviewAppleScript(aiReviewLauncherNone)

	require.EqualError(t, err, `unsupported terminal ""`)
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

func TestClaudeReviewDefaultsUseOpusHighAndIncludeFable(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: claudeReviewModelSonnet, value: claudeReviewModelSonnet},
			{label: claudeReviewModelOpus, value: claudeReviewModelOpus},
			{label: claudeReviewModelFable, value: claudeReviewModelFable},
		},
		reviewModelChoices(nil, reviewProviderClaude),
	)
	require.Equal(t, claudeReviewModelOpus, defaultReviewModel(nil, reviewProviderClaude))
	require.Equal(
		t,
		claudeReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderClaude, claudeReviewModelOpus),
	)
}

func TestBuildAIReviewCommandUsesSelectedModel(t *testing.T) {
	pr := testReviewPullRequest()
	const promptFile = "/tmp/prl-prompt.txt"
	promptExpr := fmt.Sprintf(`"$(cat %s)"`, shell.Quote(promptFile))
	cleanup := fmt.Sprintf("; rm -f %s", shell.Quote(promptFile))
	baseCmd := expectedAIReviewBaseCommand(pr)

	cmd := buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderClaude,
		claudeReviewModelSonnet,
		claudeReviewEffortHigh,
	)
	require.Equal(
		t,
		baseCmd+"claude --permission-mode plan --model=sonnet "+
			"--effort=high --system-prompt 'You are an expert code reviewer. "+
			"Be thorough, precise, and actionable.' "+promptExpr+cleanup,
		cmd,
	)

	cmd = buildAIReviewCommand(pr, promptFile, nil, reviewProviderClaude, "", "")
	require.Equal(
		t,
		baseCmd+"claude --permission-mode plan --model=opus "+
			"--effort=high --system-prompt 'You are an expert code reviewer. "+
			"Be thorough, precise, and actionable.' "+promptExpr+cleanup,
		cmd,
	)

	cmd = buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderCodex,
		codexReviewModel54Mini,
		codexReviewEffortXHigh,
	)
	require.Equal(
		t,
		baseCmd+"codex --sandbox read-only -m gpt-5.4-mini "+
			"-c model_reasoning_effort=xhigh "+promptExpr+cleanup,
		cmd,
	)

	cmd = buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderGemini,
		geminiReviewModel31Pro,
		"",
	)
	require.Equal(
		t,
		baseCmd+"/bin/rm -rf "+shell.Quote(aiReviewDir(pr, promptFile))+"/.gemini "+
			"&& /bin/mkdir -p "+shell.Quote(aiReviewDir(pr, promptFile))+"/.gemini "+
			`&& printf '%s' '{"modelConfigs":{"customAliases":{"prl-review":{"modelConfig":{"generateContentConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}},"model":"gemini-3.1-pro"}}}}}' > `+
			shell.Quote(aiReviewDir(pr, promptFile))+"/.gemini/settings.json "+
			"&& gemini --sandbox --approval-mode plan --model prl-review "+
			"--prompt-interactive "+promptExpr+cleanup,
		cmd,
	)
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

	require.Equal(
		t,
		expectedAIReviewBaseCommand(pr)+
			`codex --sandbox read-only -m gpt-5.4 -c model_reasoning_effort=medium "$(cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`,
		cmd,
	)
}

func TestBuildAIReviewCommandUsesRevisionSpecificReviewDirectory(t *testing.T) {
	pr := testReviewPullRequest()
	pr.HeadSHA = "abc123"

	cacheHome, err := shell.CacheDir()
	require.NoError(t, err)
	require.Equal(
		t,
		filepath.Join(cacheHome, "prl", "reviews", "owner", "repo", "42", "abc123"),
		aiReviewDir(pr, "/tmp/prl-review-prompt.txt"),
	)
}

func TestSafeReviewPathComponentRejectsTraversal(t *testing.T) {
	require.Equal(t, "owner", safeReviewPathComponent("owner", "fallback"))
	require.Equal(t, "fallback", safeReviewPathComponent("..", "fallback"))
	require.Equal(t, "fallback", safeReviewPathComponent("../../tmp", "fallback"))
	require.Equal(t, "fallback", safeReviewPathComponent("owner/repo", "fallback"))
}

func expectedAIReviewBaseCommand(pr PullRequest) string {
	const promptFile = "/tmp/prl-prompt.txt"
	reviewDir := aiReviewDir(pr, promptFile)
	headGuard := ""
	if pr.HeadSHA != "" {
		headGuard = fmt.Sprintf(
			`test "$(git rev-parse HEAD)" = %s && `,
			shell.Quote(pr.HeadSHA),
		)
	}
	return fmt.Sprintf(
		"/usr/bin/trash %s 2>/dev/null; /bin/mkdir -p %s && cd %s && git clone --quiet --depth 1 %s . && git fetch origin refs/pull/%d/head:pr-%d --no-tags && git checkout pr-%d && %s",
		shell.Quote(reviewDir),
		shell.Quote(reviewDir),
		shell.Quote(reviewDir),
		shell.Quote("git@github.com:"+pr.Repository.NameWithOwner),
		pr.Number,
		pr.Number,
		pr.Number,
		headGuard,
	)
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

func TestWriteReviewLaunchFileQuarantinesShellCommand(t *testing.T) {
	const promptFile = "/tmp/prl-review-prompt.txt"
	const shellCmd = "printf '%s\\n' 'line one' 'line two'"

	path, err := writeReviewLaunchFile(shellCmd, promptFile)
	require.NoError(t, err)
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	cleanup := "/bin/rm -f " + shell.Quote(promptFile) + " " + shell.Quote(path)
	require.Equal(
		t,
		"#!/bin/sh\ntrap "+shell.Quote(cleanup)+" EXIT HUP INT TERM\n"+shellCmd+"\n",
		string(got),
	)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	err = exec.Command("/bin/sh", "-n", path).Run()
	require.NoError(t, err)
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

	require.Equal(
		t,
		expectedAIReviewBaseCommand(pr)+
			`codex --sandbox read-only -m gpt-5.5 -c model_reasoning_effort=deep "$(cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`,
		cmd,
	)
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

	reviewDir := aiReviewDir(pr, "/tmp/prl-prompt.txt")
	require.Equal(
		t,
		expectedAIReviewBaseCommand(pr)+
			"/bin/rm -rf "+shell.Quote(reviewDir)+"/.gemini "+
			"&& /bin/mkdir -p "+shell.Quote(reviewDir)+"/.gemini "+
			`&& printf '%s' '{"modelConfigs":{"customAliases":{"prl-review":{"modelConfig":{"generateContentConfig":{"thinkingConfig":{"thinkingBudget":1024}},"model":"gemini-2.5-flash"}}}}}' > `+
			shell.Quote(reviewDir)+"/.gemini/settings.json "+
			`&& gemini --sandbox --approval-mode plan --model prl-review --prompt-interactive "$(cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`,
		cmd,
	)
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
		claudeReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderClaude, "claude-3.7-sonnet"),
	)
}

func TestClaudeSonnetAndFableDefaultToMediumEffort(t *testing.T) {
	require.Equal(
		t,
		claudeReviewEffortMedium,
		defaultReviewEffort(nil, reviewProviderClaude, claudeReviewModelSonnet),
	)
	require.Equal(
		t,
		claudeReviewEffortMedium,
		defaultReviewEffort(nil, reviewProviderClaude, claudeReviewModelFable),
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
		codexReviewEffortXHigh,
		defaultReviewEffort(nil, reviewProviderCodex, "gpt-5.5"),
	)
}

func TestCodex56ModelsIncludeMaxEffort(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewModel56, value: codexReviewModel56},
			{label: codexReviewModel56Terra, value: codexReviewModel56Terra},
			{label: codexReviewModel56Luna, value: codexReviewModel56Luna},
			{label: codexReviewModel55, value: codexReviewModel55},
			{label: codexReviewModel54, value: codexReviewModel54},
			{label: codexReviewModel54Mini, value: codexReviewModel54Mini},
			{label: codexReviewModel53Codex, value: codexReviewModel53Codex},
		},
		reviewModelChoices(nil, reviewProviderCodex),
	)
	require.Equal(t, codexReviewModel56, defaultReviewModel(nil, reviewProviderCodex))
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
			{label: codexReviewEffortMax, value: codexReviewEffortMax},
		},
		reviewEffortChoices(nil, reviewProviderCodex, codexReviewModel56Terra),
	)
	require.Equal(
		t,
		codexReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderCodex, codexReviewModel56),
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

func TestGemini3EffortRulesDistinguishProAndFlash(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-3.1-pro-preview"),
	)
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortMinimal, value: geminiReviewEffortMinimal},
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-3.5-flash"),
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
