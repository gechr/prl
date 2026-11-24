package main

import (
	"errors"
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
		m.actions == nil ||
		m.actions.gql == nil {
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
	gql, resolver := m.actions.gql, m.resolver
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
			continue // a later action already queued another read
		}
		m.updatePR(key, func(row *PRRowModel) { *row = item })
		m.reconcileVisibility(key)
		for i := range m.snapshotPRs {
			if makePRKey(m.snapshotPRs[i]) == key {
				confirmed := item
				if pending, ok := m.pendingActions[key]; ok &&
					time.Since(pending.at) < 2*time.Minute {
					applyKnownActionState(&confirmed, pending.action)
				}
				m.snapshotPRs[i] = confirmed.PR
			}
		}
		m.metadataCache.store(item.PR, listMetadataRequest{
			mergeStatus:    true,
			viewerApproval: msg.events == nil || msg.events[key]&eventReviews != 0,
			automerge:      msg.events != nil,
		}, newTimelineActors())
		if msg.events != nil {
			m.eventStreams.followUpCI(item.PR, msg.events[key])
		}
	}
	m.applyPendingApprovals()
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

// Fetch the status fields of notified PRs from their confirmed rows. The list
// query and local actions have priority over stream reads.
func (m *tuiModel) refreshFromEvents() tea.Cmd {
	if !m.eventsPending || m.view != tuiViewList || m.refreshing ||
		m.changedRefreshInFlight || m.actionsInFlight > 0 || m.actions == nil || m.actions.gql == nil {
		return nil
	}
	m.eventsPending = false
	changed := m.eventStreams.takeChanges()
	if !m.autoRefresh || len(changed) == 0 {
		return nil
	}
	gens := make(map[prKey]int, len(changed))
	for key := range changed {
		m.bumpPRGeneration(key)
		gens[key] = m.prChangeGen[key]
	}
	prs := eventPRs(m.snapshotPRs, changed)
	if len(prs) == 0 {
		return nil
	}
	m.changedRefreshInFlight = true
	gql, resolver := m.actions.gql, m.resolver
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
