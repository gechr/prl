package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/gofrs/flock"
)

const resumeFlag = "--resume-session="

// Each running browser owns a cache directory, even if a terminal launches
// the same resume command twice. The account namespace matches the list cache.
type tuiSession struct {
	dir          string
	lock         *flock.Flock
	command      *tea.ProgramResume
	lastPosition []byte
	lastRevision int
	lastView     tuiView
	slot         bool
}

type sessionContent struct {
	PRs    []cachedPullRequest `json:"prs"`
	Diff   string              `json:"diff"`
	Detail PRDetail            `json:"detail"`
}

type sessionPosition struct {
	Width          int                    `json:"width"`
	Height         int                    `json:"height"`
	TimeCutoff     *time.Time             `json:"timeCutoff,omitempty"`
	ContentSlot    bool                   `json:"contentSlot"`
	Login          string                 `json:"login"`
	LastRefresh    time.Time              `json:"lastRefresh"`
	View           tuiView                `json:"view"`
	Cursor         prKey                  `json:"cursor"`
	Active         prKey                  `json:"active"`
	ListOffset     int                    `json:"listOffset"`
	ViewOffset     int                    `json:"viewOffset"`
	Selected       prKeys                 `json:"selected"`
	Filter         string                 `json:"filter"`
	FilterFocused  bool                   `json:"filterFocused"`
	FilterCursor   int                    `json:"filterCursor"`
	DismissedEmpty bool                   `json:"dismissedEmpty"`
	ShowHelp       bool                   `json:"showHelp"`
	Filters        TUIFiltersConfig       `json:"filters"`
	FilterDefaults TUIFiltersConfig       `json:"filterDefaults"`
	Explicit       sessionExplicitFilters `json:"explicit"`
	Options        *sessionOptions        `json:"options"`
	PresetMenu     *sessionPresetMenu     `json:"presetMenu"`
	Sort           TUISortConfig          `json:"sort"`
	AutoRefresh    bool                   `json:"autoRefresh"`
	Stacks         bool                   `json:"stacks"`
	FoldedStacks   []string               `json:"foldedStacks"`
	DiffQueue      []prKey                `json:"diffQueue"`
	DiffHistory    []prKey                `json:"diffHistory"`
	DiffQueueTotal int                    `json:"diffQueueTotal"`
}

type sessionExplicitFilters struct {
	State    bool `json:"state"`
	Draft    bool `json:"draft"`
	Bots     bool `json:"bots"`
	Archived bool `json:"archived"`
	CI       bool `json:"ci"`
	Review   bool `json:"review"`
}

type sessionOptions struct {
	Cursor int    `json:"cursor"`
	Values []int  `json:"values"`
	Reset  []bool `json:"reset"`
}

type sessionPresetMenu struct {
	TimeFilter bool `json:"timeFilter"`
	Cursor     int  `json:"cursor"`
	Offset     int  `json:"offset"`
}

type savedSession struct {
	sessionPosition
	sessionContent
}

func sessionRoot() (string, error) {
	dir, err := shell.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, programName, "sessions", "v1", hashString(githubToken())), nil
}

func newTUISession(root string, args []string, previous string) *tuiSession {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	id := rand.Text()
	dir := filepath.Join(root, hashString(id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	lock := flock.New(filepath.Join(dir, "active.lock"))
	if ok, lockErr := lock.TryLock(); lockErr != nil || !ok {
		return nil
	}
	// Replace the old session option, preserving positional arguments after "--".
	if previous != "" {
		for i, arg := range args {
			if arg == "--" {
				break
			}
			if arg == resumeFlag+previous {
				args = slices.Concat(args[:i], args[i+1:])
				break
			}
			if arg == strings.TrimSuffix(resumeFlag, "=") && i+1 < len(args) &&
				args[i+1] == previous {
				args = slices.Concat(args[:i], args[i+2:])
				break
			}
		}
	}
	return &tuiSession{dir: dir, lock: lock, command: &tea.ProgramResume{
		Args: append([]string{exe, resumeFlag + id}, args...), Dir: cwd,
	}}
}

func sessionContentName(slot bool) string {
	if slot {
		return "content-b.json"
	}
	return "content-a.json"
}

func loadSession(root, id string) *savedSession {
	if id == "" {
		return nil
	}
	dir := filepath.Join(root, hashString(id))
	data, err := os.ReadFile(filepath.Join(dir, "position.json"))
	var saved savedSession
	if err != nil || json.Unmarshal(data, &saved.sessionPosition) != nil {
		return nil
	}
	data, err = os.ReadFile(filepath.Join(dir, sessionContentName(saved.ContentSlot)))
	if err != nil || json.Unmarshal(data, &saved.sessionContent) != nil {
		return nil
	}
	return &saved
}

// Positions are small and change on scroll. Replace the bulk PR and diff
// data only when it changes. Publish the new position last so an interrupted
// save always leaves a matching pair of files.
func (m *tuiModel) saveSession() {
	s := m.session
	if s == nil {
		return
	}
	contentChanged := s.lastPosition == nil || s.lastRevision != m.contentRevision ||
		s.lastView != m.view
	slot := s.slot
	if contentChanged {
		slot = !slot
	}
	pos := sessionPosition{
		Width: m.width, Height: m.height,
		TimeCutoff:     m.sessionTimeCutoff(),
		FilterDefaults: m.cfg.TUI.Filters,
		Explicit: sessionExplicitFilters{
			State:    m.cli.stateExplicit,
			Draft:    m.cli.draftExplicit,
			Bots:     m.cli.noBotExplicit,
			Archived: m.cli.archivedExplicit,
			CI:       m.cli.ciExplicit,
			Review:   m.cli.reviewExplicit,
		},
		ContentSlot:    slot,
		Login:          m.login,
		LastRefresh:    m.lastRefreshAt,
		View:           m.view,
		Cursor:         m.rowKeyAt(m.cursor),
		ListOffset:     m.offset,
		Selected:       m.selected,
		Filter:         m.filterInput.Value(),
		FilterFocused:  m.filterInput.Focused(),
		FilterCursor:   m.filterInput.Position(),
		ShowHelp:       m.help != nil,
		DismissedEmpty: m.dismissedEmpty,
		Sort:           m.cfg.TUI.Sort,
		Stacks:         m.stacksEnabled(),
		FoldedStacks:   m.cfg.TUI.FoldedStacks,
		AutoRefresh:    m.autoRefresh,
		DiffQueue:      m.diffQueue,
		DiffHistory:    m.diffHistory,
		DiffQueueTotal: m.diffQueueTotal,
		Filters: TUIFiltersConfig{
			State: m.cli.State, Draft: m.cli.Draft, Bots: new(m.cli.NoBot),
			Archived: new(m.cli.Archived), CI: m.cli.CI, Review: m.cli.Review,
			Preset: m.cli.preset, Time: m.cli.timePreset,
		},
	}
	switch m.view {
	case tuiViewDiff:
		pos.Active, pos.ViewOffset = m.diffKey, m.diffView.offset()
	case tuiViewDetail:
		pos.Active, pos.ViewOffset = m.detailKey, m.detailView.offset()
	case tuiViewList:
	}
	if m.showOptions {
		p := m.optionsPicker
		pos.Options = &sessionOptions{Cursor: p.Cursor, Values: p.Values, Reset: p.IsReset}
	}
	if m.presetMenu.open {
		p := m.presetMenu
		pos.PresetMenu = &sessionPresetMenu{
			TimeFilter: p.timeFilter,
			Cursor:     p.cursor,
			Offset:     p.offset,
		}
	}
	data, err := json.Marshal(pos)
	if err != nil || bytes.Equal(data, s.lastPosition) {
		return
	}
	if contentChanged {
		content := sessionContent{PRs: make([]cachedPullRequest, len(m.confirmed.prs))}
		for i, pr := range m.confirmed.prs {
			content.PRs[i] = cachedPR(pr)
		}
		switch m.view {
		case tuiViewDiff:
			content.Diff = m.diff
		case tuiViewDetail:
			content.Detail = m.detail
		case tuiViewList:
		}
		bulk, marshalErr := json.Marshal(content)
		if marshalErr != nil ||
			xos.AtomicWrite(
				filepath.Join(s.dir, sessionContentName(slot)),
				bulk,
				listResultCacheFilePerm,
				xos.WithNoSync(),
			) != nil {
			return
		}
	}
	if xos.AtomicWrite(
		filepath.Join(s.dir, "position.json"),
		data,
		listResultCacheFilePerm,
		xos.WithNoSync(),
	) != nil {
		return
	}
	s.lastPosition, s.lastRevision, s.lastView, s.slot = data, m.contentRevision, m.view, slot
}

// Freeze relative time filters at the last displayed membership. Using the
// newest excluded timestamp keeps the checkpoint stable between expirations.
func (m tuiModel) sessionTimeCutoff() *time.Time {
	if m.resumeTimeCutoff != nil || m.cli.timePreset == "" {
		return m.resumeTimeCutoff
	}
	df := m.displayFilter()
	var boundary time.Time
	for _, pr := range m.confirmed.prs {
		stamp := pr.UpdatedAt
		if df.timeField == valueCreated {
			stamp = pr.CreatedAt
		}
		if stamp.Before(df.cutoff) && !stamp.Before(boundary) {
			boundary = stamp.Add(time.Nanosecond)
		}
	}
	return &boundary
}

func (s sessionPosition) applySettings(cli *CLI, cfg *Config) {
	f := s.Filters
	cli.State, cli.Draft, cli.CI, cli.Review = f.State, f.Draft, f.CI, f.Review
	cli.NoBot = f.Bots != nil && *f.Bots
	cli.Archived = f.Archived != nil && *f.Archived
	cli.preset, cli.timePreset = f.Preset, f.Time
	cfg.TUI.Filters, cfg.TUI.Sort = s.FilterDefaults, s.Sort
	cli.stateExplicit, cli.draftExplicit = s.Explicit.State, s.Explicit.Draft
	cli.noBotExplicit, cli.archivedExplicit = s.Explicit.Bots, s.Explicit.Archived
	cli.ciExplicit, cli.reviewExplicit = s.Explicit.CI, s.Explicit.Review
	cfg.TUI.AutoRefresh.Enabled, cfg.TUI.Stacks.Enabled = s.AutoRefresh, s.Stacks
	cfg.TUI.FoldedStacks = s.FoldedStacks
}

func (s *savedSession) restore(m *tuiModel) {
	m.resumeTimeCutoff = s.TimeCutoff
	m.resumeRefreshPending = true
	if m.width == 0 && s.Width > 0 && s.Height > 0 {
		m.applyWindowSize(s.Width, s.Height)
	}
	m.refreshTerminalSize()
	m.filterInput.SetValue(s.Filter)
	m.filterInput.SetCursor(s.FilterCursor)
	if s.FilterFocused {
		m.initCmd = m.filterInput.Focus()
	}
	m.help = nil
	if s.ShowHelp {
		m.help = &helpSheet{}
	}
	m.dismissedEmpty = s.DismissedEmpty
	if s.Selected != nil {
		m.selected = s.Selected
	}
	m.cursor = max(0, m.resolveIndex(s.Cursor, -1))
	m.offset = max(0, s.ListOffset)
	m.resyncCursorAndOffset()
	if p := s.Options; p != nil {
		m.openOptions()
		picker := m.optionsPicker
		picker.Cursor = min(max(0, p.Cursor), len(picker.Rows)-1)
		for i := range min(len(picker.Rows), len(p.Values), len(p.Reset)) {
			picker.Values[i] = min(max(0, p.Values[i]), len(picker.Rows[i].Choices)-1)
			picker.IsReset[i] = p.Reset[i]
		}
	}
	if p := s.PresetMenu; p != nil {
		m.openFilterPresetMenu(p.TimeFilter)
		m.presetMenu.cursor, m.presetMenu.offset = p.Cursor, p.Offset
		m.movePresetMenu(0)
	}
	if m.resolveIndex(s.Active, -1) < 0 {
		return
	}
	switch s.View {
	case tuiViewDiff:
		m.view, m.diffKey, m.diff = tuiViewDiff, s.Active, s.Diff
		m.diffQueue, m.diffHistory, m.diffQueueTotal = s.DiffQueue, s.DiffHistory, s.DiffQueueTotal
		m.diffQueuePos = m.diffQueueTotal - len(m.diffQueue)
		m.syncDiffView()
		m.diffView.setOffset(s.ViewOffset)
	case tuiViewDetail:
		m.view, m.detailKey, m.detail = tuiViewDetail, s.Active, s.Detail
		m.detailLines = m.renderDetailContent()
		m.syncDetailView()
		m.detailView.setOffset(s.ViewOffset)
	case tuiViewList:
		return
	}
	// The saved view is immediately readable. Validate it even with polling and streams off.
	m.initCmd = tea.Batch(m.initCmd, m.refreshOpenReview())
}

// Superseded or abandoned records have no terminal callback for cleanup.
func pruneSessions(root string) {
	const retention = 30 * 24 * time.Hour
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != len(hashString("")) ||
			strings.Trim(entry.Name(), "0123456789abcdef") != "" {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		info, err := os.Stat(dir)
		if err == nil && time.Since(info.ModTime()) > retention {
			lock := flock.New(filepath.Join(dir, "active.lock"))
			if ok, lockErr := lock.TryLock(); lockErr != nil || !ok {
				continue
			}
			_ = lock.Close()
			_ = os.RemoveAll(dir)
		}
	}
}
