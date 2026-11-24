package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
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
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2))
	m.actions = NewActionRunner(nil, eventTestClient(t))
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

func eventTestClient(t *testing.T) *api.GraphQLClient {
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
				`{"data":{"mergeNodes":[{"id":"PR_node","state":"MERGED","updatedAt":"2025-08-11T10:00:00Z","headRefOid":"new-head"}]}}`,
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
		eventTestClient(t),
		testCLI(),
		&SearchParams{},
		prs,
		changed,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, []PullRequest{other}, got)
	got, err = refreshWatchEvents(
		eventTestClient(t),
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
