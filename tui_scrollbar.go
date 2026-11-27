package main

import (
	"image"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const scrollbarID bloom.ID = "scrollbar"

// A terminal can lose a release outside its window. End the gesture at the
// last held cell before Bloom processes focus loss or unpressed motion.
type scrollPointer struct {
	last tea.Mouse
}

func (p *scrollPointer) update(root *bloom.Root, msg tea.Msg) tea.Cmd {
	var interrupted bool
	switch e := msg.(type) {
	case tea.BlurMsg:
		interrupted = true
	case tea.MouseClickMsg:
		p.last = e.Mouse()
	case tea.MouseMotionMsg:
		if e.Button == tea.MouseNone {
			interrupted = true
		} else {
			p.last = e.Mouse()
		}
	case tea.MouseReleaseMsg:
		if e.Button == tea.MouseLeft || e.Button == tea.MouseNone {
			p.last.Button = tea.MouseNone
		}
	}
	if interrupted && p.last.Button == tea.MouseLeft {
		p.last.Button = tea.MouseNone
		end := root.Update(tea.MouseMotionMsg(p.last))
		return tea.Batch(end, root.Update(msg))
	}
	return root.Update(msg)
}

// A scrollbar over an existing frame. Bloom maps the gesture to an offset
// synchronously, so Update can scroll the PR list and keep its cursor visible.
type scrollbarLayer struct {
	root    *bloom.Root
	pending int
	pointer scrollPointer
}

func newScrollbarLayer(method ansi.Method) *scrollbarLayer {
	return &scrollbarLayer{root: bloom.New(bloom.WithWidthMethod(method))}
}

func (s *scrollbarLayer) contains(x, y int) bool {
	if s == nil {
		return false
	}
	rect, ok := s.root.Frame().Rect(scrollbarID)
	return ok && image.Pt(x, y).In(rect)
}

func (s *scrollbarLayer) update(msg tea.Msg) (int, tea.Cmd) {
	s.pending = -1
	cmd := s.pointer.update(s.root, msg)
	return s.pending, cmd
}

func (s *scrollbarLayer) render(frame string, width, height int, rect image.Rectangle, total, offset int) string {
	if width <= 0 || height <= 0 || rect.Empty() || total <= rect.Dy() {
		if _, ok := s.root.Frame().Rect(scrollbarID); ok {
			s.root.Render(0, 0, nil)
		}
		return frame
	}
	bar := bloom.Scrollbar(total, rect.Dy(), offset).Style(reviewBarStyle()).ID(scrollbarID).
		OnScroll(func(offset int) tea.Cmd {
			s.pending = offset
			return nil
		})
	return s.root.Render(width, height, bloom.Overlay(bloom.Text(frame).NoWrap(), bloom.Layer(bar).Place(rect)))
}

func (m tuiModel) listScrollbarActive() bool {
	return m.view == tuiViewList && !m.confirmUsesDialog() && !m.showHelp && !m.showOptions && !m.presetMenu.open
}

func (m *tuiModel) setListScrollOffset(offset int) {
	lines := m.listLines()
	height := m.listViewport()
	m.offset = max(0, min(offset, len(lines)-height))
	if height == 1 && m.offset+1 < len(lines) && lines[m.offset].row < 0 {
		m.offset++ // an expanded stack header alone has no keyboard target
	}
	// Keep the current PR when it is still visible. Otherwise move to the
	// nearest visible row that can receive keyboard input.
	previous := m.cursor
	for _, line := range lines[m.offset:min(m.offset+height, len(lines))] {
		if line.row < 0 {
			continue
		}
		m.cursor = line.row
		if m.cursor >= previous {
			return
		}
	}
}
