package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestCurrentClaudeReviewLauncher(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	require.Equal(t, claudeLauncherGhostty, currentClaudeReviewLauncher())

	t.Setenv("TERM_PROGRAM", "iTerm.app")
	require.Equal(t, claudeLauncherITerm2, currentClaudeReviewLauncher())

	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	require.Equal(t, claudeLauncherNone, currentClaudeReviewLauncher())
}

func TestBuildClaudeReviewAppleScriptGhosttyUsesNewTab(t *testing.T) {
	script, err := buildClaudeReviewAppleScript(claudeLauncherGhostty, "echo 'review'\n")

	require.NoError(t, err)
	require.Contains(t, script, `tell application "Ghostty"`)
	require.Contains(t, script, "set cfg to new surface configuration")
	require.Contains(t, script, `set initial input of cfg to "echo 'review'\n"`)
	require.Contains(t, script, "new tab in front window with configuration cfg")
	require.NotContains(t, script, "split focused terminal")
	require.NotContains(t, script, "display dialog")
}

func TestBuildClaudeReviewAppleScriptITerm2UsesNewTab(t *testing.T) {
	script, err := buildClaudeReviewAppleScript(claudeLauncherITerm2, "echo review")

	require.NoError(t, err)
	require.Contains(t, script, `tell application "iTerm2"`)
	require.Contains(t, script, `tell current window`)
	require.Contains(t, script, `set newTab to (create tab with default profile)`)
	require.Contains(t, script, `write text " " & "echo review"`)
	require.NotContains(t, script, "split horizontally")
	require.NotContains(t, script, "split vertically")
	require.NotContains(t, script, "display dialog")
}

func TestBuildClaudeReviewAppleScriptUnsupported(t *testing.T) {
	_, err := buildClaudeReviewAppleScript(claudeLauncherNone, "echo review")

	require.ErrorContains(t, err, "unsupported terminal")
}

func TestPrepareClaudeReviewConfirmUsesYesNo(t *testing.T) {
	var m browseModel
	pr := testReviewPullRequest()

	m = m.prepareClaudeReviewConfirm(pr, 0)

	require.Equal(t, "review", m.confirmAction)
	require.NotNil(t, m.confirmCmd)
	require.True(t, m.confirmYes)
	require.Contains(t, m.confirmPrompt, "This will clone the repo and open a new terminal tab.")
}

func TestUpdateListViewAltRBypassesConfirm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := browseModel{
		prs:      []PullRequest{testReviewPullRequest()},
		removed:  make(map[int]bool),
		selected: make(map[int]bool),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Nil(t, cmd)
	bm, ok := model.(browseModel)
	require.True(t, ok)
	require.Equal(t, "review", bm.confirmAction)

	model, cmd = m.updateListView(tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	require.NotNil(t, cmd)
	altModel, ok := model.(browseModel)
	require.True(t, ok)

	require.Empty(t, altModel.confirmAction)
	require.Empty(t, altModel.confirmPrompt)
	require.Nil(t, altModel.confirmCmd)
}

func TestRenderHelpOverlayIncludesAltRReviewShortcut(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := browseModel{styles: newBrowseStyles()}

	overlay := m.renderHelpOverlay()

	require.Contains(t, overlay, "Launch Claude review")
	require.Contains(t, overlay, "alt+r")
	require.Contains(t, overlay, "Launch Claude review (no confirm)")
}

func testReviewPullRequest() PullRequest {
	return PullRequest{
		Number: 42,
		Repository: Repository{
			Name:          "prl",
			NameWithOwner: "gechr/prl",
		},
		URL: "https://github.com/gechr/prl/pull/42",
	}
}
