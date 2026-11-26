package main

import (
	"strings"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const reviewBodyID bloom.ID = "review-body"

func reviewBarStyle() bloom.ScrollbarStyle {
	return bloom.ScrollbarStyle{
		Thumb: "█", ThumbStyle: lg.NewStyle().Foreground(colorAccent),
		Track: "┃", TrackStyle: lg.NewStyle().Foreground(colorAccent).Faint(true),
	}
}

// Each document owns its root so a selection or queued drag tick cannot follow
// navigation to a different PR. The position is shared by mouse and keyboard.
type reviewViewport struct {
	root      *bloom.Root
	measure   *bloom.Root
	buffer    *bloom.Buffer
	measured  bloom.ViewportView
	pos       bloom.ViewportPos
	key       prKey
	text      string
	wrap      bloom.WrapMode
	method    ansi.Method
	width     int
	height    int
	copyFlash bool
	pointer   scrollPointer
}

func (m tuiModel) reviewWidthMethod() ansi.Method {
	if m.p == nil {
		return ansi.WcWidth
	}
	return m.p.widthMethod()
}

func (m *tuiModel) syncDiffView() {
	m.diffView = m.reviewViewport(m.diffView, m.diffKey, m.diff, bloom.WrapChars, m.diffContentViewport())
}

func (m *tuiModel) syncDetailView() {
	m.detailView = m.reviewViewport(
		m.detailView,
		m.detailKey,
		strings.Join(m.detailLines, nl),
		bloom.WrapNone,
		m.detailViewport(),
	)
}

func (m tuiModel) reviewViewport(
	old *reviewViewport,
	key prKey,
	text string,
	wrap bloom.WrapMode,
	height int,
) *reviewViewport {
	width, method := max(0, m.width-tuiScrollbarWidth), m.reviewWidthMethod()
	if old != nil && old.key == key && old.text == text && old.method == method {
		old.width, old.height = width, height
		return old
	}
	v := &reviewViewport{
		buffer: bloom.NewBuffer(text), text: text, key: key,
		measure: bloom.New(bloom.WithWidthMethod(method)),
		width:   width, height: height, method: method, wrap: wrap,
		copyFlash: m.cfg == nil || m.cfg.TUI.Animations.Enabled,
	}
	v.resetInput()
	if old != nil && old.key == key {
		v.pos = old.pos
	}
	return v
}

func (v *reviewViewport) resetInput() {
	v.pointer = scrollPointer{}
	v.root = bloom.New(
		bloom.WithWidthMethod(v.method),
		bloom.WithClipboard(copyToClipboard),
		bloom.WithCopyFlash(v.copyFlash),
		bloom.WithSelectionStyle(lg.NewStyle().Background(colorSelectionBG)),
	)
}

func (v *reviewViewport) node() bloom.ViewportNode {
	return bloom.Viewport(v.buffer).Wrap(v.wrap).ID(reviewBodyID).At(v.pos).Selectable().
		OnScroll(func(pos bloom.ViewportPos) tea.Cmd {
			v.pos = pos
			return nil
		})
}

// Before the first frame, and between a scroll and the following View, the
// geometry is needed for paging and session snapshots at the new position.
func (v *reviewViewport) geometry() bloom.ViewportView {
	if v == nil || v.width <= 0 || v.height <= 0 {
		return bloom.ViewportView{}
	}
	if geo, ok := v.root.Frame().Viewport(reviewBodyID); ok && v.matches(geo) {
		return geo
	}
	if !v.matches(v.measured) {
		v.measure.Render(v.width, v.height, v.node())
		v.measured, _ = v.measure.Frame().Viewport(reviewBodyID)
		v.pos = v.measured.Pos
	}
	return v.measured
}

func (v *reviewViewport) matches(geo bloom.ViewportView) bool {
	return geo.Pos == v.pos && geo.Width == v.width && geo.Rows == v.height
}

func (v *reviewViewport) offset() int { return v.geometry().Row }

func (v *reviewViewport) setOffset(row int) {
	if v != nil {
		v.pos = v.geometry().AtRow(row)
	}
}

func (v *reviewViewport) scroll(delta int) {
	if v != nil {
		v.pos = v.geometry().Down(delta)
	}
}

func (v *reviewViewport) snapshot() string {
	return v.measure.Render(v.width, v.height, v.node())
}

func (m tuiModel) activeReviewViewport() *reviewViewport {
	if m.confirmUsesDialog() {
		return nil
	}
	switch m.view {
	case tuiViewDiff:
		return m.diffView
	case tuiViewDetail:
		return m.detailView
	case tuiViewList:
		return nil
	}
	return nil
}
