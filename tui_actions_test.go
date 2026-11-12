package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestDesktopActionFailures(t *testing.T) {
	fakeDesktopCommands(t)
	t.Setenv("PRL_TEST_FAIL", "1")
	for _, view := range []struct {
		name string
		kind tuiView
	}{
		{name: "list", kind: tuiViewList},
		{name: "detail", kind: tuiViewDetail},
		{name: "diff", kind: tuiViewDiff},
	} {
		for _, action := range []struct {
			name  string
			label string
			key   tea.KeyPressMsg
		}{
			{"open", "Open", tea.KeyPressMsg{Code: 'o', Text: "o"}},
			{"copy", "Copy", tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}},
		} {
			t.Run(view.name+"/"+action.name, func(t *testing.T) {
				pr := testReviewPullRequest()
				key := makePRKey(pr)
				m := tuiModel{
					view:      view.kind,
					items:     []PRRowModel{{PR: pr}},
					rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
					selected:  prKeys{key: true},
					detailKey: key,
					diffKey:   key,
					styles:    newTuiStyles(),
				}
				var model tea.Model
				var handled bool
				if view.kind == tuiViewList {
					model, _, handled = m.updateListActions(action.key)
				} else {
					model, _, handled = m.handleViewAction(action.key)
				}
				require.True(t, handled)
				result, ok := model.(tuiModel)
				require.True(t, ok)
				require.True(t, result.flash.Err, "failed desktop actions must not report success")
				require.Equal(t, fmt.Sprintf(
					"%s failed: %s: exit status 7: desktop action failed",
					action.label,
					filepath.Join(os.Getenv("PATH"), "powershell.exe"),
				), result.flash.Msg)
				require.Equal(t, prKeys{key: true}, result.selected, "retain selection for retry")
			})
		}
	}
}

func TestActionVisibilityFollowsFilters(t *testing.T) {
	for _, tt := range []struct {
		name   string
		action tuiAction
		state  string
		shown  bool
	}{
		{"approve open", tuiActionApproved, valueOpen, true},
		{"merge open", tuiActionMerged, valueOpen, false},
		{"merge all", tuiActionMerged, valueAll, true},
		{"merge merged", tuiActionMerged, valueMerged, true},
		{"close open", tuiActionClosed, valueOpen, false},
		{"close all", tuiActionClosed, valueAll, true},
		{"automerge open", tuiActionAutomerged, valueOpen, true},
		{"enqueue open", tuiActionEnqueued, valueOpen, true},
		{"unsubscribe open", tuiActionUnsubscribed, valueOpen, true},
	} {
		for _, batch := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/batch=%t", tt.name, batch), func(t *testing.T) {
				m := approvalTestModel("", approvalTestPR(1))
				m.cli.State = tt.state
				key := m.rowKeyAt(0)
				if tt.action == tuiActionApproved {
					m.beginApproval([]prKey{key})
					require.True(t, isShown(m, key))
				}
				var msg tea.Msg = actionMsg{key: key, action: tt.action}
				if batch {
					msg = batchActionMsg{action: tt.action, count: 1, keys: []prKey{key}}
				}
				m, _ = updateModel(t, m, msg)
				require.Equal(t, tt.shown, isShown(m, key))
				if tt.action == tuiActionMerged {
					require.Equal(t, valueMerged, m.rows[0].Item.PR.State)
					m.cli.State = valueAll
					m.filterHidden = m.locallyFilteredKeys()
					require.True(t, isShown(m, key))
				}
			})
		}
	}
}

func TestRefreshAppliesStateFiltersImmediately(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2))
	merged := approvalTestPR(1)
	merged.State = valueMerged
	key := makePRKey(merged)
	m, _ = updateModel(t, m, refreshResultMsg{
		items: []PRRowModel{{PR: merged}, {PR: approvalTestPR(2)}},
	})
	require.False(t, isShown(m, key))
	require.Len(t, m.visibleIndices(), 1)
	m.cli.State = valueAll
	require.True(t, isShown(m, key))
}

func TestBatchMergeKeepsAutomergeVisible(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2))
	k1, k2 := m.rowKeyAt(0), m.rowKeyAt(1)
	m, _ = updateModel(t, m, batchActionMsg{
		action:  tuiActionMerged,
		count:   2,
		keys:    []prKey{k1, k2},
		actions: map[prKey]tuiAction{k1: tuiActionMerged, k2: tuiActionAutomerged},
	})
	require.False(t, isShown(m, k1))
	require.True(t, isShown(m, k2))
}

func TestConfirmedActionSurvivesStaleRefresh(t *testing.T) {
	for _, action := range []tuiAction{tuiActionMerged, tuiActionClosed} {
		t.Run(action.String(), func(t *testing.T) {
			m := approvalTestModel("", approvalTestPR(1))
			key := m.rowKeyAt(0)
			m, _ = updateModel(t, m, actionMsg{key: key, action: action})
			require.False(t, isShown(m, key))
			m, _ = updateModel(t, m, refreshResultMsg{
				items: []PRRowModel{{PR: approvalTestPR(1)}},
			})
			require.False(t, isShown(m, key))
			require.Contains(t, m.pendingActions, key)
			m.cli.State = valueAll
			m.filterHidden = m.locallyFilteredKeys()
			require.True(t, isShown(m, key))
			fresh := *m.prForKey(key)
			m, _ = updateModel(t, m, refreshResultMsg{items: []PRRowModel{{PR: fresh}}})
			require.Empty(t, m.pendingActions)
		})
	}
}

func TestConfirmedActionExpiresOrLeavesResults(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			m := approvalTestModel("", approvalTestPR(1))
			key := m.rowKeyAt(0)
			m, _ = updateModel(t, m, actionMsg{key: key, action: tuiActionClosed})
			fresh := []PRRowModel{{PR: approvalTestPR(1)}}
			if missing {
				fresh = nil
			} else {
				pending := m.pendingActions[key]
				pending.at = time.Now().Add(-3 * time.Minute)
				m.pendingActions[key] = pending
			}
			m, _ = updateModel(t, m, refreshResultMsg{items: fresh})
			require.Empty(t, m.pendingActions)
			require.Equal(t, !missing, isShown(m, key))
		})
	}
}

func TestActionRerendersRowCells(t *testing.T) {
	m := approvalTestModel("", approvalTestPR(1))
	m.cli.State = valueAll
	key := m.rowKeyAt(0)
	m, _ = updateModel(t, m, actionMsg{key: key, action: tuiActionMerged})
	fields := strings.Fields(strings.ToLower(rowFilterText(m.rows[0])))
	require.GreaterOrEqual(t, len(fields), 2)
	require.Equal(t, []string{valueClosed, valueMerged}, fields[:2])
}
