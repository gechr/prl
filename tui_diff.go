package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/layout"
)

// fetchDiffCmd fetches key's diff in the background, tagged with the current
// diff session so replies from a dismissed session are dropped.
func (m *tuiModel) fetchDiffCmd(key prKey) tea.Cmd {
	idx := m.resolveIndex(key, -1)
	if idx < 0 {
		return nil
	}
	if m.diffInFlight == nil {
		m.diffInFlight = make(prKeys)
	}
	m.diffInFlight[key] = true
	pr := m.rows[idx].Item.PR
	actions := m.actions
	gen := m.diffGen
	return func() tea.Msg {
		owner, repo := prOwnerRepo(pr)
		diff, headSHA, err := actions.fetchDiff(owner, repo, pr.Number)
		return diffFetchedMsg{
			gen:     gen,
			index:   idx,
			key:     key,
			diff:    diff,
			headSHA: headSHA,
			err:     err,
		}
	}
}

// openDiff shows key's diff: straight away from the prefetch cache when it is
// still current, otherwise once its (possibly already running) fetch returns.
func (m *tuiModel) openDiff(key prKey) tea.Cmd {
	pr := m.prForKey(key)
	if pr == nil {
		return nil
	}
	if cached, ok := m.diffPrefetch[key]; ok {
		delete(m.diffPrefetch, key)
		if prefetchIsCurrent(*pr, cached) {
			return m.showDiff(cached)
		}
	}
	m.diffAwaitKey = key
	m.diffLoading = true
	flashPending(m, statusDiffing, pr)
	if m.diffInFlight[key] {
		return nil
	}
	return m.fetchDiffCmd(key)
}

// prefetchIsCurrent reports whether a prefetched diff still matches the PR's
// head commit.
func prefetchIsCurrent(pr PullRequest, cached diffFetchedMsg) bool {
	return pr.HeadSHA == "" || cached.headSHA == "" || pr.HeadSHA == cached.headSHA
}

// showDiff switches the view to a fetched diff, then refreshes the PR's status
// and prefetches the next queued diff.
func (m *tuiModel) showDiff(msg diffFetchedMsg) tea.Cmd {
	m.diffLoading = false
	m.diffAwaitKey = ""
	idx := m.resolveIndex(msg.key, msg.index)
	if idx < 0 {
		return nil // PR no longer in list
	}
	if m.view == tuiViewDetail {
		m.stopDetailRefresh(true)
	}
	m.refreshTerminalSize()
	m.diffKey = msg.key
	pr := m.rows[idx].Item.PR
	m.diff = highlightDiff(msg.diff, pr.URL, msg.headSHA)
	m.diffLines = layout.WrapLines(m.diff, m.width-tuiScrollbarWidth)
	m.syncDiffView()
	m.diffView.GotoTop()
	m.view = tuiViewDiff
	m.flash.Msg = ""
	return tea.Batch(m.statusCmd(msg.key), m.prefetchNext())
}

// prefetchNext starts fetching the next queued diff unless it is already
// cached or in flight.
func (m *tuiModel) prefetchNext() tea.Cmd {
	for _, key := range m.diffQueue {
		if m.resolveIndex(key, -1) < 0 {
			continue
		}
		if _, ok := m.diffPrefetch[key]; ok || m.diffInFlight[key] {
			return nil
		}
		return m.fetchDiffCmd(key)
	}
	return nil
}

// handleDiffFetched routes a fetched diff: the one the view is waiting for is
// shown, anything else is kept for later.
func (m tuiModel) handleDiffFetched(msg diffFetchedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.diffGen {
		return m, nil // stale fetch from a dismissed diff session
	}
	delete(m.diffInFlight, msg.key)
	if msg.key != m.diffAwaitKey {
		if msg.err == nil {
			if m.diffPrefetch == nil {
				m.diffPrefetch = make(map[prKey]diffFetchedMsg)
			}
			m.diffPrefetch[msg.key] = msg
		}
		return m, nil
	}
	m.diffLoading = false
	m.diffAwaitKey = ""
	if msg.err != nil {
		flashCmd := flashResult(&m, "Diff failed:", fmt.Sprintf("%v", msg.err), "", true)
		// Skip to next in queue if available.
		if advanced, nextCmd := advanceDiffQueue(&m); advanced {
			return m, tea.Batch(flashCmd, nextCmd)
		}
		if m.view == tuiViewDetail {
			return m, tea.Batch(flashCmd, m.scheduleDetailRefresh())
		}
		return m, flashCmd
	}
	return m, m.showDiff(msg)
}

// advanceDiffQueue pops the next PR from the diff queue and opens it. It
// returns false when the queue is exhausted.
func advanceDiffQueue(m *tuiModel) (bool, tea.Cmd) {
	for len(m.diffQueue) > 0 {
		key := m.diffQueue[0]
		m.diffQueue = m.diffQueue[1:]
		if m.resolveIndex(key, -1) < 0 {
			continue
		}
		return true, m.openDiff(key)
	}
	return false, nil
}

// advanceDiff moves to the next queued diff, recording the current one in
// history so it can be revisited.
func (m *tuiModel) advanceDiff() (bool, tea.Cmd) {
	outgoing := m.diffKey
	advanced, cmd := advanceDiffQueue(m)
	if advanced && outgoing != "" {
		m.diffHistory = append(m.diffHistory, outgoing)
	}
	return advanced, cmd
}

// advanceOrExitDiff moves to the next queued diff, or back to the list when
// the queue is exhausted.
func (m *tuiModel) advanceOrExitDiff() tea.Cmd {
	if advanced, cmd := m.advanceDiff(); advanced {
		return cmd
	}
	return m.exitDiffView()
}
