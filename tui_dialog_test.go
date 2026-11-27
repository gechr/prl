package main

import (
	"fmt"
	"image/color"
	"net/http"
	"strings"
	"testing"
	"time"

	"bloomfx.dev/bloom/canvas"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/charmbracelet/x/ansi"
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

// openPlainConfirm opens the pending confirmation as Update would.
func openPlainConfirm(t *testing.T, m tuiModel) tuiModel {
	t.Helper()
	m.syncConfirmDialog()
	require.NotNil(t, m.screen.dialog)
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

func TestReopenedConfirmationIgnoresPreviousButtonRelease(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.width, m.height = 100, 25
	m, _ = pressKey(t, m, tuiKeybindUpdateBranch)
	m.View()
	rect, ok := m.screen.root.Frame().Rect(confirmYesID)
	require.True(t, ok)
	mouse := tea.Mouse{X: rect.Min.X, Y: rect.Min.Y, Button: tea.MouseLeft}
	m, _ = updateModel(t, m, tea.MouseClickMsg(mouse))
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Empty(t, m.confirmAction)
	m.View()
	m, _ = pressKey(t, m, tuiKeybindUpdateBranch)
	m.View()
	m, _ = updateModel(t, m, tea.MouseReleaseMsg(mouse))
	require.Equal(t, tuiActionUpdateBranch, m.confirmAction)
	require.Zero(t, m.actionsInFlight)
}

func TestConfirmDragMovesButtonsAndReopeningCentersDialog(t *testing.T) {
	for _, view := range []tuiView{tuiViewList, tuiViewDetail, tuiViewDiff} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			m := actionViewTestModel(view, approvalTestPR(1))
			h := newTUIHarness(t, m)
			const prompt = "Update branch for owner/repo#1?"
			h.Key(tuiKeybindUpdateBranch).Wait(prompt)
			label := h.Locate(reel.Contains(prompt))
			border := label.Offset(0, -2)
			no := h.Locate(reel.Contains(" No "))
			h.LeftDrag(border.X, border.Y, border.X+9, border.Y+3)
			h.WaitFor(reel.ScreenFunc(func(screen string) bool {
				line := strings.Split(screen, nl)[label.Y+3]
				return ansi.Cut(line, label.X+9, label.X+9+len(prompt)) == prompt
			}))
			no.LeftClick().Assert(prompt)
			h.Locate(reel.Contains(" No ")).
				LeftClick().
				WaitNot(prompt).
				Key(tuiKeybindUpdateBranch).
				Wait(prompt)
			at := h.Locate(reel.Contains(prompt))
			require.Equal(t, label.X, at.X)
			require.Equal(t, label.Y, at.Y)
		})
	}
}

func TestConfirmFitsShortTerminalAndGuardsHiddenActions(t *testing.T) {
	m := openPlainConfirm(t, plainConfirmModel())
	m.width, m.height = 90, 6
	buttonVisible := func() bool {
		m.View()
		_, ok := m.screen.root.Frame().Rect(confirmYesID)
		return ok
	}
	require.True(t, buttonVisible())
	for _, size := range []tea.WindowSizeMsg{{Width: 90, Height: 3}, {Width: 18, Height: 24}} {
		m, _ = updateModel(t, m, size)
		m, _ = pressKey(t, m, "y")
		require.False(t, buttonVisible())
		m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, tuiActionApprove, m.confirmAction)
		require.Zero(t, m.actionsInFlight)
	}
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 90, Height: 4})
	require.True(t, buttonVisible())
	m, _ = pressKey(t, m, "y")
	require.Empty(t, m.confirmAction)
	require.Equal(t, 1, m.actionsInFlight)
}

func TestConfirmDropStaysPutWhenTerminalGrows(t *testing.T) {
	m := openPlainConfirm(t, plainConfirmModel())
	m.width, m.height = 100, 24
	m.View()
	rect, ok := m.screen.root.Frame().Rect(confirmHandleID)
	require.True(t, ok)
	down := tea.MouseClickMsg{X: rect.Min.X + 1, Y: rect.Min.Y, Button: tea.MouseLeft}
	up := tea.MouseReleaseMsg{X: m.width - 1, Y: m.height - 1, Button: tea.MouseLeft}
	m, _ = updateModel(t, m, down)
	m, _ = updateModel(t, m, up)
	m.View()
	dropped, ok := m.screen.root.Frame().Rect(confirmPanelID)
	require.True(t, ok)
	require.Equal(t, m.width, dropped.Max.X)
	require.Equal(t, m.height, dropped.Max.Y)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 160, Height: 70})
	m.View()
	widened, ok := m.screen.root.Frame().Rect(confirmPanelID)
	require.True(t, ok)
	require.Equal(t, dropped, widened)
}

func TestMovedFormFitsAfterResizeAndKeepsDraft(t *testing.T) {
	m := plainConfirmModel()
	m.confirmHasInput = true
	h := newTUIHarness(t, openPlainConfirm(t, m))
	h.Wait(m.confirmPrompt).Paste("first line").Wait("first line")
	label := h.Locate(reel.Contains(m.confirmPrompt))
	h.LeftDrag(label.X, label.Y-2, 0, 0)
	h.WaitFor(reel.ScreenFunc(func(screen string) bool { return strings.HasPrefix(screen, "╭─") }))
	h.Resize(90, 18).Wait("first line").Paste(" plus edit").Wait("first line plus edit")
}

func TestConfirmButtonHover(t *testing.T) {
	for _, light := range []bool{false, true} {
		for _, mode := range []IconMode{IconUnicode, IconNerd} {
			t.Run(fmt.Sprintf("light=%t/%s", light, mode), func(t *testing.T) {
				oldLight, oldIcons := paletteIsLight, activeIcons
				initPalette(light)
				useIcons(iconsFor(mode))
				t.Cleanup(func() {
					initPalette(oldLight)
					useIcons(oldIcons)
				})

				h := newTUIHarness(t, openPlainConfirm(t, plainConfirmModel()))
				yes := h.Locate(reel.Contains(" Yes ")).Offset(1, 0)
				no := h.Locate(reel.Contains(" No ")).Offset(1, 0)
				for _, at := range []steep.Location{yes, no} {
					base := cellStyle(h, at)
					at.Hover()
					waitCellStyle(t, h, at, func(s reel.CellStyle) bool { return s.Bg != base.Bg })
					requireScreenColor(t, cellStyle(h, at).Bg, colorSelectionBG)
					requireScreenColor(t, cellStyle(h, at).Fg, colorText)
					requireScreenColor(t, cellStyle(h, at.Offset(-1, 0)).Bg, colorSelectionBG)
					if mode == IconNerd {
						requireScreenColor(t, cellStyle(h, at.Offset(-2, 0)).Fg, colorSelectionBG)
					}
					at.Offset(0, -1).Hover()
					waitCellStyle(t, h, at, func(s reel.CellStyle) bool { return s == base })
				}
				no.Hover()
				h.Key("enter").Wait("Approving")
			})
		}
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

func TestInfoInterruptsFormAndPreservesDraft(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m, _ = pressKey(t, m, tuiKeybindComment)
	m, _ = updateModel(t, m, tea.PasteMsg{Content: "draft comment"})
	m, _ = updateModel(t, m, batchActionMsg{count: 1, failed: 1})
	require.Equal(t, tuiActionInfo, m.confirmAction)
	require.Nil(t, m.screen.dialog.form)
	require.True(t, m.screen.focus.Is(confirmOKID))
	m, _ = pressKey(t, m, "y")
	require.Zero(t, m.actionsInFlight)
	m, _ = pressKey(t, m, tuiKeybindComment)
	require.Equal(t, "draft comment", m.screen.dialog.form.Value(0))
}

func TestInfoConfirmDialogQDismisses(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.confirmAction, m.confirmPrompt = tuiActionInfo, "Approve failed"
	m.syncConfirmDialog()
	require.True(t, m.screen.focus.Is(confirmOKID))
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
	require.NotNil(t, m.screen.dialog.form)
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

func TestModalBackdropTracksTerminalColors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		light bool
		bg    color.Color
	}{
		{"dark", false, lg.Color("#222430")},
		{"light", true, lg.Color("#f1efe9")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := paletteIsLight
			initPalette(tc.light)
			t.Cleanup(func() { initPalette(previous) })

			m := numberedListTestModel(3)
			m.width, m.height = 140, 52
			h := newTUIHarness(t, m)
			cell := h.Locate(reel.Contains("❯ 1")).Offset(2, 0)
			original := cellStyle(h, cell)
			require.Nil(t, original.Fg.Color)
			fg := lg.LightDark(!tc.light)(lg.Color("#242f39"), lg.Color("#bfcde1"))
			h.SendProgram(tea.ForegroundColorMsg{Color: fg})
			h.SendProgram(tea.BackgroundColorMsg{Color: tc.bg})
			var mixer canvas.Mixer
			for _, modal := range []struct{ key, label string }{
				{"?", "Press any key to dismiss"},
				{"O", "enter apply"},
				{"c", "Comment (optional)"},
			} {
				h.Key(modal.key).Wait(modal.label)
				requireScreenColor(t, cellStyle(h, cell).Fg, mixer.Mix(fg, tc.bg, 0.5))
				border := h.Locate(reel.Contains("╭"))
				requireScreenColor(t, cellStyle(h, border).Fg, colorAccent)
				requireScreenColor(t, cellStyle(h, border).Bg, tc.bg)
				cell.Wheel(vt.MouseWheelDown)
				if modal.key == "?" {
					h.LeftClick(cell.X, cell.Y+1)
				}
				h.KeyEsc().WaitNot(modal.label).Assert("❯ 1")
				require.Equal(t, original, cellStyle(h, cell))
			}
		})
	}
}

func TestFormResizePreservesDraft(t *testing.T) {
	m := plainConfirmModel()
	m.confirmHasInput = true
	h := newTUIHarness(t, openPlainConfirm(t, m))
	h.Wait(m.confirmPrompt).Paste("draft in progress").Wait("draft in progress").
		Resize(32, 10).Wait("terminal too narrow").
		Resize(100, 24).Wait("draft in progress")
}
