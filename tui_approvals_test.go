package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"getsteep.dev/steep/reel"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/stretchr/testify/require"
)

var errApproveTest = errors.New("approve failed")

func approvalTestPR(number int) PullRequest {
	return PullRequest{
		Number:               number,
		NodeID:               "PR_node",
		State:                valueOpen,
		MergeStatus:          MergeStatusBlocked,
		ReviewDecision:       valueReviewRequired,
		reviewDecisionLoaded: true,
		viewerApprovalLoaded: true,
		Repository:           Repository{Name: "repo", NameWithOwner: "owner/repo"},
		URL:                  "https://github.com/owner/repo/pull/1",
	}
}

func approvalTestModel(review string, prs ...PullRequest) tuiModel {
	cli := testCLI()
	cli.Review = review
	items := make([]PRRowModel, len(prs))
	rows := make([]TableRow, len(prs))
	for i, pr := range prs {
		items[i] = PRRowModel{PR: pr, MergeStatus: pr.MergeStatus}
		rows[i] = TableRow{Item: items[i]}
	}
	return tuiModel{
		cli:           cli,
		items:         items,
		confirmed:     newConfirmedPRList(items),
		rows:          rows,
		selected:      make(prKeys),
		styles:        newTuiStyles(),
		login:         "viewer",
		metadataCache: newListMetadataCache(),
		width:         80,
		height:        20,
		p:             testPRL,
	}
}

func updateModel(t *testing.T, m tuiModel, msg tea.Msg) (tuiModel, tea.Cmd) {
	t.Helper()
	model, cmd := m.Update(msg)
	tm, ok := model.(tuiModel)
	require.True(t, ok)
	return tm, cmd
}

func pressKey(t *testing.T, m tuiModel, keyText string) (tuiModel, tea.Cmd) {
	t.Helper()
	return updateModel(t, m, tea.KeyPressMsg{Code: rune(keyText[0]), Text: keyText})
}

// isShown reports whether key's row is in the visible list.
func isShown(m tuiModel, key prKey) bool {
	idx := m.resolveIndex(key, -1)
	return idx >= 0 && !m.isRowHidden(idx)
}

func approvedMsg(key prKey, err error) actionMsg {
	return actionMsg{key: key, index: -1, ref: string(key), action: tuiActionApproved, err: err}
}

func TestApprovalHelpMatchesViewerState(t *testing.T) {
	for _, tt := range []struct {
		name    string
		prepare func(*PullRequest)
		pending bool
		want    bool
	}{
		{name: "unapproved", want: true},
		{name: "approved by viewer", prepare: func(pr *PullRequest) { pr.viewerApproved = true }, want: true},
		{name: "pending", pending: true},
		{name: "approved by others", prepare: func(pr *PullRequest) { pr.ReviewDecision = valueReviewApproved }, want: true},
		{name: "unknown viewer approval", prepare: func(pr *PullRequest) { pr.viewerApprovalLoaded = false }, want: true},
		{name: "own PR", prepare: func(pr *PullRequest) { pr.Author.Login = "viewer" }},
		{name: "draft", prepare: func(pr *PullRequest) { pr.IsDraft = true }},
		{name: "closed", prepare: func(pr *PullRequest) { pr.State = valueClosed }},
		{name: "merged", prepare: func(pr *PullRequest) { pr.State = valueMerged }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pr := approvalTestPR(1)
			if tt.prepare != nil {
				tt.prepare(&pr)
			}
			m := approvalTestModel("", pr)
			m.diffKey, m.detailKey = makePRKey(pr), makePRKey(pr)
			if tt.pending {
				m.beginApproval([]prKey{makePRKey(pr)})
			}
			for view, hints := range map[string][]key.Hint{
				"list":   m.listHelpPairs(),
				"diff":   m.diffHelpPairs(),
				"detail": m.detailHelpPairs(),
			} {
				require.Equal(t, tt.want, slices.ContainsFunc(hints, func(h key.Hint) bool {
					return h.Key == tuiKeybindApprove
				}), view)
				if view == "diff" && (tt.pending || isViewerApproved(pr)) {
					require.Equal(t, !tt.pending, slices.ContainsFunc(hints, func(h key.Hint) bool {
						return h.Key == tuiKeybindApproveMerge
					}))
				}
			}
		})
	}
}

func TestListApprovalHelpUsesSelectedTargets(t *testing.T) {
	approved, unapproved := approvalTestPR(1), approvalTestPR(2)
	approved.viewerApproved = true
	approved.Title, unapproved.Title = "First change", "Second change"
	m := listTUIModel(approved, unapproved)
	m.width, m.sortColumn, m.sortAsc = 160, colTitle, true
	h := newTUIHarness(t, m)
	h.Wait(" unapprove ").Key("shift+down").Wait(" approve ").Assert("❯ 2 ")
	h.Key("space").Wait(" unapprove ").Assert("❯ 2 ")
	h.KeyUp().Wait("❯ 1 ").Key("space").KeyDown().Wait("❯ 2 ").Assert(" approve ")
}

func TestBeginApprovalHidesSelfRequiredRowImmediately(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	k1, k2 := makePRKey(m.rows[0].Item.PR), makePRKey(m.rows[1].Item.PR)
	m.selected[k1], m.selected[k2] = true, true

	m.beginApproval([]prKey{k1})

	require.True(t, m.filterHidden[k1])
	require.False(t, m.selected[k1])
	require.True(t, m.selected[k2])
	require.Equal(t, 1, m.cursor)
	require.Contains(t, m.pendingApprovals, k1)
	require.True(t, isViewerApproved(m.rows[0].Item.PR))
	require.True(t, isViewerApproved(m.items[0].PR))
}

func TestBeginApprovalWithoutFilterKeepsRowAndMarksIt(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	key := makePRKey(m.rows[0].Item.PR)
	require.Equal(t, tuiNonCursorPrefix, m.rowGutter(0))
	require.Equal(t, m.styles.cursor.Render(tuiCursorPrefix), m.rowCursor(0))

	m.beginApproval([]prKey{key})

	require.False(t, m.filterHidden[key])
	require.Equal(t, activeIcons.Check+" ", ansi.Strip(m.rowGutter(0)))
	require.Equal(t, m.styles.statusOK.Bold(true).Render(tuiCursorPrefix), m.rowCursor(0))
}

func TestApprovalFailureRestoresRow(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	m.beginApproval([]prKey{key})

	m, _ = updateModel(t, m, approvedMsg(key, errApproveTest))

	require.False(t, m.filterHidden[key])
	require.Empty(t, m.pendingApprovals)
	require.False(t, m.rows[0].Item.PR.viewerApproved)
	require.True(t, m.rows[0].Item.PR.viewerApprovalLoaded)
	require.Equal(t, "Approve failed: owner/repo#1: approve failed", ansi.Strip(m.flash.Msg))
}

func TestApprovalFailureKeepsRowHiddenWhenNewFilterExcludesIt(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	m.beginApproval([]prKey{key})
	m.cli.CI = ciStatusFailure // applied while the approval was in flight

	m, _ = updateModel(t, m, approvedMsg(key, errApproveTest))

	require.False(t, isShown(m, key))
	require.False(t, m.rows[0].Item.PR.viewerApproved)
}

func TestApprovalFailureAfterRowLeftListRefreshes(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	m.beginApproval([]prKey{key})
	m.replaceListItems(m.items[1:])
	m.rows = m.rows[1:]

	m, cmd := updateModel(t, m, approvedMsg(key, errApproveTest))

	require.NotNil(t, cmd)
	require.True(t, m.refreshing)
	require.Empty(t, m.pendingApprovals)
	require.Equal(t, "Approve failed: owner/repo#1: approve failed", ansi.Strip(m.flash.Msg))
}

func TestApprovalSuccessAfterRowLeftListSettles(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	m.beginApproval([]prKey{key})
	m.replaceListItems(m.items[1:])
	m.rows = m.rows[1:]

	m, _ = updateModel(t, m, approvedMsg(key, nil))

	require.Empty(t, m.pendingApprovals)
	require.False(t, m.refreshing)
	require.Equal(t, "Approved owner/repo#1", ansi.Strip(m.flash.Msg))
	require.Equal(t, 2, m.items[0].PR.Number)
	require.Equal(t, 2, m.rows[0].Item.PR.Number)
}

func TestApprovalSuccessInvalidatesInFlightRefresh(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	oldCache := m.metadataCache
	m.refreshing = true
	m.refreshCancellable = true
	m.queryGen = 5
	m.beginApproval([]prKey{key})

	m, cmd := updateModel(t, m, approvedMsg(key, nil))

	require.NotNil(t, cmd)
	require.NotSame(t, oldCache, m.metadataCache)
	require.Equal(t, 6, m.queryGen)
	require.False(t, m.refreshing)
	require.True(t, m.filterHidden[key])

	// The superseded refresh still carries the PR as unapproved; it's dropped.
	stale := approvalTestPR(1)
	stale.viewerApproved = false
	m, _ = updateModel(t, m, refreshResultMsg{
		queryGen: 5,
		items:    []PRRowModel{{PR: stale}},
	})
	require.Len(t, m.rows, 2)
	require.True(t, isViewerApproved(m.rows[0].Item.PR))
}

func TestRefreshDuringPendingApprovalKeepsRowHidden(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	m.beginApproval([]prKey{key})

	fresh := []PRRowModel{{PR: approvalTestPR(1)}, {PR: approvalTestPR(2)}}
	m, _ = updateModel(t, m, refreshResultMsg{queryGen: m.queryGen, items: fresh})

	require.True(t, m.filterHidden[key])
	require.True(t, isViewerApproved(*m.prForKey(key)))
	require.Equal(t, makePRKey(approvalTestPR(2)), m.rowKeyAt(m.cursor))
}

func TestBatchApprovalRestoresOnlyFailedKeys(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired,
		approvalTestPR(1), approvalTestPR(2), approvalTestPR(3))
	k1, k2 := makePRKey(m.rows[0].Item.PR), makePRKey(m.rows[1].Item.PR)
	m.beginApproval([]prKey{k1, k2})

	m, _ = updateModel(t, m, batchActionMsg{
		action:   tuiActionApproved,
		count:    2,
		failed:   1,
		keys:     []prKey{k1},
		failures: []batchResult{{key: k2, ref: string(k2), err: errApproveTest}},
		outcomes: map[prKey]approveOutcome{k1: approvePosted},
	})

	require.True(t, m.filterHidden[k1])
	require.False(t, m.filterHidden[k2])
	require.False(t, m.rows[1].Item.PR.viewerApproved)
	require.Empty(t, m.pendingApprovals)
}

func TestAggregateSkipRollsBackViewerFlag(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	m.beginApproval([]prKey{key})

	msg := approvedMsg(key, nil)
	msg.outcome = approveSkippedAggregate
	m, _ = updateModel(t, m, msg)

	require.False(t, m.rows[0].Item.PR.viewerApproved)
	require.False(t, m.filterHidden[key])
}

func TestPRStatusRespectsApprovalGeneration(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	key := makePRKey(m.rows[0].Item.PR)
	unapproved := approvalTestPR(1)
	unapproved.MergeStatus = MergeStatusCIFailed

	beforeBegin := m.approvalGen[key]
	m.beginApproval([]prKey{key})
	duringPending := m.approvalGen[key]

	// Requested before the approval, arriving while it is pending.
	m, _ = updateModel(t, m, prStatusMsg{key: key, gen: beforeBegin, pr: unapproved})
	require.True(t, isViewerApproved(m.rows[0].Item.PR))
	// Non-viewer fields still apply.
	require.Equal(t, MergeStatusCIFailed, m.rows[0].Item.PR.MergeStatus)

	m, _ = updateModel(t, m, approvedMsg(key, nil))

	// Requested while pending, arriving after settlement.
	m, _ = updateModel(t, m, prStatusMsg{key: key, gen: duringPending, pr: unapproved})
	require.True(t, isViewerApproved(m.rows[0].Item.PR))

	// Requested after settlement: authoritative.
	m, _ = updateModel(
		t,
		m,
		prStatusMsg{
			key:       key,
			gen:       m.approvalGen[key],
			changeGen: m.prChangeGen[key],
			pr:        unapproved,
		},
	)
	require.False(t, m.rows[0].Item.PR.viewerApproved)
}

func TestListApprovePromptsToUnapprove(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	h := newTUIHarness(t, listTUIModel(pr))
	h.Key("a").
		Wait("Unapprove " + pr.Ref() + "?").
		Assert([]string{"Reason", defaultUnapproveMessage})
	h.KeyEsc().WaitNot(defaultUnapproveMessage).Assert("unapprove")
}

func TestListApproveSkipsPending(t *testing.T) {
	pr := approvalTestPR(1)
	m := listTUIModel(pr)
	m.beginApproval([]prKey{makePRKey(pr)})
	h := newTUIHarness(t, m)
	h.Key("a").Wait("Approval pending " + pr.Ref()).AssertNot("Approve " + pr.Ref() + "?")
}

func TestListApproveWithUnloadedViewerStateStillApproves(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApprovalLoaded = false
	h := newTUIHarness(t, listTUIModel(pr))
	h.Key("a").Wait("Approve " + pr.Ref() + "?").Assert([]string{"Yes", "No"})
}

func TestListApproveConfirmBeginsOnAccept(t *testing.T) {
	pr, other := approvalTestPR(1), approvalTestPR(2)
	pr.Title, other.Title = "First change", "Other change"
	m := listTUIModel(pr, other)
	m.sortColumn, m.sortAsc = colTitle, true
	m.cli.Review = valueReviewFilterSelfRequired
	pending, release := holdTUIRequest()
	defer release()
	m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		<-pending
		return jsonResponse(req, http.StatusCreated, `{}`), nil
	})
	h := newTUIHarness(t, m)
	h.Wait(pr.Title).Key("a").Wait("Approve " + pr.Ref() + "?").Assert(pr.Title)
	h.Key("y").Wait("Approving " + pr.Ref()).AssertNot(pr.Title).Assert(other.Title)
	release()
	h.Wait("Approved " + pr.Ref()).AssertNot(pr.Title)
}

func TestBackgroundApprovalCompletionPreservesNewDiffQueue(t *testing.T) {
	for _, view := range []tuiView{tuiViewList, tuiViewDetail} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			var posts atomic.Int32
			m := approvalTestModel("", approvalTestPR(42), approvalTestPR(2), approvalTestPR(3))
			m.actions = approveTestRunner(t, false, &posts)
			k1, k2, k3 := m.rowKeyAt(0), m.rowKeyAt(1), m.rowKeyAt(2)
			m.view, m.detailKey = view, k1
			m, cmd := updateModel(t, m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
			require.NotNil(t, cmd)
			completion := cmd()
			done, ok := completion.(actionDoneMsg)
			require.True(t, ok)
			require.Equal(t, 1, m.actionsInFlight)
			result, ok := done.msg.(actionMsg)
			require.True(t, ok)
			require.NoError(t, result.err)
			require.EqualValues(t, 1, posts.Load())
			m.view, m.diffKey = tuiViewDiff, k2
			m.diffQueue = []prKey{k3}

			m, _ = updateModel(t, m, completion)
			require.Zero(t, m.actionsInFlight)

			require.Equal(t, k2, m.diffKey)
			require.Equal(t, []prKey{k3}, m.diffQueue)
			require.False(t, m.diffLoading)
			require.Equal(t, tuiViewDiff, m.view)
		})
	}
}

func TestAlreadyApprovedResultSwapsStaleCache(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1))
	key := m.rowKeyAt(0)
	oldCache := m.metadataCache
	m.refreshing, m.queryGen = true, 5
	m.beginApproval([]prKey{key})
	msg := approvedMsg(key, nil)
	msg.outcome = approveAlreadyViewer

	m, cmd := updateModel(t, m, msg)

	require.NotNil(t, cmd)
	require.NotSame(t, oldCache, m.metadataCache)
	require.Equal(t, 6, m.queryGen)
	require.True(t, m.refreshing)
	require.True(t, isViewerApproved(m.rows[0].Item.PR))
}

func TestApproveMergeKeepsApprovedButSkipsPending(t *testing.T) {
	t.Run("already approved", func(t *testing.T) {
		pr := approvalTestPR(1)
		pr.viewerApproved = true
		h := newTUIHarness(t, listTUIModel(pr))
		h.Key("A").Wait("Approve & merge " + pr.Ref() + "?").Assert([]string{"No", "Yes"})
	})
	t.Run("pending", func(t *testing.T) {
		pr := approvalTestPR(1)
		m := listTUIModel(pr)
		m.beginApproval([]prKey{makePRKey(pr)})
		h := newTUIHarness(t, m)
		h.Key("A").Wait("Approval pending " + pr.Ref()).AssertNot("Approve & merge")
	})
}

func TestApproveMergeBlocksAnotherApprovalUntilCompletion(t *testing.T) {
	for _, view := range []tuiView{tuiViewList, tuiViewDiff} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			m := diffTestModel(approvalTestPR(1))
			m.view = view
			key := m.rowKeyAt(0)
			m, _ = pressKey(t, m, tuiKeybindApproveMerge)
			if view == tuiViewList {
				model, _ := m.confirmAccept()
				var ok bool
				m, ok = model.(tuiModel)
				require.True(t, ok)
			}

			require.Equal(t, resultApprovalPending, m.approvalBlockReason(key, m.rows[0].Item.PR))
			m, _ = updateModel(t, m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
			require.Equal(t, 1, m.approvalGen[key])
			require.False(t, m.confirmed.get(key).viewerApproved)
		})
	}
}

func TestApproveMergeFailureKeepsSuccessfulApproval(t *testing.T) {
	var posts atomic.Int32
	actions := approveTestRunner(t, false, &posts)
	actions.gql = nil
	// The runner accepts approval, but has no merge endpoint.
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(42))
	m.actions = actions
	key := m.rowKeyAt(0)
	m, _ = pressKey(t, m, tuiKeybindApproveMerge)
	model, cmd := m.confirmAccept()
	var ok bool
	m, ok = model.(tuiModel)
	require.True(t, ok)
	require.NotNil(t, cmd)

	m, _ = updateModel(t, m, cmd())

	require.EqualValues(t, 1, posts.Load())
	require.Empty(t, m.pendingApprovals)
	require.True(t, isViewerApproved(m.rows[0].Item.PR))
	require.False(t, isShown(m, key))
	require.True(t, m.flash.Err)
}

func TestApproveMergeBatchSettlesApprovalAndMergeFailures(t *testing.T) {
	var posts atomic.Int32
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(42), approvalTestPR(43))
	m.actions = approveTestRunner(t, false, &posts)
	m.actions.gql = nil
	k1, k2 := m.rowKeyAt(0), m.rowKeyAt(1)
	m.selected[k1], m.selected[k2] = true, true
	m, _ = pressKey(t, m, tuiKeybindApproveMerge)
	model, cmd := m.confirmAccept()
	var ok bool
	m, ok = model.(tuiModel)
	require.True(t, ok)
	require.Len(t, m.pendingApprovals, 2)

	m, _ = updateModel(t, m, cmd())

	require.EqualValues(t, 1, posts.Load())
	require.Empty(t, m.pendingApprovals)
	require.True(t, isViewerApproved(m.rows[0].Item.PR))
	require.False(t, isViewerApproved(m.rows[1].Item.PR))
	require.False(t, isShown(m, k1))
	require.True(t, isShown(m, k2))
	require.True(t, m.flash.Err)
}

// diffTestModel shows the first PR's diff with the rest queued.
func diffTestModel(prs ...PullRequest) tuiModel {
	m := approvalTestModel("", prs...)
	m.view = tuiViewDiff
	m.diffView = newScrollView()
	m.diffKey = makePRKey(prs[0])
	for _, pr := range prs[1:] {
		m.diffQueue = append(m.diffQueue, makePRKey(pr))
	}
	m.diffQueueTotal = len(prs)
	m.diffQueuePos = 1
	return m
}

func (m *tuiModel) cacheDiffForTest(key prKey, headSHA string) {
	if m.diffCache == nil {
		m.diffCache = make(map[prKey]diffFetchedMsg)
	}
	m.diffCache[key] = diffFetchedMsg{index: -1, key: key, headSHA: headSHA, fetchedAt: time.Now()}
}

func TestDiffApproveAdvancesInstantlyToPrefetchedDiff(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := diffTestModel(first, second)
	key := makePRKey(second)
	m.diffCache = map[prKey]diffFetchedMsg{key: {key: key, diff: "+next change", fetchedAt: time.Now()}}
	pending, release := holdTUIRequest()
	defer release()
	m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/repos/owner/repo/pulls/1/reviews" {
			return jsonResponse(req, http.StatusNotFound, `{}`), nil
		}
		<-pending
		return jsonResponse(req, http.StatusCreated, `{}`), nil
	})
	h := newTUIHarness(t, m)
	h.Key("a").Wait("[2/2] " + second.Ref()).Assert("+next change")
	release()
	h.Wait("Approved " + first.Ref()).Assert("[2/2] " + second.Ref())
}

func TestDiffApproveOnApprovedPRPromptsWithoutAdvancing(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := diffTestModel(pr, approvalTestPR(2))
	m.cacheDiffForTest(makePRKey(approvalTestPR(2)), "")
	h := newTUIHarness(t, m)
	h.Key("a").Wait("Unapprove " + pr.Ref() + "?").Assert(defaultUnapproveMessage)
	h.KeyEsc().WaitNot(defaultUnapproveMessage).Assert("[1/2] " + pr.Ref())
}

func TestDiffApproveOnLastPRReturnsToList(t *testing.T) {
	pr := approvalTestPR(1)
	m := diffTestModel(pr)
	m.cfg = &Config{}
	pending, release := holdTUIRequest()
	defer release()
	m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		<-pending
		return jsonResponse(req, http.StatusCreated, `{}`), nil
	})
	h := newTUIHarness(t, m)
	h.Wait("[1/1] " + pr.Ref()).Key("a").Wait("All PRs ▾").AssertNot("[1/1]")
	release()
	h.Wait("Approved " + pr.Ref())
}

func TestDiffApproveCompletionDoesNotNavigate(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2), approvalTestPR(3))
	k1, k2 := makePRKey(approvalTestPR(1)), makePRKey(approvalTestPR(2))
	m.cacheDiffForTest(k2, "")
	m, _ = pressKey(t, m, tuiKeybindApprove)
	require.Equal(t, k2, m.diffKey)

	msg := approvedMsg(k1, nil)
	msg.navigated = true
	m, _ = updateModel(t, m, msg)
	// An out-of-order second completion must not navigate either.
	m, _ = updateModel(t, m, msg)

	require.Equal(t, k2, m.diffKey)
	require.Equal(t, []prKey{makePRKey(approvalTestPR(3))}, m.diffQueue)
}

func TestDiffAdvanceWaitsForInFlightFetch(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2))
	k2 := makePRKey(approvalTestPR(2))
	m.diffInFlight = prKeys{k2: true}

	m, cmd := pressKey(t, m, tuiKeybindNext)

	require.Nil(t, cmd, "no duplicate fetch for an in-flight diff")
	require.True(t, m.diffLoading)
	require.Equal(t, k2, m.diffAwaitKey)

	m, _ = updateModel(t, m, diffFetchedMsg{index: -1, key: k2})
	require.Equal(t, k2, m.diffKey)
	require.False(t, m.diffLoading)
}

func TestDiffKeysIgnoredWhileNextDiffLoads(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := diffTestModel(first, second)
	m.cfg = &Config{}
	m.diffInFlight = prKeys{makePRKey(second): true}
	m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		t.Error("approval or duplicate fetch dispatched while loading the next diff")
		return jsonResponse(req, http.StatusBadRequest, `{}`), nil
	})
	h := newTUIHarness(t, m)
	h.Key("n").Wait("Diffing " + second.Ref()).Assert("[1/2] " + first.Ref() + " »")
	h.Key("a").KeyEsc().Wait("All PRs ▾").AssertNot("Approving")
}

func TestDiffPrefetchWithStaleHeadRefetches(t *testing.T) {
	pr2 := approvalTestPR(2)
	pr2.HeadSHA = "new"
	m := diffTestModel(approvalTestPR(1), pr2)
	k2 := makePRKey(pr2)
	m.cacheDiffForTest(k2, "old")

	m, cmd := pressKey(t, m, tuiKeybindNext)

	require.NotNil(t, cmd)
	require.True(t, m.diffLoading)
	require.Equal(t, k2, m.diffAwaitKey)
	require.True(t, m.diffInFlight[k2])
}

func TestDiffAwaitedPrefetchWithChangedHeadRefetches(t *testing.T) {
	pr2 := approvalTestPR(2)
	pr2.HeadSHA = "old"
	m := diffTestModel(approvalTestPR(1), pr2)
	k1, k2 := m.diffKey, makePRKey(pr2)
	_ = m.prefetchNext()
	m.updateConfirmed(k2, func(pr *PullRequest) { pr.HeadSHA = "new" })
	m, _ = pressKey(t, m, tuiKeybindNext)

	m, cmd := updateModel(t, m, diffFetchedMsg{
		index:            -1,
		key:              k2,
		headSHA:          "old",
		requestedHeadSHA: "old",
		diff:             "stale diff",
	})

	require.NotNil(t, cmd)
	require.True(t, m.diffLoading)
	require.Equal(t, k2, m.diffAwaitKey)
	require.Equal(t, k1, m.diffKey)
	require.True(t, m.diffInFlight[k2])
	require.Empty(t, m.diff)
}

func TestDiffFetchAcceptsHeadAheadOfUnchangedRow(t *testing.T) {
	pr := approvalTestPR(1)
	pr.HeadSHA = "old"
	m := diffTestModel(pr)
	key := m.diffKey
	_ = m.openDiff(key, 0)

	m, _ = updateModel(t, m, diffFetchedMsg{
		index: -1, key: key, headSHA: "new", requestedHeadSHA: "old",
	})

	require.False(t, m.diffLoading)
	require.Empty(t, m.diffAwaitKey)
	require.Equal(t, key, m.diffKey)
	require.Empty(t, m.diffInFlight)
}

func TestDiffFetchAfterExitCachesWithoutReopening(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2))
	k2 := makePRKey(approvalTestPR(2))
	m, _ = pressKey(t, m, tuiKeybindNext) // waiting on k2
	m, _ = pressKey(t, m, "q")            // leave the diff view
	require.Equal(t, tuiViewList, m.view)

	m, _ = updateModel(t, m, diffFetchedMsg{index: -1, key: k2})

	require.Equal(t, tuiViewList, m.view)
	require.Contains(t, m.diffCache, k2)
	require.Empty(t, m.diffAwaitKey)
}

func TestDiffPrevRecordsOutgoingKeyInHistory(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2))
	k1, k2 := makePRKey(approvalTestPR(1)), makePRKey(approvalTestPR(2))
	m.cacheDiffForTest(k2, "")
	m, _ = pressKey(t, m, tuiKeybindNext)
	require.Equal(t, []prKey{k1}, m.diffHistory)

	m, _ = pressKey(t, m, tuiKeybindPrev)

	require.Empty(t, m.diffHistory)
	require.Equal(t, []prKey{k2}, m.diffQueue)
	require.Equal(t, k1, m.diffAwaitKey)
}

func TestRenderPRStatusLine(t *testing.T) {
	pr := approvalTestPR(1)
	pr.MergeStatus = MergeStatusCIFailed
	pr.viewerApproved = true
	m := approvalTestModel("", pr)

	require.Equal(
		t,
		"CI failed · Review required · "+activeIcons.Check+" Approved by you",
		ansi.Strip(m.renderPRStatusLine(pr)),
	)

	// "Needs your review" already covers a required review.
	pr.viewerApproved = false
	require.Equal(
		t,
		"CI failed · Needs your review",
		ansi.Strip(m.renderPRStatusLine(pr)),
	)

	// ...and "Needs review".
	pr.MergeStatus = MergeStatusBlocked
	require.Equal(t, "Needs your review", ansi.Strip(m.renderPRStatusLine(pr)))

	// Once approved, "Needs review" means other reviewers are still needed.
	pr.viewerApproved = true
	require.Equal(
		t,
		"Needs review · "+activeIcons.Check+" Approved by you",
		ansi.Strip(m.renderPRStatusLine(pr)),
	)

	pr.IsDraft = true
	pr.Author.Login = "viewer"
	require.Equal(
		t,
		"Draft · Review required · Your PR",
		ansi.Strip(m.renderPRStatusLine(pr)),
	)
}

func TestRenderPRStatusLineReviewStates(t *testing.T) {
	for _, tt := range []struct {
		name    string
		prepare func(*PullRequest)
		want    string
	}{
		{
			name: "approved by another reviewer",
			prepare: func(pr *PullRequest) {
				pr.MergeStatus = MergeStatusReady
				pr.ReviewDecision = valueReviewApproved
			},
			want: "Ready to merge",
		},
		{
			name: "approved with failing CI",
			prepare: func(pr *PullRequest) {
				pr.MergeStatus = MergeStatusCIFailed
				pr.ReviewDecision = valueReviewApproved
			},
			want: "CI failed · Approved",
		},
		{
			name: "ready without required reviews",
			prepare: func(pr *PullRequest) {
				pr.MergeStatus = MergeStatusReady
				pr.ReviewDecision = ""
			},
			want: "Ready to merge",
		},
		{
			name: "ready with review decision unavailable",
			prepare: func(pr *PullRequest) {
				pr.MergeStatus = MergeStatusReady
				pr.reviewDecisionLoaded = false
			},
			want: "Ready to merge",
		},
		{
			name:    "draft",
			prepare: func(pr *PullRequest) { pr.IsDraft = true },
			want:    "Draft · Review required",
		},
		{
			name:    "changes requested",
			prepare: func(pr *PullRequest) { pr.ReviewDecision = valueReviewChanges },
			want:    "Changes requested",
		},
		{
			name: "changes requested after viewer approval",
			prepare: func(pr *PullRequest) {
				pr.ReviewDecision = valueReviewChanges
				pr.viewerApproved = true
			},
			want: "Changes requested · " + activeIcons.Check + " Approved by you",
		},
		{
			name: "changes requested with failing CI",
			prepare: func(pr *PullRequest) {
				pr.ReviewDecision = valueReviewChanges
				pr.MergeStatus = MergeStatusCIFailed
			},
			want: "CI failed · Changes requested",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pr := approvalTestPR(1)
			tt.prepare(&pr)
			m := approvalTestModel("", pr)

			require.Equal(t, tt.want, ansi.Strip(m.renderPRStatusLine(pr)))
		})
	}
}

func TestRenderPRStatusLineSpacesPills(t *testing.T) {
	useIcons(iconsNerd)
	t.Cleanup(func() { useIcons(iconsUnicode) })
	pr := approvalTestPR(1)
	pr.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", pr)

	want := m.renderDetailStatus(pr) + " " +
		statusBadge("Needs your review", "", colorGitHubAccent,
			lg.NewStyle().Foreground(colorGitHubAccent))
	require.Equal(t, want, m.renderPRStatusLine(pr))
}

func TestAlignHeaderTruncatesTitleBeforeStatus(t *testing.T) {
	m := tuiModel{width: 40}

	require.Equal(
		t,
		"owner/repo#1 » a long title tha…  Status",
		m.alignHeader("owner/repo#1 » a long title that keeps going", "Status"),
	)
	require.Equal(t, "short                             Status", m.alignHeader("short", "Status"))

	// Too narrow to keep a readable title: the status is dropped.
	m.width = 24
	require.Equal(
		t,
		"owner/repo#1 » a long …",
		m.alignHeader("owner/repo#1 » a long title that keeps going", "A very long status"),
	)
}

func TestCyclePreset(t *testing.T) {
	failing, passing := approvalTestPR(1), approvalTestPR(2)
	failing.Title, passing.Title = "Failing change", "Passing change"
	failing.MergeStatus = MergeStatusCIFailed
	m := listTUIModel(failing, passing)
	m.cli.preset = "Removed from config"
	h := newTUIHarness(t, m)
	h.Key("f").Wait("Needs your review ▾").Assert(passing.Title).AssertNot(failing.Title)
	for _, preset := range []string{"Waiting for CI", "Approved by you", "Mergeable", "Outdated", "Blocked", "All PRs"} {
		h.Key("f").Wait(preset + " ▾")
	}
	h.Assert([]string{failing.Title, passing.Title})
	h.Key("F").Wait("Blocked ▾").Assert(failing.Title).AssertNot(passing.Title)
}

func TestCyclePresetUsesConfiguredPresets(t *testing.T) {
	failing, passing := approvalTestPR(1), approvalTestPR(2)
	failing.Title, passing.Title = "Broken build", "Working build"
	failing.MergeStatus = MergeStatusCIFailed
	m := listTUIModel(failing, passing)
	m.cfg.TUI.Presets.Filters = []TUIPresetConfig{{Name: "Broken", Filter: "ci:failure"}}
	h := newTUIHarness(t, m)
	h.Key("f").Wait("Broken ▾").Assert(failing.Title).AssertNot(passing.Title)
	h.Key("f").Wait("All PRs ▾").Assert(passing.Title)
}

func TestLocalFiltersSurviveRefresh(t *testing.T) {
	failing := approvalTestPR(1)
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", approvalTestPR(2))
	m.cli.CI = ciFilterNotFailing

	fresh := []PRRowModel{{PR: failing}, {PR: approvalTestPR(2)}}
	m, _ = updateModel(t, m, refreshResultMsg{queryGen: m.queryGen, items: fresh})

	require.Len(t, m.rows, 2, "rows outside the CI filter are kept, hidden")
	require.False(t, isShown(m, makePRKey(failing)))
	require.True(t, isShown(m, makePRKey(approvalTestPR(2))))
}

func TestOverlayCIOnlyChangeAppliesWithoutRefetch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	failing := approvalTestPR(1)
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", failing, approvalTestPR(2))
	m.cfg = &Config{}
	m.showOptions = true
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Values[filterRowCI] = filterChoiceIndex(filterRowCI, ciFilterNotFailing)

	model, cmd := m.applyFilterOptions()
	m, ok := model.(tuiModel)
	require.True(t, ok)

	require.Nil(t, cmd)
	require.False(t, m.refreshing)
	require.Equal(t, ciFilterNotFailing, m.cli.CI)
	require.False(t, isShown(m, makePRKey(failing)))
}

func TestTagSeparatorSetsPresetApart(t *testing.T) {
	m := approvalTestModel("")
	m.cfg = &Config{}
	m.cli.preset = "Needs your review"
	m.cli.NoBot = true

	line := ansi.Strip(m.renderTagSeparator(m.activeFilterTags(), -1))

	require.Equal(
		t,
		"┤ bots:hide │ All Time ▾ │ Needs your review ▾ ├─",
		line[len(line)-len("┤ bots:hide │ All Time ▾ │ Needs your review ▾ ├─"):],
	)

	// Narrow: the option filters are shortened, the preset stays whole.
	m.width = 31
	m.cli.State = valueMerged
	line = ansi.Strip(m.renderTagSeparator(m.activeFilterTags(), -1))
	require.Equal(t, "┤ sta… │ Needs your review ▾ ├─", line)
}

func TestCyclePresetIsRestoredOnNextLaunch(t *testing.T) {
	newTUIHarness(t, listTUIModel(approvalTestPR(1))).Key("f").Wait("Needs your review ▾")

	st, err := loadTUIState()
	require.NoError(t, err)
	cfg := &Config{}
	applyTUIState(cfg, st)
	cli := testCLI()
	applyTUIFilterDefaults(cli, cfg)
	require.Equal(t, "Needs your review", cli.preset)
}

func TestDiffArrowKeysNavigateQueue(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := diffTestModel(first, second)
	k2 := makePRKey(second)
	m.diff = "+first change"
	m.diffCache = map[prKey]diffFetchedMsg{k2: {key: k2, diff: "+second change", fetchedAt: time.Now()}}
	var reads atomic.Int32
	firstFetch, release := holdTUIRequest()
	defer release()
	m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(req, http.StatusOK, `{"head":{"sha":"fixture"}}`), nil
	})
	m.actions.diffClient = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/owner/repo/pulls/1":
			reads.Add(1)
			<-firstFetch
			return jsonResponse(req, http.StatusOK, "+reloaded first change"), nil
		case "/repos/owner/repo/pulls/2":
			t.Error("refetched a cached diff")
			return jsonResponse(req, http.StatusBadRequest, ""), nil
		default:
			return jsonResponse(req, http.StatusNotFound, `{}`), nil
		}
	}).rest
	h := newTUIHarness(t, m)
	h.Wait("[1/2] " + first.Ref()).Assert("+first change")
	h.KeyRight().Wait("[2/2] " + second.Ref()).Wait("+second change").WaitNot("+first change")
	h.KeyLeft().Wait("Diffing " + first.Ref()).Assert("+second change")
	release()
	h.Wait("[1/2] " + first.Ref()).Wait("+reloaded first change")
	h.KeyRight().Wait("[2/2] " + second.Ref()).Wait("+second change")
	h.KeyLeft().Wait("[1/2] " + first.Ref()).Wait("+reloaded first change")
	require.EqualValues(t, 1, reads.Load())
}

func TestEmptyOverlayExplainsFilteredResults(t *testing.T) {
	failing := approvalTestPR(1)
	failing.Title, failing.MergeStatus = "Failing build", MergeStatusCIFailed
	h := newTUIHarness(t, listTUIModel(failing))
	h.Wait(failing.Title).Key("f").Wait("No pull requests match the current filter")
	h.SendProgram(refreshResultMsg{}).Wait("No pull requests found for the current query")
}

// TestWaitingForCIFollowsSearchedAuthors ties the authors the search query
// restricts to with the scope:self term in the default Waiting for CI preset.
func TestWaitingForCIFollowsSearchedAuthors(t *testing.T) {
	tests := []struct {
		name      string
		authors   []string
		teams     []string
		wantQuery string // author qualifiers in the query; empty skips the check
		wantSelf  bool
	}{
		{name: "self", authors: []string{"@me"}, wantQuery: "author:@me", wantSelf: true},
		{name: "self alias", authors: []string{"me"}, wantQuery: "author:@me", wantSelf: true},
		{name: "login", authors: []string{"ALICE"}, wantQuery: "author:ALICE", wantSelf: true},
		{
			name:      "self minus someone",
			authors:   []string{"@me", "!bob"},
			wantQuery: "author:@me -author:bob",
			wantSelf:  true,
		},
		{
			name:      "self and all",
			authors:   []string{"@me", "all"},
			wantQuery: "author:@me",
			wantSelf:  true,
		},
		{
			name:      "cancelled other author",
			authors:   []string{"@me", "bob", "!bob"},
			wantQuery: "author:@me",
			wantSelf:  true,
		},
		{name: "cancelled self", authors: []string{"@me", "!@me"}, wantSelf: false},
		{
			name:      "negated only",
			authors:   []string{"!bob"},
			wantQuery: "-author:bob",
			wantSelf:  false,
		},
		{name: "self and other", authors: []string{"@me", "bob"}, wantSelf: false},
		{name: "everyone", authors: []string{"all"}, wantSelf: false},
		{name: "team", authors: []string{"@me"}, teams: []string{"ops"}, wantSelf: false},
		{name: "negated team", authors: []string{"@me"}, teams: []string{"!ops"}, wantSelf: false},
		{
			name:      "negated team containing the viewer",
			authors:   []string{"alice"},
			teams:     []string{"!ops"},
			wantQuery: "-author:bob",
			wantSelf:  false,
		},
		{
			name:     "cancelled team",
			authors:  []string{"@me"},
			teams:    []string{"ops", "!ops"},
			wantSelf: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &CLI{Author: &CSVFlag{Values: tt.authors}, Team: CSVFlag{Values: tt.teams}}
			cli.Normalize(&Config{Default: Defaults{
				Limit:  defaultLimit,
				State:  valueOpen,
				Output: valueTable,
				Sort:   valueName,
				Match:  "title",
			}})
			if tt.wantQuery != "" {
				// Without a plugin on PATH, teams resolve from config.
				t.Setenv("PATH", "")
				params, err := buildSearchQuery(cli, &Config{
					Teams: map[string][]string{"ops": {"alice", "bob"}},
				})
				require.NoError(t, err)
				require.True(t, strings.HasSuffix(params.Query, " "+tt.wantQuery), params.Query)
			}

			pr := approvalTestPR(1)
			pr.Author.Login = "alice"
			pr.MergeStatus = MergeStatusCIPending
			m := approvalTestModel("", pr)
			m.login = "alice"
			m.cli = cli
			m.cli.preset = "Waiting for CI"

			require.Equal(t, tt.wantSelf, m.searchesSelf())
			require.Equal(t, tt.wantSelf, isShown(m, makePRKey(pr)))
		})
	}
}

func TestCyclePresetSkipsUnavailablePresets(t *testing.T) {
	pr := approvalTestPR(1)
	pr.Title, pr.Author.Login = "My change", "viewer"
	m := listTUIModel(pr)
	m.cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	m.cli.preset = "Needs your review"
	h := newTUIHarness(t, m)
	h.Wait("All PRs ▾").Assert(pr.Title).AssertNot("Needs your review ▾")
	for _, preset := range []string{"Waiting for CI", "Mergeable", "Outdated", "Blocked", "All PRs", "Waiting for CI"} {
		h.Key("f").Wait(preset + " ▾")
	}
}

func TestUnavailablePresetSurvivesStateSaveAndReturns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	saved := &Config{}
	saved.TUI.Filters.Preset = "Needs your review"
	require.NoError(t, saveTUIState(saved))

	st, err := loadTUIState()
	require.NoError(t, err)
	cfg := &Config{}
	applyTUIState(cfg, st)
	cli := testCLI()
	cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	applyTUIFilterDefaults(cli, cfg)
	require.Equal(t, "Needs your review", cli.preset)

	failing := approvalTestPR(1)
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", failing)
	m.cfg = cfg
	m.cli = cli
	key := makePRKey(failing)
	require.True(t, m.searchesSelf())
	require.True(t, isShown(m, key), "an unavailable preset filters nothing")

	m.optionsPicker = m.newFilterPicker()
	m.persistFilterState()
	st, err = loadTUIState()
	require.NoError(t, err)
	require.Equal(t, "Needs your review", st.Filters.Preset, "unrelated saves keep the preset")

	m.cli.Author = &CSVFlag{Values: []string{valueAll}}
	require.False(t, isShown(m, key), "the preset filters again once available")
}

func TestCyclePresetBackFromUnavailablePreset(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	m.cli.preset = "Needs your review"
	newTUIHarness(t, m).Key("F").Wait("Blocked ▾")
}

func TestCyclePresetWithNoAvailablePresets(t *testing.T) {
	m := listTUIModel(approvalTestPR(1))
	m.cfg.TUI.Presets.Filters = []TUIPresetConfig{
		{Name: "Mine", Filter: "mine", When: "scope:self"},
	}
	h := newTUIHarness(t, m)
	for _, key := range []string{"f", "F"} {
		h.Key(key).
			Locate(reel.Contains("All PRs ▾")).
			LeftClick().
			Wait("✓ All PRs").
			AssertNot("Mine")
		h.KeyEsc().WaitNot("✓ All PRs")
	}
}

func TestFailedApprovalProjectsLatestConfirmedRead(t *testing.T) {
	pr := approvalTestPR(1)
	m := approvalTestModel("", pr)
	key := makePRKey(pr)
	m.beginApproval([]prKey{key})
	require.False(t, m.confirmed.get(key).viewerApproved)
	pr.viewerApproved = true
	m, _ = updateModel(t, m, refreshResultMsg{items: buildPRRowModels([]PullRequest{pr}, "", nil)})
	m.settleApprovals([]approvalResult{{key: key, err: errApproveTest}})
	require.True(t, m.confirmed.get(key).viewerApproved)
	require.True(t, m.prForKey(key).viewerApproved)
}
