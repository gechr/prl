package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestUnapproveFormSubmitsReasonAndSettles(t *testing.T) {
	for _, view := range []tuiView{tuiViewList, tuiViewDetail, tuiViewDiff} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/fail=%v", view, fail), func(t *testing.T) {
				pr := approvalTestPR(1)
				pr.viewerApproved = true
				m := approvalTestModel("", pr)
				key := makePRKey(pr)
				m.view, m.detailKey, m.diffKey = view, key, key
				var reasons []string
				m.actions = unapproveTestRunner(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == "/user" {
						return jsonResponse(req, http.StatusOK, `{"login":"viewer"}`), nil
					}
					if req.Method == http.MethodGet {
						return jsonResponse(
							req,
							http.StatusOK,
							`[{"id":11,"state":"APPROVED","user":{"login":"viewer"}}]`,
						), nil
					}
					require.Equal(t, http.MethodPut, req.Method)
					require.Equal(
						t,
						"/repos/owner/repo/pulls/1/reviews/11/dismissals",
						req.URL.Path,
					)
					var body map[string]string
					require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
					reasons = append(reasons, body["message"])
					if fail {
						return jsonResponse(
							req,
							http.StatusForbidden,
							`{"message":"forbidden"}`,
						), nil
					}
					return jsonResponse(req, http.StatusOK, `{}`), nil
				})
				m, _ = pressKey(t, m, tuiKeybindApprove)
				require.Equal(t, tuiActionUnapprove, m.confirmAction)
				formDialog, ok := m.dialogs.Top().(*dialog.Form)
				require.True(t, ok)
				require.Equal(t, defaultUnapproveMessage, formDialog.Model().Value(0))
				require.Empty(t, m.pendingApprovals)
				formDialog.Model().SetValue(0, "Checking updated tests.")
				m, cmd := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
				require.Empty(t, m.confirmAction)
				require.True(t, m.pendingApprovals[key].remove)
				require.False(t, isViewerApproved(*m.prForKey(key)))
				completion := cmd()
				require.Equal(t, []string{"Checking updated tests."}, reasons)
				m, _ = updateModel(t, m, completion)
				require.Empty(t, m.pendingApprovals)
				require.Equal(t, fail, isViewerApproved(*m.prForKey(key)))
				require.Equal(t, fail, m.flash.Err)
				if !fail {
					require.Equal(t, "Unapproved owner/repo#1", ansi.Strip(m.flash.Msg))
				}
			})
		}
	}
}

func TestUnapproveFormRequiresReasonAndCanCancel(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := approvalTestModel("", pr)
	m, _ = pressKey(t, m, tuiKeybindApprove)
	formDialog, ok := m.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	formDialog.Model().SetValue(0, "")
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	require.Equal(t, tuiActionUnapprove, m.confirmAction)
	require.Empty(t, m.pendingApprovals)
	m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Empty(t, m.confirmAction)
	require.True(t, isViewerApproved(*m.prForKey(makePRKey(pr))))
}

func TestUnapproveHelpAndMixedSelection(t *testing.T) {
	approved, unapproved := approvalTestPR(1), approvalTestPR(2)
	approved.viewerApproved = true
	m := approvalTestModel("", approved, unapproved)
	m.diffKey, m.detailKey = makePRKey(approved), makePRKey(approved)
	for _, hints := range [][]key.Hint{m.listHelpPairs(), m.detailHelpPairs(), m.diffHelpPairs()} {
		require.Contains(t, hints, key.Hint{Key: tuiKeybindApprove, Desc: tuiHelpUnapprove})
	}
	m.selected[makePRKey(approved)], m.selected[makePRKey(unapproved)] = true, true
	targets, _, remove := m.approvalToggleTargets()
	require.False(t, remove)
	require.Equal(t, []prKey{makePRKey(unapproved)}, targetKeys(targets))
	m, _ = pressKey(t, m, tuiKeybindApprove)
	require.Equal(t, tuiActionApprove, m.confirmAction)
}

func TestPendingUnapprovalSurvivesRefreshAndRejectsStaleStatus(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := approvalTestModel("", pr)
	key := makePRKey(pr)
	m.beginApprovalChange([]prKey{key}, true)
	m.items[0].PR = pr
	m.applyPendingApprovals()
	require.False(t, isViewerApproved(m.items[0].PR))
	m, _ = updateModel(t, m, prStatusMsg{key: key, pr: pr})
	require.False(t, isViewerApproved(*m.prForKey(key)))
	m.settleApprovals([]approvalResult{{key: key, remove: true}})
	m, _ = updateModel(t, m, prStatusMsg{key: key, gen: 1, pr: pr})
	require.False(t, isViewerApproved(*m.prForKey(key)))
}

func TestUnapproveBatchSettlesFailuresIndividually(t *testing.T) {
	first, second := approvalTestPR(1), approvalTestPR(2)
	first.viewerApproved, second.viewerApproved = true, true
	m := approvalTestModel("", first, second)
	firstKey, secondKey := makePRKey(first), makePRKey(second)
	m.selected[firstKey], m.selected[secondKey] = true, true
	m.actions = unapproveTestRunner(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/user" {
			return jsonResponse(req, http.StatusOK, `{"login":"viewer"}`), nil
		}
		if req.Method == http.MethodGet {
			return jsonResponse(
				req,
				http.StatusOK,
				`[{"id":11,"state":"APPROVED","user":{"login":"viewer"}}]`,
			), nil
		}
		var body map[string]string
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		require.Equal(t, "Revisiting both reviews.", body["message"])
		if req.URL.Path == "/repos/owner/repo/pulls/2/reviews/11/dismissals" {
			return jsonResponse(req, http.StatusForbidden, `{"message":"forbidden"}`), nil
		}
		require.Equal(t, "/repos/owner/repo/pulls/1/reviews/11/dismissals", req.URL.Path)
		return jsonResponse(req, http.StatusOK, `{}`), nil
	})
	m, _ = pressKey(t, m, tuiKeybindApprove)
	require.Equal(t, "Unapprove 2 PRs?", m.confirmPrompt)
	formDialog, ok := m.dialogs.Top().(*dialog.Form)
	require.True(t, ok)
	formDialog.Model().SetValue(0, "Revisiting both reviews.")
	m, cmd := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	require.Len(t, m.pendingApprovals, 2)
	m, _ = updateModel(t, m, cmd())
	require.Empty(t, m.pendingApprovals)
	require.False(t, isViewerApproved(*m.prForKey(firstKey)))
	require.True(t, isViewerApproved(*m.prForKey(secondKey)))
	require.True(t, m.flash.Err)
}

func TestUnapproveWithoutConfirmationUsesDefaultReason(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := approvalTestModel("", pr)
	var reason string
	m.actions = unapproveTestRunner(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/user" {
			return jsonResponse(req, http.StatusOK, `{"login":"viewer"}`), nil
		}
		if req.Method == http.MethodGet {
			return jsonResponse(
				req,
				http.StatusOK,
				`[{"id":11,"state":"APPROVED","user":{"login":"viewer"}}]`,
			), nil
		}
		var body map[string]string
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		reason = body["message"]
		return jsonResponse(req, http.StatusOK, `{}`), nil
	})
	m, cmd := updateModel(t, m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	require.Empty(t, m.confirmAction)
	require.True(t, m.pendingApprovals[makePRKey(pr)].remove)
	m, _ = updateModel(t, m, cmd())
	require.Equal(t, defaultUnapproveMessage, reason)
	require.False(t, isViewerApproved(*m.prForKey(makePRKey(pr))))
}
