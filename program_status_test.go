package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/gechr/clog"
	"github.com/gechr/primer/flash"
	"github.com/gechr/primer/pick"
	"github.com/stretchr/testify/require"
)

func TestCompletedProgramState(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want clog.ProgramState
	}{
		{name: "success", want: clog.ProgramStateDone},
		{name: "handled success", err: errOK, want: clog.ProgramStateDone},
		{name: "failure", err: errors.New("request failed"), want: clog.ProgramStateError},
		{name: "handled failure", err: errFatal, want: clog.ProgramStateError},
		{name: "canceled", err: errCanceled, want: clog.ProgramStateIdle},
		{name: "picker canceled", err: pick.ErrCanceled, want: clog.ProgramStateIdle},
		{name: "form canceled", err: huh.ErrUserAborted, want: clog.ProgramStateIdle},
		{name: "TUI interrupted", err: fmt.Errorf("interactive TUI: %w", tea.ErrInterrupted), want: clog.ProgramStateIdle},
		{name: "wrapped cancellation", err: fmt.Errorf("select: %w", pick.ErrCanceled), want: clog.ProgramStateIdle},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, completedProgramState(tt.err))
		})
	}
	require.Zero(t, exitCode(errCanceled))
}

func TestTUIProgramStatus(t *testing.T) {
	for _, tt := range []struct {
		name  string
		model tuiModel
		state clog.ProgramState
		kind  clog.ProgramStatusKind
	}{
		{name: "browsing", state: clog.ProgramStateIdle},
		{name: "refresh", model: tuiModel{refreshing: true}, state: clog.ProgramStateWorking},
		{name: "changed PR refresh", model: tuiModel{changedRefreshInFlight: true}, state: clog.ProgramStateWorking},
		{name: "queued changes", model: tuiModel{changedPRs: prKeys{"example/repo#1": true}}, state: clog.ProgramStateIdle},
		{name: "error during changed PR refresh", model: tuiModel{flash: flash.State{Err: true}, changedRefreshInFlight: true}, state: clog.ProgramStateError},
		{name: "prompt during changed PR refresh", model: tuiModel{confirmAction: tuiActionInfo, changedRefreshInFlight: true}, state: clog.ProgramStateBlocked, kind: clog.ProgramStatusKindQuestion},
		{name: "diff", model: tuiModel{diffLoading: true}, state: clog.ProgramStateWorking},
		{name: "detail", model: tuiModel{detailLoading: true}, state: clog.ProgramStateWorking},
		{name: "approval", model: tuiModel{pendingApprovals: map[prKey]pendingApproval{"example/repo#1": {}}}, state: clog.ProgramStateWorking},
		{name: "action", model: tuiModel{actionsInFlight: 1}, state: clog.ProgramStateWorking},
		{name: "background read", model: tuiModel{readsInFlight: 1}, state: clog.ProgramStateWorking},
		{name: "error", model: tuiModel{flash: flash.State{Err: true}}, state: clog.ProgramStateError},
		{name: "error during refresh", model: tuiModel{flash: flash.State{Err: true}, refreshing: true}, state: clog.ProgramStateError},
		{name: "failure summary", model: tuiModel{confirmAction: tuiActionInfo, flash: flash.State{Err: true}}, state: clog.ProgramStateError},
		{name: "permission", model: tuiModel{confirmAction: tuiActionClose, confirmCmd: func() tea.Msg { return nil }}, state: clog.ProgramStateBlocked, kind: clog.ProgramStatusKindPermission},
		{name: "input", model: tuiModel{confirmAction: tuiActionComment, confirmHasInput: true}, state: clog.ProgramStateBlocked, kind: clog.ProgramStatusKindQuestion},
		{name: "information", model: tuiModel{confirmAction: tuiActionInfo}, state: clog.ProgramStateBlocked, kind: clog.ProgramStatusKindQuestion},
		{name: "prompt during refresh", model: tuiModel{confirmAction: tuiActionInfo, refreshing: true}, state: clog.ProgramStateBlocked, kind: clog.ProgramStatusKindQuestion},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state, kind := tt.model.programStatus()
			require.Equal(t, tt.state, state)
			require.Equal(t, tt.kind, kind)
		})
	}
}

func TestProgramStatusRedirectedOutput(t *testing.T) {
	previous := clog.Default()
	t.Cleanup(func() { clog.SetDefault(previous) })
	for _, mode := range []clog.ColorMode{clog.ColorAlways, clog.ColorNever} {
		var output bytes.Buffer
		clog.SetDefault(clog.New(clog.NewOutput(&output, mode)))
		reportProgramStatus(clog.ProgramStateWorking, "")
		reportProgramStatus(clog.ProgramStateBlocked, clog.ProgramStatusKindPermission)
		reportProgramStatus(clog.ProgramStateDone, "")
		require.Empty(t, output.String())
	}
}

func TestBackgroundReadStatusLifecycle(t *testing.T) {
	m := tuiModel{queryGen: 2}
	require.Nil(t, m.trackRead(nil))
	first := m.trackRead(func() tea.Msg { return diffFetchedMsg{key: "repo#2"} })
	second := m.trackRead(func() tea.Msg { return headValidationMsg{queryGen: 1} })
	state, _ := m.programStatus()
	require.Equal(t, clog.ProgramStateWorking, state)

	m, _ = updateModel(t, m, first())
	state, _ = m.programStatus()
	require.Equal(t, clog.ProgramStateWorking, state, "another read is still running")
	require.Equal(t, 1, m.readsInFlight)

	m, _ = updateModel(t, m, second())
	state, _ = m.programStatus()
	require.Equal(t, clog.ProgramStateIdle, state, "stale results still settle their reads")
	require.Zero(t, m.readsInFlight)

	m.detailLoading, m.detailAwaitKey = true, "repo#1"
	failed := m.trackRead(func() tea.Msg {
		return detailFetchedMsg{key: m.detailAwaitKey, err: errors.New("read failed")}
	})
	m, _ = updateModel(t, m, failed())
	state, _ = m.programStatus()
	require.Equal(t, clog.ProgramStateError, state)
	require.Zero(t, m.readsInFlight)
}

func TestBackgroundReadDispatchReportsWorking(t *testing.T) {
	for _, tt := range []struct {
		name  string
		start func(*tuiModel, prKey) tea.Cmd
	}{
		{name: "diff prefetch", start: func(m *tuiModel, key prKey) tea.Cmd { return m.fetchDiffCmd(key) }},
		{name: "PR status", start: func(m *tuiModel, key prKey) tea.Cmd { return m.statusCmd(key) }},
		{name: "detail refresh", start: func(m *tuiModel, _ prKey) tea.Cmd { return m.refetchDetail() }},
		{name: "head validation", start: func(m *tuiModel, _ prKey) tea.Cmd { return m.startHeadValidation(m.items) }},
		{name: "detail checks", start: func(m *tuiModel, key prKey) tea.Cmd {
			var cmd tea.Cmd
			*m, cmd = updateModel(t, *m, detailRefreshTickMsg{key: key})
			return cmd
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pr := approvalTestPR(1)
			m := approvalTestModel("", pr)
			m.actions = NewActionRunner(nil, &api.GraphQLClient{})
			m.gql = m.actions.gql
			m.view = tuiViewDetail
			m.detailKey = makePRKey(pr)
			require.NotNil(t, tt.start(&m, m.detailKey))
			state, _ := m.programStatus()
			require.Equal(t, clog.ProgramStateWorking, state)
			require.Equal(t, 1, m.readsInFlight)
		})
	}
}

func TestWatchProgramStatus(t *testing.T) {
	for _, tt := range []struct {
		name       string
		fetching   bool
		validating int
		failed     bool
		want       clog.ProgramState
	}{
		{name: "waiting", want: clog.ProgramStateIdle},
		{name: "fetching", fetching: true, want: clog.ProgramStateWorking},
		{name: "validating", validating: 1, want: clog.ProgramStateWorking},
		{name: "overlapping validations", validating: 2, want: clog.ProgramStateWorking},
		{name: "fetch during validation", fetching: true, validating: 1, want: clog.ProgramStateWorking},
		{name: "failed", failed: true, want: clog.ProgramStateError},
		{name: "validation after failure", validating: 1, failed: true, want: clog.ProgramStateError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, watchProgramStatus(tt.fetching, tt.validating, tt.failed))
		})
	}
}
