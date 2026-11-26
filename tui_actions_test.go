package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep/reel"
	"github.com/charmbracelet/x/ansi"
	xos "github.com/gechr/x/os"
	"github.com/stretchr/testify/require"
)

func TestDesktopActionFailures(t *testing.T) {
	command := "powershell.exe"
	if xos.IsDarwin() {
		command = "open"
		stubDesktopCommands(t)
	} else {
		fakeWSLDesktopCommands(t)
	}
	t.Setenv("PRL_TEST_FAIL", "1")
	openFailure := filepath.Join(
		os.Getenv("PATH"),
		command,
	) + ": exit status 7: desktop action failed"
	copyFailure := openFailure
	if xos.IsDarwin() {
		copyFailure = "exit status 7"
	}
	for _, view := range []struct {
		name string
		kind tuiView
	}{{"list", tuiViewList}, {"detail", tuiViewDetail}, {"diff", tuiViewDiff}} {
		for _, action := range []struct{ label, key, failure string }{{"Open", "o", openFailure}, {"Copy", "alt+c", copyFailure}} {
			t.Run(view.name+"/"+action.label, func(t *testing.T) {
				pr := approvalTestPR(1)
				m := actionViewTestModel(view.kind, pr)
				m.selected[makePRKey(pr)] = true
				want := action.label + " failed: " + action.failure
				m.width = max(m.width, ansi.StringWidth(want)+4)
				h := newTUIHarness(t, m)
				h.Key(action.key)
				errorAt := h.Locate(reel.Contains(want))
				requireScreenColor(t, cellStyle(h, errorAt).Fg, m.styles.statusErr.GetForeground())
				if view.kind != tuiViewList {
					h.KeyEsc().Wait("All PRs ▾")
				}
				waitListSelection(t, h, "1", true)
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
