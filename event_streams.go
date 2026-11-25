package main

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/cli/v2/pkg/cmd/event/client"
	"github.com/gechr/clog"
)

// Each listener holds one open HTTP connection.
const (
	maxEventStreams  = 8
	eventBatchDelay  = 250 * time.Millisecond
	eventAttachDelay = time.Second
)

const (
	eventReview     = "com.github.pull_request.review.submitted.v1"
	eventState      = "com.github.pull_request.state.changed.v1"
	eventCheckSuite = "com.github.pull_request.check_suite.status.changed.v1"
	eventConflict   = "com.github.pull_request.merge_conflict.v1"
	eventHead       = "com.github.pull_request.head.changed.v1"
)

type prEventScope uint8

const (
	eventStatus prEventScope = 1 << iota
	eventReviews
	eventNewHead
	eventFirstHeadProbe

	eventStatusAndReviews = eventStatus | eventReviews
)

type prEvents map[prKey]prEventScope

type eventTargets map[prKey]string

func (targets eventTargets) add(pr PullRequest) {
	if strings.HasPrefix(pr.URL, "https://github.com/") {
		targets[makePRKey(pr)] = pr.URL
	}
}

var pullRequestEvents = []string{
	eventCheckSuite,
	eventHead,
	eventConflict,
	eventReview,
	eventState,
}

type eventStreams struct {
	client    func() (client.EventClient, error)
	listeners map[prKey]context.CancelFunc // owned by the watch or TUI loop
	workers   sync.WaitGroup
	stopped   chan struct{}
	wake      chan struct{}
	failed    chan struct{}
	warnOnce  sync.Once
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
		client:    sync.OnceValues(newEventClient),
		listeners: make(map[prKey]context.CancelFunc),
		stopped:   make(chan struct{}),
		wake:      make(chan struct{}, 1),
		failed:    make(chan struct{}, 1),
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
	wanted := make(eventTargets)
	for _, pr := range prs {
		wanted.add(pr)
		if len(wanted) == maxEventStreams {
			break
		}
	}
	s.detachHidden(wanted)
	s.attach(wanted)
}

func (s *eventStreams) detachHidden(wanted eventTargets) {
	for key, cancel := range s.listeners {
		if _, keep := wanted[key]; !keep {
			cancel()
			delete(s.listeners, key)
			s.mu.Lock()
			delete(s.pending, key)
			s.mu.Unlock()
		}
	}
}

func (s *eventStreams) attach(wanted eventTargets) {
	for key, url := range wanted {
		if _, listening := s.listeners[key]; listening {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.listeners[key] = cancel
		s.workers.Go(func() {
			err := s.listenPREvents(ctx, url, func(scope prEventScope) {
				s.queue(ctx, key, scope)
			})
			if ctx.Err() == nil {
				s.warnOnce.Do(func() {
					clog.Warn().Err(err).Msg("GitHub Event Stream unavailable")
					s.failed <- struct{}{}
				})
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
	s.enqueue(key, scope)
}

// After a push GitHub may still report a conflict or have no checks yet.
// Use the batch timer for at most two more reads while either condition holds.
func (s *eventStreams) followUpHead(pr PullRequest, scope prEventScope) {
	if pr.State != valueOpen || (!pr.awaitingChecks && pr.MergeStatus != MergeStatusConflict) {
		return
	}
	key := makePRKey(pr)
	if _, active := s.listeners[key]; !active {
		return
	}
	next := eventStatus
	switch {
	case scope&eventNewHead != 0:
		next |= eventFirstHeadProbe
	case scope&eventFirstHeadProbe == 0:
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueue(key, next)
}

// Caller holds mu. Combine scopes so pushes and follow-up probes share a single read.
func (s *eventStreams) enqueue(key prKey, scope prEventScope) {
	if len(s.pending) == 0 && s.timer == nil {
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
	s.pending[key] |= scope
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

type (
	prEventsMsg           struct{}
	eventStreamFailureMsg struct{}
)

func (s *eventStreams) wait() tea.Msg {
	select {
	case <-s.stopped:
		return nil
	case <-s.wake:
		return prEventsMsg{}
	case <-s.failed:
		return eventStreamFailureMsg{}
	}
}
