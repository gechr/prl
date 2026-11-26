package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	cansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func resumeTestModel(prs []PullRequest) tuiModel {
	m := approvalTestModel("", prs...)
	m.cfg = &Config{}
	m.resolver = &AuthorResolver{}
	m.filterInput = textinput.New()
	m.replaceListItems(buildPRRowModels(prs, "", m.resolver))
	m.applyWindowSize(90, 15)
	m.lastRefreshAt = time.Now().Add(-2 * tuiListResultCacheMaxAge).UTC()
	return m
}

func TestResumeCachedBrowser(t *testing.T) {
	prs := make([]PullRequest, 28)
	for i := range prs {
		prs[i] = approvalTestPR(i + 1)
		prs[i].Title = "Example change"
	}
	for _, tc := range []struct {
		name string
		view tuiView
	}{
		{"list", tuiViewList}, {"detail", tuiViewDetail}, {"diff", tuiViewDiff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := resumeTestModel(prs)
			m.session = newTUISession(root, []string{"-i", "two words"}, "")
			require.NotNil(t, m.session)
			require.Nil(t, m.View().ProgramResume)
			m.cursor, m.offset = 20, 16
			m.selected[m.rowKeyAt(4)] = true
			m.filterInput.SetValue("Example")
			m.view = tc.view
			switch tc.view {
			case tuiViewDetail:
				m.detailKey = m.rowKeyAt(18)
				m.detail.Body = strings.Repeat("Example paragraph.\n\n", 50)
				m.detailLines = m.renderDetailContent()
				m.syncDetailView()
				m.detailView.setOffset(10)
			case tuiViewDiff:
				m.diffKey = m.rowKeyAt(19)
				m.diff = strings.Repeat("+ example line\n", 50)
				m.diffQueue = []prKey{m.rowKeyAt(21)}
				m.diffHistory = []prKey{m.rowKeyAt(17)}
				m.diffQueuePos, m.diffQueueTotal = 2, 3
				m.applyWindowSize(m.width, m.height)
				m.diffView.setOffset(10)
			case tuiViewList:
			}

			m.saveSession()
			command := m.View().ProgramResume
			require.NotNil(t, command)
			require.True(t, filepath.IsAbs(command.Args[0]))
			require.True(t, filepath.IsAbs(command.Dir))
			require.Equal(t, []string{"-i", "two words"}, command.Args[2:])
			id, ok := strings.CutPrefix(command.Args[1], resumeFlag)
			require.True(t, ok)

			saved := loadSession(root, id)
			require.NotNil(t, saved)
			require.Equal(t, m.lastRefreshAt, saved.LastRefresh)
			var cached []PullRequest
			for _, pr := range saved.PRs {
				cached = append(cached, pr.pullRequest())
			}
			restored := resumeTestModel(cached)
			saved.applySettings(restored.cli, restored.cfg)
			saved.restore(&restored)
			require.Equal(t, m.cursor, restored.cursor)
			require.Equal(t, m.offset, restored.offset)
			require.Equal(t, m.selected, restored.selected)
			before, after := m.View().Content, restored.View().Content
			if tc.view == tuiViewDiff {
				// The restored header marks the saved patch outdated until its live read.
				require.True(t, restored.reviewOutdated())
				_, before, _ = strings.Cut(before, nl)
				_, after, _ = strings.Cut(after, nl)
			}
			require.Equal(t, before, after)
			require.Equal(t, m.diffQueue, restored.diffQueue)
			require.Equal(t, m.diffHistory, restored.diffHistory)
			require.Equal(t, m.detailView.offset(), restored.detailView.offset())
			require.Equal(t, m.diffView.offset(), restored.diffView.offset())
			restored, _ = updateModel(
				t,
				restored,
				tea.WindowSizeMsg{Width: m.width, Height: m.height},
			)
			if tc.view != tuiViewList {
				require.False(t, restored.refreshing)
				restored, _ = updateModel(t, restored, tea.KeyPressMsg{Code: tea.KeyEsc})
			}
			require.True(t, restored.refreshing)

			// A repeated resume gets an independent writable cache.
			second := newTUISession(root, command.Args[1:], id)
			require.NotNil(t, second)
			defer second.lock.Close()
			require.NotEqual(t, m.session.dir, second.dir)
			require.Equal(t, command.Args[2:], second.command.Args[2:])

			if tc.view != tuiViewList {
				m, _ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
				require.NotNil(t, m.View().ProgramResume)
			}
			m, _ = pressKey(t, m, "q")
			require.Nil(t, m.View().ProgramResume)
			require.Nil(t, loadSession(root, id))
		})
	}
}

func TestResumeRequiresCompleteSnapshot(t *testing.T) {
	root := t.TempDir()
	m := resumeTestModel(nil)
	m.session = newTUISession(root, []string{"-i"}, "")
	require.NotNil(t, m.session)
	defer m.session.lock.Close()
	m.saveSession()
	id := strings.TrimPrefix(m.session.command.Args[1], resumeFlag)
	require.NotNil(t, loadSession(root, id))
	require.NoError(t, os.Remove(filepath.Join(m.session.dir, sessionContentName(m.session.slot))))
	require.Nil(t, loadSession(root, id))
}

func TestResumeKeepsTimeFilteredRowsUntilRefresh(t *testing.T) {
	pr := approvalTestPR(1)
	pr.UpdatedAt = time.Now().Add(-time.Minute)
	older := approvalTestPR(2)
	older.UpdatedAt = pr.UpdatedAt.Add(-24 * time.Hour)
	m := resumeTestModel([]PullRequest{pr, older})
	m.cli.timePreset = "10 minutes"
	cutoff := m.sessionTimeCutoff()
	require.Equal(t, []int{m.resolveIndex(makePRKey(pr), -1)}, m.visibleIndices())

	// Move the whole snapshot into the past to simulate a later launch.
	age := -time.Hour
	pr.UpdatedAt = pr.UpdatedAt.Add(age)
	older.UpdatedAt = older.UpdatedAt.Add(age)
	m = resumeTestModel([]PullRequest{pr, older})
	m.cli.timePreset = "10 minutes"
	require.Empty(t, m.visibleIndices())
	saved := savedSession{TimeCutoff: new(cutoff.Add(age))}
	saved.restore(&m)
	require.Equal(t, []int{m.resolveIndex(makePRKey(pr), -1)}, m.visibleIndices())
	m, _ = updateModel(t, m, refreshResultMsg{items: m.items})
	require.Empty(t, m.visibleIndices())
}

func TestResumeUsesTerminalEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv(envKeyGitHubToken, t.Name())
	t.Setenv(cansi.ProgramResumeTokenEnv, t.Name())
	m := resumeTestModel(nil)
	for _, msg := range []tea.EnvMsg{nil, {cansi.ProgramResumeTokenEnv + "="}} {
		m, _ = updateModel(t, m, msg)
		require.Nil(t, m.View().ProgramResume)
	}
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)

	env := tea.EnvMsg{cansi.ProgramResumeTokenEnv + "=" + t.Name()}
	m, _ = updateModel(t, m, env)
	require.NotNil(t, m.View().ProgramResume)
	m, _ = pressKey(t, m, "q")
	m, _ = updateModel(t, m, env)
	require.Nil(t, m.View().ProgramResume)
}

func TestResumeApprovalUsesConfirmedVisibility(t *testing.T) {
	root := t.TempDir()
	pr := approvalTestPR(1)
	m := resumeTestModel([]PullRequest{pr})
	m.session = newTUISession(root, []string{"-i"}, "")
	require.NotNil(t, m.session)
	defer m.session.lock.Close()
	m.cli.Review = valueReviewFilterSelfRequired
	m.beginApproval([]prKey{makePRKey(pr)})
	require.Empty(t, m.visibleIndices())
	m.saveSession()
	saved := loadSession(root, strings.TrimPrefix(m.session.command.Args[1], resumeFlag))
	require.NotNil(t, saved)
	restored := resumeTestModel([]PullRequest{saved.PRs[0].pullRequest()})
	saved.applySettings(restored.cli, restored.cfg)
	saved.restore(&restored)
	require.Equal(t, []int{0}, restored.visibleIndices())
	require.Empty(t, restored.pendingApprovals)
}

func TestPruneSessionsPreservesLiveBrowser(t *testing.T) {
	root := t.TempDir()
	session := newTUISession(root, []string{"-i"}, "")
	require.NotNil(t, session)
	old := time.Now().AddDate(0, -2, 0)
	require.NoError(t, os.Chtimes(session.dir, old, old))
	pruneSessions(root)
	require.DirExists(t, session.dir)
	require.NoError(t, session.lock.Close())
	pruneSessions(root)
	require.NoDirExists(t, session.dir)
}

func TestResumedReviewWaitsForLiveContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		view tuiView
	}{
		{"diff", tuiViewDiff}, {"detail", tuiViewDetail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := approvalTestPR(1)
			pr.NodeID = ""
			key := makePRKey(pr)
			m := resumeTestModel([]PullRequest{pr})
			m.actions = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Path, "/reviews") || strings.HasSuffix(req.URL.Path, "/files") {
					return jsonResponse(req, http.StatusOK, "[]"), nil
				}
				return jsonResponse(req, http.StatusOK, `{"head":{"sha":"current"},"body":"Live description"}`), nil
			})
			m.actions.diffClient = testActionRunner(t, func(req *http.Request) (*http.Response, error) {
				return jsonResponse(req, http.StatusOK, "+live patch"), nil
			}).rest
			saved := savedSession{
				Active: key, View: tc.view,
				Diff: "+saved patch", Detail: PRDetail{Body: "Saved description"},
			}
			saved.restore(&m)
			require.True(t, m.reviewOutdated())
			start := m.Init()
			require.NotNil(t, start)
			for _, binding := range []string{tuiKeybindApproveNoConfirm, tuiKeybindMerge} {
				m, _ = pressKey(t, m, binding)
				require.Equal(t, tc.view, m.view)
				require.Zero(t, m.actionsInFlight)
				require.Empty(t, m.confirmAction)
				require.Equal(t, 1, m.readsInFlight)
			}
			m, _ = updateModel(t, m, start())
			require.False(t, m.reviewOutdated())
			switch tc.view {
			case tuiViewDiff:
				require.Equal(t, "+live patch"+nl, cansi.Strip(m.diff))
			case tuiViewDetail:
				require.Equal(t, "Live description", m.detail.Body)
			case tuiViewList:
			}
			m, _ = pressKey(t, m, tuiKeybindApproveNoConfirm)
			require.Equal(t, 1, m.actionsInFlight)
		})
	}
}

func TestFailedResumeReadRetriesBeforeApproval(t *testing.T) {
	pr := approvalTestPR(1)
	key := makePRKey(pr)
	m := resumeTestModel([]PullRequest{pr})
	saved := savedSession{View: tuiViewDiff, Active: key, Diff: "+saved"}
	saved.restore(&m)
	m, _ = updateModel(t, m, readDoneMsg{msg: diffFetchedMsg{key: key, err: errApproveTest}})
	require.False(t, m.diffInFlight[key])
	m, _ = pressKey(t, m, tuiKeybindApproveNoConfirm)
	require.Equal(t, tuiViewDiff, m.view)
	require.Zero(t, m.actionsInFlight)
	require.True(t, m.diffInFlight[key])
}

func TestFailedNavigationPreservesLiveResumedDetail(t *testing.T) {
	pr, next := approvalTestPR(1), approvalTestPR(2)
	key := makePRKey(pr)
	m := resumeTestModel([]PullRequest{pr, next})
	saved := savedSession{View: tuiViewDetail, Active: key, Detail: PRDetail{Body: "Saved description"}}
	saved.restore(&m)
	m.openDetail(m.resolveIndex(makePRKey(next), -1), 0)
	m, _ = updateModel(t, m, detailFetchedMsg{
		key: key, reviewVersion: m.reviewVersion(pr), detail: PRDetail{Body: "Live description"},
	})
	m, _ = updateModel(t, m, detailFetchedMsg{
		key: makePRKey(next), reviewVersion: m.reviewVersion(next), err: errApproveTest,
	})
	require.Equal(t, key, m.detailKey)
	require.Equal(t, "Live description", m.detail.Body)
}
