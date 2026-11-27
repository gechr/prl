package main

import (
	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
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

func (s *tuiScreen) listScrollbar(total, visible, offset int) bloom.Child {
	return bloom.Scrollbar(total, visible, offset).Style(reviewBarStyle()).ID(scrollbarID).
		OnScroll(func(offset int) tea.Cmd {
			s.listOffset = offset
			return nil
		})
}

func (m tuiModel) listScrollbarActive() bool {
	return m.view == tuiViewList && !m.confirmUsesDialog() && m.help == nil && !m.showOptions &&
		!m.presetMenu.open
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
