package main

import (
	"fmt"
	"time"

	"bloomfx.dev/bloom/fx"
	tea "charm.land/bubbletea/v2"
)

// A stamp records what the list and event stream knew when a read started.
type reviewStamp struct {
	reviewVersion

	fetchedAt time.Time
}

type reviewVersion struct {
	head, base, state, review string
	updatedAt                 time.Time
	mergeStatus               MergeStatus
	draft                     bool
	gen                       int
	eventGen                  int
}

func (m tuiModel) reviewVersion(pr PullRequest) reviewVersion {
	key := makePRKey(pr)
	return reviewVersion{
		head: pr.HeadSHA, base: pr.BaseRefName, state: pr.State, draft: pr.IsDraft,
		updatedAt: pr.UpdatedAt, review: pr.ReviewDecision, mergeStatus: pr.MergeStatus,
		gen: m.prChangeGen[key], eventGen: m.prEventGen[key],
	}
}

func (m tuiModel) reviewFresh(pr PullRequest, stamp reviewStamp) bool {
	return stamp.reviewVersion == m.reviewVersion(pr) &&
		m.reviewReadFresh(makePRKey(pr), stamp.fetchedAt)
}

func (m tuiModel) reviewReadFresh(key prKey, fetchedAt time.Time) bool {
	// Even a live stream can miss body edits, dismissed reviews and base changes.
	if !m.streamFailed && m.eventStreams != nil && m.eventStreams.listeners[key] != nil {
		return time.Since(fetchedAt) < quietStatusInterval
	}
	return time.Since(fetchedAt) < visibleStatusInterval
}

func (m *tuiModel) openDetail(idx int, from fx.Direction) tea.Cmd {
	m.stopDetailRefresh(false)
	pr := m.rows[idx].Item.PR
	key := makePRKey(pr)
	if cached, ok := m.detailCache[key]; ok {
		show := m.showDetail(key, cached.detail, idx, from)
		if !m.reviewFresh(pr, cached.reviewStamp) {
			return tea.Batch(show, m.refetchDetail())
		}
		return show
	}
	m.detailLoading = true
	m.detailAwaitKey, m.detailAwaitFrom = key, from
	flashPending(m, "Fetching", &pr)
	return m.fetchDetailCmd(idx)
}

func (m *tuiModel) refetchDetail() tea.Cmd {
	idx := m.resolveIndex(m.detailKey, -1)
	if idx < 0 || m.detailLoading || m.diffLoading {
		return nil
	}
	m.detailRefreshID++ // Supersede any pending check poll.
	return m.fetchDetailCmd(idx)
}

func (m *tuiModel) fetchDetailCmd(idx int) tea.Cmd {
	pr := m.rows[idx].Item.PR
	key := makePRKey(pr)
	if m.detailInFlight[key] {
		return nil
	}
	if m.detailInFlight == nil {
		m.detailInFlight = make(prKeys)
	}
	m.detailInFlight[key] = true
	version, actions := m.reviewVersion(pr), m.actions
	return m.trackRead(func() tea.Msg {
		owner, repo := prOwnerRepo(pr)
		detail, err := actions.fetchPRDetail(owner, repo, pr.Number, pr.NodeID)
		stamp := reviewStamp{reviewVersion: version, fetchedAt: time.Now()}
		return detailFetchedMsg{reviewStamp: stamp, key: key, detail: detail, err: err}
	})
}

func (m *tuiModel) handleDetailFetched(msg detailFetchedMsg) tea.Cmd {
	delete(m.detailInFlight, msg.key)
	awaited := m.detailLoading && m.detailAwaitKey == msg.key
	visible := m.view == tuiViewDetail && m.detailKey == msg.key
	active := visible && !m.detailLoading && !m.diffLoading
	idx := m.resolveIndex(msg.key, -1)
	if idx >= 0 && msg.reviewVersion != m.reviewVersion(m.rows[idx].Item.PR) {
		if awaited || active {
			return m.fetchDetailCmd(idx)
		}
		return nil
	}
	from := m.detailAwaitFrom
	if awaited {
		m.detailLoading = false
		m.detailAwaitKey, m.detailAwaitFrom = "", 0
	}
	if msg.err != nil {
		if !awaited && !active {
			return nil
		}
		flash := flashResult(m, "Detail failed:", fmt.Sprint(msg.err), "", true)
		if awaited {
			return tea.Batch(flash, m.resumeOpenReview())
		}
		return tea.Batch(flash, m.scheduleDetailRefresh())
	}
	if idx < 0 {
		if awaited {
			m.flash.Msg = ""
			return m.resumeOpenReview()
		}
		return nil
	}
	if msg.detail.MergeStatus != nil && *msg.detail.MergeStatus != msg.mergeStatus {
		m.applyDetailMergeStatus(msg.key, msg.detail.MergeStatus)
		msg.mergeStatus = *msg.detail.MergeStatus
		idx = m.resolveIndex(msg.key, idx)
	}
	if m.detailCache == nil {
		m.detailCache = make(map[prKey]detailFetchedMsg)
	}
	m.detailCache[msg.key] = msg
	if awaited {
		return m.showDetail(msg.key, msg.detail, idx, from)
	}
	if visible {
		// Navigation can fail and leave the outgoing detail on screen.
		m.setDetailContent(msg.detail)
	}
	if active {
		return m.scheduleDetailRefresh()
	}
	return nil
}

func (m *tuiModel) setDetailContent(detail PRDetail) {
	m.detail = detail
	m.contentRevision++
	m.detailLines = m.renderDetailContent()
	m.syncDetailView()
}

func (m *tuiModel) showDetail(key prKey, detail PRDetail, idx int, from fx.Direction) tea.Cmd {
	m.refreshTerminalSize()
	var slide tea.Cmd
	if from != 0 && m.view == tuiViewDetail && m.detailKey != key {
		slide = m.startPRSlide(from, m.detailView)
		m.cursor = idx
		m.offset = m.scrolledOffset()
	}
	m.detailKey, m.view = key, tuiViewDetail
	m.setDetailContent(detail)
	m.detailView.setOffset(0)
	m.flash.Msg = ""
	var prefetch tea.Cmd
	if _, next := m.detailNeighbors(); next >= 0 {
		pr := m.rows[next].Item.PR
		if cached, ok := m.detailCache[makePRKey(pr)]; !ok ||
			!m.reviewFresh(pr, cached.reviewStamp) {
			prefetch = m.fetchDetailCmd(next)
		}
	}
	return tea.Batch(slide, prefetch, m.scheduleDetailRefresh())
}
