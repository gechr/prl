package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/stretchr/testify/require"
)

func refreshTestPR(number int) PullRequest {
	pr := approvalTestPR(number)
	pr.NodeID = fmt.Sprintf("PR_%d", number)
	pr.HeadSHA = fmt.Sprintf("head-%d", number)
	pr.MergeStatus = MergeStatusReady
	pr.statusFetchedAt = time.Now().Add(-45 * time.Second)
	return pr
}

func TestPeriodicMetadataPolicy(t *testing.T) {
	pr := refreshTestPR(1)
	pr.viewerIsAuthor = true
	cache := newListMetadataCache()
	req := listMetadataRequest{mergeStatus: true, viewerApproval: true}
	cache.store(pr, req, newTimelineActors())
	key, _ := listMetadataKey(pr)
	entry := cache.entries[key]
	entry.viewerApprovalFetchedAt = pr.statusFetchedAt
	cache.entries[key] = entry
	for _, tc := range []struct {
		name    string
		age     time.Duration
		visible bool
		status  MergeStatus
		want    bool
	}{
		{"visible retained", 45 * time.Second, true, MergeStatusReady, true},
		{"visible due", time.Minute, true, MergeStatusReady, false},
		{"offscreen retained", 2 * time.Minute, false, MergeStatusReady, true},
		{"offscreen due", 3 * time.Minute, false, MergeStatusReady, false},
		{"pending due", time.Second, false, MergeStatusCIPending, false},
		{"unknown due", time.Second, false, MergeStatusUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry.mergeStatus = tc.status
			cache.entries[key] = entry
			got := pr
			got.viewerIsAuthor = false
			actors := newTimelineActors()
			policy := &listRefreshPolicy{
				now:     pr.statusFetchedAt.Add(tc.age),
				visible: prKeys{makePRKey(pr): tc.visible},
			}
			require.Equal(t, tc.want, cache.applyWithPolicy(&got, req, &actors, policy))
			if tc.want {
				require.Equal(t, pr.statusFetchedAt, got.statusFetchedAt)
				require.True(t, got.viewerIsAuthor)
			}
		})
	}
	actors := newTimelineActors()
	require.False(t, cache.apply(&pr, req, &actors), "explicit reads retain the 30 second lifetime")
	entry.mergeStatus = MergeStatusReady
	cache.entries[key] = entry
	policy := &listRefreshPolicy{now: pr.statusFetchedAt.Add(time.Second)}
	changed := pr
	changed.HeadSHA = "different-head"
	require.False(t, cache.applyWithPolicy(&changed, req, &actors, policy))
	changed = pr
	changed.UpdatedAt = time.Now()
	require.False(t, cache.applyWithPolicy(&changed, req, &actors, policy))
}

func TestPeriodicHydrationOnlyFetchesDueRows(t *testing.T) {
	prs := []PullRequest{refreshTestPR(1), refreshTestPR(2), refreshTestPR(3)}
	prs[1].MergeStatus = MergeStatusCIPending
	cache := newListMetadataCache()
	req := listMetadataRequest{mergeStatus: true}
	for _, pr := range prs {
		cache.store(pr, req, newTimelineActors())
	}
	var calls int
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test", Host: "github.com",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			body := decodeGraphQLBody(t, readBody(t, req.Body))
			require.Equal(t, []any{"PR_2"}, body.Variables["mergeIDs"])
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"mergeNodes":[{"id":"PR_2","headRefOid":"head-2","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED"}]}}`,
			), nil
		}),
	})
	require.NoError(t, err)
	policy := &listRefreshPolicy{now: time.Now(), visible: prKeys{makePRKey(prs[0]): true}}
	_, err = hydrateListMetadataForRefresh(gql, prs, req, cache, policy)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.True(t, prs[1].headValidated)
	require.Equal(t, []string{"PR_1", "PR_3"}, cache.pendingHeadChecks(prs))
	_, err = hydrateListMetadataForRefresh(gql, prs, req, cache, policy)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "settled results are reused next tick")
	require.Equal(t, []string{"PR_1", "PR_2", "PR_3"}, cache.pendingHeadChecks(prs))
}

func TestMissingMetadataDoesNotRenewFreshness(t *testing.T) {
	pr := refreshTestPR(1)
	cache := newListMetadataCache()
	req := listMetadataRequest{mergeStatus: true, viewerApproval: true}
	cache.store(pr, req, newTimelineActors())
	gql, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "test", Host: "github.com",
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(
				req,
				http.StatusOK,
				`{"data":{"mergeNodes":[null],"viewer":{"login":"viewer"},"viewerReviewNodes":[null]}}`,
			), nil
		}),
	})
	require.NoError(t, err)
	prs := []PullRequest{pr}
	_, err = hydrateListMetadataCached(gql, prs, req, cache)
	require.NoError(t, err)
	require.Equal(t, pr.statusFetchedAt, prs[0].statusFetchedAt)
	require.False(t, prs[0].headValidated)
	actors := newTimelineActors()
	require.False(
		t,
		cache.applyWithPolicy(&prs[0], req, &actors, &listRefreshPolicy{now: time.Now()}),
	)
}

func TestScopedCacheInvalidationIsolatesOldWriters(t *testing.T) {
	prs := []PullRequest{refreshTestPR(1), refreshTestPR(2)}
	m := approvalTestModel("", prs...)
	req := listMetadataRequest{mergeStatus: true}
	for _, pr := range prs {
		m.metadataCache.store(pr, req, newTimelineActors())
	}
	old := m.metadataCache
	key1, _ := listMetadataKey(prs[0])
	key2, _ := listMetadataKey(prs[1])
	original := old.entries[key2]
	m.invalidatePRMetadata(prKeys{makePRKey(prs[0]): true})
	_, exists := m.metadataCache.entries[key1]
	require.False(t, exists)
	require.Equal(t, original, m.metadataCache.entries[key2])
	old.store(prs[0], req, newTimelineActors())
	prs[1].MergeStatus = MergeStatusCIFailed
	old.store(prs[1], req, newTimelineActors())
	require.Len(t, m.metadataCache.entries, 1)
	require.Equal(t, original, m.metadataCache.entries[key2])
}

func TestTargetedReadKeepsActionUntilSearchCatchesUp(t *testing.T) {
	pr := refreshTestPR(1)
	m := approvalTestModel("", pr)
	m.cli.State = valueAll
	key := makePRKey(pr)
	m.applyActionState(key, tuiActionClosed)
	closed := pr
	closed.State = valueClosed
	m, _ = updateModel(t, m, changedPRsMsg{items: buildPRRowModels([]PullRequest{closed}, "", nil)})
	require.Equal(t, valueClosed, m.items[0].PR.State)
	require.Len(t, m.pendingActions, 1)
	m, _ = updateModel(t, m, refreshResultMsg{items: buildPRRowModels([]PullRequest{pr}, "", nil)})
	require.Equal(t, valueClosed, m.items[0].PR.State)
	require.Len(t, m.pendingActions, 1)
	m, _ = updateModel(
		t,
		m,
		refreshResultMsg{items: buildPRRowModels([]PullRequest{closed}, "", nil)},
	)
	require.Empty(t, m.pendingActions)
}

func TestTargetedSnapshotPreservesAgesAndConfirmedRows(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(envKeyGitHubToken, "test")
	prs := []PullRequest{refreshTestPR(1), refreshTestPR(2)}
	m := approvalTestModel("", prs...)
	m.params = testCacheParams()
	m.lastRefreshAt = time.Now().Add(-4 * time.Minute).UTC()
	m.beginApproval([]prKey{makePRKey(prs[1])})
	prs[0].statusFetchedAt = time.Now().UTC()
	prs[0].viewerApproved = true
	m.applyChangedPRs(changedPRsMsg{items: buildPRRowModels(prs[:1], "", nil)})
	loaded, checkedAt, err := loadListResultSnapshot(m.cli, m.params, tuiListResultCacheMaxAge)
	require.NoError(t, err)
	require.Equal(t, m.lastRefreshAt, checkedAt)
	require.True(t, loaded[0].statusFetchedAt.Equal(prs[0].statusFetchedAt))
	require.True(t, loaded[1].statusFetchedAt.Equal(prs[1].statusFetchedAt))
	require.True(t, loaded[0].viewerApproved)
	require.False(t, loaded[1].viewerApproved, "do not persist an approval still in flight")
	key, _ := listMetadataKey(prs[0])
	require.True(t, m.metadataCache.entries[key].viewerApproved)
	m.lastRefreshAt = time.Now().Add(-tuiListResultCacheMaxAge - time.Minute)
	m.saveSnapshot()
	loaded, checkedAt, err = loadListResultSnapshot(m.cli, m.params, tuiListResultCacheMaxAge)
	require.NoError(t, err)
	require.Nil(t, loaded)
	require.True(t, checkedAt.IsZero(), "a fresh row cannot renew stale membership")
}

func TestRefreshKeepsCursorScreenLine(t *testing.T) {
	var prs []PullRequest
	for n := 1; n <= 25; n++ {
		prs = append(prs, refreshTestPR(n))
	}
	m := approvalTestModel("", prs...)
	m.cursor, m.offset = 12, 8
	key := m.rowKeyAt(m.cursor)
	line := m.cursorLinePosition() - m.offset
	newPRs := append([]PullRequest{refreshTestPR(26)}, prs...)
	newModel := approvalTestModel("", newPRs...)
	m.replaceListItems(newModel.items)
	m = m.mergeRefresh(newModel.rows)
	require.Equal(t, key, m.rowKeyAt(m.cursor))
	require.Equal(t, line, m.cursorLinePosition()-m.offset)
	newPRs = append(newPRs[:m.cursor:m.cursor], newPRs[m.cursor+1:]...)
	newModel = approvalTestModel("", newPRs...)
	oldCursor := m.cursor
	m.replaceListItems(newModel.items)
	m = m.mergeRefresh(newModel.rows)
	require.Equal(t, oldCursor, m.cursor, "keep the nearest surviving row")
}

func TestRefreshAtTopRevealsNewRows(t *testing.T) {
	var prs []PullRequest
	for n := 1; n <= 25; n++ {
		prs = append(prs, refreshTestPR(n))
	}
	for _, reindex := range []bool{false, true} {
		m := approvalTestModel("", prs...)
		key := m.rowKeyAt(0)
		newPRs := append([]PullRequest{refreshTestPR(26)}, prs...)
		newModel := approvalTestModel("", newPRs...)
		if reindex {
			m = m.reindexRows(m.rows, newModel.rows)
		} else {
			m.replaceListItems(newModel.items)
			m = m.mergeRefresh(newModel.rows)
		}
		require.Zero(t, m.offset)
		require.Equal(t, key, m.rowKeyAt(m.cursor))
		require.Equal(t, makePRKey(newPRs[0]), m.rowKeyAt(m.listLines()[m.offset].row))
	}
}

func TestRefreshPolicyUsesViewport(t *testing.T) {
	var prs []PullRequest
	for n := 1; n <= 25; n++ {
		prs = append(prs, refreshTestPR(n))
	}
	m := approvalTestModel("", prs...)
	m.offset = 3
	policy := m.refreshPolicy()
	lines := m.listLines()
	first := m.rows[lines[m.offset].row].Item.PR
	outside := m.rows[lines[m.offset+m.listViewport()].row].Item.PR
	require.Len(t, policy.visible, m.listViewport())
	require.Equal(t, time.Minute, policy.maxAge(first, MergeStatusReady))
	require.Equal(t, 3*time.Minute, policy.maxAge(outside, MergeStatusReady))
	require.False(t, policy.visible[m.rowKeyAt(0)])
}

func TestFocusCatchupKeepsFooterQuiet(t *testing.T) {
	m := approvalTestModel("", refreshTestPR(1))
	m.autoRefresh = true
	m.lastRefreshAt = time.Now().Add(-10 * time.Minute)
	m.lastInteraction = time.Now().Add(-time.Hour)
	m, _ = updateModel(t, m, tea.FocusMsg{})
	require.True(t, m.refreshing)
	require.Less(t, time.Since(m.lastInteraction), time.Second)
	gen := m.queryGen
	m, _ = updateModel(t, m, tea.FocusMsg{})
	require.Equal(t, gen, m.queryGen)
	require.True(t, m.View().ReportFocus)
	m, _ = updateModel(t, m, tea.BlurMsg{})
	require.True(t, m.autoRefresh)
	m.refreshing = false
	m.autoRefresh = false
	m, _ = updateModel(t, m, tea.FocusMsg{})
	require.False(t, m.refreshing)
	m.lastRefreshAt = time.Now()
	m.items[0].PR.statusFetchedAt = time.Now().Add(-2*time.Minute - time.Second)
	m.rows[0].Item = m.items[0]
	require.Empty(t, m.listStatus())
	m.lastRefreshAt = time.Now().Add(-4*time.Minute - time.Second)
	m.refreshing = true
	require.Empty(t, m.listStatus())
}

func TestGraphQLQueriesPreserveConditionalResponses(t *testing.T) {
	for _, tc := range []struct {
		query string
		read  bool
	}{
		{`query Rows { nodes(ids: []) { id } }`, true},
		{`{ viewer { login } }`, true},
		{"# mutation is a comment\nquery Rows { viewer { login } }", true},
		{`query Rows($x: String = "mutation") { mutation: viewer { login } }`, true},
		{`fragment Fields on User { login } query Rows { viewer { ...Fields } }`, true},
		{`query Rows { viewer { login } } mutation Change { updateThing { id } }`, false},
		{`mutation Change { updateThing { id } }`, false},
		{`subscription Events { node { id } }`, false},
		{`query Broken {`, false},
		{``, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			limiter := newGitHubRateLimiter()
			limiter.conditional["cached"] = conditionalResponse{}
			body, err := json.Marshal(map[string]string{"query": tc.query})
			require.NoError(t, err)
			req, err := http.NewRequest(
				http.MethodPost,
				"https://api.github.com/graphql",
				bytes.NewReader(body),
			)
			require.NoError(t, err)
			limiter.noteSuccess(req)
			require.Equal(t, tc.read, len(limiter.conditional) == 1)
			require.Equal(t, string(body), readBody(t, req.Body))
		})
	}
}
