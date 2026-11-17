package main

import (
	"errors"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/gechr/clog"
	"github.com/gechr/primer/pick"
)

const (
	programName = "prl"

	exitInterrupted = 130 // 128 + SIGINT, for platforms that cannot re-raise
)

var errCanceled = errors.New("canceled")

// Only static application metadata belongs in terminal status reports.
func reportProgramStatus(state clog.ProgramState, kind clog.ProgramStatusKind) {
	clog.SetProgramStatus(clog.ProgramStatus{State: state, Kind: kind, App: programName})
}

// idleOnInterrupt reports idle if SIGINT or SIGTERM ends the run, then
// re-delivers the signal so the process exits exactly as it would without the
// handler. The returned function stops watching.
func idleOnInterrupt() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			reportProgramStatus(clog.ProgramStateIdle, "")
			signal.Stop(ch)
			if p, err := os.FindProcess(os.Getpid()); err == nil && p.Signal(sig) == nil {
				return
			}
			os.Exit(exitInterrupted)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

func completedProgramState(err error) clog.ProgramState {
	switch {
	case errors.Is(err, errCanceled),
		errors.Is(err, pick.ErrCanceled),
		errors.Is(err, huh.ErrUserAborted),
		errors.Is(err, tea.ErrInterrupted):
		return clog.ProgramStateIdle
	case err == nil, errors.Is(err, errOK):
		return clog.ProgramStateDone
	default:
		return clog.ProgramStateError
	}
}

// actionDoneMsg carries a tracked action's result back to the TUI.
type actionDoneMsg struct{ msg tea.Msg }

// trackAction counts cmd as running until its result arrives. cmd must be a
// single action, not a batch, so its result marks the action as finished.
func (m *tuiModel) trackAction(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	m.actionsInFlight++
	return func() tea.Msg { return actionDoneMsg{msg: cmd()} }
}

// readDoneMsg settles a background read even if its result is stale.
type readDoneMsg struct{ msg tea.Msg }

// trackRead tracks a single read command, never a batch or a scheduled timer.
func (m *tuiModel) trackRead(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	m.readsInFlight++
	return func() tea.Msg { return readDoneMsg{msg: cmd()} }
}

func watchProgramStatus(fetching bool, validating int, failed bool) clog.ProgramState {
	switch {
	case failed:
		return clog.ProgramStateError
	case fetching || validating > 0:
		return clog.ProgramStateWorking
	default:
		return clog.ProgramStateIdle
	}
}

func (m tuiModel) programStatus() (clog.ProgramState, clog.ProgramStatusKind) {
	switch {
	case m.confirmAction == tuiActionInfo && m.flash.Err:
		return clog.ProgramStateError, ""
	case m.confirmAction != "":
		if m.confirmHasInput || (m.confirmCmd == nil && m.confirmCmdFn == nil) {
			return clog.ProgramStateBlocked, clog.ProgramStatusKindQuestion
		}
		return clog.ProgramStateBlocked, clog.ProgramStatusKindPermission
	case m.flash.Err:
		return clog.ProgramStateError, ""
	case m.refreshing || m.changedRefreshInFlight || m.diffLoading || m.detailLoading ||
		len(m.pendingApprovals) > 0 || m.actionsInFlight > 0 || m.readsInFlight > 0:
		return clog.ProgramStateWorking, ""
	default:
		return clog.ProgramStateIdle, ""
	}
}
