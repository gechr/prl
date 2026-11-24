package main

import (
	"bufio"
	"context"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/clog"
)

// gh maintains one long-lived process and connection per pull request.
const (
	maxEventStreams = 8
	eventBatchDelay = 250 * time.Millisecond
)

const (
	eventCheckSuite = "com.github.pull_request.check_suite.status.changed.v1"
	eventConflict   = "com.github.pull_request.merge_conflict.v1"
)

type prEventScope uint8

const (
	eventStatus prEventScope = iota + 1
	eventStatusAndReviews
)

type prEvents map[prKey]prEventScope

var pullRequestEvents = []string{
	eventCheckSuite,
	"com.github.pull_request.head.changed.v1",
	eventConflict,
	"com.github.pull_request.review.submitted.v1",
	"com.github.pull_request.state.changed.v1",
}

type eventStreams struct {
	listeners map[prKey]context.CancelFunc // owned by the watch or TUI loop
	workers   sync.WaitGroup
	stopped   chan struct{}
	wake      chan struct{}
	mu        sync.Mutex
	pending   prEvents
	timer     *time.Timer
	nextRead  time.Time
}

func newEventStreams(enabled bool) *eventStreams {
	if !enabled {
		return nil
	}
	return &eventStreams{
		listeners: make(map[prKey]context.CancelFunc),
		stopped:   make(chan struct{}),
		wake:      make(chan struct{}, 1),
		pending:   make(prEvents),
	}
}

func (s *eventStreams) close() {
	if s == nil {
		return
	}
	close(s.stopped)
	s.sync(nil)
	s.workers.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (s *eventStreams) sync(prs []PullRequest) {
	if s == nil {
		return
	}
	wanted := make(map[prKey]string)
	for _, pr := range prs {
		if !strings.HasPrefix(pr.URL, "https://github.com/") {
			continue
		}
		wanted[makePRKey(pr)] = pr.URL
		if len(wanted) == maxEventStreams {
			break
		}
	}
	for key, cancel := range s.listeners {
		if _, keep := wanted[key]; !keep {
			cancel()
			delete(s.listeners, key)
		}
	}
	for key, url := range wanted {
		if _, listening := s.listeners[key]; listening {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.listeners[key] = cancel
		s.workers.Go(func() {
			err := listenPREvents(ctx, url, func(scope prEventScope) {
				s.queue(ctx, key, scope)
			})
			if err != nil && ctx.Err() == nil {
				clog.Debug().
					Err(err).
					Msg("GitHub Event Stream unavailable - continuing periodic refresh")
			}
		})
	}
}

// Delay the first notification briefly to batch simultaneous attachments. After
// a read, accumulate new events until the normal minimum refresh interval.
func (s *eventStreams) queue(ctx context.Context, key prKey, scope prEventScope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if len(s.pending) == 0 {
		s.timer = time.AfterFunc(max(eventBatchDelay, time.Until(s.nextRead)), func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.timer = nil
			select {
			case s.wake <- struct{}{}:
			default:
			}
		})
	}
	s.pending[key] = max(s.pending[key], scope)
}

// takeChanges coalesces bursts and ignores events from PRs that left the list.
func (s *eventStreams) takeChanges() prEvents {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil || len(s.pending) == 0 {
		return nil
	}
	changed := s.pending
	s.pending = make(prEvents)
	for key := range changed {
		if _, active := s.listeners[key]; !active {
			delete(changed, key)
		}
	}
	if len(changed) > 0 {
		s.nextRead = time.Now().Add(watchMinInterval)
	}
	return changed
}

type prEventsMsg struct{}

func (s *eventStreams) wait() tea.Msg {
	select {
	case <-s.stopped:
		return nil
	case <-s.wake:
		return prEventsMsg{}
	}
}

func listenPREvents(ctx context.Context, url string, notify func(prEventScope)) error {
	cmd := exec.CommandContext(ctx, "gh", "event", "listen", "--follow", "--resource", url,
		"--event", strings.Join(pullRequestEvents, ","), "--jq", ".type")
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// gh writes its attachment notice to stderr, including after reconnects.
	// Only the notice and requested event types trigger an authoritative read.
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == eventCheckSuite || line == eventConflict:
			notify(eventStatus)
		case strings.HasPrefix(line, "Listening for events") || slices.Contains(pullRequestEvents, line):
			notify(eventStatusAndReviews)
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}
