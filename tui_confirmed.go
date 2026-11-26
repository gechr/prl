package main

// confirmedPRList retains search order for disk snapshots and addresses individual updates by key.
// Optimistic approvals are applied only when projecting these values into display rows.
type confirmedPRList struct {
	prs   []PullRequest
	index map[prKey]int
}

func newConfirmedPRList(items []PRRowModel) confirmedPRList {
	list := confirmedPRList{prs: prItems(items), index: make(map[prKey]int, len(items))}
	for i, pr := range list.prs {
		list.index[makePRKey(pr)] = i
	}
	return list
}

func (l confirmedPRList) get(key prKey) *PullRequest {
	i, ok := l.index[key]
	if !ok {
		return nil
	}
	return &l.prs[i]
}

func (m *tuiModel) replaceListItems(items []PRRowModel) {
	m.contentRevision++
	m.items = items
	m.confirmed = newConfirmedPRList(items)
	m.filterHidden = nil
}

func (m *tuiModel) projectPR(key prKey) {
	confirmed := m.confirmed.get(key)
	display := m.itemForKey(key)
	if confirmed == nil || display == nil {
		return
	}
	m.contentRevision++
	pr := *confirmed
	if pending, ok := m.pendingApprovals[key]; ok {
		pr.viewerApproved = !pending.remove
		pr.viewerApprovalLoaded = true
	}
	display.setPR(pr)
	item := *display
	if idx := m.resolveIndex(key, -1); idx >= 0 {
		row := &m.rows[idx].Item
		item.StackKey, item.StackHeader = row.StackKey, row.StackHeader
		*row = item
	}
}

func (m *tuiModel) updateConfirmed(key prKey, change func(*PullRequest)) {
	if pr := m.confirmed.get(key); pr != nil {
		change(pr)
		m.projectPR(key)
	}
}
