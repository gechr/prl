package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	cliapi "github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/pkg/cmd/event/client"
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
	require.Nil(t, newEventStreams(cfg.Features.EventStreams, nil))

	path, err := configPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("features:\n  event_streams: true\n"), 0o600))
	cfg, err = loadConfig()
	require.NoError(t, err)
	require.True(t, cfg.Features.EventStreams)
}

func TestEventStreamsListenAndReconcile(t *testing.T) {
	streams := testEventStreams(http.StatusCreated)
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
	m.gql = eventTestClient(t, mergedEventNode, eventStatus)
	m.autoRefresh = true
	m.refreshing = true
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	key := m.rowKeyAt(0)
	m.eventStreams.listeners[key] = func() {}
	m.eventStreams.pending[key] = eventStatus
	m, _ = updateModel(t, m, prEventsMsg{})
	require.NotEmpty(t, m.pendingEvents)
	require.Zero(t, m.queryGen)

	m.refreshing = false
	cmd := m.refreshFromEvents()
	require.Empty(t, m.pendingEvents)
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
	pr := approvalTestPR(1)
	m := approvalTestModel("", pr)
	m.autoRefresh = true
	m.eventStreams = testEventStreams(http.StatusForbidden)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.sync([]PullRequest{pr})
	m.eventStreams.workers.Wait()
	require.Empty(t, m.eventStreams.takeChanges())

	m, cmd := updateModel(t, m, refreshTickMsg{id: m.refreshID})
	require.True(t, m.refreshing)
	require.NotNil(t, cmd)
}

func TestTUIEventClearsConflictWithPollingOff(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pr := approvalTestPR(1)
	pr.MergeStatus = MergeStatusConflict
	m := approvalTestModel("", pr)
	m.cfg = &Config{}
	m.tty = true
	m.cli.Columns.Values = []string{colRef}
	m.eventStreams = testEventStreams(http.StatusCreated)
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
	m.gql = eventTestClient(t, clean, eventStatusAndReviews)
	m, cmd := updateModel(t, m, prEventsMsg{})
	require.True(t, m.changedRefreshInFlight)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	// The first command waits for the next event, the second refreshes this PR.
	require.Len(t, batch, 2)
	m, cmd = updateModel(t, m, batch[1]())
	require.Equal(t, MergeStatusReady, m.confirmed.prs[0].MergeStatus)
	require.Equal(
		t,
		ansi.Force().Hyperlink(pr.URL, m.p.theme.BoldGreen.Render(pr.Ref())),
		m.rows[0].Cells[0].Text,
	)
	require.Nil(t, cmd)
}

func TestTUIEventStreamsFollowFilteredViewport(t *testing.T) {
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
	m.eventStreams = testEventStreams(http.StatusForbidden)
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
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	key := makePRKey(pr)
	canceled := false
	m.eventStreams.listeners[key] = func() { canceled = true }
	m.eventStreams.pending[key] = eventStatus | eventFirstHeadProbe
	m.view = tuiViewDetail
	m, _ = updateModel(t, m, prEventsMsg{})
	require.NotEmpty(t, m.pendingEvents)
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	require.Equal(t, tuiViewList, m.view)
	require.False(t, canceled)
	require.Equal(t, prEvents{key: eventStatus | eventFirstHeadProbe}, m.pendingEvents)
}

func TestTUIKeepsListeningAfterStreamFailure(t *testing.T) {
	for _, polling := range []bool{false, true} {
		t.Run("polling="+strconv.FormatBool(polling), func(t *testing.T) {
			pr := approvalTestPR(1)
			m := approvalTestModel("", pr)
			m.autoRefresh = polling
			m.eventStreams = testEventStreams(http.StatusForbidden)
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
		s := newEventStreams(true, nil)
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
		s := newEventStreams(true, nil)
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
		s := newEventStreams(true, nil)
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
		s := newEventStreams(true, nil)
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

func eventTestClient(t *testing.T, node string, scope prEventScope) *api.GraphQLClient {
	t.Helper()
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		Host: "github.com", AuthToken: "test",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := decodeGraphQLBody(t, readBody(t, req.Body))
			require.Equal(t, []any{"PR_node"}, body.Variables["mergeIDs"])
			vars := "$mergeIDs: [ID!]!"
			fields := "mergeNodes:nodes(ids:$mergeIDs){... on PullRequest{" + eventStatusFields + " autoMergeRequest{enabledAt}}}"
			if scope&eventReviews != 0 {
				require.Equal(t, []any{"PR_node"}, body.Variables["viewerReviewIDs"])
				vars += ", $viewerReviewIDs: [ID!]!"
				fields += " viewer{login} viewerReviewNodes:nodes(ids:$viewerReviewIDs){... on PullRequest{id latestOpinionatedReviews(last:100){nodes{author{login} state}}}}"
			}
			require.Equal(t, "query ListMetadata("+vars+"){"+fields+"}", body.Query)
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
		eventTestClient(t, mergedEventNode, eventStatus),
		testCLI(),
		&SearchParams{},
		prs,
		changed,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, []PullRequest{other}, got)
	got, err = refreshWatchEvents(
		eventTestClient(t, mergedEventNode, eventStatus),
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

func testEventStreams(createStatus int) *eventStreams {
	streams := newEventStreams(true, nil)
	streams.client = func() (client.EventClient, error) {
		return client.NewEventClient(
			&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					return jsonResponse(req, createStatus, `{"id":"one"}`), nil
				}
				resp := jsonResponse(req, http.StatusOK, "event: "+eventCheckSuite+"\ndata: {}\n\n")
				resp.Header.Set("Content-Type", "text/event-stream")
				return resp, nil
			})},
			"github.com",
		), nil
	}
	return streams
}

func TestEventStreamReconnectAndRenew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const resource = "https://github.com/owner/repo/pull/9"
		const end = "event: github.stream.end\ndata: "
		var requests []string
		start := time.Now()
		var lastAttach time.Time
		transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, req.Method+" "+req.URL.Path)
			if req.Method == http.MethodPost {
				var subscription struct {
					Resource string   `json:"resource_url"`
					Events   []string `json:"event_types"`
				}
				require.NoError(t, json.NewDecoder(req.Body).Decode(&subscription))
				require.Equal(t, resource, subscription.Resource)
				require.ElementsMatch(t, pullRequestEvents, subscription.Events)
			} else {
				require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
			}
			switch len(requests) {
			case 1:
				resp := jsonResponse(
					req,
					http.StatusServiceUnavailable,
					`{"message":"retry later"}`,
				)
				resp.Header.Set(headerRetryAfter, "10")
				return resp, nil
			case 2:
				require.GreaterOrEqual(t, time.Since(start), 10*time.Second)
				return jsonResponse(req, http.StatusCreated, `{"id":"first"}`), nil
			case 7:
				return jsonResponse(req, http.StatusOK, `{"id":"second"}`), nil
			}
			var body string
			switch len(requests) {
			case 3:
			case 4:
				// First attach after creation has its own backoff.
				require.Less(t, time.Since(lastAttach), 1500*time.Millisecond)
			case 5:
				require.GreaterOrEqual(t, time.Since(lastAttach), 2*time.Second)
				body = ": heartbeat\n\nevent: " + eventHead + "\ndata: {\"type\":\"" + eventHead + "\"}\n\n" +
					"event: " + eventReview + "\ndata: {\"type\":\"" + eventReview + "\"}\n\n"
			case 6:
				require.Less(t, time.Since(lastAttach), 1500*time.Millisecond)
				body = end + `{"outcome":"subscription_ended","reason":"expired"}` + "\n\n"
			case 8:
				body = "event: " + eventConflict + "\ndata: {}\n\n" + end + `{"outcome":"subscription_ended","reason":"access_revoked"}` + "\n\n"
			default:
				t.Fatalf("unexpected request %d", len(requests))
			}
			lastAttach = time.Now()
			resp := jsonResponse(req, http.StatusOK, body)
			resp.Header.Set("Content-Type", "text/event-stream")
			return resp, nil
		})
		streams := newEventStreams(true, nil)
		streams.client = func() (client.EventClient, error) {
			return client.NewEventClient(&http.Client{Transport: transport}, "github.com"), nil
		}
		var changes []prEventScope
		err := streams.listenPREvents(t.Context(), resource, func(scope prEventScope) {
			changes = append(changes, scope)
		})
		require.EqualError(t, err, "GitHub ended the event subscription (access_revoked)")
		require.Equal(t, []string{
			"POST /event-subscriptions",
			"POST /event-subscriptions",
			"GET /event-subscriptions/first/events",
			"GET /event-subscriptions/first/events",
			"GET /event-subscriptions/first/events",
			"GET /event-subscriptions/first/events",
			"POST /event-subscriptions",
			"GET /event-subscriptions/second/events",
		}, requests)
		require.Equal(t, []prEventScope{
			eventStatusAndReviews, eventStatusAndReviews, eventStatusAndReviews,
			eventStatusAndReviews | eventNewHead, eventStatusAndReviews,
			eventStatusAndReviews, eventStatusAndReviews, eventStatus,
		}, changes)
	})
}

func TestEventStreamMissingSubscriptionRenewsOnce(t *testing.T) {
	var methods []string
	c := client.NewEventClient(
		&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			methods = append(methods, req.Method)
			require.LessOrEqual(t, len(methods), 4)
			if req.Method == http.MethodPost {
				return jsonResponse(req, http.StatusCreated, `{"id":"missing"}`), nil
			}
			return jsonResponse(req, http.StatusNotFound, `{"message":"not found"}`), nil
		})},
		"github.com",
	)
	streams := newEventStreams(true, nil)
	streams.client = func() (client.EventClient, error) { return c, nil }
	err := streams.listenPREvents(
		t.Context(),
		"https://github.com/owner/repo/pull/9",
		func(prEventScope) {
			t.Fatal("received a notification before attachment")
		},
	)
	require.ErrorIs(t, err, client.ErrSubscriptionNotFound)
	require.Equal(t, []string{"POST", "GET", "POST", "GET"}, methods)
}

func TestEventStreamEndOutcomes(t *testing.T) {
	require.NoError(t, streamEndError(`{"outcome":"reconnect"}`))
	require.NoError(t, streamEndError("{"))
	require.EqualError(
		t,
		streamEndError(`{"outcome":"unexpected"}`),
		"GitHub returned an unknown event stream outcome",
	)
	require.ErrorIs(t, streamEndError(`{"outcome":"subscription_ended"}`), errRenewSubscription)
	require.ErrorIs(
		t,
		streamEndError(`{"outcome":"subscription_ended","reason":"another_reason"}`),
		errRenewSubscription,
	)
	require.EqualError(
		t,
		streamEndError(`{"outcome":"subscription_ended","reason":"deleted"}`),
		"GitHub ended the event subscription (deleted)",
	)
}

func TestEventStreamIdleReconnectAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var opens int
		mock := &client.EventClientMock{
			CreateSubscriptionFunc: func(context.Context, string, []string) (*client.Subscription, bool, error) {
				return &client.Subscription{ID: "one"}, true, nil
			},
			OpenStreamFunc: func(ctx context.Context, _ string) (io.ReadCloser, error) {
				opens++
				r, w := io.Pipe()
				context.AfterFunc(ctx, func() { _ = w.Close() })
				if opens == 1 {
					go func() {
						for range 2 {
							time.Sleep(30 * time.Second)
							if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
								return
							}
						}
					}()
				}
				return r, nil
			},
		}
		streams := newEventStreams(true, nil)
		streams.client = func() (client.EventClient, error) { return mock, nil }
		start := time.Now()
		err := streams.listenPREvents(
			ctx,
			"https://github.com/owner/repo/pull/9",
			func(prEventScope) {
				if opens == 2 {
					cancel()
				}
			},
		)
		require.ErrorIs(t, err, context.Canceled)
		require.Len(t, mock.CreateSubscriptionCalls(), 1)
		require.Equal(t, 2, opens)
		require.GreaterOrEqual(t, time.Since(start), 105*time.Second)
		require.Less(t, time.Since(start), 107*time.Second)
	})
}

func TestEventClientStreamsWithEnvironmentAuthentication(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	t.Setenv("GH_TOKEN", "test")
	t.Setenv("GH_DEBUG", "api")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var streamWriter *io.PipeWriter
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		authenticated := req.Header.Get("Authorization") == "token test"
		require.True(t, authenticated)
		if req.Method == http.MethodPost {
			return jsonResponse(req, http.StatusOK, `{"id":"one"}`), nil
		}
		require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
		resp := jsonResponse(req, http.StatusOK, "")
		resp.Header.Set("Content-Type", "text/event-stream")
		reader, writer := io.Pipe()
		resp.Body, streamWriter = reader, writer
		context.AfterFunc(req.Context(), func() { _ = writer.Close() })
		return resp, nil
	})
	c, err := newEventClient()
	require.NoError(t, err)
	sub, _, err := c.CreateSubscription(
		ctx,
		"https://github.com/owner/repo/pull/9",
		pullRequestEvents,
	)
	require.NoError(t, err)
	body, err := c.OpenStream(ctx, sub.ID)
	require.NoError(t, err)
	defer body.Close()
	require.NoError(t, ctx.Err())
	require.NoError(t, streamWriter.Close())
}

func TestEventBackoffRetriesAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backoff := eventBackoff(time.Second)
		start := time.Now()
		err := backoff.wait(t.Context(), cliapi.HTTPError{HTTPError: &api.HTTPError{
			StatusCode: http.StatusTooManyRequests,
			Headers:    http.Header{headerRetryAfter: []string{"8"}},
		}})
		require.NoError(t, err)
		require.GreaterOrEqual(t, time.Since(start), 8*time.Second)
		require.Less(t, time.Since(start), 12*time.Second)

		invalid := cliapi.HTTPError{
			HTTPError: &api.HTTPError{StatusCode: http.StatusUnprocessableEntity},
		}
		require.ErrorIs(t, backoff.wait(t.Context(), invalid), invalid)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(time.Second, cancel)
		start = time.Now()
		err = backoff.wait(ctx, &url.Error{Op: "GET", Err: io.ErrUnexpectedEOF})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, time.Second, time.Since(start))
	})
}

func TestReviewSubscribesToPRBeyondViewport(t *testing.T) {
	m := numberedListTestModel(maxEventStreams + 2)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.syncVisibleEventStreams(nil)
	last := m.rows[len(m.rows)-1].Item.PR
	key := makePRKey(last)
	require.NotContains(t, m.eventTargets, key)
	m.view, m.diffKey = tuiViewDiff, key
	m.syncVisibleEventStreams(nil)
	require.Len(t, m.eventTargets, maxEventStreams)
	require.Contains(t, m.eventTargets, key)
}

func TestTUIDeferredEventReadsObserveMinimumInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pr := approvalTestPR(1)
		key := makePRKey(pr)
		m := approvalTestModel("", pr)
		m.gql = eventTestClient(t, mergedEventNode, eventStatus)
		m.eventStreams = newEventStreams(true, nil)
		defer m.eventStreams.close()
		m.eventStreams.listeners[key] = func() {}
		m.eventStreams.pending[key] = eventStatus
		m.queueReviewEvents()
		time.Sleep(watchMinInterval)
		require.NotNil(t, m.refreshFromEvents())
		m.changedRefreshInFlight = false
		m.eventStreams.queue(t.Context(), key, eventReviews)
		time.Sleep(eventBatchDelay)
		synctest.Wait()
		m.queueReviewEvents()
		wake := m.refreshFromEvents()
		require.False(t, m.changedRefreshInFlight)
		require.NotNil(t, wake)
		require.Nil(t, m.refreshFromEvents(), "one wake suffices for the pending batch")
		m, _ = updateModel(t, m, wake())
		require.True(t, m.changedRefreshInFlight)
	})
}
