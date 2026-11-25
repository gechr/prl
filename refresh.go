package main

import (
	"time"

	"github.com/gechr/clog"
)

const (
	visibleStatusInterval = time.Minute
	quietStatusInterval   = 3 * time.Minute
)

// listRefreshPolicy controls when periodic enrichment is due. Retained display
// state keeps its original timestamp, even when it is older than the normal
// metadata cache lifetime. Explicit reads still use the normal cache policy.
type listRefreshPolicy struct {
	now     time.Time
	visible prKeys
}

func (p listRefreshPolicy) maxAge(pr PullRequest, status MergeStatus) time.Duration {
	if status == MergeStatusCIPending || status == MergeStatusUnknown {
		return 0
	}
	if p.visible[makePRKey(pr)] {
		return visibleStatusInterval
	}
	return quietStatusInterval
}

func (m tuiModel) viewportLines() []tuiListLine {
	lines := m.listLines()
	start := min(m.offset, len(lines))
	end := min(start+m.listViewport(), len(lines))
	return lines[start:end]
}

func (m tuiModel) refreshPolicy() *listRefreshPolicy {
	visible := make(prKeys)
	for _, line := range m.viewportLines() {
		if line.row >= 0 {
			visible[m.rowKeyAt(line.row)] = true
		}
	}
	return &listRefreshPolicy{now: time.Now(), visible: visible}
}

// cloneForRows retains only current identities outside the invalidated set.
// In-flight fetches still write to the old cache, preserving the isolation of
// whole-cache replacement without discarding unrelated fresh entries.
func (c *listMetadataCache) cloneForRows(prs []PullRequest, invalid prKeys) *listMetadataCache {
	cloned := newListMetadataCache()
	if c == nil {
		return cloned
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pr := range prs {
		if invalid[makePRKey(pr)] {
			continue
		}
		key, ok := listMetadataKey(pr)
		if !ok {
			continue
		}
		if entry, found := c.entries[key]; found {
			cloned.entries[key] = entry
		}
	}
	return cloned
}

func (m *tuiModel) invalidatePRMetadata(keys prKeys) {
	m.metadataCache = m.metadataCache.cloneForRows(m.confirmed.prs, keys)
}

// saveSnapshot writes confirmed rows only. A targeted read never advances the
// membership timestamp or persists optimistic approvals that can still fail.
func (m tuiModel) saveSnapshot() {
	if m.cli == nil || m.params == nil || m.lastRefreshAt.IsZero() {
		return
	}
	if err := saveListResultCacheAt(m.cli, m.params, m.confirmed.prs, m.lastRefreshAt); err != nil {
		clog.Debug().Err(err).Msg("list cache save failed")
	}
}
