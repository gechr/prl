package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestEventStreamsConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg, err := loadConfig()
	require.NoError(t, err)
	require.False(t, cfg.Features.EventStreams)
	require.Nil(t, newEventStreams(cfg.Features.EventStreams))

	path, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("features:\n  event_streams: true\n"), 0o600))
	cfg, err = loadConfig()
	require.NoError(t, err)
	require.True(t, cfg.Features.EventStreams)
}

func TestEventStreamsListenAndReconcile(t *testing.T) {
	dir := t.TempDir()
	// Emit the documented gh output, then stay attached until canceled.
	const script = `#!/bin/sh
[ "$1 $2 $3 $4" = "event listen --follow --resource" ] || exit 1
[ "$6" = "--event" ] && [ "$8 $9" = "--jq .type" ] || exit 1
case "$5" in
  */1) echo 'Listening for events.' >&2 ;;
  */2) echo 'Listening for events on a new subscription.' >&2 ;;
  *) echo 'com.github.pull_request.head.changed.v1' ;;
esac
exec /bin/sleep 60
`
	writeExecutable(t, dir, "gh", script)
	t.Setenv("PATH", dir)
	streams := newEventStreams(true)
	t.Cleanup(streams.close)

	var prs []PullRequest
	for i := 1; i <= maxEventStreams+1; i++ {
		pr := approvalTestPR(i)
		pr.URL = "https://github.com/owner/repo/pull/" + strconv.Itoa(i)
		prs = append(prs, pr)
	}
	streams.sync(prs)
	require.Len(t, streams.listeners, maxEventStreams)
	require.NotContains(t, streams.listeners, makePRKey(prs[maxEventStreams]))
	seen := make(prKeys)
	require.Eventually(t, func() bool {
		for key := range streams.takeChanges() {
			seen[key] = true
		}
		return len(seen) == maxEventStreams
	}, 2*watchMinInterval, 10*time.Millisecond)
	streams.sync(prs[1:])
	require.NotContains(t, streams.listeners, makePRKey(prs[0]))
	require.Contains(t, streams.listeners, makePRKey(prs[maxEventStreams]))
	streams.sync(nil)
	require.Empty(t, streams.takeChanges())
}

func TestTUIQueuesEventsDuringRefresh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2))
	m.actions = NewActionRunner(nil, eventTestClient(t, mergedEventNode))
	m.snapshotPRs = prItems(m.items)
	m.autoRefresh = true
	m.refreshing = true
	m.eventStreams = newEventStreams(true)
	t.Cleanup(m.eventStreams.close)
	key := m.rowKeyAt(0)
	m.eventStreams.listeners[key] = func() {}
	m.eventStreams.pending[key] = eventStatus
	m, _ = updateModel(t, m, prEventsMsg{})
	require.True(t, m.eventsPending)
	require.Zero(t, m.queryGen)

	m.refreshing = false
	cmd := m.refreshFromEvents()
	require.False(t, m.eventsPending)
	require.NotNil(t, cmd)
	msg, ok := cmd().(changedPRsMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
	require.Len(t, msg.items, 1)
	require.Equal(t, key, makePRKey(msg.items[0].PR))
	require.False(t, m.refreshing)
	require.Zero(t, m.queryGen)
}

func TestUnavailableEventStreamKeepsPeriodicRefresh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	pr := approvalTestPR(1)
	m := approvalTestModel("", pr)
	m.autoRefresh = true
	m.eventStreams = newEventStreams(true)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.sync([]PullRequest{pr})
	m.eventStreams.workers.Wait()
	require.Empty(t, m.eventStreams.takeChanges())

	m, cmd := updateModel(t, m, refreshTickMsg{id: m.refreshID})
	require.True(t, m.refreshing)
	require.NotNil(t, cmd)
}

func TestTUIEventClearsConflictWithPollingOff(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "gh", "#!/bin/sh\necho '"+eventCheckSuite+"'\nexec /bin/sleep 60\n")
	t.Setenv("PATH", dir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pr := approvalTestPR(1)
	pr.MergeStatus = MergeStatusConflict
	m := approvalTestModel("", pr)
	m.cfg = &Config{}
	m.tty = true
	m.cli.Columns.Values = []string{colRef}
	m.eventStreams = newEventStreams(true)
	t.Cleanup(m.eventStreams.close)
	m, attach := updateModel(
		t,
		m,
		refreshResultMsg{items: buildPRRowModels([]PullRequest{pr}, "", nil)},
	)
	require.Empty(t, m.eventStreams.listeners)
	require.NotNil(t, attach)
	m, _ = updateModel(t, m, attach())
	require.Contains(t, m.eventStreams.listeners, makePRKey(pr))
	require.Equal(
		t,
		ansi.Force().Hyperlink(pr.URL, styleHeading.Render(pr.Ref())),
		m.rows[0].Cells[0].Text,
	)

	m, _ = pressKey(t, m, tuiKeybindToggleRefresh)
	require.True(t, m.autoRefresh)
	m, _ = pressKey(t, m, tuiKeybindToggleRefresh)
	require.False(t, m.autoRefresh)
	require.Contains(t, m.eventStreams.listeners, makePRKey(pr))
	select {
	case <-m.eventStreams.wake:
	case <-time.After(3 * time.Second):
		t.Fatal("missing stream notification")
	}
	const clean = `{"id":"PR_node","state":"OPEN","updatedAt":"2025-08-11T10:00:00Z","mergeStateStatus":"CLEAN"}`
	m.actions = NewActionRunner(nil, eventTestClient(t, clean))
	m, cmd := updateModel(t, m, prEventsMsg{})
	require.True(t, m.changedRefreshInFlight)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	// The first command waits for the next event, the second refreshes this PR.
	require.Len(t, batch, 2)
	m, cmd = updateModel(t, m, batch[1]())
	require.Equal(t, MergeStatusReady, m.snapshotPRs[0].MergeStatus)
	require.Equal(
		t,
		ansi.Force().Hyperlink(pr.URL, m.p.theme.BoldGreen.Render(pr.Ref())),
		m.rows[0].Cells[0].Text,
	)
	require.Nil(t, cmd)
}

func TestTUIEventStreamsFollowFilteredViewport(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var prs []PullRequest
	for n := 1; n <= maxEventStreams+1; n++ {
		pr := approvalTestPR(n)
		pr.URL = "https://github.com/owner/repo/pull/" + strconv.Itoa(n)
		prs = append(prs, pr)
	}
	target := prs[0]
	target.Title = "Distinctive title"
	prs[0] = target
	m := approvalTestModel("", prs...)
	m.filterInput = textinput.New()
	m.cli.Columns.Values = []string{colTitle}
	m.items = buildPRRowModels(prs, "", nil)
	m.rerenderList()
	m.height = 7
	m.cursor, m.offset = 0, 0
	m.eventStreams = newEventStreams(true)
	t.Cleanup(m.eventStreams.close)
	m, _ = pressKey(t, m, "/")
	initial := eventAttachMsg{gen: m.eventAttachGen}
	m, _ = updateModel(t, m, initial)
	first := m.rowKeyAt(m.visibleIndices()[0])
	require.Len(t, m.eventStreams.listeners, m.listViewport())
	require.Contains(t, m.eventStreams.listeners, first)
	require.NotContains(t, m.eventStreams.listeners, makePRKey(target))
	m.eventStreams.queue(t.Context(), first, eventStatus)

	m, _ = updateModel(t, m, tea.PasteMsg{Content: target.Title})
	m, _ = updateModel(t, m, initial)
	require.Empty(t, m.eventStreams.listeners)
	m, _ = updateModel(t, m, eventAttachMsg{gen: m.eventAttachGen})
	require.Len(t, m.eventStreams.listeners, 1)
	require.Contains(t, m.eventStreams.listeners, makePRKey(target))
	require.Empty(t, m.eventStreams.pending)

	m, _ = updateModel(t, m, tea.PasteMsg{Content: " without a match"})
	require.Empty(t, m.eventStreams.listeners)
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	intermediate := eventAttachMsg{gen: m.eventAttachGen}
	m, _ = pressKey(t, m, tuiKeybindTop)
	m, _ = updateModel(t, m, intermediate)
	require.Empty(t, m.eventStreams.listeners)
	m, _ = updateModel(t, m, eventAttachMsg{gen: m.eventAttachGen})
	require.NotContains(t, m.eventStreams.listeners, makePRKey(target))
	require.Contains(t, m.eventStreams.listeners, first)
}

func TestTUIReviewPreservesPendingHeadProbe(t *testing.T) {
	pr := approvalTestPR(1)
	m := approvalTestModel("", pr)
	m.eventStreams = newEventStreams(true)
	t.Cleanup(m.eventStreams.close)
	key := makePRKey(pr)
	canceled := false
	m.eventStreams.listeners[key] = func() { canceled = true }
	m.eventStreams.pending[key] = eventStatus | eventFirstHeadProbe
	m.view = tuiViewDetail
	m, _ = updateModel(t, m, prEventsMsg{})
	require.True(t, m.eventsPending)
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	require.Equal(t, tuiViewList, m.view)
	require.False(t, canceled)
	require.Equal(t, prEvents{key: eventStatus | eventFirstHeadProbe}, m.eventStreams.takeChanges())
}

func TestTUIKeepsListeningAfterStreamFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, polling := range []bool{false, true} {
		t.Run("polling="+strconv.FormatBool(polling), func(t *testing.T) {
			pr := approvalTestPR(1)
			m := approvalTestModel("", pr)
			m.autoRefresh = polling
			m.eventStreams = newEventStreams(true)
			t.Cleanup(m.eventStreams.close)
			m.eventStreams.sync([]PullRequest{pr, approvalTestPR(2)})
			m.eventStreams.workers.Wait()
			require.Len(t, m.eventStreams.failed, 1)
			m, cmd := updateModel(t, m, m.eventStreams.wait())
			require.NotNil(t, cmd)
			require.Equal(t, !polling, m.flash.Err)
			if !polling {
				require.Equal(
					t,
					"Event stream unavailable for a PR (press R for periodic refresh)",
					m.flash.Msg,
				)
			}
			m.eventStreams.wake <- struct{}{}
			next := cmd()
			if batch, ok := next.(tea.BatchMsg); ok {
				for _, command := range batch {
					next = command()
					if _, received := next.(prEventsMsg); received {
						break
					}
				}
			}
			require.IsType(t, prEventsMsg{}, next)
		})
	}
}

func TestEventStreamsBatchBursts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newEventStreams(true)
		defer s.close()
		first, second := prKey("one"), prKey("two")
		s.listeners[first], s.listeners[second] = func() {}, func() {}
		s.queue(t.Context(), first, eventStatus)
		s.queue(t.Context(), first, eventStatus)
		s.queue(t.Context(), second, eventStatusAndReviews)
		s.queue(t.Context(), second, eventStatus)
		require.Empty(t, s.takeChanges())
		require.IsType(t, prEventsMsg{}, s.wait())
		require.Equal(
			t,
			prEvents{first: eventStatus, second: eventStatusAndReviews},
			s.takeChanges(),
		)

		s.queue(t.Context(), first, eventStatus)
		time.Sleep(watchMinInterval - time.Millisecond)
		require.Empty(t, s.takeChanges())
		require.IsType(t, prEventsMsg{}, s.wait())
		require.Equal(t, prEvents{first: eventStatus}, s.takeChanges())
	})
}

const eventStatusFields = "id headRefOid mergeStateStatus reviewDecision " +
	"commits(last:1){nodes{commit{statusCheckRollup{state} checkSuites(first:50){totalCount nodes{conclusion checkRuns(first:1){totalCount}}}}}} " +
	"state isDraft updatedAt"

func TestHeadEventCIProbes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newEventStreams(true)
		defer s.close()
		pr := approvalTestPR(1)
		pr.awaitingChecks = true
		key := makePRKey(pr)
		s.listeners[key] = func() {}
		head := eventStatusAndReviews | eventNewHead
		s.queue(t.Context(), key, head)
		s.wait()
		s.followUpHead(pr, s.takeChanges()[key])

		start := time.Now()
		for range 6 {
			time.Sleep(time.Second)
			s.queue(t.Context(), key, head)
			require.Empty(t, s.takeChanges())
		}
		s.wait()
		require.Equal(t, watchMinInterval, time.Since(start))
		events := s.takeChanges()
		require.Equal(t, head|eventFirstHeadProbe, events[key])
		s.followUpHead(pr, events[key])

		for _, want := range []prEventScope{eventStatus | eventFirstHeadProbe, eventStatus} {
			s.wait()
			events = s.takeChanges()
			require.Equal(t, prEvents{key: want}, events)
			s.followUpHead(pr, events[key])
		}
		require.Empty(t, s.pending)

		pr.awaitingChecks = false
		s.followUpHead(pr, head)
		require.Empty(t, s.pending)
	})
}

func TestHeadEventRechecksPersistentConflict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newEventStreams(true)
		defer s.close()
		pr := approvalTestPR(1)
		pr.MergeStatus = MergeStatusConflict
		key := makePRKey(pr)
		s.listeners[key] = func() {}

		s.followUpHead(pr, eventNewHead)
		for _, want := range []prEventScope{eventStatus | eventFirstHeadProbe, eventStatus} {
			s.wait()
			events := s.takeChanges()
			require.Equal(t, prEvents{key: want}, events)
			s.followUpHead(pr, events[key])
		}
		require.Empty(t, s.pending)
	})
}

func TestDetachedCIProbeIsDiscarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newEventStreams(true)
		defer s.close()
		pr := approvalTestPR(1)
		pr.awaitingChecks = true
		key := makePRKey(pr)
		s.listeners[key] = func() {}
		s.nextRead = time.Now().Add(watchMinInterval)
		s.followUpHead(pr, eventNewHead)
		timer := s.timer
		s.sync(nil)

		// The same PR returns before the pending timer fires.
		s.listeners[key] = func() {}
		s.queue(t.Context(), key, eventStatusAndReviews)
		require.Same(t, timer, s.timer)
		require.Empty(t, s.takeChanges())
		s.wait()
		require.True(t, time.Now().Equal(s.nextRead))
		require.Equal(t, prEvents{key: eventStatusAndReviews}, s.takeChanges())
	})
}

const mergedEventNode = `{"id":"PR_node","state":"MERGED","updatedAt":"2025-08-11T10:00:00Z","headRefOid":"new-head"}`

func eventTestClient(t *testing.T, node string) *api.GraphQLClient {
	t.Helper()
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		Host: "github.com", AuthToken: "test",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := decodeGraphQLBody(t, readBody(t, req.Body))
			require.Equal(t, []any{"PR_node"}, body.Variables["mergeIDs"])
			require.Equal(
				t,
				"query ListMetadata($mergeIDs: [ID!]!){mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{"+eventStatusFields+" autoMergeRequest{enabledAt}}}}",
				body.Query,
			)
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"mergeNodes":[`+node+`]}}`,
			), nil
		}),
	})
	require.NoError(t, err)
	return gql
}

func TestWatchEventReadsOnlyChangedStatus(t *testing.T) {
	pr := approvalTestPR(1)
	pr.Title = "Original title"
	other := approvalTestPR(2)
	other.NodeID = "other"
	prs := []PullRequest{pr, other}
	changed := prEvents{makePRKey(pr): eventStatus}
	got, err := refreshWatchEvents(
		eventTestClient(t, mergedEventNode),
		testCLI(),
		&SearchParams{},
		prs,
		changed,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, []PullRequest{other}, got)
	got, err = refreshWatchEvents(
		eventTestClient(t, mergedEventNode),
		testCLI(),
		&SearchParams{PRRefs: []prRef{{Number: 1}}},
		prs,
		changed,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, row := range got {
		if row.Number == pr.Number {
			require.Equal(t, valueMerged, row.State)
			require.Equal(t, pr.Title, row.Title)
			require.Equal(t, "new-head", row.HeadSHA)
		} else {
			require.Equal(t, other, row)
		}
	}
	require.Equal(t, valueOpen, prs[0].State)
}

func TestMixedEventBatchRequestsOnlyAffectedViewerReviews(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	first.NodeID, second.NodeID = "review", "check"
	first.State = valueClosed
	first.Title = "Keep title"
	second.viewerApproved = true
	prs := []PullRequest{first, second}
	events := prEvents{makePRKey(first): eventStatusAndReviews, makePRKey(second): eventStatus}
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		Host: "github.com", AuthToken: "test",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := decodeGraphQLBody(t, readBody(t, req.Body))
			require.Equal(t, []any{"review", "check"}, body.Variables["mergeIDs"])
			require.Equal(t, []any{"review"}, body.Variables["viewerReviewIDs"])
			require.Equal(
				t,
				"query ListMetadata($mergeIDs: [ID!]!, $viewerReviewIDs: [ID!]!){mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{"+eventStatusFields+"}} viewer{login} viewerReviewNodes:nodes(ids:$viewerReviewIDs){... on PullRequest{id latestOpinionatedReviews(last:100){nodes{author{login} state}}}}}",
				body.Query,
			)
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"mergeNodes":[{"id":"review","state":"OPEN","updatedAt":"2025-08-11T10:00:00Z","headRefOid":"new","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED"},{"id":"check","state":"OPEN","updatedAt":"2025-08-11T10:00:00Z","mergeStateStatus":"DIRTY"}],"viewer":{"login":"tester"},"viewerReviewNodes":[{"id":"review","latestOpinionatedReviews":{"nodes":[{"author":{"login":"tester"},"state":"APPROVED"}]}}]}}`,
			), nil
		}),
	})
	require.NoError(t, err)
	require.NoError(
		t,
		refreshEventFields(
			gql,
			prs,
			events,
			listMetadataRequest{mergeStatus: true, viewerApproval: true},
		),
	)
	require.Equal(t, valueOpen, prs[0].State)
	require.Equal(t, first.Title, prs[0].Title)
	require.True(t, prs[0].viewerApproved)
	require.Equal(t, MergeStatusConflict, prs[1].MergeStatus)
	require.True(t, prs[1].viewerApproved)
}
