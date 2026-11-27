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

type tuiModal struct {
	root    *bloom.Root
	fg, bg  color.Color
	pointer scrollPointer

	dialog       dialog.Dialog
	offset       int
	maxOffset    int
	manualScroll bool
}

func newTUIModal(method ansi.Method) *tuiModal {
	bg := lg.LightDark(!paletteIsLight)(color.White, color.Black)
	modal := &tuiModal{fg: colorText, bg: bg}
	modal.resetRoot(method)
	return modal
}

func (s *tuiModal) resetRoot(method ansi.Method) {
	s.root = bloom.New(bloom.WithWidthMethod(method), bloom.WithColors(s.fg, s.bg))
	s.pointer = scrollPointer{}
}

func (m *tuiModel) modalLayer() *tuiModal {
	if m.modal == nil {
		m.modal = newTUIModal(m.reviewWidthMethod())
	}
	return m.modal
}

func (m *tuiModel) renderModal(backdrop string, content bloom.Child) string {
	s := m.modalLayer()
	// Paint the whole panel so a highlighted PR row cannot show through it.
	panel := bloom.Box(lg.NewStyle().Background(s.bg), content)
	return s.root.Render(m.width, m.height, bloom.Overlay(
		bloom.Text(backdrop).NoWrap(),
		bloom.Layer(panel).Modal().Backdrop(fx.Dim{Amount: modalDim}),
	))
}

func (s *tuiModal) bodyClick(click tea.MouseClickMsg) (dialog.ClickMsg, bool) {
	rect, ok := s.root.Frame().Rect(modalBodyID)
	point := image.Pt(click.X, click.Y)
	if !ok || click.Button != tea.MouseLeft || !point.In(rect) {
		return dialog.ClickMsg{}, false
	}
	local := point.Sub(rect.Min)
	return dialog.ClickMsg{X: local.X, Y: local.Y + s.offset}, true
}

func (s *tuiModal) scrollTo(offset int) tea.Cmd {
	if s.maxOffset > 0 {
		s.offset = max(0, min(offset, s.maxOffset))
		s.manualScroll = true
	}
	return nil
}

func (m tuiModel) viewConfirmDialog(backdrop string) string {
	s := m.modal
	if s == nil || s.dialog == nil {
		return backdrop
	}
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
		return m.renderModal(backdrop, bloom.Box(box, bloom.Text(notice)))
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
	return m.renderModal(backdrop, bloom.Box(box, bloom.Column(rows...)))
}
