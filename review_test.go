package main

import (
	"fmt"
	"strings"
	"testing"

	"al.essio.dev/pkg/shellescape"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestCurrentAIReviewLauncher(t *testing.T) {
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
	require.True(t, m.confirmYes)
	require.True(t, m.confirmHasInput)
	require.Equal(t, "Prompt", m.confirmInputLabel)
	require.Len(t, m.confirmOptions, 3)
	require.Equal(t, reviewProviderOptionLabel, m.confirmOptions[0].label)
	require.Equal(t, reviewModelOptionLabel, m.confirmOptions[1].label)
	require.Equal(t, reviewEffortOptionLabel, m.confirmOptions[2].label)
	require.Equal(t, string(defaultReviewProvider), m.selectedConfirmOptionValue(0))
	require.Equal(t, defaultReviewModel(defaultReviewProvider), m.selectedConfirmOptionValue(1))
	require.Equal(
		t,
		defaultReviewEffort(defaultReviewProvider, defaultReviewModel(defaultReviewProvider)),
		m.selectedConfirmOptionValue(2),
	)
	require.Equal(t, tuiAIReviewConfirmInputWid, m.confirmInput.Width())
	require.True(t, m.confirmOptFocus)
	require.False(t, m.confirmInput.Focused())
	require.Equal(t, 0, m.confirmOptCursor)
	require.Equal(t, reviewPrompt(pr, nil, defaultReviewProvider), m.confirmInput.Value())
}

func TestUpdateConfirmOverlaySwitchesFocusBetweenPromptAndModel(t *testing.T) {
	m := tuiModel{
		confirmInput: newConfirmInput(),
		styles:       newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)

	require.True(t, m.confirmOptFocus)
	require.False(t, m.confirmInput.Focused())

	model, cmd := m.updateConfirmOverlay(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Nil(t, cmd)

	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.confirmOptFocus)
	require.False(t, bm.confirmInput.Focused())
	require.Equal(t, 1, bm.confirmOptCursor)

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
	require.True(t, bm.confirmOptFocus)
	require.False(t, bm.confirmInput.Focused())
	require.Equal(t, 0, bm.confirmOptCursor)
}

func TestBuildAIReviewCommandUsesSelectedModel(t *testing.T) {
	pr := testReviewPullRequest()

	cmd := buildAIReviewCommand(
		pr,
		"review prompt",
		reviewProviderClaude,
		claudeReviewModelSonnet,
		claudeReviewEffortHigh,
	)
	require.Equal(t, 1, strings.Count(cmd, "--model="+shellescape.Quote(claudeReviewModelSonnet)))
	require.Equal(t, 0, strings.Count(cmd, "--model="+shellescape.Quote(claudeReviewModelOpus)))
	require.Equal(t, 1, strings.Count(cmd, "--effort="+shellescape.Quote(claudeReviewEffortHigh)))

	cmd = buildAIReviewCommand(pr, "review prompt", reviewProviderClaude, "", "")
	require.Equal(t, 1, strings.Count(cmd, "--model="+shellescape.Quote(claudeReviewModelOpus)))
	require.Equal(
		t,
		1,
		strings.Count(
			cmd,
			"--effort="+shellescape.Quote(claudeReviewEffortMedium),
		),
	)

	cmd = buildAIReviewCommand(
		pr,
		"review prompt",
		reviewProviderCodex,
		codexReviewModel54Mini,
		codexReviewEffortXHigh,
	)
	require.Contains(
		t,
		cmd,
		fmt.Sprintf(
			"codex -m %s -c model_reasoning_effort=%s %s",
			shellescape.Quote(codexReviewModel54Mini),
			shellescape.Quote(codexReviewEffortXHigh),
			shellescape.Quote("review prompt"),
		),
	)
}

func TestBuildAIReviewCommandPreservesPromptNewlines(t *testing.T) {
	pr := testReviewPullRequest()
	prompt := `line one

line two`

	cmd := buildAIReviewCommand(
		pr,
		prompt,
		reviewProviderCodex,
		codexReviewModel54,
		codexReviewEffortMedium,
	)

	require.Contains(t, cmd, `'line one

line two'`)
	require.NotContains(t, cmd, `line one\n\nline two`)
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
