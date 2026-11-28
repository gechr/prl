package main

import (
	"errors"
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"
)

// changedPRsMsg carries a batch of individual PR reads after actions or stream events.
type changedPRsMsg struct {
	items    []PRRowModel
	events   prEvents
	gens     map[prKey]int
	queryGen int
	err      error
}

// Required loads (initial data, options and recovery) must finish even when
// automatic refresh is disabled. Only periodic reads may be superseded.
func (m *tuiModel) startPeriodicRefresh() tea.Cmd {
	cmd := m.startRefreshWithPolicy(false, m.refreshPolicy())
	m.refreshCancellable = true
	return cmd
}

// recordChangedPRs runs before action handling, which may leave the review view.
// Failed and skipped actions do not need an authoritative read.
func (m *tuiModel) recordChangedPRs(msg tea.Msg) {
	switch msg := msg.(type) {
	case actionDoneMsg:
		m.recordChangedPRs(msg.msg)
	case actionMsg:
		for _, approval := range msg.approvals {
			if approval.err == nil && approval.outcome == approvePosted {
				m.markPRChanged(approval.key)
			}
		}
		_, hint := errors.AsType[*HintError](msg.err)
		if msg.err != nil && !hint {
			return
		}
		if msg.action != tuiActionOpened &&
			(msg.action != tuiActionApproved || msg.outcome == approvePosted) {
			m.markPRChanged(msg.key)
			if msg.action == tuiActionBranchUpdated {
				m.invalidateDiffHead(msg.key)
			}
		}
	case batchActionMsg:
		for _, approval := range msg.approvals {
			if approval.err == nil && approval.outcome == approvePosted {
				m.markPRChanged(approval.key)
			}
		}
		for _, key := range msg.keys {
			if msg.action != tuiActionOpened &&
				(msg.action != tuiActionApproved || msg.outcomes[key] == approvePosted) {
				m.markPRChanged(key)
				if msg.action == tuiActionBranchUpdated {
					m.invalidateDiffHead(key)
				}
			}
		}
	}
}

func (m *tuiModel) markPRChanged(key prKey) {
	if m.itemForKey(key) == nil {
		return
	}
	if m.changedPRs == nil {
		m.changedPRs = make(prKeys)
	}
	m.changedPRs[key] = true
	m.bumpPRGeneration(key)
	// In-flight list reads and cache writes predate this mutation.
	m.invalidatePRMetadata(prKeys{key: true})
	if m.refreshing && m.refreshCancellable {
		m.queryGen++
		m.refreshing = false
		m.showRefreshStatus = false
	}
}

func (m *tuiModel) bumpPRGeneration(key prKey) {
	if m.prChangeGen == nil {
		m.prChangeGen = make(map[prKey]int)
	}
	m.prChangeGen[key]++
}

// refreshChangedPRs waits until review ends. Late action completions use the
// same path once the list is visible, even with automatic refresh disabled.
func (m *tuiModel) refreshChangedPRs() tea.Cmd {
	if m.view != tuiViewList || m.refreshing || m.changedRefreshInFlight ||
		len(m.changedPRs) == 0 ||
		m.gql == nil {
		return nil
	}
	var refs []prRef
	gens := make(map[prKey]int)
	for _, item := range m.items {
		key := makePRKey(item.PR)
		if !m.changedPRs[key] {
			continue
		}
		owner, repo := prOwnerRepo(item.PR)
		refs = append(refs, prRef{Owner: owner, Repo: repo, Number: item.PR.Number})
		gens[key] = m.prChangeGen[key]
	}
	clear(m.changedPRs)
	if len(refs) == 0 {
		return nil
	}
	m.changedRefreshInFlight = true
	gql, resolver := m.gql, m.resolver
	ownerFilter := singleOwner(m.cli.Owner.Values)
	queryGen := m.queryGen
	return func() tea.Msg {
		msg := changedPRsMsg{gens: gens, queryGen: queryGen}
		prs, err := fetchPRsByRef(gql, refs)
		if err == nil {
			_, err = hydrateListMetadata(gql, prs, listMetadataRequest{
				mergeStatus: true, viewerApproval: true,
			})
		}
		msg.err = err
		if err == nil {
			msg.items = buildPRRowModels(prs, ownerFilter, resolver)
		}
		return msg
	}
}

func (m *tuiModel) applyChangedPRs(msg changedPRsMsg) tea.Cmd {
	m.changedRefreshInFlight = false
	if msg.queryGen != m.queryGen {
		if msg.events != nil {
			return nil // the newer list query supersedes this read
		}
		// Options or another list query changed while this read was running.
		for key := range msg.gens {
			m.changedPRs[key] = true
		}
		return nil
	}
	if msg.err != nil {
		return tea.Batch(
			tuiFlashMessage(m, "PR refresh failed: "+msg.err.Error(), true),
			m.scheduleMembershipCheck(),
		)
	}
	for _, item := range msg.items {
		key := makePRKey(item.PR)
		if msg.gens[key] != m.prChangeGen[key] {
			continue // a later mutation already queued an authoritative read
		}
		pr := m.confirmed.get(key)
		display := m.itemForKey(key)
		if pr == nil || display == nil {
			continue
		}
		if msg.events != nil && msg.events[key]&eventReviews == 0 {
			// Status-only reads carry the viewer fields captured when the read began.
			item.PR.viewerApproved = pr.viewerApproved
			item.PR.viewerApprovalLoaded = pr.viewerApprovalLoaded
			item.PR.viewerIsAuthor = pr.viewerIsAuthor
		}
		*display = item
		*pr = item.PR
		m.projectPR(key)
		m.reconcileVisibility(key)
		m.metadataCache.store(item.PR, listMetadataRequest{
			mergeStatus:    true,
			viewerApproval: msg.events == nil || msg.events[key]&eventReviews != 0,
			automerge:      msg.events != nil,
		}, newTimelineActors())
		if msg.events != nil {
			m.eventStreams.followUpHead(item.PR, msg.events[key])
		}
	}
	m.reconcilePendingActions(false)
	m.rerenderList()
	m.saveSnapshot()
	return m.scheduleMembershipCheck()
}

// rerenderList rebuilds cells and styles as well as row data, retaining the
// cursor and selections by PR identity when sorting changes the row order.
func (m *tuiModel) rerenderList() {
	if len(m.items) == 0 || m.p == nil || m.cli == nil {
		return
	}
	header, rows, widths := m.rerender()
	*m = m.reindexRows(m.rows, rows)
	m.header, m.colWidths = header, widths
}

// A targeted read must not postpone the next full membership check.
func (m *tuiModel) scheduleMembershipCheck() tea.Cmd {
	if !m.autoRefresh {
		return nil
	}
	m.invalidateRefresh()
	delay := refreshDelay(len(m.items), time.Since(m.lastInteraction), m.refreshIntervalOverride())
	remaining := delay - time.Since(m.lastRefreshAt)
	if remaining <= 0 && m.view == tuiViewList && !m.refreshing && m.actionsInFlight == 0 &&
		len(m.changedPRs) == 0 {
		return m.startPeriodicRefresh()
	}
	return scheduleTick(max(0, remaining), refreshTickMsg{id: m.refreshID})
}

type eventAttachMsg struct{ gen int }

func (m *tuiModel) syncVisibleEventStreams(msg tea.Msg) tea.Cmd {
	if m.eventStreams == nil {
		return nil
	}
	if m.height == 0 {
		return nil
	}
	wanted := make(eventTargets)
	// The review queue can extend beyond the list viewport.
	switch m.view {
	case tuiViewDetail:
		if pr := m.prForKey(m.detailKey); pr != nil {
			wanted.add(*pr)
		}
	case tuiViewDiff:
		if pr := m.prForKey(m.diffKey); pr != nil {
			wanted.add(*pr)
		}
	case tuiViewList:
	}
	for _, line := range m.viewportLines() {
		if line.row >= 0 && line.header == "" {
			wanted.add(m.rows[line.row].Item.PR)
		}
		if len(wanted) == maxEventStreams {
			break
		}
	}
	if m.eventTargets != nil && maps.Equal(m.eventTargets, wanted) {
		if tick, ok := msg.(eventAttachMsg); ok && tick.gen == m.eventAttachGen {
			m.eventStreams.attach(wanted)
		}
		return nil
	}
	m.eventTargets = wanted
	m.eventAttachGen++
	m.eventStreams.detachHidden(wanted)
	if len(m.eventStreams.listeners) == len(wanted) {
		return nil
	}
	return scheduleTick(eventAttachDelay, eventAttachMsg{gen: m.eventAttachGen})
}

func (m *tuiModel) invalidateDiffHead(key prKey) {
	if m.diffHeadGen == nil {
		m.diffHeadGen = make(map[prKey]int)
	}
	m.diffHeadGen[key]++
}

// Invalidate review content as events arrive. The list still reconciles its
// rows after review, coalescing all pending scopes for each PR.
func (m *tuiModel) queueReviewEvents() tea.Cmd {
	changed := m.eventStreams.takeChanges()
	if len(changed) == 0 {
		return nil
	}
	if m.pendingEvents == nil {
		m.pendingEvents = make(prEvents)
	}
	if m.prEventGen == nil {
		m.prEventGen = make(map[prKey]int)
	}
	for key, scope := range changed {
		m.pendingEvents[key] |= scope
		m.prEventGen[key]++
		if scope&eventNewHead != 0 {
			m.invalidateDiffHead(key)
		}
	}
	if changed[m.detailKey] != 0 || changed[m.diffKey] != 0 {
		return m.refreshOpenReview()
	}
	return nil
}

// Disk snapshots have no live cache entry until the first successful read.
func (m tuiModel) reviewOutdated() bool {
	switch m.view {
	case tuiViewDiff:
		cached, ok := m.diffCache[m.diffKey]
		if !ok {
			return m.resumeRefreshPending || m.diffHeadGen[m.diffKey] != 0
		}
		pr := m.prForKey(m.diffKey)
		return pr == nil || !m.diffCurrent(*pr, cached)
	case tuiViewDetail:
		_, ok := m.detailCache[m.detailKey]
		return m.resumeRefreshPending && !ok
	case tuiViewList:
	}
	return false
}

// A failed navigation leaves the previous PR visible. Its refresh may have
// been superseded while the destination was loading.
func (m *tuiModel) resumeOpenReview() tea.Cmd {
	refresh := m.refreshOpenReview()
	if refresh != nil || m.detailInFlight[m.detailKey] {
		return refresh
	}
	return m.scheduleDetailRefresh()
}

func (m *tuiModel) refreshOpenReview() tea.Cmd {
	switch m.view {
	case tuiViewDetail:
		pr := m.prForKey(m.detailKey)
		cached, ok := m.detailCache[m.detailKey]
		if pr != nil && (!ok || !m.reviewFresh(*pr, cached.reviewStamp)) {
			return m.refetchDetail()
		}
	case tuiViewDiff:
		if !m.diffLoading {
			return tea.Batch(m.statusCmd(m.diffKey), m.refreshCachedDiff(m.diffKey))
		}
	case tuiViewList:
	}
	return nil
}

type eventListReadTickMsg struct{}

// Fetch the status fields of notified PRs from their confirmed rows. The list
// query and local actions have priority over stream reads.
func (m *tuiModel) refreshFromEvents() tea.Cmd {
	if len(m.pendingEvents) == 0 || m.view != tuiViewList || m.refreshing ||
		m.changedRefreshInFlight || m.actionsInFlight > 0 || m.gql == nil {
		return nil
	}
	// Event delivery can precede a list read by the entire time spent in review.
	if delay := time.Until(m.nextEventListRead); delay > 0 {
		if m.eventListReadScheduled {
			return nil
		}
		m.eventListReadScheduled = true
		return scheduleTick(delay, eventListReadTickMsg{})
	}
	changed := m.pendingEvents
	m.pendingEvents = nil
	gens := make(map[prKey]int, len(changed))
	for key := range changed {
		gens[key] = m.prChangeGen[key]
	}
	prs := eventPRs(m.confirmed.prs, changed)
	if len(prs) == 0 {
		return nil
	}
	m.changedRefreshInFlight = true
	m.nextEventListRead = time.Now().Add(watchMinInterval)
	gql, resolver := m.gql, m.resolver
	owner := singleOwner(m.cli.Owner.Values)
	gen := m.queryGen
	return func() tea.Msg {
		msg := changedPRsMsg{events: changed, gens: gens, queryGen: gen}
		msg.err = refreshEventFields(
			gql,
			prs,
			changed,
			listMetadataRequest{automerge: true, mergeStatus: true, viewerApproval: true},
		)
		if msg.err == nil {
			msg.items = buildPRRowModels(prs, owner, resolver)
		}
		return msg
	}
}
