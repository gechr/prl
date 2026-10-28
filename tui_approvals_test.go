package main

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
		rows:          rows,
		removed:       make(prKeys),
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

	m.beginApproval([]prKey{key})

	require.False(t, m.filterHidden[key])
	require.Equal(t, activeIcons.Check+" ", ansi.Strip(m.rowGutter(0)))
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
	m.items, m.rows = m.items[1:], m.rows[1:]

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
	m.items, m.rows = m.items[1:], m.rows[1:]

	m, _ = updateModel(t, m, approvedMsg(key, nil))

	require.Empty(t, m.pendingApprovals)
	require.False(t, m.refreshing)
	require.Equal(t, "Approved owner/repo#1", ansi.Strip(m.flash.Msg))
}

func TestApprovalSuccessSwapsCacheAndRestartsInFlightRefresh(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)
	oldCache := m.metadataCache
	m.refreshing = true
	m.queryGen = 5
	m.beginApproval([]prKey{key})

	m, cmd := updateModel(t, m, approvedMsg(key, nil))

	require.NotNil(t, cmd)
	require.NotSame(t, oldCache, m.metadataCache)
	require.Equal(t, 6, m.queryGen)
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
	m, _ = updateModel(t, m, prStatusMsg{key: key, gen: m.approvalGen[key], pr: unapproved})
	require.False(t, m.rows[0].Item.PR.viewerApproved)
}

func TestListApproveSkipsAlreadyApproved(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := approvalTestModel("", pr)

	m, _ = pressKey(t, m, tuiKeybindApprove)

	require.Empty(t, m.confirmAction)
	require.Empty(t, m.pendingApprovals)
	require.Equal(t, "Already approved owner/repo#1", ansi.Strip(m.flash.Msg))
}

func TestListApproveSkipsPending(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.beginApproval([]prKey{makePRKey(m.rows[0].Item.PR)})

	m, _ = pressKey(t, m, tuiKeybindApprove)

	require.Empty(t, m.confirmAction)
	require.Equal(t, "Approval pending owner/repo#1", ansi.Strip(m.flash.Msg))
}

func TestListApproveWithUnloadedViewerStateStillApproves(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApprovalLoaded = false // e.g. a cached snapshot from an older run
	m := approvalTestModel("", pr)

	m, _ = pressKey(t, m, tuiKeybindApprove)

	require.Equal(t, tuiActionApprove, m.confirmAction)
}

func TestListApproveConfirmBeginsOnAccept(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1), approvalTestPR(2))
	key := makePRKey(m.rows[0].Item.PR)

	m, _ = pressKey(t, m, tuiKeybindApprove)
	require.Equal(t, tuiActionApprove, m.confirmAction)
	require.Empty(t, m.pendingApprovals)
	require.False(t, m.filterHidden[key])

	model, cmd := m.confirmAccept()
	require.NotNil(t, cmd)
	m, ok := model.(tuiModel)
	require.True(t, ok)
	require.Contains(t, m.pendingApprovals, key)
	require.True(t, m.filterHidden[key])
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
			result, ok := completion.(actionMsg)
			require.True(t, ok)
			require.NoError(t, result.err)
			require.EqualValues(t, 1, posts.Load())
			m.view, m.diffKey = tuiViewDiff, k2
			m.diffQueue = []prKey{k3}

			m, _ = updateModel(t, m, completion)

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
	require.True(t, isViewerApproved(m.rows[0].Item.PR))
}

func TestApproveMergeKeepsApprovedButSkipsPending(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := approvalTestModel("", pr)

	got, _ := pressKey(t, m, tuiKeybindApproveMerge)
	require.Equal(t, tuiActionApproveMerge, got.confirmAction)

	m = approvalTestModel("", approvalTestPR(1))
	m.beginApproval([]prKey{makePRKey(m.rows[0].Item.PR)})
	got, _ = pressKey(t, m, tuiKeybindApproveMerge)
	require.Empty(t, got.confirmAction)
	require.Equal(t, "Approval pending owner/repo#1", ansi.Strip(got.flash.Msg))
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
			require.False(t, m.pendingApprovals[key].prev.viewerApproved)
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
	return m
}

func (m *tuiModel) cacheDiffForTest(key prKey, headSHA string) {
	if m.diffPrefetch == nil {
		m.diffPrefetch = make(map[prKey]diffFetchedMsg)
	}
	m.diffPrefetch[key] = diffFetchedMsg{gen: m.diffGen, index: -1, key: key, headSHA: headSHA}
}

func TestDiffApproveAdvancesInstantlyToPrefetchedDiff(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2))
	k1, k2 := makePRKey(approvalTestPR(1)), makePRKey(approvalTestPR(2))
	m.cacheDiffForTest(k2, "")

	m, cmd := pressKey(t, m, tuiKeybindApprove)

	require.NotNil(t, cmd)
	require.Equal(t, tuiViewDiff, m.view)
	require.Equal(t, k2, m.diffKey)
	require.False(t, m.diffLoading)
	require.Equal(t, []prKey{k1}, m.diffHistory)
	require.Contains(t, m.pendingApprovals, k1)
	require.Empty(t, m.diffPrefetch)
}

func TestDiffApproveOnApprovedPRAdvancesWithoutSending(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := diffTestModel(pr, approvalTestPR(2))
	k2 := makePRKey(approvalTestPR(2))
	m.cacheDiffForTest(k2, "")

	m, _ = pressKey(t, m, tuiKeybindApprove)

	require.Empty(t, m.pendingApprovals)
	require.Equal(t, k2, m.diffKey)
	require.Equal(t, "Already approved owner/repo#1", ansi.Strip(m.flash.Msg))
}

func TestDiffApproveOnLastPRReturnsToList(t *testing.T) {
	m := diffTestModel(approvalTestPR(1))

	m, _ = pressKey(t, m, tuiKeybindApprove)

	require.Equal(t, tuiViewList, m.view)
	require.Contains(t, m.pendingApprovals, makePRKey(approvalTestPR(1)))
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

	m, _ = updateModel(t, m, diffFetchedMsg{gen: m.diffGen, index: -1, key: k2})
	require.Equal(t, k2, m.diffKey)
	require.False(t, m.diffLoading)
}

func TestDiffKeysIgnoredWhileNextDiffLoads(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2), approvalTestPR(3))
	m, _ = pressKey(t, m, tuiKeybindNext)
	require.True(t, m.diffLoading)

	m, _ = pressKey(t, m, tuiKeybindApprove)

	require.Empty(t, m.pendingApprovals, "approve must not land on the previous diff")
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
	m.updatePR(k2, func(item *PRRowModel) { item.PR.HeadSHA = "new" })
	m, _ = pressKey(t, m, tuiKeybindNext)

	m, cmd := updateModel(t, m, diffFetchedMsg{
		gen:              m.diffGen,
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
	_ = m.openDiff(key)

	m, _ = updateModel(t, m, diffFetchedMsg{
		gen: m.diffGen, index: -1, key: key, headSHA: "new", requestedHeadSHA: "old",
	})

	require.False(t, m.diffLoading)
	require.Empty(t, m.diffAwaitKey)
	require.Equal(t, key, m.diffKey)
	require.Empty(t, m.diffInFlight)
}

func TestDiffFetchFromDismissedSessionIsDropped(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2))
	k2 := makePRKey(approvalTestPR(2))
	oldGen := m.diffGen
	m, _ = pressKey(t, m, tuiKeybindNext) // waiting on k2
	m, _ = pressKey(t, m, "q")            // leave the diff view
	require.Equal(t, tuiViewList, m.view)

	m, _ = updateModel(t, m, diffFetchedMsg{gen: oldGen, index: -1, key: k2})

	require.Equal(t, tuiViewList, m.view)
	require.Empty(t, m.diffPrefetch)
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
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	failing := approvalTestPR(1)
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", failing, approvalTestPR(2))
	m.cfg = &Config{}
	key := makePRKey(failing)

	m, cmd := pressKey(t, m, tuiKeybindCyclePreset)
	require.Nil(t, cmd, "presets filter locally, without a refetch")
	require.False(t, m.refreshing)
	require.Equal(t, "Approvable", m.cli.preset)
	require.Equal(t, "Approvable", m.cfg.TUI.Filters.Preset)
	require.False(t, isShown(m, key), "failing CI is not approvable")
	require.Equal(t, "Approvable", m.activeFilterTags()[0])

	var seen []string
	for range defaultPresets {
		m, _ = pressKey(t, m, tuiKeybindCyclePreset)
		seen = append(seen, m.cli.preset)
	}
	require.Equal(
		t,
		[]string{"Waiting for CI", "Approved by you", "Mergeable", "Outdated", "Blocked", ""},
		seen,
	)
	require.Empty(t, m.cfg.TUI.Filters.Preset)
	require.True(t, isShown(m, key), "no preset shows every row again")

	m, _ = pressKey(t, m, tuiKeybindCyclePresetBack)
	require.Equal(t, "Blocked", m.cli.preset)
	require.True(t, isShown(m, key))
	require.False(t, isShown(m, makePRKey(approvalTestPR(2))))

	// An unknown active preset counts as "no preset".
	m.cli.preset = "Removed from config"
	m, _ = pressKey(t, m, tuiKeybindCyclePreset)
	require.Equal(t, "Approvable", m.cli.preset)
}

func TestCyclePresetUsesConfiguredPresets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	failing := approvalTestPR(1)
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", failing, approvalTestPR(2))
	m.cfg = &Config{}
	m.cfg.TUI.Presets.Filters = []TUIPresetConfig{{Name: "Broken", Filter: "ci:failure"}}

	m, _ = pressKey(t, m, tuiKeybindCyclePreset)

	require.Equal(t, "Broken", m.cli.preset)
	require.True(t, isShown(m, makePRKey(failing)))
	require.False(t, isShown(m, makePRKey(approvalTestPR(2))))

	m.refreshing = false
	m, _ = pressKey(t, m, tuiKeybindCyclePreset)
	require.Empty(t, m.cli.preset)
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
	m.cli.preset = "Approvable"
	m.cli.NoBot = true

	line := ansi.Strip(m.renderTagSeparator(m.activeFilterTags(), -1))

	require.Equal(
		t,
		" bots:hide · Approvable ──",
		line[len(line)-len(" bots:hide · Approvable ──"):],
	)

	// Narrow: the option filters are shortened, the preset stays whole.
	m.width = 22
	m.cli.State = valueMerged
	line = ansi.Strip(m.renderTagSeparator(m.activeFilterTags(), -1))
	require.Equal(t, " stat… · Approvable ──", line)
}

func TestCyclePresetIsRestoredOnNextLaunch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}

	_, _ = pressKey(t, m, tuiKeybindCyclePreset)

	st, err := loadTUIState()
	require.NoError(t, err)
	cfg := &Config{}
	applyTUIState(cfg, st)
	cli := testCLI()
	applyTUIFilterDefaults(cli, cfg)
	require.Equal(t, "Approvable", cli.preset)
}

func TestDiffArrowKeysNavigateQueue(t *testing.T) {
	m := diffTestModel(approvalTestPR(1), approvalTestPR(2))
	k1, k2 := makePRKey(approvalTestPR(1)), makePRKey(approvalTestPR(2))
	m.cacheDiffForTest(k2, "")

	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, k2, m.diffKey)
	require.Equal(t, []prKey{k1}, m.diffHistory)

	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Empty(t, m.diffHistory)
	require.Equal(t, k1, m.diffAwaitKey)
}

func TestEmptyOverlayExplainsFilteredResults(t *testing.T) {
	failing := approvalTestPR(1)
	failing.MergeStatus = MergeStatusCIFailed
	m := approvalTestModel("", failing)
	m.filterInput = textinput.New()
	m.cfg = &Config{}
	m.cli.preset = "Approvable"
	require.Equal(t, "No pull requests match the current filter", emptyOverlayHeadline(m))

	m.removed[makePRKey(failing)] = true
	require.Equal(t, "All pull requests have been processed", emptyOverlayHeadline(m))

	m.rows, m.items, m.removed = nil, nil, make(prKeys)
	require.Equal(t, "No pull requests found for the current query", emptyOverlayHeadline(m))
}

func emptyOverlayHeadline(m tuiModel) string {
	for line := range strings.SplitSeq(ansi.Strip(m.renderEmptyOverlay()), "\n") {
		if text := strings.Trim(line, "│ "); text != "" && !strings.HasPrefix(text, "╭") {
			return text
		}
	}
	return ""
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
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	pr := approvalTestPR(1)
	pr.Author.Login = "viewer"
	m := approvalTestModel("", pr)
	m.cfg = &Config{}
	m.cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	require.True(t, m.searchesSelf())

	var seen []string
	for range defaultPresets {
		m, _ = pressKey(t, m, tuiKeybindCyclePreset)
		seen = append(seen, m.cli.preset)
	}
	require.Equal(
		t,
		[]string{"Waiting for CI", "Mergeable", "Outdated", "Blocked", "", "Waiting for CI"},
		seen,
	)

	// A saved preset the search rules out applies no filter and shows no tag,
	// but stays saved for searches it suits.
	m.cli.preset = "Approvable"
	require.True(t, isShown(m, makePRKey(pr)))
	require.NotContains(t, m.activeFilterTags(), "Approvable")
	m.cli.Author = &CSVFlag{Values: []string{valueAll}}
	require.Equal(t, "Approvable", m.activeFilterTags()[0])
}

func TestUnavailablePresetSurvivesStateSaveAndReturns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	saved := &Config{}
	saved.TUI.Filters.Preset = "Approvable"
	require.NoError(t, saveTUIState(saved))

	st, err := loadTUIState()
	require.NoError(t, err)
	cfg := &Config{}
	applyTUIState(cfg, st)
	cli := testCLI()
	cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	applyTUIFilterDefaults(cli, cfg)
	require.Equal(t, "Approvable", cli.preset)

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
	require.Equal(t, "Approvable", st.Filters.Preset, "unrelated saves keep the preset")

	m.cli.Author = &CSVFlag{Values: []string{valueAll}}
	require.False(t, isShown(m, key), "the preset filters again once available")
}

func TestCyclePresetBackFromUnavailablePreset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.cli.Author = &CSVFlag{Values: []string{valueAtMe}}
	m.cli.preset = "Approvable"

	m, _ = pressKey(t, m, tuiKeybindCyclePresetBack)
	require.Equal(t, "Blocked", m.cli.preset)
}

func TestCyclePresetWithNoAvailablePresets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	m := approvalTestModel("", approvalTestPR(1))
	m.cfg = &Config{}
	m.cfg.TUI.Presets.Filters = []TUIPresetConfig{
		{Name: "Mine", Filter: "mine", When: "scope:self"},
	}
	require.False(t, m.searchesSelf())

	m, _ = pressKey(t, m, tuiKeybindCyclePreset)
	require.Empty(t, m.cli.preset)
	m, _ = pressKey(t, m, tuiKeybindCyclePresetBack)
	require.Empty(t, m.cli.preset)
}
