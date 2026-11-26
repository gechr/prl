package main

import (
	"errors"
	"fmt"
	"image/color"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	cansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/filter"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/scrollwheel"
	"github.com/gechr/primer/table"
	"github.com/gechr/x/ansi"
	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/stretchr/testify/require"
)

func TestShellSingleQuoteEscapesSingleQuotes(t *testing.T) {
	require.Equal(t, `'it'"'"'s fine'`, shell.Quote("it's fine"))
}

func TestUpdateListViewAltRBypassesConfirm(t *testing.T) {
	if !xos.IsDarwin() {
		t.Skip("AI review requires macOS")
	}

	t.Setenv(herdrEnvVar, "")
	t.Setenv("TERM_PROGRAM", "ghostty")
	pr := testReviewPullRequest()
	m := tuiModel{
		items:    []PRRowModel{{PR: pr}},
		rows:     []TableRow{{Item: PRRowModel{PR: pr}}},
		selected: make(prKeys),
	}

	model, cmd := m.updateListView(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, "review", bm.confirmAction)

	model, cmd = m.updateListView(tea.KeyPressMsg{Code: 'r', Mod: tea.ModAlt})
	require.NotNil(t, cmd)
	altModel, ok := model.(tuiModel)
	require.True(t, ok)

	require.Empty(t, altModel.confirmAction)
	require.Empty(t, altModel.confirmPrompt)
	require.Nil(t, altModel.confirmCmd)
}

func TestCopilotReviewShortcut(t *testing.T) {
	for _, view := range []tuiView{tuiViewList, tuiViewDetail} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			pr := approvalTestPR(1)
			m := actionViewTestModel(view, pr)
			pending, release := holdTUIRequest()
			defer release()
			m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost &&
					req.URL.Path == "/repos/owner/repo/pulls/1/requested_reviewers" {
					<-pending
					return jsonResponse(req, http.StatusCreated, `{}`), nil
				}
				if req.URL.Path == "/repos/owner/repo/pulls/1" {
					return jsonResponse(
						req,
						http.StatusOK,
						`{"body":"Updated review details"}`,
					), nil
				}
				return jsonResponse(req, http.StatusOK, `[]`), nil
			})
			h := newTUIHarness(t, m)
			h.Key("ctrl+r").
				Wait("Requesting Copilot review " + pr.Ref()).
				AssertNot("Request Copilot review for")
			release()
			if view == tuiViewDetail {
				h.Wait("Updated review details")
			} else {
				h.Wait("Copilot review requested " + pr.Ref())
			}
		})
	}
}

func TestUpdateListViewCtrlRMultiplePRsRequiresConfirm(t *testing.T) {
	h := newTUIHarness(t, listTUIModel(approvalTestPR(1), approvalTestPR(2)))
	h.Key("shift+down").
		Wait("❯ 2 ").
		Key("ctrl+r").
		Wait("Request Copilot review for 2 PRs?").
		Assert([]string{"Yes", "No"})
	h.KeyEsc().WaitNot("Request Copilot review").Assert("❯ 2 ")
}

func TestUpdateDiffViewRLaunchesAIReview(t *testing.T) {
	if !xos.IsDarwin() {
		t.Skip("AI review requires macOS")
	}
	t.Setenv(herdrEnvVar, "")
	t.Setenv("TERM_PROGRAM", "ghostty")
	pr := testReviewPullRequest()
	h := newTUIHarness(t, actionViewTestModel(tuiViewDiff, pr))
	h.Key("r").Wait("Launch AI review for " + pr.Ref() + "?").Assert("Provider")
	h.KeyEsc().WaitNot("Launch AI review").Assert("+change")
}

func TestRenderHelpOverlayIncludesAltRReviewShortcut(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WT_SESSION", "")
	t.Setenv(herdrEnvVar, "")
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := listTUIModel(testReviewPullRequest())
	m.width, m.height = 120, 40
	h := newTUIHarness(t, m)
	h.Key("?").Wait("shift+↕  Extend selection")
	if xos.IsDarwin() {
		h.Assert([]string{
			"D  Toggle draft                          r  Launch AI review ",
			"m  Merge/Automerge PRs               alt+r  Launch AI review (no confirm)",
			"shift+↕  Extend selection                      s  Send to Slack",
			"A  Approve/Merge PRs                   t/T  Cycle time presets",
			"alt+a  Toggle approval (no confirm)     ctrl+x  Clear all presets",
		})
	} else {
		h.AssertNot("Launch AI review")
	}
}

func TestInlineHelpKeyEmbedsModifiedSingleLetterShortcut(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}

	rendered, ok := key.Inline(
		"alt+c",
		"copy",
		m.styles.helpKey,
		m.styles.helpText,
	)

	require.True(t, ok)
	require.Equal(
		t,
		m.styles.helpKey.Render("alt+")+
			m.styles.helpKey.Render("c")+
			m.styles.helpText.Render("opy"),
		rendered,
	)
	require.Equal(t, "alt+copy", ansi.Strip(rendered))
}

func TestInlineHelpKeyEmbedsModifiedCtrlShiftChord(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}

	rendered, ok := key.Inline(
		"ctrl+shift+t",
		"toggle",
		m.styles.helpKey,
		m.styles.helpText,
	)

	require.True(t, ok)
	require.Equal(
		t,
		m.styles.helpKey.Render("ctrl+shift+")+
			m.styles.helpKey.Render("t")+
			m.styles.helpText.Render("oggle"),
		rendered,
	)
	require.Equal(t, "ctrl+shift+toggle", ansi.Strip(rendered))
}

func TestDoPasteRoutesToFocusedReviewPrompt(t *testing.T) {
	m := listTUIModel(testReviewPullRequest())
	m.width, m.height = 120, 40
	m = m.prepareAIReviewConfirm(testReviewPullRequest(), 0)
	m.confirmInputValue = ""
	m.syncConfirmDialog()
	h := newTUIHarness(t, m)
	h.Key("tab", reel.Repeat(len(m.confirmOptions))).Paste("line one\nline two")
	first := h.Locate(reel.Contains("line one"))
	second := h.Locate(reel.Contains("line two"))
	require.Equal(t, first.Y+1, second.Y)
	require.Equal(t, first.X, second.X)
}

func TestDoPasteRoutesToFocusedFilter(t *testing.T) {
	alpha, beta := approvalTestPR(1), approvalTestPR(2)
	alpha.Title, beta.Title = "alpha", "beta"
	h := newTUIHarness(t, listTUIModel(alpha, beta))
	h.Wait([]string{"alpha", "beta"}).Key("/").Paste("beta")
	h.WaitNot("alpha").Assert("❯ 1  beta")
}

func TestRenderHelpOverlayAlignsExtendedSelectionKey(t *testing.T) {
	m := listTUIModel(testReviewPullRequest())
	m.width, m.height = 120, 40
	h := newTUIHarness(t, m)
	h.Key("?")
	space := h.Locate(reel.Contains("Toggle selection"))
	shift := h.Locate(reel.Contains("Extend selection"))
	require.Equal(t, space.X, shift.X)
}

func TestTuiTableRendererSuppressesIndexColumn(t *testing.T) {
	models := testModels("owner")
	m := tuiModel{
		p:     testPRL,
		cli:   &CLI{Author: &CSVFlag{}},
		width: 120,
	}

	rt := m.tableRendererFor(len(models)).Render(models)

	require.NotEmpty(t, rt.Rows)
	display := ansi.Strip(rt.Rows[0].Display)
	require.Equal(t, "newest PR", display[:len("newest PR")])
}

func TestApplyModeReportSwitchesToGraphemeWidth(t *testing.T) {
	m := tuiModel{
		p:     New(),
		cli:   testCLI(),
		width: 120,
	}
	require.Equal(t, ansi.WcWidth, m.p.widthMethod())

	m = m.applyModeReport(tea.ModeReportMsg{
		Mode:  cansi.ModeUnicodeCore,
		Value: cansi.ModeReset,
	})

	require.Equal(t, ansi.GraphemeWidth, m.p.widthMethod())
}

func TestApplyModeReportIgnoresUnrecognizedMode(t *testing.T) {
	m := tuiModel{
		p:     New(),
		cli:   testCLI(),
		width: 120,
	}

	m = m.applyModeReport(tea.ModeReportMsg{
		Mode:  cansi.ModeUnicodeCore,
		Value: cansi.ModeNotRecognized,
	})
	m = m.applyModeReport(tea.ModeReportMsg{
		Mode:  cansi.ModeSynchronizedOutput,
		Value: cansi.ModeSet,
	})

	require.Equal(t, ansi.WcWidth, m.p.widthMethod())
}

func TestRerenderShowsEstimatedHeaderWithoutRows(t *testing.T) {
	m := tuiModel{
		p:     testPRL,
		cli:   testCLI(),
		width: 120,
	}

	header, rows, colWidths := m.rerender()
	renderer := m.tableRendererFor(len(m.items))

	titleIdx := -1
	for i, col := range renderer.Columns() {
		if col.Name == colTitle {
			titleIdx = i
			break
		}
	}

	require.Empty(t, rows)
	require.NotEmpty(t, colWidths)
	require.NotEqual(t, -1, titleIdx)
	require.Equal(
		t,
		"TITLE                           PR                    STATUS    REASON        CREATED  UPDATED",
		ansi.Strip(header),
	)
	require.GreaterOrEqual(t, colWidths[titleIdx], columnWidthEstimate[colTitle])
}

func TestRefreshResultClearsRowsButKeepsHeader(t *testing.T) {
	items := testModels("owner")[:1]
	renderer := testPRL.newTableRenderer(testCLI(), true, 120, table.WithShowIndex(false))
	rt := renderer.Render(items)

	m := tuiModel{
		items:       items,
		rows:        rt.Rows,
		header:      rt.Header,
		colWidths:   rt.ColWidths,
		width:       120,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
	}

	model, cmd := m.Update(refreshResultMsg{})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Empty(t, bm.items)
	require.Empty(t, bm.rows)
	require.Equal(
		t,
		"TITLE                           PR                    STATUS    REASON        CREATED  UPDATED",
		ansi.Strip(bm.header),
	)
	require.NotEmpty(t, bm.colWidths)
}

func TestViewListShowsRefreshingHeaderWithoutRows(t *testing.T) {
	m := listTUIModel()
	m.cli.Columns.Values = nil
	m.width, m.height = 120, 12
	m.spinner, m.refreshing = spinner{frames: []string{"*"}}, true
	want := "*    TITLE                           PR                    STATUS    REASON        CREATED  UPDATED"
	h := newTUIHarness(t, m)
	require.Zero(t, h.Locate(reel.Contains(want)).Y)
}

func TestViewListNumbersVisibleRows(t *testing.T) {
	first, second, third := approvalTestPR(1), approvalTestPR(2), approvalTestPR(3)
	first.Title, second.Title, third.Title = "alpha", "beta", "gamma"
	m := listTUIModel(first, second, third)
	m.sortColumn, m.sortAsc = colTitle, true
	h := newTUIHarness(t, m)
	h.Wait("❯ 1  alpha").Assert("  2  beta")
	h.Key("/").Paste("eta").Key("enter").Wait("❯ 1  beta").
		Assert("/> eta").AssertNot([]string{first.Title, third.Title})
}

func TestViewListFilterIndicatorIsLeftClamped(t *testing.T) {
	m := listTUIModel()
	m.height = 12
	m.cli.NoBot, m.cli.State = false, valueClosed
	line := strings.Repeat("─", 38) + "┤ state:closed │ All Time ▾ │ All PRs ▾ ├─"
	h := newTUIHarness(t, m)
	require.Zero(t, h.Locate(reel.Contains(line)).X)
}

func TestListRangeSelection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start      int
		key        string
		cursor     string
		selected   []string
		unselected string
	}{
		{"extend down", 0, "shift+down", "2", []string{"1", "2"}, "3"},
		{"extend up", 2, "shift+up", "2", []string{"2", "3"}, "1"},
		{"at top", 0, "shift+up", "1", []string{"1"}, "2"},
		{"at bottom", 2, "shift+down", "3", []string{"3"}, "2"},
		{"space", 1, "space", "2", []string{"2"}, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := numberedListTestModel(3)
			m.cursor = tc.start
			h := newTUIHarness(t, m)
			h.Key(tc.key).Wait("❯ " + tc.cursor + " ")
			for _, label := range tc.selected {
				waitListSelection(t, h, label, true)
			}
			waitListSelection(t, h, tc.unselected, false)
		})
	}
}

func waitListSelection(t *testing.T, h *steep.Harness, label string, selected bool) {
	t.Helper()
	row := h.Locate(reel.Matches(regexp.MustCompile("(?m)^.." + regexp.QuoteMeta(label) + " ")))
	waitCellStyle(
		t,
		h,
		row.Offset(2, 0),
		func(style reel.CellStyle) bool { return (style.Attrs&vt.AttrBold != 0) == selected },
	)
}

func TestListRangeSelectionSurvivesRepeatedAndReversedMovement(t *testing.T) {
	h := newTUIHarness(t, numberedListTestModel(2))
	h.Key("shift+down").Wait("❯ 2 ")
	waitListSelection(t, h, "1", true)
	waitListSelection(t, h, "2", true)
	h.Key("shift+down").KeyUp().Wait("❯ 1 ")
	waitListSelection(t, h, "1", true)
	waitListSelection(t, h, "2", true)
	h.Key("shift+down").Wait("❯ 2 ").Key("shift+up").Wait("❯ 1 ")
	waitListSelection(t, h, "1", true)
	waitListSelection(t, h, "2", true)
}

func TestListNumericJumps(t *testing.T) {
	h := newTUIHarness(t, numberedListTestModel(15))
	h.Key("2").Wait("❯ 2 ").Assert("  1 ")
	h.Key("1").Key("5").Wait("❯ 15 ")
	h.Key("1").Wait("❯ 1 ")
}

func TestRefreshTickIgnoresStaleAndInFlight(t *testing.T) {
	m := tuiModel{
		autoRefresh:     true,
		refreshID:       1,
		view:            tuiViewDetail,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now(),
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, 2, bm.refreshID)

	model, cmd = bm.Update(refreshTickMsg{id: 1})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.refreshing)

	bm.refreshing = true
	model, cmd = bm.Update(refreshTickMsg{id: bm.refreshID})
	require.Nil(t, cmd)
	bm, ok = model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.refreshing)
}

func TestExitDetailViewReschedulesWhenRefreshNotDue(t *testing.T) {
	pr := testReviewPullRequest()
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detailLines:     []string{"line"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now(),
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.Equal(t, prKey(""), bm.detailKey)
	require.False(t, bm.refreshing)
	require.Equal(t, 1, bm.detailRefreshID)
	require.Equal(t, 1, bm.refreshID)
}

func TestExitDetailViewStartsImmediateRefreshWhenDue(t *testing.T) {
	pr := testReviewPullRequest()
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detailLines:     []string{"line"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now().Add(-2 * watchMaxInterval),
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.Equal(t, prKey(""), bm.detailKey)
	require.True(t, bm.refreshing)
	require.Equal(t, 1, bm.detailRefreshID)
	require.Equal(t, 1, bm.refreshID)
}

func TestStaleDetailRefreshCompletionIgnoredAfterExitAndReopen(t *testing.T) {
	pr := testReviewPullRequest()
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detail:          PRDetail{Checks: []PRCheck{{Name: "build", Status: ciStatusPending}}},
		detailLines:     []string{"old"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now(),
	}

	model, _ := m.updateDetailView(tea.KeyPressMsg{Code: tea.KeyEsc})
	bm, ok := model.(tuiModel)
	require.True(t, ok)

	bm.view = tuiViewDetail
	bm.detailKey = key
	bm.detail = PRDetail{Checks: []PRCheck{{Name: "build", Status: ciStatusPending}}}
	bm.detailLines = []string{"old"}

	model, cmd := bm.Update(detailChecksRefreshedMsg{
		id:  0,
		key: key,
		checks: []PRCheck{{
			Name:       "build",
			Status:     ciStatusCompleted,
			Conclusion: ciStatusSuccess,
		}},
	})

	require.Nil(t, cmd)
	after, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, ciStatusPending, after.detail.Checks[0].Status)
}

func TestDetailApproveUsesImmediateRefreshPathWhenDue(t *testing.T) {
	pr := testReviewPullRequest()
	pr.State = valueOpen
	key := makePRKey(pr)
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDetail,
		detailKey:       key,
		detailLines:     []string{"line"},
		actions:         &ActionRunner{},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now().Add(-2 * watchMaxInterval),
	}

	model, cmd := m.updateDetailView(tea.KeyPressMsg{Code: 'a', Text: "a"})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.Equal(t, tuiActionApprove, bm.confirmAction)
	require.True(t, bm.refreshing)
	require.Equal(t, prKey(""), bm.detailKey)
}

func TestExitDiffViewStartsImmediateRefreshWhenDue(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{
		rows:            []TableRow{{Item: PRRowModel{PR: pr}}},
		items:           []PRRowModel{{PR: pr}},
		view:            tuiViewDiff,
		diffKey:         makePRKey(pr),
		diffLines:       []string{"diff"},
		autoRefresh:     true,
		lastInteraction: time.Now(),
		lastRefreshAt:   time.Now().Add(-2 * watchMaxInterval),
	}

	model, cmd := m.updateDiffView(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, tuiViewList, bm.view)
	require.True(t, bm.refreshing)
	require.Equal(t, prKey(""), bm.diffKey)
}

func TestRenderDetailContentShowsCopilotReviewIcon(t *testing.T) {
	pr := testReviewPullRequest()
	pr.Author.Login = "alice"
	resolver := NewAuthorResolver(&Config{})
	m := tuiModel{
		p:         testPRL,
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		detailKey: makePRKey(pr),
		detail: PRDetail{
			Reviews: []PRReview{{
				User:  copilotReviewer,
				State: "COMMENTED",
			}},
		},
		resolver: resolver,
		width:    80,
	}

	lines := m.renderDetailContent()
	rendered := strings.Join(lines, nl)

	stripped := ansi.Strip(rendered)
	require.Equal(
		t,
		"Overview\n\n    Title: \n   Author: @alice\n      URL: https://github.com/owner/repo/pull/42\n"+
			"  Reviews: 🤖 Copilot\n   Status: Unknown\n\nNo description provided.",
		stripped,
	)
}

func TestRenderReviewIconStylesApprovalGreen(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconNerd))
	t.Cleanup(func() { useIcons(previous) })

	require.Equal(
		t,
		styleOK.Render("\u2714"),
		renderReviewIcon(PRReview{State: valueReviewApproved}),
	)
}

func TestRenderReviewIconKeepsUnicodeApproval(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconUnicode))
	t.Cleanup(func() { useIcons(previous) })

	require.Equal(
		t,
		"✅",
		renderReviewIcon(PRReview{State: valueReviewApproved}),
	)
}

func TestDetailFetchReplacesUnknownListStatus(t *testing.T) {
	pr := testReviewPullRequest()
	pr.State = valueOpen
	pr.MergeStatus = MergeStatusUnknown
	status := MergeStatusReady
	m := tuiModel{
		items:     []PRRowModel{{PR: pr, MergeStatus: MergeStatusUnknown}},
		rows:      []TableRow{{Item: PRRowModel{PR: pr, MergeStatus: MergeStatusUnknown}}},
		detailKey: makePRKey(pr),
		p:         testPRL,
		resolver:  NewAuthorResolver(&Config{}),
		width:     80,
	}

	m.confirmed = newConfirmedPRList(m.items)
	m.applyDetailMergeStatus(makePRKey(pr), &status)

	require.Equal(t, MergeStatusReady, m.rows[0].Item.PR.MergeStatus)
	require.Equal(t, MergeStatusReady, m.items[0].PR.MergeStatus)
	rendered := ansi.Strip(strings.Join(m.renderDetailContent(), nl))
	require.Equal(
		t,
		"Overview\n\n    Title: \n   Author: @\n      URL: https://github.com/owner/repo/pull/42\n"+
			"   Status: Ready to merge\n\nNo description provided.",
		rendered,
	)
}

func TestRenderDetailContentUsesNerdReviewAndStatusIcons(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconNerd))
	t.Cleanup(func() { useIcons(previous) })

	pr := testReviewPullRequest()
	pr.Author.Login = "alice"
	pr.State = valueMerged
	m := tuiModel{
		p:         testPRL,
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		detailKey: makePRKey(pr),
		detail: PRDetail{
			Reviews: []PRReview{{
				User:  copilotReviewer,
				State: "COMMENTED",
			}},
		},
		resolver: NewAuthorResolver(&Config{}),
		width:    80,
	}

	rendered := ansi.Strip(strings.Join(m.renderDetailContent(), nl))
	require.Equal(
		t,
		"Overview\n\n    Title: \n   Author: @alice\n      URL: https://github.com/owner/repo/pull/42\n"+
			"  Reviews:  Copilot\n   Status:  Merged\n\nNo description provided.",
		rendered,
	)
}

func TestRenderDetailStatusUsesGitHubPillsInNerdMode(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconNerd))
	t.Cleanup(func() { useIcons(previous) })

	tests := []struct {
		name       string
		pr         PullRequest
		label      string
		glyph      string
		background color.Color
	}{
		{
			"draft",
			PullRequest{State: valueOpen, IsDraft: true},
			"Draft",
			activeIcons.StatusDraft,
			colorGitHubDraft,
		},
		{
			"merged",
			PullRequest{State: valueMerged},
			"Merged",
			activeIcons.StatusMerged,
			colorGitHubDone,
		},
		{
			"closed",
			PullRequest{State: valueClosed},
			"Closed",
			activeIcons.StatusClosed,
			colorGitHubClosed,
		},
		{
			"ready",
			PullRequest{State: valueOpen, MergeStatus: MergeStatusReady},
			"Ready to merge",
			activeIcons.StatusReady,
			colorGitHubOpen,
		},
		{
			"ci pending",
			PullRequest{State: valueOpen, MergeStatus: MergeStatusCIPending},
			"CI pending",
			activeIcons.StatusCIPending,
			colorGitHubAttention,
		},
		{
			"ci failed",
			PullRequest{State: valueOpen, MergeStatus: MergeStatusCIFailed},
			"CI failed",
			activeIcons.StatusCIFailed,
			colorGitHubClosed,
		},
		{
			"needs review",
			PullRequest{State: valueOpen, MergeStatus: MergeStatusBlocked},
			"Needs review",
			activeIcons.StatusNeedsReview,
			colorGitHubAttention,
		},
		{
			"changes requested",
			PullRequest{
				State: valueOpen, MergeStatus: MergeStatusBlocked,
				ReviewDecision: valueReviewChanges, reviewDecisionLoaded: true,
			},
			"Changes requested",
			"",
			colorGitHubClosed,
		},
		{
			"conflict",
			PullRequest{State: valueOpen, MergeStatus: MergeStatusConflict},
			"Merge conflicts",
			activeIcons.StatusConflict,
			colorGitHubClosed,
		},
		{valueUnknown, PullRequest{}, "Unknown", activeIcons.StatusUnknown, colorGitHubDraft},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := lg.NewStyle().
				Foreground(colorGitHubOnEmphasis).
				Background(tt.background).
				Bold(true).
				Render(statusPrefix(tt.glyph) + tt.label)
			want := pillWrap(body, tt.background, false)
			require.Equal(t, want, (tuiModel{}).renderDetailStatus(tt.pr))
		})
	}
}

func TestRenderDetailStatusKeepsUnicodeLabelsPlain(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconUnicode))
	t.Cleanup(func() { useIcons(previous) })

	require.Equal(
		t,
		styleClosed.Render("Closed"),
		(tuiModel{}).renderDetailStatus(PullRequest{State: valueClosed}),
	)
}

func TestRenderDetailContentStylesCheckIcons(t *testing.T) {
	previous := activeIcons
	useIcons(iconsFor(IconNerd))
	t.Cleanup(func() { useIcons(previous) })

	pr := testReviewPullRequest()
	pr.Author.Login = "alice"
	checks := []PRCheck{
		{Name: "pending", Status: ciStatusPending},
		{
			Name:       "success",
			Status:     ciStatusCompleted,
			Conclusion: ciStatusSuccess,
			Duration:   5 * time.Second,
		},
		{Name: "failure", Status: ciStatusCompleted, Conclusion: ciStatusFailure},
		{Name: ciConclusionCancelled, Status: ciStatusCompleted, Conclusion: ciConclusionCancelled},
		{Name: ciConclusionSkipped, Status: ciStatusCompleted, Conclusion: ciConclusionSkipped},
		{Name: "timed out", Status: ciStatusCompleted, Conclusion: ciConclusionTimedOut},
		{
			Name:       "action required",
			Status:     ciStatusCompleted,
			Conclusion: ciConclusionActionRequired,
		},
		{Name: ciConclusionNeutral, Status: ciStatusCompleted, Conclusion: ciConclusionNeutral},
		{Name: ciConclusionStale, Status: ciStatusCompleted, Conclusion: ciConclusionStale},
		{Name: valueUnknown, Status: ciStatusCompleted, Conclusion: valueUnknown},
	}
	m := tuiModel{
		p:         testPRL,
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		detailKey: makePRKey(pr),
		detail:    PRDetail{Checks: checks},
		resolver:  NewAuthorResolver(&Config{}),
		width:     80,
	}

	lines := m.renderDetailContent()
	want := []string{
		detailIndent + styleWarning.Render(activeIcons.CIInProgress) + " pending",
		detailIndent + styleOK.Render(
			activeIcons.Approved,
		) + " success " + styleCheckDur.Render(
			"[5s]",
		),
		detailIndent + styleDanger.Render(activeIcons.Rejected) + " failure",
		detailIndent + styleDismiss.Render(activeIcons.Dismissed) + " cancelled",
		detailIndent + styleDim.Render(activeIcons.CISkipped) + " skipped",
		detailIndent + styleDanger.Render(activeIcons.CITimedOut) + " timed out",
		detailIndent + styleWarning.Render(activeIcons.CIActionRequired) + " action required",
		detailIndent + styleDim.Render(activeIcons.CINeutral) + " neutral",
		detailIndent + styleDim.Render(activeIcons.CIStale) + " stale",
		detailIndent + styleDim.Render(activeIcons.CIUnknown) + " unknown",
	}
	require.Equal(t, want, lines[9:9+len(want)])
}

func TestViewDiffHandlesTinyViewport(t *testing.T) {
	m := diffTestModel(approvalTestPR(1))
	m.width, m.height = 20, 2
	m.diff = "+small"
	h := newTUIHarness(t, m)
	h.Resize(80, 12).Wait("+small")
}

func TestUpdateDiffViewBottomUsesContentViewport(t *testing.T) {
	m := diffTestModel(approvalTestPR(1))
	m.width, m.height = 250, 12
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i+1)
	}
	m.diff = strings.Join(lines, nl)
	h := newTUIHarness(t, m)
	h.Wait("1-8/20 (0%)").Assert([]string{"line 01", "line 08"}).AssertNot("line 09")
	h.Key("G").Wait("13-20/20 (100%)").Assert([]string{"line 13", "line 20"}).AssertNot("line 12")
	h.Key("g").Wait("1-8/20 (0%)").Assert("line 01")
}

func TestWrapDiffLinesCreatesStandaloneANSIWrappedRows(t *testing.T) {
	line := styleDanger.Render("+abcdef")

	rows := layout.WrapLines(line, 4)

	require.Len(t, rows, 2)
	require.Equal(t, []string{"+abc", "def"}, []string{
		ansi.Strip(rows[0]),
		ansi.Strip(rows[1]),
	})
	require.LessOrEqual(t, lg.Width(rows[0]), 4)
	require.LessOrEqual(t, lg.Width(rows[1]), 4)
	require.Equal(t, "\x1b[", rows[1][:len("\x1b[")])
}

func TestWindowSizeMsgRewrapsDiffAndClampsScroll(t *testing.T) {
	m := diffTestModel(approvalTestPR(1))
	m.width, m.height = 80, 8
	line := "+start " + strings.Repeat("0123456789", 28) + " end"
	m.diff = styleDanger.Render(line)
	h := newTUIHarness(t, m)
	h.Wait("+start ").Key("G").Wait(" end").AssertNot("+start ")
	h.Resize(320, 12).Wait(line).AssertNot("100%")
}

func TestViewDiffShowsWrappedContinuationRows(t *testing.T) {
	// Pin launcher detection: the footer's "review" shortcut is gated on
	// hasAIReviewLauncher(), which is env- and platform-dependent.
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WT_SESSION", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv(herdrEnvVar, "")
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := diffTestModel(testReviewPullRequest())
	m.width, m.height, m.diffQueueTotal = 80, 8, 0
	m.diff = styleDanger.Render("+" + strings.Repeat("a", 85))
	h := newTUIHarness(t, m)
	lines := strings.Split(h.Wait("+aaaa").View(), nl)
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	out := strings.Join(lines, nl)

	// The "review" shortcut only renders where an AI-review launcher
	// exists (darwin).
	footer := "open  alt+copy  slack  ctrl+r copilot  dismiss"
	if xos.IsDarwin() {
		footer = "open  alt+copy  slack  review  ctrl+r copilot  dismiss"
	}
	require.Equal(
		t,
		"owner/repo#42 »                                                          Unknown\n"+
			"────────────────────────────────────────────────────────────────────────────────\n"+
			"+aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"+
			"aaaaaaa\n\n"+
			"────────────────────────────────────────────────────────────────────────────────\n"+
			" ↕ scroll  automerge  Draft  approve  Approve/merge  unsubscribe  comment  Close\n"+
			footer,
		out,
	)
}

func TestViewDiffFillsTerminalRectangle(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{
		p:         testPRL,
		rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
		diffKey:   makePRKey(pr),
		diffLines: []string{"+small"},
		diffView:  newScrollView(),
		height:    8,
		width:     120,
		styles:    newTuiStyles(),
	}
	m.syncDiffView()

	assertRenderedFullScreen(t, m.viewDiff().Content, m.width, m.height)
}

func TestViewDetailFillsTerminalRectangle(t *testing.T) {
	m := tuiModel{
		detailLines: []string{"title", "body"},
		detailView:  newScrollView(),
		height:      8,
		width:       120,
		styles:      newTuiStyles(),
	}
	m.syncDetailView()

	assertRenderedFullScreen(t, m.viewDetail().Content, m.width, m.height)
}

func TestAppendRightStatusDoesNotIncreaseFooterLineCount(t *testing.T) {
	m := tuiModel{width: 24}
	help := " ↑↓ scroll\nc comment"
	status := "Diffing owner/repo#42…"

	got := m.appendRightStatus(help, status)

	require.Equal(t, strings.Count(help, nl), strings.Count(got, nl))
	lines := strings.Split(ansi.Strip(got), nl)
	require.Len(t, lines, 2)
	require.LessOrEqual(t, lg.Width(lines[1]), m.width)
	require.Equal(t, " Diffing owner/repo#42…", lines[1])
}

func TestAppendRightStatusTruncatesLongStatusToSingleLine(t *testing.T) {
	m := tuiModel{width: 10}

	got := ansi.Strip(m.appendRightStatus("", "Diffing owner/repo#42…"))

	require.Equal(t, "Diffing …", got)
	require.LessOrEqual(t, lg.Width(got), m.width)
}

func TestAppendRightStatusUsesLeftPaddingForExactFit(t *testing.T) {
	m := tuiModel{width: 21}

	got := ansi.Strip(m.appendRightStatus("", "Diffing foo/bar#123…"))

	require.Equal(t, "Diffing foo/bar#123…", got)
}

func TestViewListDiffingStatusDoesNotAddFooterLines(t *testing.T) {
	for width := 20; width <= 120; width++ {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			base := tuiModel{
				header: "TITLE",
				rows: []TableRow{{
					Item:    PRRowModel{PR: testReviewPullRequest()},
					Display: "demo row",
				}},
				height:      8,
				width:       width,
				styles:      newTuiStyles(),
				filterInput: textinput.New(),
				selected:    make(prKeys),
				p:           testPRL,
				cli:         testCLI(),
			}

			withStatus := base
			withStatus.flash.Msg = withStatus.styles.statusPending.Render(statusDiffing) +
				" " + styleRef.Render("foo/bar#123") + valueEllipsis

			baseLines := strings.Split(ansi.Strip(base.viewList().Content), nl)
			lines := strings.Split(ansi.Strip(withStatus.viewList().Content), nl)
			require.Len(t, lines, len(baseLines))
			for _, line := range lines {
				require.LessOrEqual(t, lg.Width(line), withStatus.width)
			}
		})
	}
}

func TestSyncDiffViewCachesNormalizedRenderLines(t *testing.T) {
	m := tuiModel{
		diffLines: []string{
			"left\tright",
			strings.Repeat("x", 40),
		},
		diffView: newScrollView(),
		height:   6,
		width:    20,
		styles:   newTuiStyles(),
	}

	m.syncDiffView()

	require.Len(t, m.diffRenderLines, 2)
	require.Equal(t, "left    right      ", ansi.Strip(m.diffRenderLines[0]))
	require.Equal(t, 19, ansi.WcWidth.StringWidth(m.diffRenderLines[0]))
	require.Equal(t, 19, ansi.WcWidth.StringWidth(m.diffRenderLines[1]))
	require.Equal(t, len(m.diffRenderLines), m.diffView.TotalLineCount())
}

func TestRenderViewportContentUsesCachedLinesWithScrollbar(t *testing.T) {
	m := tuiModel{styles: newTuiStyles()}
	lines := []string{
		layout.NormalizeLine("line 1", 10),
		layout.NormalizeLine("line 2", 10),
		layout.NormalizeLine("line 3", 10),
		layout.NormalizeLine("line 4", 10),
	}
	vp := newScrollView()
	vp.SetWidth(10)
	vp.SetHeight(3)
	vp.SetContentLines(lines)
	vp.SetYOffset(1)

	got := ansi.Strip(m.renderViewportContent(lines, vp, true))
	rows := strings.Split(got, nl)

	require.Len(t, rows, 3)
	require.Equal(t, "line 2    ┃\nline 3    ┃\nline 4    █", got)
	for _, row := range rows {
		require.Equal(t, 11, ansi.WcWidth.StringWidth(row))
	}
}

func TestTUIWheelFilterCoalescesMouseWheelInput(t *testing.T) {
	ch := make(chan tea.Msg, 1)
	sw := scrollwheel.New(resolveWheelTarget, func(msg tea.Msg) { ch <- msg },
		scrollwheel.WithDelay(time.Millisecond))
	defer sw.Stop()

	model := tuiModel{view: tuiViewDiff}

	require.Nil(t, sw.Filter(model, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown})))
	require.Nil(t, sw.Filter(model, tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown})))

	select {
	case msg := <-ch:
		wm, ok := msg.(scrollwheel.Msg[wheelTarget])
		require.True(t, ok)
		require.Equal(t, wheelTargetDiff, wm.Target)
		require.Equal(t, 2, wm.Delta)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for coalesced wheel message")
	}
}

func TestTUIWheelFilterPassesNonWheelMessages(t *testing.T) {
	sw := scrollwheel.New(resolveWheelTarget, nil, scrollwheel.WithDelay(time.Millisecond))
	defer sw.Stop()

	model := tuiModel{view: tuiViewDiff}
	key := tea.KeyPressMsg{}

	got := sw.Filter(model, key)

	require.Equal(t, key, got)
}

func TestDiffScrollbar(t *testing.T) {
	m := diffTestModel(testReviewPullRequest())
	const first, last = "+first line", "+last line"
	m.diff = first + nl + strings.Repeat(" context\n", 60) + last
	m.applyWindowSize(m.width, m.height)

	testViewportScrollbar(t, m, first, last)
}

func TestDetailScrollbar(t *testing.T) {
	pr := testReviewPullRequest()
	m := approvalTestModel("", pr)
	m.view = tuiViewDetail
	m.detailKey = makePRKey(pr)
	m.detailView = newScrollView()
	m.resolver = &AuthorResolver{}
	const last = "End of description."
	m.detail.Body = strings.Repeat("A paragraph.\n\n", 30) + last
	m.detailLines = m.renderDetailContent()
	m.syncDetailView()

	testViewportScrollbar(t, m, "Overview", last)
}

func testViewportScrollbar(t *testing.T, m tuiModel, first, last string) {
	t.Helper()
	h := newTUIHarness(t, m)
	h.Wait(first).AssertNot(last)
	require.Equal(t, cansi.ModeSet, h.ModeSetting(cansi.ModeMouseButtonEvent))
	require.NotEqual(t, cansi.ModeSet, h.ModeSetting(cansi.ModeMouseAnyEvent))

	top := h.Locate(reel.Contains("█"))
	bottom := h.Locate(reel.Nth(-1, reel.Contains("┃")))
	bottom.LeftClick().Wait(last).AssertNot(first)
	bottom.LeftDrag(top).Wait(first).AssertNot(last)

	// After release, a drag in the text must leave the viewport at the top.
	// Down then advances exactly one row, as shown by the footer's range.
	top.Offset(-1, 0).LeftDrag(bottom.Offset(-1, 0))
	h.KeyDown().Wait(" 2-").AssertNot(first)
}

func TestScrollbarTrackClickJumpsConfirmViewport(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.confirmAction = tuiActionInfo
	m.confirmPrompt = "First message\n" + strings.Repeat("Message\n", 50) + "Last message"
	m.syncConfirmDialog()
	h := newTUIHarness(t, m)
	h.Wait("First message").AssertNot("Last message")
	h.Locate(reel.Nth(-1, reel.Contains("┃"))).
		LeftClick().
		Wait("Last message").
		AssertNot("First message")
}

func TestWrapDiffLinesExpandsTabs(t *testing.T) {
	rows := layout.WrapLines("a\tb", 80)

	require.Len(t, rows, 1)
	require.Equal(t, "a    b", ansi.Strip(rows[0]))
}

func TestSyncDetailViewExpandsTabs(t *testing.T) {
	m := tuiModel{
		detailLines: []string{"left\tright"},
		detailView:  newScrollView(),
		height:      5,
		width:       20,
		styles:      newTuiStyles(),
	}

	m.syncDetailView()

	out := ansi.Strip(m.detailView.View())
	require.Equal(t, "left    right      ", out)
}

func TestFillViewToTerminalExpandsTabs(t *testing.T) {
	m := tuiModel{width: 12, height: 2}

	got := layout.Fill("left\tright", m.width, m.height)

	lines := strings.Split(got, nl)
	require.Len(t, lines, 2)
	require.Equal(t, "left    right", lines[0])
	require.Equal(t, 12, lg.Width(lines[1]))
}

func assertRenderedFullScreen(t *testing.T, content string, width, height int) {
	t.Helper()

	lines := strings.Split(ansi.Strip(content), nl)
	require.Len(t, lines, height)
	for _, line := range lines {
		require.LessOrEqual(t, lg.Width(line), width)
	}
}

func TestActionMsgRemovalRecomputesOffset(t *testing.T) {
	prs := []PullRequest{
		testReviewPullRequest(),
		{Number: 43, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
		{Number: 44, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
		{Number: 45, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
		{Number: 46, Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"}},
	}
	rows := make([]TableRow, len(prs))
	for i, pr := range prs {
		pr.State = valueOpen
		rows[i] = TableRow{Item: PRRowModel{PR: pr}}
	}
	m := tuiModel{
		cli:         testCLI(),
		rows:        rows,
		cursor:      len(rows) - 1,
		offset:      len(rows) - 1,
		height:      6,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		selected:    make(prKeys),
	}

	for _, row := range rows {
		m.items = append(m.items, row.Item)
	}
	m.confirmed = newConfirmedPRList(m.items)
	model, cmd := m.Update(actionMsg{
		index:  len(rows) - 1,
		key:    makePRKey(prs[len(prs)-1]),
		action: tuiActionClosed,
	})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, isShown(bm, makePRKey(prs[len(prs)-1])))
	require.Less(t, bm.offset, m.offset)
	require.Equal(t, bm.scrolledOffset(), bm.offset)
}

func TestBatchActionFailuresSurfaceDetails(t *testing.T) {
	pr := approvalTestPR(42)
	h := newTUIHarness(t, listTUIModel(pr))
	h.SendProgram(batchActionMsg{
		action: tuiActionApproved,
		count:  1,
		failed: 1,
		failures: []batchResult{
			{key: makePRKey(pr), ref: pr.Ref(), url: pr.URL, err: errors.New("boom")},
		},
	}).Wait("Approve failed:").Assert(pr.Ref() + ": boom")
	h.Locate(reel.Contains("OK")).LeftClick().WaitNot("Approve failed:").Assert("❯ 1 ")
}

func TestMergeRefreshKeepsKeyedStateAcrossReorder(t *testing.T) {
	prA := testReviewPullRequest()
	prB := PullRequest{
		Number: 43,
		Repository: Repository{
			Name:          "repo",
			NameWithOwner: "owner/repo",
		},
	}
	oldRows := []TableRow{
		{Item: PRRowModel{PR: prA}},
		{Item: PRRowModel{PR: prB}},
	}
	newRows := []TableRow{
		{Item: PRRowModel{PR: prB}},
		{Item: PRRowModel{PR: prA}},
	}
	m := tuiModel{
		rows:        oldRows,
		items:       []PRRowModel{{PR: prA}, {PR: prB}},
		cursor:      1,
		height:      8,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		selected: prKeys{
			makePRKey(prB): true,
		},
		diffQueue: []prKey{makePRKey(prB)},
	}

	m.replaceListItems([]PRRowModel{{PR: prB}, {PR: prA}})
	bm := m.mergeRefresh(newRows)

	require.True(t, bm.selected[makePRKey(prB)])
	require.Equal(t, 0, bm.cursor)
	require.Equal(t, []int{0, 1}, bm.visibleIndices())
	require.Equal(t, []prKey{makePRKey(prB)}, bm.diffQueue)
}

func TestParseFilterTerm(t *testing.T) {
	tests := []struct {
		input string
		want  filter.Term
	}{
		{"foo", filter.Term{Text: "foo"}},
		{"^foo", filter.Term{Text: "foo", Prefix: true}},
		{"foo$", filter.Term{Text: "foo", Suffix: true}},
		{"^foo$", filter.Term{Text: "foo", Prefix: true, Suffix: true}},
		{"!foo", filter.Term{Text: "foo", Negate: true}},
		{"!^foo", filter.Term{Text: "foo", Negate: true, Prefix: true}},
		{"!foo$", filter.Term{Text: "foo", Negate: true, Suffix: true}},
		{"!^foo$", filter.Term{Text: "foo", Negate: true, Prefix: true, Suffix: true}},
		{"Foo", filter.Term{Text: "Foo", Case: filter.CaseSensitive}},
		// Bare modifiers: flags set but empty text matches everything.
		{"^", filter.Term{Text: "", Prefix: true}},
		{"$", filter.Term{Text: "", Suffix: true}},
		{"!", filter.Term{Text: "", Negate: true}},
		{"!^", filter.Term{Text: "", Negate: true, Prefix: true}},
		{"!$", filter.Term{Text: "", Negate: true, Suffix: true}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, filter.Parse(tt.input))
		})
	}
}

func TestMatchesTerm(t *testing.T) {
	tests := []struct {
		name string
		text string
		term filter.Term
		want bool
	}{
		{"contains", "hello world", filter.Term{Text: "world"}, true},
		{"contains miss", "hello world", filter.Term{Text: "xyz"}, false},
		{"case insensitive", "Hello World", filter.Term{Text: "hello"}, true},
		{
			"case sensitive",
			"Hello World",
			filter.Term{Text: "Hello", Case: filter.CaseSensitive},
			true,
		},
		{
			"case sensitive miss",
			"hello world",
			filter.Term{Text: "Hello", Case: filter.CaseSensitive},
			false,
		},
		{"prefix", "hello world", filter.Term{Text: "hello", Prefix: true}, true},
		{"prefix miss", "hello world", filter.Term{Text: "world", Prefix: true}, false},
		{"suffix", "hello world", filter.Term{Text: "world", Suffix: true}, true},
		{"suffix miss", "hello world", filter.Term{Text: "hello", Suffix: true}, false},
		{"exact", "hello", filter.Term{Text: "hello", Prefix: true, Suffix: true}, true},
		{
			"exact miss",
			"hello world",
			filter.Term{Text: "hello", Prefix: true, Suffix: true},
			false,
		},
		{"negate", "hello world", filter.Term{Text: "xyz", Negate: true}, true},
		{"negate miss", "hello world", filter.Term{Text: "hello", Negate: true}, false},
		{
			"negate prefix",
			"hello world",
			filter.Term{Text: "world", Prefix: true, Negate: true},
			true,
		},
		{
			"negate suffix",
			"hello world",
			filter.Term{Text: "hello", Suffix: true, Negate: true},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.term.Match(tt.text))
		})
	}
}

// --- Filter options overlay tests ---

func TestCurrentFilterValuesDefaultCLI(t *testing.T) {
	cli := testCLI()
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	// testCLI() Normalize sets NoBot=true (from Default.Bots=false),
	// so Bots is "hide" (index 1). Draft defaults to "show" (index 0), CI/Review default to "all",
	// and Archived defaults to "hide" (index 1).
	require.Equal(t, []int{0, 1, 0, 1, 1, 4, 5}, vals)
}

func TestCurrentFilterValuesMapsStateCorrectly(t *testing.T) {
	cli := testCLI()
	cli.State = "merged"
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 2, vals[filterRowState]) // "merged" is index 2
}

func TestCurrentFilterValuesMapsCIFromAlias(t *testing.T) {
	cli := testCLI()
	cli.CI = "s" // alias for "success"
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 0, vals[filterRowCI]) // "success" is index 0
}

func TestCurrentFilterValuesDraft(t *testing.T) {
	cli := testCLI()
	cli.Draft = new(false)
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 1, vals[filterRowDraft]) // "hide" is index 1
}

func TestCurrentFilterValuesDraftTrueMapsToShow(t *testing.T) {
	cli := testCLI()
	cli.Draft = new(true)
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 0, vals[filterRowDraft]) // "show" is index 0
}

func TestCurrentFilterValuesNoBots(t *testing.T) {
	cli := testCLI()
	cli.NoBot = true
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 1, vals[filterRowBots]) // "hide" is index 1
}

func TestCurrentFilterValuesArchived(t *testing.T) {
	cli := testCLI()
	cli.Archived = true
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, 0, vals[filterRowArchived]) // "show" is index 0
}

func TestUpdateOptionsOverlayNavigation(t *testing.T) {
	h := newTUIHarness(t, filterClickTestModel())
	h.Key("j")
	waitFilterRow(h, filterRowStacks)
	h.Key("k").Key("k").Key("l")
	waitFilterRow(h, filterRowState)
	waitFilterChoice(t, h, filterRowState, valueClosed)
}

func TestUpdateOptionsOverlayChangeValue(t *testing.T) {
	h := newTUIHarness(t, filterClickTestModel())
	h.Key("l")
	waitFilterChoice(t, h, filterRowState, valueClosed)
	h.Key("h")
	waitFilterChoice(t, h, filterRowState, valueOpen)
}

func TestUpdateOptionsOverlaySpaceCyclesAndWraps(t *testing.T) {
	h := newTUIHarness(t, filterClickTestModel())
	for _, choice := range []string{valueClosed, valueMerged, valueReady, valueAll, valueOpen} {
		h.Key("space")
		waitFilterChoice(t, h, filterRowState, choice)
	}
}

func TestOptionsOverlayClosesWithExplicitFlags(t *testing.T) {
	m := filterClickTestModel()
	m.cli.stateExplicit, m.cli.draftExplicit, m.cli.noBotExplicit = true, true, true
	m.cli.archivedExplicit, m.cli.ciExplicit, m.cli.reviewExplicit = true, true, true
	m.optionsPicker = m.newFilterPicker()
	h := newTUIHarness(t, m)
	for _, exit := range []string{"O", "q"} {
		h.Wait("enter apply").Key(exit).WaitNot("enter apply").Assert("Options").Key("O")
	}
}

func TestUpdateOptionsOverlayCLIRowsAreEditable(t *testing.T) {
	m := filterClickTestModel()
	m.cli.stateExplicit = true
	m.optionsPicker = m.newFilterPicker()
	h := newTUIHarness(t, m)
	h.Key("l")
	waitFilterChoice(t, h, filterRowState, valueClosed)
	h.KeyBackspace()
	waitFilterChoice(t, h, filterRowState, valueOpen)
}

func TestApplyFilterOptionsSavesOnlyChangedCLIRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")

	cli := testCLI()
	cli.State, cli.stateExplicit = valueMerged, true
	cli.Review, cli.reviewExplicit = valueReviewFilterApproved, true
	m := tuiModel{
		showOptions: true,
		cli:         cli,
		cfg:         &Config{},
		styles:      newTuiStyles(),
	}
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Values[filterRowState] = filterChoiceIndex(filterRowState, valueClosed)

	model, _ := m.applyFilterOptions()
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, valueClosed, bm.cli.State)
	require.Equal(t, valueReviewFilterApproved, bm.cli.Review)
	require.Equal(t, valueClosed, bm.cfg.TUI.Filters.State)
	require.Empty(t, bm.cfg.TUI.Filters.Review, "an unchanged flag stays session-only")
}

func TestRenderOptionsOverlayCLIRowMatchesOtherRows(t *testing.T) {
	m := filterClickTestModel()
	m.cli.State, m.cli.stateExplicit = valueMerged, true
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Cursor = int(filterRowDraft)
	h := newTUIHarness(t, m)
	merged := cellStyle(h, filterChoiceButton(t, h, filterRowState, valueMerged))
	require.Equal(t, vt.AttrBold, merged.Attrs)
	requireScreenColor(t, merged.Fg, styleTitle.GetForeground())
	open := cellStyle(h, filterChoiceButton(t, h, filterRowState, valueOpen))
	require.Equal(t, vt.AttrFaint, open.Attrs)
	requireScreenColor(t, open.Fg, m.styles.defaultChoice.GetForeground())
	closed := cellStyle(h, filterChoiceButton(t, h, filterRowState, valueClosed))
	require.Equal(t, vt.AttrFaint, closed.Attrs)
	require.Nil(t, closed.Fg.Color)
}

func TestRenderOptionsOverlayHighlightsActiveRow(t *testing.T) {
	m := filterClickTestModel()
	m.optionsPicker.Cursor = int(filterRowDraft)
	h := newTUIHarness(t, m)
	label := h.Locate(reel.Contains("Drafts  "))
	bg := cellStyle(h, label).Bg
	require.NotNil(t, bg.Color)
	require.Equal(t, bg, cellStyle(h, filterChoiceButton(t, h, filterRowDraft, tuiHelpShow)).Bg)
	require.Equal(
		t,
		bg,
		cellStyle(h, filterChoiceButton(t, h, filterRowDraft, valueHide).Offset(8, 0)).Bg,
	)
	require.Nil(t, cellStyle(h, h.Locate(reel.Contains("Stacks  "))).Bg.Color)
}

func TestRenderOptionsOverlayStylesDefaultChoices(t *testing.T) {
	m := filterClickTestModel()
	h := newTUIHarness(t, m)
	open := filterChoiceButton(t, h, filterRowState, valueOpen)
	base := cellStyle(h, open)
	require.Equal(t, vt.AttrBold, base.Attrs)
	requireScreenColor(t, base.Fg, styleTitle.GetForeground())
	h.KeyRight()
	waitFilterChoice(t, h, filterRowState, valueClosed)
	requireScreenColor(t, cellStyle(h, open).Fg, m.styles.defaultChoice.GetForeground())
	require.Equal(t, vt.AttrFaint, cellStyle(h, open).Attrs)
	h.KeyBackspace()
	waitCellStyle(t, h, open, func(s reel.CellStyle) bool { return s == base })
}

func TestUpdateOptionsOverlayResetUsesConfigDefaults(t *testing.T) {
	m := filterClickTestModel()
	m.cfg.Default.State = valueMerged
	m.optionsPicker = m.newFilterPicker()
	h := newTUIHarness(t, m)
	for _, tc := range []struct {
		row           filterRow
		staged, reset string
	}{
		{filterRowState, valueClosed, valueMerged},
		{filterRowBots, tuiHelpShow, valueHide},
		{filterRowDraft, valueHide, tuiHelpShow},
		{filterRowArchived, tuiHelpShow, valueHide},
	} {
		filterChoiceButton(t, h, tc.row, tc.staged).LeftClick()
		waitFilterChoice(t, h, tc.row, tc.staged)
		h.KeyBackspace()
		waitFilterChoice(t, h, tc.row, tc.reset)
	}
}

func TestActiveFilterTagsNoBotsFromTestCLI(t *testing.T) {
	// testCLI() Normalize sets NoBot=true from Default.Bots=false,
	// so "bots:hide" is always present.
	cli := testCLI()
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Equal(t, []string{"bots:hide"}, tags)
}

func TestActiveFilterTagsEmptyWhenAllDefaults(t *testing.T) {
	cli := &CLI{State: valueOpen}
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Empty(t, tags)
}

func TestActiveFilterTagsVariousFilters(t *testing.T) {
	cli := testCLI()
	cli.State = "merged"
	cli.Draft = new(false)
	cli.NoBot = true
	cli.CI = "success"
	cli.Review = "approved"
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Contains(t, tags, "state:merged")
	require.Contains(t, tags, "drafts:hide")
	require.Contains(t, tags, "bots:hide")
	require.Contains(t, tags, "ci:success")
	require.Contains(t, tags, "review:approved")
}

func TestActiveFilterTagsAbbreviatesCIFailure(t *testing.T) {
	cli := testCLI()
	cli.CI = "failure"
	m := tuiModel{cli: cli}

	tags := m.activeFilterTags()

	require.Contains(t, tags, "ci:fail")
	require.NotContains(t, tags, "ci:failure")
}

func TestActiveFilterTagsNilCLI(t *testing.T) {
	m := tuiModel{}
	require.Nil(t, m.activeFilterTags())
}

func TestListViewportAccountsForFilterIndicator(t *testing.T) {
	// CLI with no active filter tags → no indicator line.
	cliClean := &CLI{State: valueOpen}
	m := tuiModel{
		height:      20,
		width:       80,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		cli:         cliClean,
		p:           testPRL,
	}

	vpNoIndicator := m.listViewport()

	// Activate a filter → tags render inline on the separator, so viewport stays the same.
	cliClean.State = "merged"
	vpWithIndicator := m.listViewport()

	require.Equal(t, vpNoIndicator, vpWithIndicator)
}

func TestStaleRefreshResultWithOldQueryGenDiscarded(t *testing.T) {
	items := testModels("owner")[:1]
	renderer := testPRL.newTableRenderer(testCLI(), true, 120, table.WithShowIndex(false))
	rt := renderer.Render(items)

	m := tuiModel{
		items:       items,
		rows:        rt.Rows,
		header:      rt.Header,
		colWidths:   rt.ColWidths,
		width:       120,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
		queryGen:    2,
	}

	// Stale result with old queryGen should be discarded.
	model, cmd := m.Update(refreshResultMsg{
		items:    nil,
		rows:     nil,
		queryGen: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	// Items should not be cleared since the result was stale.
	require.Len(t, bm.items, len(items))
}

func TestStaleRefreshResultWithOldQueryGenKeepsRefreshing(t *testing.T) {
	m := tuiModel{
		refreshing: true,
		queryGen:   2,
	}

	model, cmd := m.Update(refreshResultMsg{
		queryGen: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.refreshing)
}

func TestRefreshResultCompletesSilently(t *testing.T) {
	items := testModels("owner")[:1]
	renderer := testPRL.newTableRenderer(testCLI(), true, 120, table.WithShowIndex(false))
	rt := renderer.Render(items)

	m := tuiModel{
		width:       120,
		height:      12,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
		refreshing:  true,
		queryGen:    1,
	}

	model, cmd := m.Update(refreshResultMsg{
		items:    items,
		rows:     rt.Rows,
		queryGen: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.refreshing)
	require.False(t, bm.flash.Active())
}

func TestHeadValidationMsgStartsRefreshWhenChanged(t *testing.T) {
	m := tuiModel{
		view:        tuiViewList,
		styles:      newTuiStyles(),
		filterInput: textinput.New(),
		selected:    make(prKeys),
		p:           testPRL,
		cli:         testCLI(),
		queryGen:    2,
		autoRefresh: true,
	}

	model, cmd := m.Update(headValidationMsg{
		changed:  true,
		queryGen: 2,
	})

	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.True(t, bm.refreshing)
	require.Equal(t, 3, bm.queryGen)
}

func TestHeadValidationMsgIgnoredWhenStale(t *testing.T) {
	m := tuiModel{
		view:     tuiViewList,
		queryGen: 2,
	}

	model, cmd := m.Update(headValidationMsg{
		changed:  true,
		queryGen: 1,
	})

	require.Nil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.False(t, bm.refreshing)
	require.Equal(t, 2, bm.queryGen)
}

func TestApplyTUIFilterDefaultsSetsNonExplicitFields(t *testing.T) {
	cli := &CLI{}
	cli.Normalize(&Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueOpen,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
		},
	})

	cfg := &Config{
		TUI: TUIConfig{
			Filters: TUIFiltersConfig{
				State: "merged",
				CI:    "success",
			},
		},
	}

	changed := applyTUIFilterDefaults(cli, cfg)

	require.True(t, changed)
	require.Equal(t, "merged", cli.State)
	require.Equal(t, "success", cli.CI)
}

func TestApplyTUIFilterDefaultsSkipsExplicitFields(t *testing.T) {
	cli := &CLI{
		State: "closed",
	}
	cli.stateExplicit = true
	cli.Normalize(&Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueOpen,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
		},
	})

	cfg := &Config{
		TUI: TUIConfig{
			Filters: TUIFiltersConfig{
				State: "merged",
			},
		},
	}

	changed := applyTUIFilterDefaults(cli, cfg)

	require.False(t, changed)
	require.Equal(t, "closed", cli.State) // not overridden
}

func TestApplyTUIFilterDefaultsIgnoresLegacyDraftTrue(t *testing.T) {
	cli := &CLI{}
	cli.Normalize(&Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueOpen,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
		},
	})

	cfg := &Config{
		TUI: TUIConfig{
			Filters: TUIFiltersConfig{
				Draft: new(true),
			},
		},
	}

	changed := applyTUIFilterDefaults(cli, cfg)

	require.False(t, changed)
	require.Nil(t, cli.Draft)
}

func testFilterRow(number int, mutate func(*PRRowModel)) TableRow {
	item := PRRowModel{
		PR: PullRequest{
			Number:     number,
			State:      valueOpen,
			Repository: Repository{Name: "repo", NameWithOwner: "owner/repo"},
		},
	}
	if mutate != nil {
		mutate(&item)
	}
	return TableRow{Item: item}
}

func TestApplyFilterOptionsHidesNonMatchingRowsImmediately(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	human := testFilterRow(1, nil)
	bot := testFilterRow(2, func(item *PRRowModel) {
		item.Author.IsBot = true
	})
	draft := testFilterRow(3, func(item *PRRowModel) {
		item.PR.IsDraft = true
	})

	m := tuiModel{
		cli:         testCLI(),
		cfg:         &Config{},
		styles:      newTuiStyles(),
		rows:        []TableRow{human, bot, draft},
		items:       []PRRowModel{human.Item, bot.Item, draft.Item},
		height:      20,
		width:       120,
		filterInput: textinput.New(),
		selected:    make(prKeys),
		p:           testPRL,
	}
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Values[filterRowBots] = filterChoiceIndex(filterRowBots, filterChoiceTrue)
	m.optionsPicker.Values[filterRowDraft] = filterChoiceIndex(filterRowDraft, filterChoiceFalse)

	model, cmd := m.applyFilterOptions()
	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)

	// Rows stay loaded, but the bot and draft PRs drop out of view right away.
	require.Len(t, bm.rows, 3)
	require.Equal(t, []int{0}, bm.visibleIndices())
	require.True(t, bm.filterHidden[makePRKey(bot.Item.PR)])
	require.True(t, bm.filterHidden[makePRKey(draft.Item.PR)])
}

func TestRefreshResultClearsLocalFilterHiding(t *testing.T) {
	row := testFilterRow(1, nil)

	m := tuiModel{
		cli:         testCLI(),
		cfg:         &Config{},
		styles:      newTuiStyles(),
		rows:        []TableRow{row},
		items:       []PRRowModel{row.Item},
		height:      20,
		width:       120,
		filterInput: textinput.New(),
		selected:    make(prKeys),
		filterHidden: prKeys{
			makePRKey(row.Item.PR): true,
		},
		p: testPRL,
	}
	require.Empty(t, m.visibleIndices())

	model, _ := m.Update(refreshResultMsg{
		rows:  []TableRow{row},
		items: []PRRowModel{row.Item},
	})
	bm, ok := model.(tuiModel)
	require.True(t, ok)

	require.Empty(t, bm.filterHidden)
	require.Equal(t, []int{0}, bm.visibleIndices())
}

func TestMatchesLocalFiltersKeepsUnknownReviewDecision(t *testing.T) {
	cli := testCLI()
	cli.Review = valueReviewFilterApproved
	m := tuiModel{cli: cli}

	unhydrated := testFilterRow(1, nil).Item
	require.True(t, m.matchesLocalFilters(unhydrated))

	approved := testFilterRow(2, func(item *PRRowModel) {
		item.PR.ReviewDecision = valueReviewApproved
		item.PR.reviewDecisionLoaded = true
	}).Item
	require.True(t, m.matchesLocalFilters(approved))

	changes := testFilterRow(3, func(item *PRRowModel) {
		item.PR.ReviewDecision = valueReviewChanges
		item.PR.reviewDecisionLoaded = true
	}).Item
	require.False(t, m.matchesLocalFilters(changes))
}

func TestApplyFilterOptionsResetClearsOverridesAndRestoresDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(t, os.WriteFile(cp, []byte(defaultConfigYAML), 0o600))

	cfg := &Config{
		Default: Defaults{
			Limit:  defaultLimit,
			State:  valueMerged,
			Output: valueTable,
			Sort:   valueName,
			Match:  "title",
			Bots:   false,
		},
	}

	cli := &CLI{}
	cli.Normalize(cfg)
	cli.State = valueClosed
	cli.NoBot = false
	cli.Archived = true

	m := tuiModel{
		cli:      cli,
		cfg:      cfg,
		styles:   newTuiStyles(),
		selected: make(prKeys),
		p:        testPRL,
	}
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.IsReset[filterRowState] = true
	m.optionsPicker.IsReset[filterRowBots] = true
	m.optionsPicker.IsReset[filterRowArchived] = true

	model, cmd := m.applyFilterOptions()
	require.NotNil(t, cmd)
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, valueMerged, bm.cli.State)
	require.True(t, bm.cli.NoBot)
	require.False(t, bm.cli.Archived)

	// Reset filters are persisted to the state file (not config): state cleared
	// to empty, and the boolean filters cleared to nil (rendered as null).
	st, err := loadTUIState()
	require.NoError(t, err)
	require.NotNil(t, st)
	require.Empty(t, st.Filters.State)
	require.Nil(t, st.Filters.Bots)
	require.Nil(t, st.Filters.Archived)

	// Config is left untouched - the user manages config; the TUI writes state.
	loaded, err := loadConfig()
	require.NoError(t, err)
	require.Empty(t, loaded.TUI.Filters.State)
	require.Nil(t, loaded.TUI.Filters.Bots)
	require.Nil(t, loaded.TUI.Filters.Archived)

	// State takes precedence: overlaying it clears the merged-state config default.
	applyTUIState(loaded, st)
	require.Empty(t, loaded.TUI.Filters.State)
	require.Nil(t, loaded.TUI.Filters.Bots)
	require.Nil(t, loaded.TUI.Filters.Archived)
}

func testReviewPullRequest() PullRequest {
	return PullRequest{
		Number: 42,
		Repository: Repository{
			Name:          "repo",
			NameWithOwner: "owner/repo",
		},
		URL: "https://github.com/owner/repo/pull/42",
	}
}

func TestActiveFilterTagsRendersCIExpression(t *testing.T) {
	cli := testCLI()
	cli.CI = "pending,-failure"
	m := tuiModel{cli: cli}

	require.Contains(t, m.activeFilterTags(), "ci:pending,!fail")
}

func TestCurrentFilterValuesMapsCINotFailing(t *testing.T) {
	cli := testCLI()
	cli.CI = "-f"
	m := tuiModel{cli: cli}

	vals := m.currentFilterValues()

	require.Equal(t, ciFilterNotFailing, m.filterChoices(filterRowCI)[vals[filterRowCI]].value)
}

func TestApplyFilterOptionsKeepsUnrepresentedCIExpression(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")

	cfg := &Config{Default: Defaults{
		Limit:  defaultLimit,
		State:  valueOpen,
		Output: valueTable,
		Sort:   valueName,
		Match:  "title",
	}}
	cli := &CLI{}
	cli.Normalize(cfg)
	cli.CI = "pending,!success"
	m := tuiModel{showOptions: true, cli: cli, cfg: cfg, styles: newTuiStyles()}
	m.optionsPicker = m.newFilterPicker()

	choices := m.filterChoices(filterRowCI)
	require.Equal(t, "pending,!success", choices[m.optionsPicker.Values[filterRowCI]].label)
	require.Len(t, choices, len(filterOptionDefs[filterRowCI].choices)+1)

	// Change an unrelated row, then apply: the CI expression must survive.
	m.optionsPicker.Values[filterRowArchived] = 0
	model, _ := m.applyFilterOptions()
	bm, ok := model.(tuiModel)
	require.True(t, ok)
	require.Equal(t, "pending,!success", bm.cli.CIFilter().String())
	require.True(t, bm.cli.Archived)
	require.Equal(t, "pending,!success", bm.cfg.TUI.Filters.CI)
}
