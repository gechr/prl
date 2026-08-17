package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	cansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/x/ansi"
	"github.com/stretchr/testify/require"
)

type confirmDoneMsg struct{}

func plainConfirmModel() tuiModel {
	m := tuiModel{
		confirmAction:  tuiActionApprove,
		confirmPrompt:  "Approve owner/repo#42?",
		confirmSubject: "owner/repo#42",
		confirmCmd:     func() tea.Msg { return confirmDoneMsg{} },
		styles:         newTuiStyles(),
		width:          80,
		height:         24,
	}
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

// buttonAt locates label in the rendered screen and returns a screen cell
// inside it, so click tests hit the button wherever the modal centered.
func buttonAt(t *testing.T, content, label string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(content), "\n") {
		if before, _, ok := strings.Cut(line, label); ok {
			return cansi.StringWidth(before), y
		}
	}
	t.Fatalf("button %q not rendered", label)
	return 0, 0
}

func TestPlainConfirmDialogYAccepts(t *testing.T) {
	bm := openPlainConfirm(t, plainConfirmModel())

	model, cmd := bm.Update(tea.KeyPressMsg{Text: "y", Code: 'y'})
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.NotNil(t, cmd)
	require.Equal(t, confirmDoneMsg{}, cmd())
	require.Empty(t, bm.confirmAction)
	require.False(t, bm.dialogs.Active())
	require.Equal(t, 1, strings.Count(ansi.Strip(bm.flash.Msg), "Approving"))
}

func TestPlainConfirmDialogEscDismisses(t *testing.T) {
	bm := openPlainConfirm(t, plainConfirmModel())

	model, cmd := bm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Nil(t, cmd)
	require.Empty(t, bm.confirmAction)
	require.False(t, bm.dialogs.Active())
	require.Empty(t, bm.flash.Msg)
}

func TestPlainConfirmDialogEnterOnNoDeclines(t *testing.T) {
	bm := openPlainConfirm(t, plainConfirmModel())

	model, _ := bm.Update(tea.KeyPressMsg{Code: tea.KeyLeft}) // focus No
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	model, cmd := bm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Nil(t, cmd)
	require.Empty(t, bm.confirmAction)
}

func TestPlainConfirmDialogClickButtons(t *testing.T) {
	bm := openPlainConfirm(t, plainConfirmModel())
	x, y := buttonAt(t, bm.View().Content, "Yes")
	model, cmd := bm.Update(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.NotNil(t, cmd)
	require.Equal(t, confirmDoneMsg{}, cmd())
	require.Empty(t, bm.confirmAction)

	bm = openPlainConfirm(t, plainConfirmModel())
	x, y = buttonAt(t, bm.View().Content, "No")
	model, cmd = bm.Update(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Nil(t, cmd)
	require.Empty(t, bm.confirmAction)

	// A click on the prompt row presses nothing: the confirm stays open.
	bm = openPlainConfirm(t, plainConfirmModel())
	x, y = buttonAt(t, bm.View().Content, "Approve owner/repo#42?")
	model, _ = bm.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiActionApprove, bm.confirmAction)
	require.True(t, bm.dialogs.Active())
}

func TestPlainConfirmDialogRendersPromptAndButtons(t *testing.T) {
	bm := openPlainConfirm(t, plainConfirmModel())
	content := ansi.Strip(bm.View().Content)
	require.Equal(t, 1, strings.Count(content, "Approve owner/repo#42?"))
	require.Equal(t, 1, strings.Count(content, "No"))
	require.Equal(t, 1, strings.Count(content, "Yes"))
}

func TestNerdConfirmDialogsRenderPillCapsWithoutShifting(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconNerd))
	t.Cleanup(func() { useIcons(previous) })

	bm := openPlainConfirm(t, plainConfirmModel())
	activeRow := strings.Split(bm.dialogs.Top().Content(bm.width), nl)[2]
	require.Equal(t, 2, strings.Count(activeRow, activeIcons.PillLeft))
	require.Equal(t, 2, strings.Count(activeRow, activeIcons.PillRight))

	model, cmd := bm.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	dimRow := strings.Split(bm.dialogs.Top().Content(bm.width), nl)[2]
	require.Equal(t, lg.Width(activeRow), lg.Width(dimRow))

	info := tuiModel{
		confirmAction: tuiActionInfo,
		confirmPrompt: "Approve failed",
		styles:        newTuiStyles(),
		width:         80,
		height:        24,
	}
	info.syncConfirmDialog()
	infoRow := strings.Split(info.dialogs.Top().Content(info.width), nl)[2]
	require.Equal(t, 1, strings.Count(infoRow, activeIcons.PillLeft))
	require.Equal(t, 1, strings.Count(infoRow, activeIcons.PillRight))
}

func TestInfoConfirmUsesDialog(t *testing.T) {
	m := tuiModel{
		confirmAction: tuiActionInfo,
		confirmPrompt: "Approve failed",
		styles:        newTuiStyles(),
		width:         80,
		height:        24,
	}
	require.True(t, m.confirmUsesDialog())

	m.syncConfirmDialog()
	require.True(t, m.dialogs.Active())
	_, ok := m.dialogs.Top().(*dialog.Info)
	require.True(t, ok)
	require.Equal(t, 1, strings.Count(ansi.Strip(m.View().Content), "Approve failed"))
}

func TestInfoConfirmDialogQDismisses(t *testing.T) {
	m := tuiModel{
		confirmAction: tuiActionInfo,
		confirmPrompt: "Approve failed",
		styles:        newTuiStyles(),
		width:         80,
		height:        24,
	}
	m.syncConfirmDialog()

	model, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Nil(t, cmd)
	require.Empty(t, bm.confirmAction)
	require.False(t, bm.dialogs.Active())
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

func TestInputConfirmDialogSeparatesAndDimsField(t *testing.T) {
	m := plainConfirmModel()
	m.confirmHasInput = true
	m.confirmCmd = nil
	m.confirmCmdFn = func(confirmSubmission) tea.Cmd { return nil }
	m.syncConfirmDialog()

	d, ok := m.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	body := d.Model().Body()
	lines := strings.Split(ansi.Strip(body), nl)
	require.Equal(t, "Approve owner/repo#42?", strings.TrimSpace(lines[0]))
	require.Empty(t, strings.TrimSpace(lines[1]))
	require.Equal(
		t,
		"╭ Comment (optional) "+strings.Repeat("─", 48)+"╮",
		lines[2],
	)
	dimAccent := lg.NewStyle().Foreground(colorAccent).Faint(true)
	require.Equal(t, 1, strings.Count(body, dimAccent.Render(" Comment (optional) ")))
	require.Equal(t, 1, strings.Count(body, dimAccent.Render("╭")))
}

func TestInputConfirmDialogSubmitsTrimmedText(t *testing.T) {
	m := plainConfirmModel()
	m.confirmHasInput = true
	m.confirmCmd = nil
	var submission confirmSubmission
	m.confirmCmdFn = func(got confirmSubmission) tea.Cmd {
		submission = got
		return func() tea.Msg { return confirmDoneMsg{} }
	}
	m.syncConfirmDialog()
	d, ok := m.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	d.Model().SetValue(0, "  line one\nline two  ")

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.NotNil(t, cmd)
	require.Equal(t, "line one\nline two", submission.Input)
	require.Empty(t, bm.confirmAction)
	require.False(t, bm.dialogs.Active())
}

func TestInputConfirmDialogRestoresCanceledDraftForSamePR(t *testing.T) {
	open := func(m tuiModel, url string) tuiModel {
		m.confirmAction = tuiActionClose
		m.confirmPrompt = "Close owner/repo#42?"
		m.confirmSubject = "owner/repo#42"
		m.confirmURL = url
		m.confirmHasInput = true
		m.confirmCmdFn = func(confirmSubmission) tea.Cmd { return nil }
		m.syncConfirmDialog()
		return m
	}

	m := open(plainConfirmModel(), "https://example.com/owner/repo/pull/42")
	d, ok := m.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	d.Model().SetValue(0, "half-written\ncomment")
	model, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m, ok = model.(tuiModel)
	require.True(t, ok)

	other := open(m, "https://example.com/owner/repo/pull/43")
	d, ok = other.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	require.Empty(t, d.Model().Value(0))
	model, _ = other.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m, ok = model.(tuiModel)
	require.True(t, ok)

	m = open(m, "https://example.com/owner/repo/pull/42")
	d, ok = m.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	require.Equal(t, "half-written\ncomment", d.Model().Value(0))

	model, _ = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m, ok = model.(tuiModel)
	require.True(t, ok)
	_, saved := m.confirmDrafts[confirmDraftKey{
		action: tuiActionClose,
		url:    "https://example.com/owner/repo/pull/42",
	}]
	require.False(t, saved)
}

func TestReviewConfirmDialogUpdatesDependentFields(t *testing.T) {
	m := tuiModel{
		styles: newTuiStyles(),
	}
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)
	m.syncConfirmDialog()

	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	d, ok := bm.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	require.Equal(t, string(reviewProviderCodex), d.Model().Value(0))
	require.Equal(t, defaultReviewModel(nil, reviewProviderCodex), d.Model().Value(1))
	require.Equal(
		t,
		defaultReviewEffort(nil, reviewProviderCodex, defaultReviewModel(nil, reviewProviderCodex)),
		d.Model().Value(reviewEffortOptionRow),
	)

	model, cmd = bm.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	d, ok = bm.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	require.Equal(t, string(reviewProviderGemini), d.Model().Value(0))
	require.Equal(t, defaultReviewModel(nil, reviewProviderGemini), d.Model().Value(1))
	require.Equal(
		t,
		defaultReviewEffort(
			nil,
			reviewProviderGemini,
			defaultReviewModel(nil, reviewProviderGemini),
		),
		d.Model().Value(reviewEffortOptionRow),
	)
}
