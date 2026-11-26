package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/dialog"
	"github.com/stretchr/testify/require"
)

type confirmDoneMsg struct{}

func plainConfirmModel() tuiModel {
	m := approvalTestModel("", testReviewPullRequest())
	m.confirmAction = tuiActionApprove
	m.confirmPrompt = "Approve owner/repo#42?"
	m.confirmSubject = "owner/repo#42"
	m.confirmCmd = func() tea.Msg { return confirmDoneMsg{} }
	m.height = 24
	return m
}

// openPlainConfirm reconciles the stack the way Update does after the keybind
// that set the confirmation up, leaving the dialog open.
func openPlainConfirm(t *testing.T, m tuiModel) tuiModel {
	t.Helper()
	m.syncConfirmDialog()
	require.NotNil(t, m.dialogs)
	require.True(t, m.dialogs.Active())
	return m
}

func TestPlainConfirmDialogYAccepts(t *testing.T) {
	m := plainConfirmModel()
	started := make(chan int, 1)
	m.confirmOnAccept = func(m *tuiModel) { started <- m.actionsInFlight }
	ran := make(chan struct{})
	m.confirmCmd = func() tea.Msg { close(ran); return confirmDoneMsg{} }
	h := newTUIHarness(t, openPlainConfirm(t, m))
	screen := h.Wait(m.confirmPrompt).Key("y").Wait("Approving").AssertNot(m.confirmPrompt).View()
	require.Equal(t, 1, strings.Count(screen, "Approving"))
	require.Len(t, started, 1)
	require.Equal(t, 1, <-started)
	select {
	case <-ran:
	case <-time.After(steep.DefaultTimeout):
		t.Fatal("the confirmation command did not run")
	}
}

func TestPlainConfirmDialogEscDismisses(t *testing.T) {
	m := openPlainConfirm(t, plainConfirmModel())
	h := newTUIHarness(t, m)
	h.Wait(m.confirmPrompt).KeyEsc().WaitNot(m.confirmPrompt).AssertNot("Approving")
}

func TestPlainConfirmDialogEnterOnNoDeclines(t *testing.T) {
	m := openPlainConfirm(t, plainConfirmModel())
	h := newTUIHarness(t, m)
	h.Wait(m.confirmPrompt).KeyLeft().Key("enter").WaitNot(m.confirmPrompt).AssertNot("Approving")
}

func TestPlainConfirmDialogClickButtons(t *testing.T) {
	for _, button := range []string{"Yes", "No"} {
		t.Run(button, func(t *testing.T) {
			m := openPlainConfirm(t, plainConfirmModel())
			h := newTUIHarness(t, m)
			h.Locate(reel.Contains(m.confirmPrompt)).LeftClick()
			h.Locate(reel.Contains(button)).Offset(1, 0).LeftClick().WaitNot(m.confirmPrompt)
			if button == "Yes" {
				h.Wait("Approving")
			} else {
				h.AssertNot("Approving")
			}
		})
	}
}

func TestPlainConfirmDialogRendersPromptAndButtons(t *testing.T) {
	m := openPlainConfirm(t, plainConfirmModel())
	h := newTUIHarness(t, m)
	screen := h.Wait(m.confirmPrompt).View()
	for _, label := range []string{m.confirmPrompt, "No", "Yes"} {
		require.Equal(t, 1, strings.Count(screen, label), label)
	}
}

func TestNerdConfirmDialogsRenderPillCapsWithoutShifting(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconNerd))
	t.Cleanup(func() { useIcons(previous) })
	assertCaps := func(t *testing.T, h *steep.Harness, label string) {
		t.Helper()
		at := h.Locate(
			reel.Contains(activeIcons.PillLeft + " " + label + " " + activeIcons.PillRight),
		)
		line := strings.Split(h.View(), nl)[at.Y]
		require.Equal(t, 1, strings.Count(line, activeIcons.PillLeft))
		require.Equal(t, 1, strings.Count(line, activeIcons.PillRight))
	}
	t.Run("confirmation", func(t *testing.T) {
		h := newTUIHarness(t, openPlainConfirm(t, plainConfirmModel()))
		assertCaps(t, h, "Yes")
		yes, no := h.Locate(reel.Contains("Yes")), h.Locate(reel.Contains("No"))
		h.KeyLeft()
		assertCaps(t, h, "No")
		require.Equal(t, yes.X, h.Locate(reel.Contains("Yes")).X)
		require.Equal(t, no.X, h.Locate(reel.Contains("No")).X)
	})
	t.Run("message", func(t *testing.T) {
		m := plainConfirmModel()
		m.confirmAction, m.confirmCmd = tuiActionInfo, nil
		assertCaps(t, newTUIHarness(t, openPlainConfirm(t, m)), "OK")
	})
}

func TestInfoConfirmDialogQDismisses(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.confirmAction, m.confirmPrompt = tuiActionInfo, "Approve failed"
	m.syncConfirmDialog()
	require.IsType(t, &dialog.Info{}, m.dialogs.Top())
	h := newTUIHarness(t, m)
	screen := h.Wait(m.confirmPrompt).Assert("OK").View()
	require.Equal(t, 1, strings.Count(screen, m.confirmPrompt))
	h.Key("q").WaitNot(m.confirmPrompt).Assert("Options")
}

func TestInputConfirmUsesFormDialog(t *testing.T) {
	m := plainConfirmModel()
	m.confirmHasInput = true
	m.confirmCmd = nil
	m.confirmCmdFn = func(confirmSubmission) tea.Cmd { return nil }
	require.True(t, m.confirmUsesDialog())
	m.syncConfirmDialog()
	_, ok := m.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
}

func TestInputConfirmDialogSeparatesAndAccentsField(t *testing.T) {
	m := plainConfirmModel()
	m.confirmHasInput = true
	m.confirmCmd = nil
	m.confirmCmdFn = func(confirmSubmission) tea.Cmd { return nil }
	h := newTUIHarness(t, openPlainConfirm(t, m))
	prompt := h.Locate(reel.Contains(m.confirmPrompt))
	top := "╭ Comment (optional) " + strings.Repeat("─", 48) + "╮"
	border := h.Locate(reel.Contains(top))
	require.Equal(t, prompt.Y+2, border.Y)
	gap := strings.Split(h.View(), nl)[prompt.Y+1]
	require.Empty(t, strings.TrimSpace(ansi.Cut(gap, border.X, border.X+ansi.StringWidth(top))))
	for _, at := range []steep.Location{border, border.Offset(2, 0)} {
		style := cellStyle(h, at)
		require.NotZero(t, style.Attrs&vt.AttrBold)
		requireScreenColor(t, style.Fg, colorAccent)
	}
}

func TestInputConfirmDialogSubmitsTrimmedText(t *testing.T) {
	m := plainConfirmModel()
	m.confirmHasInput = true
	m.confirmCmd = nil
	submitted := make(chan string, 1)
	m.confirmCmdFn = func(s confirmSubmission) tea.Cmd {
		submitted <- s.Input
		return func() tea.Msg { return confirmDoneMsg{} }
	}
	m.syncConfirmDialog()
	h := newTUIHarness(t, m)
	h.Wait(m.confirmPrompt).Paste("  line one\nline two  ").
		Wait("line two").Key("alt+enter").Wait("Approving").AssertNot(m.confirmPrompt)
	require.Len(t, submitted, 1)
	require.Equal(t, "line one\nline two", <-submitted)
}

func TestInputConfirmDialogRestoresCanceledDraftForSamePR(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	first.Title, second.Title = "First change", "Second change"
	second.URL = "https://example.com/pulls/2"
	m := listTUIModel(first, second)
	m.actions = testActionRunner(t, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/repos/owner/repo/issues/1/comments", r.URL.Path)
		return jsonResponse(r, http.StatusCreated, "{}"), nil
	})
	h := newTUIHarness(t, m)
	firstPrompt, secondPrompt := "Comment on "+first.Ref()+"?", "Comment on "+second.Ref()+"?"
	h.Locate(reel.Contains(first.Title)).
		LeftClick().
		Key("c").
		Wait(firstPrompt).
		Paste("half-written\ncomment body").
		Wait("comment body").
		KeyEsc().
		WaitNot(firstPrompt)
	h.Locate(reel.Contains(second.Title)).
		LeftClick().
		Key("c").
		Wait(secondPrompt).
		AssertNot("half-written").
		KeyEsc().
		WaitNot(secondPrompt)
	h.Locate(reel.Contains(first.Title)).
		LeftClick().
		Key("c").
		Wait(firstPrompt).
		Wait([]string{"half-written", "comment body"})
	h.Key("ctrl+s").Wait("Commented").Key("c").Wait(firstPrompt).AssertNot("comment body")
}

func TestReviewConfirmDialogUpdatesDependentFields(t *testing.T) {
	m := approvalTestModel("", testReviewPullRequest())
	m.width, m.height = 120, 40
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)
	m.syncConfirmDialog()
	h := newTUIHarness(t, m)
	for _, provider := range []reviewProvider{reviewProviderGemini, reviewProviderClaude} {
		model := defaultReviewModel(nil, provider)
		h.KeyRight().
			Wait([]string{string(provider), model, defaultReviewEffort(nil, provider, model)})
	}
}
