package main

import (
	"image"
	"image/color"
	"strings"

	"bloomfx.dev/bloom"
	"bloomfx.dev/bloom/canvas"
	"bloomfx.dev/bloom/fx"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/dialog"
)

const (
	modalBodyID    bloom.ID = "modal-body"
	optionsModalID bloom.ID = "filter-options"
	modalDim                = 0.5
)

type tuiScreen struct {
	root       *bloom.Root
	fg, bg     color.Color
	pointer    scrollPointer
	listOffset int // set synchronously by the list scrollbar during Update

	dialog       dialog.Dialog
	offset       int
	maxOffset    int
	manualScroll bool
}

func newTUIScreen(method ansi.Method) *tuiScreen {
	bg := lg.LightDark(!paletteIsLight)(color.White, color.Black)
	s := &tuiScreen{fg: colorText, bg: bg}
	s.resetRoot(method)
	return s
}

func (s *tuiScreen) resetRoot(method ansi.Method) {
	s.root = bloom.New(bloom.WithWidthMethod(method), bloom.WithColors(s.fg, s.bg))
	s.pointer = scrollPointer{}
}

func (m *tuiModel) ensureScreen() *tuiScreen {
	if m.screen == nil {
		m.screen = newTUIScreen(m.reviewWidthMethod())
	}
	return m.screen
}

func (s *tuiScreen) panelLayer(content bloom.Child) bloom.LayerNode {
	// Paint the whole panel so a highlighted PR row cannot show through it.
	panel := bloom.Box(lg.NewStyle().Background(s.bg), content)
	return bloom.Layer(panel).Modal()
}

func (s *tuiScreen) modalLayer(content bloom.Child) bloom.LayerNode {
	return s.panelLayer(content).Backdrop(fx.Dim{Amount: modalDim})
}

func (s *tuiScreen) hit(x, y int) bloom.ID {
	path := s.root.Frame().Hit(x, y)
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

func (s *tuiScreen) contains(id bloom.ID, x, y int) bool {
	if s == nil {
		return false
	}
	rect, ok := s.root.Frame().Rect(id)
	return ok && image.Pt(x, y).In(rect)
}

func (s *tuiScreen) bodyClick(click tea.MouseClickMsg) (dialog.ClickMsg, bool) {
	rect, ok := s.root.Frame().Rect(modalBodyID)
	point := image.Pt(click.X, click.Y)
	if !ok || click.Button != tea.MouseLeft || !point.In(rect) {
		return dialog.ClickMsg{}, false
	}
	local := point.Sub(rect.Min)
	return dialog.ClickMsg{X: local.X, Y: local.Y + s.offset}, true
}

func (s *tuiScreen) scrollTo(offset int) tea.Cmd {
	if s.maxOffset > 0 {
		s.offset = max(0, min(offset, s.maxOffset))
		s.manualScroll = true
	}
	return nil
}

func (m tuiModel) confirmPanel() bloom.Child {
	s := m.screen
	box := m.styles.overlayBox.Padding(1, tuiConfirmPadX-1)
	innerW := max(1, m.width-box.GetHorizontalFrameSize())
	innerH := max(1, m.height-box.GetVerticalFrameSize())
	body := s.dialog.Content(innerW)
	footer := ""
	if f, ok := s.dialog.(dialog.Footered); ok {
		footer = f.Footer()
		body = strings.TrimSuffix(body, nl)
	}
	method := m.reviewWidthMethod()
	width, total := canvas.Measure(body, method)
	footerWidth, footerHeight := canvas.Measure(footer, method)
	if width > innerW || footerWidth > innerW {
		s.maxOffset = 0
		notice := m.styles.helpText.Render("terminal too narrow")
		return bloom.Box(box, bloom.Text(notice))
	}

	height := min(total, max(1, innerH-footerHeight))
	s.maxOffset = total - height
	s.offset = min(s.offset, s.maxOffset)
	if s.maxOffset == 0 {
		s.manualScroll = false
	} else if hint, ok := s.dialog.(dialog.ScrollHint); ok && !s.manualScroll {
		if top, rows, ok := hint.ScrollTo(); ok {
			s.offset = max(0, min(top, top+rows-height, s.maxOffset))
		}
	}

	viewport := bloom.Scroll(bloom.Text(body).NoWrap(), s.offset).ID(modalBodyID)
	var columns []bloom.Child
	if s.maxOffset > 0 {
		width = min(width, max(1, innerW-tuiScrollbarWidth))
		bar := bloom.Scrollbar(total, height, s.offset).Style(reviewBarStyle()).OnScroll(s.scrollTo)
		columns = append(columns, bloom.Fixed(width, viewport), bloom.Fixed(tuiScrollbarWidth, bar))
	} else {
		columns = append(columns, bloom.Fixed(width, viewport))
	}
	rows := []bloom.Child{bloom.Fixed(height, bloom.Row(columns...))}
	if footerHeight > 0 {
		rows = append(rows, bloom.Fixed(footerHeight, bloom.Text(footer).NoWrap()))
	}
	return bloom.Box(box, bloom.Column(rows...))
}
