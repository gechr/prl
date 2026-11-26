package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gechr/primer/key"
	"github.com/stretchr/testify/require"
)

func dismissalTestRunner(t *testing.T, dismiss func(path, reason string) int) *ActionRunner {
	t.Helper()
	return testActionRunner(t, func(req *http.Request) (*http.Response, error) {
		path := req.URL.Path
		switch {
		case req.Method == http.MethodGet && path == "/user":
			return jsonResponse(req, http.StatusOK, `{"login":"viewer"}`), nil
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/reviews"):
			return jsonResponse(
				req,
				http.StatusOK,
				`[{"id":11,"state":"APPROVED","user":{"login":"viewer"}}]`,
			), nil
		case req.Method == http.MethodPut && strings.HasSuffix(path, "/reviews/11/dismissals"):
			var body struct {
				Message string `json:"message"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			return jsonResponse(req, dismiss(path, body.Message), `{}`), nil
		default:
			return jsonResponse(req, http.StatusNotFound, `{}`), nil
		}
	})
}

func TestUnapproveFormSubmitsReasonAndSettles(t *testing.T) {
	for _, view := range []tuiView{tuiViewList, tuiViewDetail, tuiViewDiff} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/fail=%v", view, fail), func(t *testing.T) {
				pr := approvalTestPR(1)
				pr.viewerApproved = true
				m := actionViewTestModel(view, pr)
				const reason = "Checking the revised change."
				pending, release := holdTUIRequest()
				defer release()
				m.actions = dismissalTestRunner(t, func(path, message string) int {
					if path != "/repos/owner/repo/pulls/1/reviews/11/dismissals" ||
						message != reason {
						t.Error("unexpected review dismissal request")
					}
					<-pending
					if fail {
						return http.StatusForbidden
					}
					return http.StatusOK
				})
				h := newTUIHarness(t, m)
				h.Key("a").Wait("Unapprove " + pr.Ref() + "?").Assert(defaultUnapproveMessage).
					Key("ctrl+a").Key("ctrl+k").Paste(reason).Wait(reason).
					Key("alt+enter").Wait("Unapproving " + pr.Ref()).AssertNot(reason)
				release()
				if fail {
					h.Wait("Unapprove failed:").Key("a").Wait("Unapprove " + pr.Ref() + "?")
				} else {
					h.Wait("Unapproved " + pr.Ref()).Assert(" approve ").AssertNot("unapprove")
				}
			})
		}
	}
}

func TestUnapproveFormRequiresReasonAndCanCancel(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	h := newTUIHarness(t, listTUIModel(pr))
	h.Key(tuiKeybindApprove).Wait(defaultUnapproveMessage).Key("ctrl+a").Key("ctrl+k").
		WaitNot(defaultUnapproveMessage).
		Key("alt+enter").Paste("Still editing").Wait("Still editing").Assert("Reason")
	h.KeyEsc().
		WaitNot("Still editing").
		Key(tuiKeybindApprove).
		Wait("Unapprove " + pr.Ref() + "?").
		Assert("Still editing")
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
	m.cfg = &Config{}
	newTUIHarness(t, m).Key("a").Wait("Approve " + unapproved.Ref() + "?")
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
	first.Title, second.Title = "First change", "Second change"
	first.viewerApproved, second.viewerApproved = true, true
	m := listTUIModel(first, second)
	m.sortColumn, m.sortAsc = colTitle, true
	m.width = 160
	const reason = "Revisiting both reviews."
	pending, release := holdTUIRequest()
	defer release()
	m.actions = dismissalTestRunner(t, func(path, message string) int {
		if message != reason {
			t.Error("unexpected batch dismissal reason")
		}
		<-pending
		switch path {
		case "/repos/owner/repo/pulls/1/reviews/11/dismissals":
			return http.StatusOK
		case "/repos/owner/repo/pulls/2/reviews/11/dismissals":
			return http.StatusForbidden
		default:
			t.Error("unexpected batch dismissal path")
			return http.StatusNotFound
		}
	})
	h := newTUIHarness(t, m)
	h.Key("shift+down").Wait("❯ 2 ").Key("a").Wait("Unapprove 2 PRs?").
		Key("ctrl+a").Key("ctrl+k").Paste(reason).Wait(reason).
		Key("alt+enter").Wait("Unapproving 2 PRs")
	release()
	h.Wait("Unapprove failed:").Assert(second.Ref() + ":").Assert("1/2 (1 failed)").KeyEsc()
	h.Key("1").Wait("❯ 1 ").Key("a").Wait("Approve " + first.Ref() + "?").KeyEsc()
	h.Key("2").Wait("❯ 2 ").Key("a").Wait("Unapprove " + second.Ref() + "?")
}

func TestUnapproveWithoutConfirmationUsesDefaultReason(t *testing.T) {
	pr := approvalTestPR(1)
	pr.viewerApproved = true
	m := listTUIModel(pr)
	m.actions = dismissalTestRunner(t, func(_, message string) int {
		if message != defaultUnapproveMessage {
			t.Error("unexpected default review dismissal reason")
		}
		return http.StatusOK
	})
	h := newTUIHarness(t, m)
	h.Key("alt+a").Wait("Unapproved " + pr.Ref()).AssertNot("unapprove")
}
