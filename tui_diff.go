package main

import (
	"fmt"
	"time"

	"bloomfx.dev/bloom/fx"
	tea "charm.land/bubbletea/v2"
)

// fetchDiffCmd shares a single outstanding read per PR across review queues.
func (m *tuiModel) fetchDiffCmd(key prKey) tea.Cmd {
	idx := m.resolveIndex(key, -1)
	if idx < 0 || m.diffInFlight[key] {
		return nil
	}
	if m.diffInFlight == nil {
		m.diffInFlight = make(prKeys)
	}
	m.diffInFlight[key] = true
	pr := m.rows[idx].Item.PR
	actions := m.actions
	headGen := m.diffHeadGen[key]
	return m.trackRead(func() tea.Msg {
		owner, repo := prOwnerRepo(pr)
		diff, headSHA, err := actions.fetchDiff(owner, repo, pr.Number)
		return diffFetchedMsg{
			headGen:          headGen,
			baseRef:          pr.BaseRefName,
			updatedAt:        pr.UpdatedAt,
			index:            idx,
			key:              key,
			diff:             diff,
			headSHA:          headSHA,
			requestedHeadSHA: pr.HeadSHA,
			err:              err,
		}
	})
}

// Revisits show the last viewed diff while revalidating it. A speculative
// prefetch must still match the known head before it is shown for the first time.
func (m *tuiModel) openDiff(key prKey, from fx.Direction) tea.Cmd {
	pr := m.prForKey(key)
	if pr == nil {
		return nil
	}
	if cached, ok := m.diffCache[key]; ok && (cached.visited || m.diffCurrent(*pr, cached)) {
		show := m.showDiff(cached, from)
		return tea.Batch(show, m.refreshCachedDiff(key))
	}
	m.diffAwaitKey, m.diffAwaitFrom = key, from
	m.diffLoading = true
	flashPending(m, statusDiffing, pr)
	return m.fetchDiffCmd(key)
}

func (m tuiModel) diffCurrent(pr PullRequest, cached diffFetchedMsg) bool {
	if cached.headGen != m.diffHeadGen[cached.key] || pr.BaseRefName != cached.baseRef {
		return false
	}
	if pr.HeadSHA == "" {
		return cached.updatedAt.Equal(pr.UpdatedAt)
	}
	return pr.HeadSHA == cached.requestedHeadSHA || pr.HeadSHA == cached.headSHA
}

func (m *tuiModel) refreshCachedDiff(key prKey) tea.Cmd {
	pr := m.prForKey(key)
	if pr == nil {
		return nil
	}
	if cached, ok := m.diffCache[key]; ok && m.diffCurrent(*pr, cached) &&
		m.reviewReadFresh(key, cached.fetchedAt) {
		return nil
	}
	return m.fetchDiffCmd(key)
}

// showDiff switches the view to a fetched diff, then refreshes the PR's status
// and prefetches the next queued diff.
func (m *tuiModel) showDiff(msg diffFetchedMsg, from fx.Direction) tea.Cmd {
	m.diffLoading = false
	m.diffAwaitKey, m.diffAwaitFrom = "", 0
	idx := m.resolveIndex(msg.key, msg.index)
	if idx < 0 {
		m.flash.Msg = ""
		return nil // PR no longer in list
	}
	m.refreshTerminalSize()
	var slide tea.Cmd
	if m.view == tuiViewDiff && m.diffKey != msg.key {
		slide = m.startPRSlide(from, m.diffView)
	}
	if m.view == tuiViewDetail {
		m.stopDetailRefresh(true)
	}
	msg.visited = true
	m.diffCache[msg.key] = msg
	m.diffKey = msg.key
	m.diffQueuePos = m.diffQueueTotal - len(m.diffQueue)
	m.setDiffContent(msg.diff)
	m.diffView.setOffset(0)
	m.view = tuiViewDiff
	m.flash.Msg = ""
	return tea.Batch(slide, m.statusCmd(msg.key), m.prefetchNext())
}

func (m *tuiModel) setDiffContent(diff string) {
	m.contentRevision++
	m.diff = diff
	m.syncDiffView()
}

// prefetchNext starts fetching the next queued diff unless it is already
// cached or in flight.
func (m *tuiModel) prefetchNext() tea.Cmd {
	for _, key := range m.diffQueue {
		if m.resolveIndex(key, -1) < 0 {
			continue
		}
		return m.refreshCachedDiff(key)
	}
	return nil
}

// handleDiffFetched routes a fetched diff: the one the view is waiting for is
// shown, anything else is kept for later.
func (m tuiModel) handleDiffFetched(msg diffFetchedMsg) (tea.Model, tea.Cmd) {
	delete(m.diffInFlight, msg.key)
	pr := m.prForKey(msg.key)
	awaited := msg.key == m.diffAwaitKey
	visible := m.view == tuiViewDiff && msg.key == m.diffKey
	if pr != nil && !m.diffCurrent(*pr, msg) {
		if awaited || (visible && !m.diffLoading) {
			cmd := m.fetchDiffCmd(msg.key)
			return m, cmd
		}
		return m, nil
	}
	if msg.err == nil && pr != nil {
		if m.diffCache == nil {
			m.diffCache = make(map[prKey]diffFetchedMsg)
		}
		msg.fetchedAt = time.Now()
		msg.diff = highlightDiff(msg.diff, pr.URL, msg.headSHA)
		msg.visited = visible || m.diffCache[msg.key].visited
		m.diffCache[msg.key] = msg
		if visible && !awaited {
			m.setDiffContent(msg.diff)
		}
	}
	if !awaited {
		if visible && !m.diffLoading && msg.err != nil {
			return m, flashResult(&m, "Diff refresh failed:", msg.err.Error(), "", true)
		}
		return m, nil
	}
	from := m.diffAwaitFrom
	m.diffLoading = false
	m.diffAwaitKey, m.diffAwaitFrom = "", 0
	if msg.err != nil {
		flashCmd := flashResult(&m, "Diff failed:", fmt.Sprintf("%v", msg.err), "", true)
		// Skip to next in queue if available.
		if advanced, nextCmd := advanceDiffQueue(&m, 0); advanced {
			return m, tea.Batch(flashCmd, nextCmd)
		}
		if m.view == tuiViewDetail {
			return m, tea.Batch(flashCmd, m.scheduleDetailRefresh())
		}
		return m, flashCmd
	}
	return m, m.showDiff(msg, from)
}

// advanceDiffQueue pops the next PR from the diff queue and opens it. It
// returns false when the queue is exhausted.
func advanceDiffQueue(m *tuiModel, from fx.Direction) (bool, tea.Cmd) {
	for len(m.diffQueue) > 0 {
		key := m.diffQueue[0]
		m.diffQueue = m.diffQueue[1:]
		if m.resolveIndex(key, -1) < 0 {
			continue
		}
		return true, m.openDiff(key, from)
	}
	return false, nil
}

// advanceDiff moves to the next queued diff, recording the current one in
// history so it can be revisited.
func (m *tuiModel) advanceDiff(from fx.Direction) (bool, tea.Cmd) {
	outgoing := m.diffKey
	advanced, cmd := advanceDiffQueue(m, from)
	if advanced && outgoing != "" {
		m.diffHistory = append(m.diffHistory, outgoing)
	}
	return advanced, cmd
}

// advanceOrExitDiff moves to the next queued diff, or back to the list when
// the queue is exhausted.
func (m *tuiModel) advanceOrExitDiff() tea.Cmd {
	if advanced, cmd := m.advanceDiff(0); advanced {
		return cmd
	}
	return m.exitDiffView()
}
