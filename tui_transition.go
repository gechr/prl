package main

import (
	"math"
	"time"

	"bloomfx.dev/bloom"
	"bloomfx.dev/bloom/anim"
	"bloomfx.dev/bloom/fx"
	tea "charm.land/bubbletea/v2"
)

const prSlideDuration = 200 * time.Millisecond

type prSlide struct {
	previous string
	from     fx.Direction
	progress anim.Value
}

func (m *tuiModel) startPRSlide(from fx.Direction, vp *reviewViewport) tea.Cmd {
	if m.cfg != nil && !m.cfg.TUI.Animations.Enabled {
		return nil
	}
	if from == 0 || !anim.Motion() || vp == nil || vp.width <= 0 || vp.height <= 0 {
		return nil
	}
	m.slide = prSlide{
		previous: vp.snapshot(),
		from:     from,
		progress: anim.NewValue(0, anim.Duration(prSlideDuration), anim.Ease(anim.InOutQuad)),
	}
	return m.slide.progress.To(1)
}

func (s prSlide) layout(width, height int, current bloom.Child) bloom.Child {
	// Both layers must share a cell boundary, including at half-cell positions.
	cells := float64(width)
	t := math.Round(s.progress.Float()*cells) / cells
	exit := fx.Left
	if s.from == fx.Left {
		exit = fx.Right
	}
	outgoing := bloom.Layer(bloom.Text(s.previous).NoWrap()).Size(width, height).
		Transition(1-t, fx.Slide{From: exit})
	incoming := bloom.Layer(current).Size(width, height).
		Transition(t, fx.Slide{From: s.from})
	return bloom.Overlay(bloom.Text(""), outgoing, incoming)
}

// detailNeighbors follows the current list order and skips folded stack headers.
func (m tuiModel) detailNeighbors() (int, int) {
	idx := m.resolveIndex(m.detailKey, -1)
	if idx < 0 {
		return -1, -1
	}
	prev := -1
	for _, i := range m.visibleIndices() {
		if m.isCollapsedRow(i) {
			continue
		}
		if i > idx {
			return prev, i
		}
		if i < idx {
			prev = i
		}
	}
	return prev, -1
}
