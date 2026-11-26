package main

import (
	"net/http"
	"strings"
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
			root:     bloom.New(),
			previous: "ABCDEFGH",
			from:     tc.from,
			progress: anim.NewValue(0.5),
		}
		require.Equal(t, tc.want, ansi.Strip(s.render(8, 1, "abcdefgh")))
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
	m.diffPrefetch = map[prKey]diffFetchedMsg{
		dest: {key: dest, diff: strings.Repeat(newLine+nl, 30)},
	}

	m, _ = pressKey(t, m, tuiKeybindNext)
	require.True(t, m.slide.progress.Animating())
	m.slide.progress.Play(0.5, 1)
	mid := strings.Split(ansi.Strip(m.View().Content), nl)
	m.slide = prSlide{}
	end := strings.Split(ansi.Strip(m.View().Content), nl)

	const bodyTop = 2 // diff header and separator
	bodyEnd := bodyTop + m.diffView.Height()
	require.Equal(t, end[:bodyTop], mid[:bodyTop])
	require.Equal(t, end[bodyEnd:], mid[bodyEnd:])
	sharedBody := oldLine[:bodyWidth/2] + newLine + strings.Repeat(" ", bodyWidth/2-len(newLine))
	for row := bodyTop; row < bodyEnd; row++ {
		require.Equal(t, sharedBody+end[row][bodyWidth:], mid[row])
	}
}

func TestDetailNavigationFollowsFilteredList(t *testing.T) {
	first, hidden, last := approvalTestPR(1), approvalTestPR(2), approvalTestPR(3)
	first.Title, hidden.Title, last.Title = "Visible A", "Hidden B", "Visible C"
	m := listTUIModel(first, hidden, last)
	m.sortColumn, m.sortAsc = colTitle, true
	m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/owner/repo/pulls/1":
			return jsonResponse(req, http.StatusOK, `{"body":"First description"}`), nil
		case "/repos/owner/repo/pulls/3":
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
}

func TestDismissedDetailNavigationIgnoresLateReply(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := listTUIModel(first, second)
	m.view, m.detailKey, m.detailView = tuiViewDetail, makePRKey(first), newScrollView()
	m.openDetail(m.resolveIndex(makePRKey(second), -1), fx.Right)
	late := detailFetchedMsg{id: m.detailRefreshID, key: makePRKey(second), from: fx.Right}
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m, _ = updateModel(t, m, late)
	require.Equal(t, tuiViewList, m.view)
	require.False(t, m.detailLoading)
	require.Empty(t, m.flash.Msg)
	require.False(t, m.slide.progress.Animating())
}

func TestPRSlideOddWidthHasNoGap(t *testing.T) {
	s := prSlide{root: bloom.New(), previous: "ABCDEFGHI", progress: anim.NewValue(0.5)}
	s.from = fx.Right
	require.Equal(t, "FGHIabcde", ansi.Strip(s.render(9, 1, "abcdefghi")))
	s.from = fx.Left
	require.Equal(t, "efghiABCD", ansi.Strip(s.render(9, 1, "abcdefghi")))
}

func TestPRSlideFrameRouting(t *testing.T) {
	m := diffTestModel(approvalTestPR(1))
	m.diff = strings.Repeat("line\n", 40)
	m.applyWindowSize(80, 12)
	oldFrame := m.startPRSlide(fx.Right, m.diffView, m.diffRenderLines)()
	m, _ = updateModel(t, m, scrollwheel.Msg[wheelTarget]{Target: wheelTargetDiff, Delta: 1})
	require.False(t, m.slide.progress.Animating())
	require.Positive(t, m.diffView.YOffset())

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
