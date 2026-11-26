package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep/reel"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	"github.com/stretchr/testify/require"
)

func changedPRTestClient(t *testing.T, numbers []int) (*ActionRunner, *int) {
	t.Helper()
	calls := 0
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test",
		Host:      "github.com",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			body := decodeGraphQLBody(t, readBody(t, req.Body))
			operation, _, _ := strings.Cut(body.Query, "(")
			data := map[string]any{}
			if calls == 1 {
				require.Equal(t, "query PullRequestsByRef", operation)
				require.Len(t, body.Variables, len(numbers)*3)
				for i, number := range numbers {
					require.InDelta(t, number, body.Variables[fmt.Sprintf("n%d", i)], 0)
					data[fmt.Sprintf("pr%d", i)] = map[string]any{"pullRequest": map[string]any{
						"id":     fmt.Sprintf("PR_%d", number),
						"number": number,
						"state":  "OPEN",
						"title":  "Updated title",
						"repository": map[string]string{
							"name":          "repo",
							"nameWithOwner": "owner/repo",
						},
					}}
				}
			} else {
				require.Equal(t, 2, calls)
				require.Equal(t, "query ListMetadata", operation)
				var ids []any
				var mergeNodes, reviewNodes []any
				for _, number := range numbers {
					id := fmt.Sprintf("PR_%d", number)
					ids = append(ids, id)
					mergeNodes = append(mergeNodes, map[string]any{
						"id": id, "mergeStateStatus": "CLEAN", "reviewDecision": "APPROVED",
					})
					reviewNodes = append(reviewNodes, map[string]any{
						"id": id, "latestOpinionatedReviews": map[string]any{
							"nodes": []any{map[string]any{
								"author": map[string]string{"login": "viewer"}, "state": "APPROVED",
							}},
						},
					})
				}
				require.Equal(t, ids, body.Variables["mergeIDs"])
				require.Equal(t, ids, body.Variables["viewerReviewIDs"])
				data["mergeNodes"], data["viewerReviewNodes"] = mergeNodes, reviewNodes
				data["viewer"] = map[string]string{"login": "viewer"}
			}
			response, marshalErr := json.Marshal(map[string]any{"data": data})
			require.NoError(t, marshalErr)
			return jsonResponse(req, http.StatusOK, string(response)), nil
		}),
	})
	require.NoError(t, err)
	return NewActionRunner(nil, gql), &calls
}

func TestReviewExitRefreshesOnlyChangedPRs(t *testing.T) {
	for _, view := range []tuiView{tuiViewDiff, tuiViewDetail} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			var prs []PullRequest
			for n := 1; n <= 7; n++ {
				prs = append(prs, approvalTestPR(n))
			}
			m := approvalTestModel("", prs...)
			m.cli.Columns.Values = []string{colTitle, colStatus, colReview}
			m.items = buildPRRowModels(prs, "", nil)
			m.rerenderList()
			unchanged := m.items[0]
			m.view = view
			m.diffKey, m.detailKey = m.rowKeyAt(6), m.rowKeyAt(6)
			m.cursor = 5
			cursorKey := m.rowKeyAt(m.cursor)
			m.selected[cursorKey] = true
			actions, calls := changedPRTestClient(t, []int{2, 4})
			m.actions = actions
			m.gql = m.actions.gql
			for _, n := range []int{2, 4} {
				key := makePRKey(prs[n-1])
				m.beginApproval([]prKey{key})
				msg := approvedMsg(key, nil)
				msg.navigated = true
				m, _ = updateModel(t, m, msg)
			}
			require.Zero(t, *calls)
			require.Len(t, m.changedPRs, 2)
			m, cmd := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
			require.True(t, m.changedRefreshInFlight)
			require.False(t, m.refreshing)
			state, _ := m.programStatus()
			require.Equal(t, clog.ProgramStateWorking, state)
			require.NotNil(t, cmd)
			result, ok := cmd().(changedPRsMsg)
			require.True(t, ok)
			require.NoError(t, result.err)
			m, cmd = updateModel(t, m, result)
			require.Nil(t, cmd)
			state, _ = m.programStatus()
			require.Equal(t, clog.ProgramStateIdle, state)
			require.Equal(t, 2, *calls)
			require.Equal(t, unchanged, m.items[0])
			require.Equal(t, cursorKey, m.rowKeyAt(m.cursor))
			require.True(t, m.selected[cursorKey])
			for _, n := range []int{2, 4} {
				key := makePRKey(prs[n-1])
				row := m.rows[m.resolveIndex(key, -1)]
				require.Equal(t, MergeStatusReady, row.Item.MergeStatus)
				require.Equal(t, valueReviewFilterApproved, row.Item.Review)
				require.Equal(t, valueReadyToMerge, row.Item.MergeReason)
				require.True(t, isViewerApproved(row.Item.PR))
				require.Equal(t, "Updated title ready approved", rowFilterText(row))
			}
		})
	}
}

func TestUnchangedActionsDoNotQueueRefresh(t *testing.T) {
	for _, msg := range []actionMsg{
		{action: tuiActionApproved, err: errApproveTest},
		{action: tuiActionApproved, outcome: approveSkippedAggregate},
		{action: tuiActionApproved, outcome: approveAlreadyViewer},
		{action: tuiActionClosed, err: errors.New("close failed")},
		{action: tuiActionOpened},
	} {
		m := approvalTestModel("", approvalTestPR(1))
		msg.key = m.rowKeyAt(0)
		m, _ = updateModel(t, m, msg)
		require.Empty(t, m.changedPRs)
		require.False(t, m.changedRefreshInFlight)
	}
}

func TestLateActionRefreshesAfterExit(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	key := m.rowKeyAt(0)
	m.view, m.diffKey = tuiViewDiff, key
	m.actionsInFlight = 1
	m.autoRefresh = true
	m.lastRefreshAt = time.Now().Add(-2 * watchMaxInterval)
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	require.False(t, m.refreshing)
	actions, calls := changedPRTestClient(t, []int{1})
	m.actions = actions
	m.gql = m.actions.gql
	m, cmd := updateModel(t, m, actionDoneMsg{msg: approvedMsg(key, nil)})
	require.True(t, m.changedRefreshInFlight)
	require.False(t, m.refreshing)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	result := batch[len(batch)-1]()
	m, _ = updateModel(t, m, result)
	require.Equal(t, 2, *calls)
	require.Equal(t, MergeStatusReady, m.items[0].MergeStatus)
}

func TestChangedPRRefreshRejectsOlderActionState(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	key := m.rowKeyAt(0)
	m.markPRChanged(key)
	gens := map[prKey]int{key: m.prChangeGen[key]}
	m.markPRChanged(key)
	m.applyActionState(key, tuiActionClosed)
	m, _ = updateModel(t, m, changedPRsMsg{
		gens: gens, items: []PRRowModel{{PR: approvalTestPR(1)}},
	})
	require.Equal(t, valueClosed, m.prForKey(key).State)
	require.True(t, m.changedPRs[key])
}

func TestChangedPRRefreshFailureDoesNotLoop(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.changedRefreshInFlight = true
	m, _ = updateModel(t, m, changedPRsMsg{err: errors.New("read failed")})
	require.False(t, m.changedRefreshInFlight)
	require.Empty(t, m.changedPRs)
	require.Equal(t, "PR refresh failed: read failed", m.flash.Msg)
}

func TestBatchRefreshIncludesOnlySuccessfulMutations(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2), approvalTestPR(3))
	k1, k2, k3 := m.rowKeyAt(0), m.rowKeyAt(1), m.rowKeyAt(2)
	m, _ = updateModel(t, m, batchActionMsg{
		action: tuiActionApproved, count: 3, failed: 1,
		keys: []prKey{k1, k2},
		outcomes: map[prKey]approveOutcome{
			k1: approvePosted, k2: approveAlreadyViewer,
		},
		failures: []batchResult{{key: k3, err: errApproveTest}},
	})
	require.Equal(t, prKeys{k1: true}, m.changedPRs)
}

func TestApprovalRefreshesEvenWhenMergeFails(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	key := m.rowKeyAt(0)
	m, _ = updateModel(t, m, actionMsg{
		key: key, action: tuiActionMerged, err: errors.New("merge failed"),
		approvals: []approvalResult{{key: key, outcome: approvePosted}},
	})
	require.Equal(t, prKeys{key: true}, m.changedPRs)
}

func TestChangedPRRefreshPreservesPendingApproval(t *testing.T) {
	m := approvalTestModel(valueReviewFilterSelfRequired, approvalTestPR(1))
	key := m.rowKeyAt(0)
	m.markPRChanged(key)
	m.beginApproval([]prKey{key})
	m, _ = updateModel(t, m, changedPRsMsg{
		gens:  map[prKey]int{key: m.prChangeGen[key]},
		items: buildPRRowModels([]PullRequest{approvalTestPR(1)}, "", nil),
	})
	require.True(t, isViewerApproved(*m.prForKey(key)))
	require.False(t, isShown(m, key))
}

func TestChangedPRRefreshAfterQueryChangeIsRetried(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	key := m.rowKeyAt(0)
	m.markPRChanged(key)
	clear(m.changedPRs)
	m.queryGen++
	m, _ = updateModel(t, m, changedPRsMsg{gens: map[prKey]int{key: 1}})
	require.True(t, m.changedPRs[key])
}

func TestMutationPreservesOptionsRefresh(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.refreshing, m.showRefreshStatus = true, true
	m.queryGen = 4
	m.markPRChanged(m.rowKeyAt(0))
	require.True(t, m.refreshing)
	require.True(t, m.showRefreshStatus)
	require.Equal(t, 4, m.queryGen)
	require.Nil(t, m.refreshChangedPRs())
}

func TestMutationPreservesInitialLoadWithAutoRefreshDisabled(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2))
	key := m.rowKeyAt(0)
	_ = m.startRefresh(false)
	queryGen := m.queryGen
	m, _ = updateModel(t, m, actionMsg{key: key, action: tuiActionClosed})
	require.True(t, m.refreshing)
	require.Equal(t, queryGen, m.queryGen)
	require.False(t, m.autoRefresh)
	loaded := approvalTestPR(2)
	loaded.Title = "Loaded from server"
	m, _ = updateModel(t, m, refreshResultMsg{
		queryGen: queryGen,
		items:    buildPRRowModels([]PullRequest{approvalTestPR(1), loaded}, "", nil),
	})
	require.False(t, m.refreshing)
	require.Equal(t, "Loaded from server", m.itemForKey(makePRKey(loaded)).Title)
	require.Equal(t, valueClosed, m.prForKey(key).State)
	require.True(t, m.changedPRs[key])
}

func TestStatusReplyBeforeMutationCannotOverwriteChangedPR(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	key := m.rowKeyAt(0)
	staleGen := m.prChangeGen[key]
	m.markPRChanged(key)
	fresh := approvalTestPR(1)
	fresh.MergeStatus = MergeStatusReady
	fresh.ReviewDecision = valueReviewApproved
	m, _ = updateModel(t, m, changedPRsMsg{
		gens:  map[prKey]int{key: m.prChangeGen[key]},
		items: buildPRRowModels([]PullRequest{fresh}, "", nil),
	})
	m, _ = updateModel(t, m, prStatusMsg{
		key: key, changeGen: staleGen, pr: approvalTestPR(1),
	})
	require.Equal(t, MergeStatusReady, m.prForKey(key).MergeStatus)
	require.Equal(t, valueReviewApproved, m.prForKey(key).ReviewDecision)
}

func TestListUpdatesRefreshSearchableCells(t *testing.T) {
	pr, other := approvalTestPR(1), approvalTestPR(2)
	pr.Title, other.Title = "First change", "Other change"
	fresh := pr
	fresh.MergeStatus, fresh.ReviewDecision = MergeStatusReady, valueReviewApproved
	key := makePRKey(pr)
	for _, tc := range []struct {
		name    string
		columns []string
		msg     tea.Msg
		query   string
	}{
		{"merge action", []string{colStatus, colState, colTitle}, actionMsg{key: key, action: tuiActionMerged}, "closed merged"},
		{"status refresh", []string{colStatus, colReview, colTitle}, prStatusMsg{key: key, pr: fresh}, "ready approved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := listTUIModel(pr, other)
			m.cli.State, m.cli.Columns.Values = valueAll, tc.columns
			h := newTUIHarness(t, m)
			h.Wait([]string{pr.Title, other.Title}).SendProgram(tc.msg)
			h.Key("/").Paste(tc.query).Key("enter").Wait("/> " + tc.query).AssertNot(other.Title)
			row := h.Locate(reel.Contains(pr.Title))
			fields := strings.Fields(strings.Split(h.View(), nl)[row.Y])
			require.Equal(t, "❯ 1 "+tc.query+" "+pr.Title, strings.Join(fields, " "))
		})
	}
}

func TestStatusEventKeepsConfirmedApprovalAndHiddenRows(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	m := approvalTestModel(valueReviewFilterSelfRequired, first, second)
	key := makePRKey(first)
	m.settleApprovals([]approvalResult{{key: key, outcome: approveAlreadyViewer}})
	other := makePRKey(second)
	m.applyActionState(other, tuiActionClosed)
	m.applyChangedPRs(
		changedPRsMsg{
			items:  buildPRRowModels([]PullRequest{first}, "", nil),
			events: prEvents{key: eventStatus},
		},
	)
	require.True(t, m.confirmed.get(key).viewerApproved)
	require.True(t, m.items[0].PR.viewerApproved)
	require.True(t, m.filterHidden[key])
	require.True(t, m.filterHidden[other])
}

func TestDiffStatusCacheFollowsEventCoverage(t *testing.T) {
	pr := approvalTestPR(1)
	key := makePRKey(pr)
	m := diffTestModel(pr)
	m.gql = eventTestClient(t, mergedEventNode, eventStatus)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.listeners[key] = func() {}
	require.NotNil(t, m.statusCmd(key))
	require.Nil(t, m.statusCmd(key), "reopening reuses the in-flight read")
	old := m.diffStatus[key]
	old.fetchedAt = time.Now().Add(-2 * visibleStatusInterval)
	m.diffStatus[key] = old
	require.Nil(t, m.statusCmd(key), "an unchanged subscribed PR stays current")
	delete(m.eventStreams.listeners, key)
	require.NotNil(t, m.statusCmd(key), "an uncovered PR revalidates in the background")
	m.markPRChanged(key)
	require.NotNil(t, m.statusCmd(key), "a local mutation invalidates the status")
}

func TestEventPreservesInFlightMutationRead(t *testing.T) {
	pr := approvalTestPR(1)
	key := makePRKey(pr)
	m := approvalTestModel("", pr)
	m.eventStreams = testEventStreams(http.StatusCreated)
	t.Cleanup(m.eventStreams.close)
	m.eventStreams.listeners[key] = func() {}
	m.markPRChanged(key)
	gens := map[prKey]int{key: m.prChangeGen[key]}
	clear(m.changedPRs)
	m.eventStreams.pending[key] = eventReviews
	m.queueReviewEvents()
	pr.Title = "Updated title"
	m.applyChangedPRs(changedPRsMsg{gens: gens, items: buildPRRowModels([]PullRequest{pr}, "", nil)})
	require.Equal(t, pr.Title, m.prForKey(key).Title)
	require.Empty(t, m.changedPRs)
	require.Equal(t, eventReviews, m.pendingEvents[key])
}
