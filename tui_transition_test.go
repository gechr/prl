package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bloomfx.dev/bloom"
	"bloomfx.dev/bloom/anim"
	"bloomfx.dev/bloom/fx"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/scrollwheel"
	"github.com/stretchr/testify/require"
)

func TestPRSlideMovesBothPages(t *testing.T) {
	for _, tc := range []struct {
		from fx.Direction
		want string
	}{
		{fx.Right, "EFGHabcd"},
		{fx.Left, "efghABCD"},
	} {
		s := prSlide{
			previous: "ABCDEFGH",
			from:     tc.from,
			progress: anim.NewValue(0.5),
		}
		require.Equal(
			t,
			tc.want,
			ansi.Strip(bloom.Render(8, 1, s.layout(8, 1, bloom.Text("abcdefgh")))),
		)
	}
}

func TestDiffSlideKeepsFrameStationary(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := diffTestModel(first, second)
	const bodyWidth = 80
	oldLine := strings.Repeat("A", bodyWidth)
	newLine := "short"
	m.diff = strings.Repeat(oldLine+nl, 30)
	m.applyWindowSize(bodyWidth+1, 12)
	dest := makePRKey(second)
	m.diffCache = map[prKey]diffFetchedMsg{
		dest: {key: dest, diff: strings.Repeat(newLine+nl, 30), fetchedAt: time.Now()},
	}

	m, _ = pressKey(t, m, tuiKeybindNext)
	require.True(t, m.slide.progress.Animating())
	m.slide.progress.Play(0.5, 1)
	mid := strings.Split(ansi.Strip(m.View().Content), nl)
	m.slide = prSlide{}
	end := strings.Split(ansi.Strip(m.View().Content), nl)

	const bodyTop = 2 // diff header and separator
	bodyEnd := bodyTop + m.diffView.height
	require.Equal(t, end[:bodyTop], mid[:bodyTop])
	require.Equal(t, end[bodyEnd:], mid[bodyEnd:])
	sharedBody := oldLine[:bodyWidth/2] + newLine + strings.Repeat(" ", bodyWidth/2-len(newLine))
	for row := bodyTop; row < bodyEnd; row++ {
		require.Equal(t, sharedBody+end[row][bodyWidth:], mid[row])
	}
}

func TestDetailNavigationFollowsFilteredList(t *testing.T) {
	first, hidden, last := approvalTestPR(1), approvalTestPR(2), approvalTestPR(3)
	var firstReads, lastReads atomic.Int32
	first.Title, hidden.Title, last.Title = "Visible A", "Hidden B", "Visible C"
	m := listTUIModel(first, hidden, last)
	m.sortColumn, m.sortAsc = colTitle, true
	m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/owner/repo/pulls/1":
			firstReads.Add(1)
			return jsonResponse(req, http.StatusOK, `{"body":"First description"}`), nil
		case "/repos/owner/repo/pulls/3":
			lastReads.Add(1)
			return jsonResponse(req, http.StatusOK, `{"body":"Last description"}`), nil
		default:
			if strings.HasSuffix(req.URL.Path, "/files") ||
				strings.HasSuffix(req.URL.Path, "/reviews") {
				return jsonResponse(req, http.StatusOK, `[]`), nil
			}
			return jsonResponse(req, http.StatusNotFound, `{}`), nil
		}
	})
	h := newTUIHarness(t, m)
	h.Key("/").
		Paste("Visible").
		Key("enter").
		Wait("❯ 1  Visible A").
		Key("enter").
		Wait("First description")
	h.KeyLeft().KeyRight().Wait("Last description").WaitNot("First description")
	h.Key("n").Key("p").Wait("First description").WaitNot("Last description")
	h.Key("n").Wait("Last description").KeyEsc().Wait("❯ 2  Visible C").AssertNot(hidden.Title)
	require.EqualValues(t, 1, firstReads.Load())
	require.EqualValues(t, 1, lastReads.Load())
}

func TestDismissedDetailNavigationIgnoresLateReply(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := listTUIModel(first, second)
	m.view, m.detailKey = tuiViewDetail, makePRKey(first)
	m.openDetail(m.resolveIndex(makePRKey(second), -1), fx.Right)
	late := detailFetchedMsg{reviewVersion: m.reviewVersion(second), key: makePRKey(second)}
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m, _ = updateModel(t, m, late)
	require.Equal(t, tuiViewList, m.view)
	require.False(t, m.detailLoading)
	require.Empty(t, m.flash.Msg)
	require.False(t, m.slide.progress.Animating())
}

func TestPRSlideOddWidthHasNoGap(t *testing.T) {
	s := prSlide{previous: "ABCDEFGHI", progress: anim.NewValue(0.5)}
	s.from = fx.Right
	require.Equal(
		t,
		"FGHIabcde",
		ansi.Strip(bloom.Render(9, 1, s.layout(9, 1, bloom.Text("abcdefghi")))),
	)
	s.from = fx.Left
	require.Equal(
		t,
		"efghiABCD",
		ansi.Strip(bloom.Render(9, 1, s.layout(9, 1, bloom.Text("abcdefghi")))),
	)
}

func TestPRSlideFrameRouting(t *testing.T) {
	m := diffTestModel(approvalTestPR(1))
	m.diff = strings.Repeat("line\n", 40)
	m.applyWindowSize(80, 12)
	oldFrame := m.startPRSlide(fx.Right, m.diffView)()
	m, _ = updateModel(t, m, scrollwheel.Msg[wheelTarget]{Target: wheelTargetDiff, Delta: 1})
	require.False(t, m.slide.progress.Animating())
	require.Positive(t, m.diffView.offset())

	now := time.Unix(0, 0)
	m.slide.progress = anim.NewValue(
		0,
		anim.Duration(prSlideDuration),
		anim.Clock(func() time.Time { return now }),
	)
	ownFrame := m.slide.progress.To(1)()
	now = now.Add(prSlideDuration)
	m, cmd := updateModel(t, m, oldFrame)
	require.Nil(t, cmd)
	require.True(t, m.slide.progress.Animating())
	m, _ = updateModel(t, m, ownFrame)
	require.False(t, m.slide.progress.Animating())
}

func TestDetailNeighborsSkipFoldedStack(t *testing.T) {
	m := newStackTestModel(t)
	m.toggleStack(m.cursorStackKey())
	visible := m.visibleIndices()
	require.Len(t, visible, 2)
	m.detailKey = m.rowKeyAt(visible[1])
	prev, next := m.detailNeighbors()
	require.Equal(t, -1, prev)
	require.Equal(t, -1, next)
}

func TestDialogDismissalPreservesPendingDetail(t *testing.T) {
	m := listTUIModel(approvalTestPR(1), approvalTestPR(2))
	m.view, m.detailKey, m.detailLoading = tuiViewDetail, m.rowKeyAt(0), true
	m.confirmAction, m.confirmPrompt = tuiActionInfo, "Request completed"
	m.syncConfirmDialog()
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Empty(t, m.confirmAction)
	require.Equal(t, tuiViewDetail, m.view)
	require.True(t, m.detailLoading)
}

func TestActionCompletionPreservesPendingPreviousDiff(t *testing.T) {
	first, second, third := approvalTestPR(1), approvalTestPR(2), approvalTestPR(3)
	m := diffTestModel(second, third, first)
	prev := makePRKey(first)
	m.diffHistory, m.diffQueue = []prKey{prev}, []prKey{makePRKey(third)}
	m.diffInFlight = prKeys{prev: true}
	m, _ = pressKey(t, m, tuiKeybindPrev)
	require.Equal(t, prev, m.diffAwaitKey)
	m, _ = updateModel(t, m, actionMsg{key: makePRKey(second), action: tuiActionCommented})
	require.Equal(t, prev, m.diffAwaitKey)
	require.Equal(t, []prKey{makePRKey(second), makePRKey(third)}, m.diffQueue)
}

func TestMissingAwaitedDiffClearsLoadingStatus(t *testing.T) {
	m := diffTestModel(approvalTestPR(1))
	key := makePRKey(approvalTestPR(2))
	m.diffLoading, m.diffAwaitKey, m.flash.Msg = true, key, "Loading"
	m, _ = updateModel(t, m, diffFetchedMsg{key: key, index: -1})
	require.False(t, m.diffLoading)
	require.Empty(t, m.flash.Msg)
	require.False(t, m.slide.progress.Animating())
}

func TestDetailRefreshAfterEventKeepsCachedNavigation(t *testing.T) {
	m := listTUIModel(approvalTestPR(1), approvalTestPR(2))
	first, second := m.rows[0].Item.PR, m.rows[1].Item.PR
	k1, k2 := makePRKey(first), makePRKey(second)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.listeners[k1] = func() {}
	result := func(pr PullRequest, body string) detailFetchedMsg {
		return detailFetchedMsg{
			reviewVersion: m.reviewVersion(pr), fetchedAt: time.Now(),
			key: makePRKey(pr), detail: PRDetail{Body: body},
		}
	}
	m.openDetail(m.resolveIndex(k1, -1), 0)
	m, _ = updateModel(t, m, result(first, "First body"))
	require.True(t, m.detailInFlight[k2], "next detail was prefetched")
	m.openDetail(m.resolveIndex(k2, -1), fx.Right)
	m, _ = updateModel(t, m, result(second, "Second body"))
	m.eventStreams.pending[k1] = eventReviews
	m, _ = updateModel(t, m, prEventsMsg{})
	m.openDetail(m.resolveIndex(k1, -1), fx.Left)
	require.False(t, m.detailLoading)
	require.Equal(t, "First body", m.detail.Body)
	require.True(t, m.detailInFlight[k1])

	// Another event arrives before the background read completes.
	stale := result(first, "Superseded body")
	m.eventStreams.pending[k1] = eventStatus
	m, _ = updateModel(t, m, prEventsMsg{})
	m, _ = updateModel(t, m, stale)
	require.True(t, m.detailInFlight[k1])
	require.Equal(t, "First body", m.detail.Body)
	m, _ = updateModel(t, m, result(first, "Current body"))
	require.Equal(t, "Current body", m.detail.Body)
	require.False(t, m.detailInFlight[k1])
	m.exitDetailView()
	m.openDetail(m.resolveIndex(k1, -1), 0)
	require.Equal(t, "Current body", m.detail.Body)
	require.False(t, m.detailInFlight[k1])
}

func TestDiffHeadEventRefreshesOnlyChangedContent(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := diffTestModel(first, second)
	k1, k2 := makePRKey(first), makePRKey(second)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.listeners[k1] = func() {}
	m.openDiff(k1, 0)
	m, _ = updateModel(t, m, diffFetchedMsg{key: k1, diff: "+first"})
	m, _ = updateModel(t, m, diffFetchedMsg{key: k2, diff: "+second"})
	m.eventStreams.pending[k1] = eventReviews
	m, _ = updateModel(t, m, prEventsMsg{})
	require.Empty(t, m.diffInFlight)

	m.eventStreams.pending[k1] = eventNewHead
	m, _ = updateModel(t, m, prEventsMsg{})
	require.True(t, m.diffInFlight[k1])
	require.False(t, m.diffLoading)
	m, _ = pressKey(t, m, tuiKeybindNext)
	m, _ = pressKey(t, m, tuiKeybindPrev)
	require.Equal(t, k1, m.diffKey)
	require.False(t, m.diffLoading)
	require.Equal(t, "+first"+nl, ansi.Strip(m.diff))
	m, _ = pressKey(t, m, tuiKeybindNext)
	m, _ = updateModel(t, m, diffFetchedMsg{key: k1, headGen: m.diffHeadGen[k1], diff: "+updated"})
	require.Equal(t, k2, m.diffKey)
	m, _ = pressKey(t, m, tuiKeybindPrev)
	require.Equal(t, "+updated"+nl, ansi.Strip(m.diff))
	m.exitDiffView()
	m.openDiff(k1, 0)
	require.Equal(t, "+updated"+nl, ansi.Strip(m.diff))
	require.Empty(t, m.diffInFlight)
}

func TestOutdatedDiffBlocksReviewActions(t *testing.T) {
	for _, binding := range []string{
		tuiKeybindApprove, tuiKeybindApproveNoConfirm, tuiKeybindMerge, tuiKeybindApproveMerge,
	} {
		t.Run(binding, func(t *testing.T) {
			pr := approvalTestPR(1)
			m := diffTestModel(pr)
			key := makePRKey(pr)
			m.openDiff(key, 0)
			m, _ = updateModel(t, m, diffFetchedMsg{key: key, diff: "+initial"})
			m.invalidateDiffHead(key)
			m.openDiff(key, 0)
			m, _ = pressKey(t, m, binding)
			require.Zero(t, m.actionsInFlight)
			require.Equal(t, tuiViewDiff, m.view)
			require.True(t, m.diffInFlight[key])
			m, _ = updateModel(
				t,
				m,
				diffFetchedMsg{key: key, headGen: m.diffHeadGen[key], err: errApproveTest},
			)
			m, _ = pressKey(t, m, binding)
			require.Zero(t, m.actionsInFlight)
			require.True(t, m.diffInFlight[key], "retry the failed refresh")
			m, _ = updateModel(
				t,
				m,
				diffFetchedMsg{key: key, headGen: m.diffHeadGen[key], diff: "+current"},
			)
			m, _ = pressKey(t, m, binding)
			require.Equal(t, 1, m.actionsInFlight)
		})
	}
}

func TestSubscribedReviewRevalidatesContentAfterQuietInterval(t *testing.T) {
	pr := approvalTestPR(1)
	pr.HeadSHA = "same"
	key := makePRKey(pr)
	m := listTUIModel(pr)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.listeners[key] = func() {}
	old := time.Now().Add(-quietStatusInterval)
	m.detailCache = map[prKey]detailFetchedMsg{key: {
		reviewVersion: m.reviewVersion(pr), fetchedAt: old, detail: PRDetail{Body: "Earlier body"},
	}}
	m.openDetail(0, 0)
	require.Equal(t, "Earlier body", m.detail.Body)
	require.True(t, m.detailInFlight[key])
	m.diffCache = map[prKey]diffFetchedMsg{key: {
		key: key, headSHA: pr.HeadSHA, diff: "+earlier", fetchedAt: old, visited: true,
	}}
	m.openDiff(key, 0)
	require.Equal(t, "+earlier", m.diff)
	require.True(t, m.diffInFlight[key])
}

func TestFailedDiffNavigationKeepsRefreshedPatch(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := diffTestModel(first, second)
	active := makePRKey(first)
	m.openDiff(active, 0)
	m, _ = updateModel(t, m, diffFetchedMsg{key: active, diff: "+old"})
	m.invalidateDiffHead(active)
	m.refreshCachedDiff(active)
	m, _ = pressKey(t, m, tuiKeybindNext)
	m, _ = updateModel(
		t,
		m,
		diffFetchedMsg{key: active, headGen: m.diffHeadGen[active], diff: "+fresh"},
	)
	m, _ = updateModel(t, m, diffFetchedMsg{key: makePRKey(second), err: errApproveTest})
	require.False(t, m.diffLoading)
	require.Equal(t, active, m.diffKey)
	require.Equal(t, "+fresh"+nl, ansi.Strip(m.diff))
}

func TestFailedDetailNavigationResumesStaleRefresh(t *testing.T) {
	m := listTUIModel(approvalTestPR(1), approvalTestPR(2))
	first, second := m.rows[0].Item.PR, m.rows[1].Item.PR
	active := makePRKey(first)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.listeners[active] = func() {}
	detail := func(body string) detailFetchedMsg {
		return detailFetchedMsg{
			key: active, reviewVersion: m.reviewVersion(first),
			fetchedAt: time.Now(), detail: PRDetail{Body: body},
		}
	}
	m.openDetail(0, 0)
	m, _ = updateModel(t, m, detail("Original"))
	m.eventStreams.pending[active] = eventReviews
	m, _ = updateModel(t, m, prEventsMsg{})
	superseded := detail("Intermediate")
	m.eventStreams.pending[active] = eventReviews
	m, _ = updateModel(t, m, prEventsMsg{})
	m, _ = pressKey(t, m, tuiKeybindNext)
	m, _ = updateModel(t, m, superseded)
	require.True(t, m.detailLoading)
	require.False(t, m.detailInFlight[active])

	m, _ = updateModel(t, m, detailFetchedMsg{
		key: makePRKey(second), reviewVersion: m.reviewVersion(second), err: errApproveTest,
	})
	require.False(t, m.detailLoading)
	require.Equal(t, active, m.detailKey)
	require.Equal(t, "Original", m.detail.Body)
	require.True(t, m.detailInFlight[active])
	m, _ = updateModel(t, m, detail("Current"))
	require.Equal(t, "Current", m.detail.Body)
	require.False(t, m.detailInFlight[active])
}

func TestFailedDiffNavigationResumesStaleRefresh(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	active := makePRKey(first)
	m := diffTestModel(first, second)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.listeners[active] = func() {}
	m.openDiff(active, 0)
	m, _ = updateModel(t, m, diffFetchedMsg{key: active, diff: "+old"})
	m.eventStreams.pending[active] = eventNewHead
	m, _ = updateModel(t, m, prEventsMsg{})
	superseded := diffFetchedMsg{key: active, headGen: m.diffHeadGen[active], diff: "+intermediate"}
	m.eventStreams.pending[active] = eventNewHead
	m, _ = updateModel(t, m, prEventsMsg{})
	m, _ = pressKey(t, m, tuiKeybindNext)
	m, _ = updateModel(t, m, superseded)
	require.True(t, m.diffLoading)
	require.False(t, m.diffInFlight[active])

	m, _ = updateModel(t, m, diffFetchedMsg{key: makePRKey(second), err: errApproveTest})
	require.False(t, m.diffLoading)
	require.Equal(t, active, m.diffKey)
	require.Equal(t, "+old"+nl, ansi.Strip(m.diff))
	require.True(t, m.diffInFlight[active])
	m, _ = updateModel(
		t,
		m,
		diffFetchedMsg{key: active, headGen: m.diffHeadGen[active], diff: "+current"},
	)
	require.Equal(t, "+current"+nl, ansi.Strip(m.diff))
	require.False(t, m.reviewOutdated())
}
