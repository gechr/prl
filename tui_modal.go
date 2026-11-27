package main

import (
	"image"
	"image/color"
	"strings"

	"bloomfx.dev/bloom"
	"bloomfx.dev/bloom/canvas"
	"bloomfx.dev/bloom/flex"
	"bloomfx.dev/bloom/fx"
	"bloomfx.dev/bloom/on"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/form"
)

const (
	confirmPanelID  bloom.ID = "confirm-panel"
	confirmHandleID bloom.ID = "confirm-handle"
	confirmNoID     bloom.ID = "confirm-no"
	confirmYesID    bloom.ID = "confirm-yes"
	confirmOKID     bloom.ID = "confirm-ok"
	modalBodyID     bloom.ID = "modal-body"
	optionsModalID  bloom.ID = "filter-options"
	modalDim                 = 0.5
)

type tuiScreen struct {
	root       *bloom.Root
	fg, bg     color.Color
	pointer    scrollPointer
	listOffset int // set synchronously by the list scrollbar during Update

	focus  bloom.Focus
	dialog *confirmDialog
}

type confirmDialog struct {
	action, prompt string
	info           bool
	form           *form.Model
	options        []filterOptionDef
	provider       reviewProvider
	pressed        bloom.ID
	position       *image.Point // nil centers a newly opened dialog
	grab           *image.Point // pointer offset while the handle is held
	limit          image.Point  // furthest visible top-left corner
	cramped        bool
	measuredAt     image.Point
	offset         int
	maxOffset      int
	manualScroll   bool
}

func newTUIScreen(method ansi.Method) *tuiScreen {
	bg := lg.LightDark(!paletteIsLight)(color.White, color.Black)
	s := &tuiScreen{fg: colorText, bg: bg}
	s.resetRoot(method)
	return s
}

func (s *tuiScreen) resetRoot(method ansi.Method) {
	s.root = bloom.New(
		bloom.WithWidthMethod(method),
		bloom.WithColors(s.fg, s.bg),
		bloom.WithFocus(&s.focus),
	)
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

func (s *tuiScreen) focusFormAt(click tea.MouseClickMsg) {
	rect, ok := s.root.Frame().Rect(modalBodyID)
	point := image.Pt(click.X, click.Y)
	if ok && click.Button == tea.MouseLeft && point.In(rect) {
		local := point.Sub(rect.Min)
		s.dialog.form.FocusAt(local.X, local.Y+s.dialog.offset)
	}
}

func (d *confirmDialog) scrollTo(offset int) tea.Cmd {
	if d.maxOffset > 0 {
		d.offset = max(0, min(offset, d.maxOffset))
		d.manualScroll = true
	}
	return nil
}

func (m tuiModel) confirmFits() bool {
	d := m.screen.dialog
	if d.measuredAt != image.Pt(m.width, m.height) {
		// Resize and keyboard events can arrive before the next frame is drawn.
		m.confirmLayer()
	}
	return !d.cramped && m.width > 0 && m.height > 0
}

func (m tuiModel) confirmLayer() bloom.LayerNode {
	d := m.screen.dialog
	box := m.styles.overlayBox.Padding(1, tuiConfirmPadX-1)
	innerW := max(1, m.width-box.GetHorizontalFrameSize())
	innerH := m.height - box.GetVerticalFrameSize()
	body := d.prompt
	var footer bloom.Child
	var footerWidth, footerHeight, gap int
	method := m.reviewWidthMethod()
	if d.form == nil {
		footer, footerWidth = m.confirmButtons()
		footerHeight, gap = 1, 1
	} else {
		body = strings.TrimSuffix(d.form.Body(), nl)
		hints := d.form.Foot()
		footer = bloom.Text(hints).NoWrap()
		footerWidth, footerHeight = canvas.Measure(hints, method)
	}
	width, total := canvas.Measure(body, method)
	if innerH < 1+gap+footerHeight {
		box = box.PaddingTop(0).PaddingBottom(0)
		innerH = m.height - box.GetVerticalFrameSize()
		gap = min(gap, max(0, innerH-footerHeight-1))
	}
	d.cramped = width > innerW || footerWidth > innerW || innerH <= footerHeight
	if d.cramped {
		d.offset, d.maxOffset = 0, 0
		notice := "terminal too narrow"
		if innerH <= footerHeight {
			notice = "terminal too short"
			box = lg.NewStyle()
		}
		return m.positionConfirm(box, bloom.Text(m.styles.helpText.Render(notice)), len(notice), 1)
	}

	height := min(total, innerH-footerHeight-gap)
	d.maxOffset = total - height
	d.offset = min(d.offset, d.maxOffset)
	if d.maxOffset == 0 {
		d.manualScroll = false
	} else if d.form != nil && !d.manualScroll {
		if top, rows, ok := d.form.FocusRegion(); ok {
			d.offset = max(0, min(top, top+rows-height, d.maxOffset))
		}
	}

	viewport := bloom.Scroll(bloom.Text(body).NoWrap(), d.offset).ID(modalBodyID)
	var columns []bloom.Child
	if d.maxOffset > 0 {
		width = max(width, footerWidth-tuiScrollbarWidth)
		if width+tuiScrollbarWidth > innerW {
			// Borrow a padding cell so the bar never clips the last column of a form field.
			box = box.PaddingRight(box.GetPaddingRight() - tuiScrollbarWidth)
		}
		bar := bloom.Scrollbar(total, height, d.offset).Style(reviewBarStyle()).OnScroll(d.scrollTo)
		columns = append(columns, bloom.Fixed(width, viewport), bloom.Fixed(tuiScrollbarWidth, bar))
		width += tuiScrollbarWidth
	} else {
		columns = append(columns, bloom.Fixed(width, viewport))
	}
	rows := []bloom.Child{bloom.Fixed(height, bloom.Row(columns...))}
	if gap > 0 {
		rows = append(rows, bloom.Fixed(gap, bloom.Space()))
	}
	if footerHeight > 0 {
		rows = append(rows, bloom.Fixed(footerHeight, footer))
	}
	return m.positionConfirm(
		box,
		bloom.Column(rows...),
		max(width, footerWidth),
		height+gap+footerHeight,
	)
}

func (m tuiModel) confirmButtons() (bloom.Child, int) {
	s := m.screen
	d := s.dialog
	var buttons []bloom.Child
	width := 0
	add := func(id bloom.ID, label string, style lg.Style) {
		w := m.reviewWidthMethod().StringWidth(style.Render(label))
		button := bloom.Text(label).NoWrap().Style(style).ID(id).Focusable().
			On(on.Press(func() tea.Cmd {
				d.pressed = id
				return nil
			}))
		if !d.info {
			hover := lg.NewStyle().Foreground(colorText).Background(colorSelectionBG).
				Bold(s.focus.Is(id)).Padding(0, 1)
			button = button.HoverStyle(pillButtonStyle(hover, colorSelectionBG, false))
		}
		buttons = append(buttons, bloom.Fixed(w, button))
		width += w
	}
	const gap = 2
	if d.info {
		style := lg.NewStyle().
			Background(colorTitle).
			Foreground(colorBlack).
			Bold(true).
			Padding(0, 1)
		add(confirmOKID, "OK", pillButtonStyle(style, colorTitle, false))
	} else {
		no, yes := m.styles.confirmNoDim, m.styles.confirmYesDim
		if s.focus.Is(confirmYesID) {
			yes = m.styles.confirmYes
		} else {
			no = m.styles.confirmNo
		}
		add(confirmNoID, "No", no)
		add(confirmYesID, "Yes", yes)
		width += gap
	}
	row := bloom.Row(buttons...).Gap(gap).Justify(flex.Center)
	return row, width
}

// Only the top border moves the panel. Its body, buttons and scrollbar keep their own input.
func (m tuiModel) positionConfirm(
	box lg.Style,
	body bloom.Child,
	width, height int,
) bloom.LayerNode {
	s := m.screen
	d := s.dialog
	width = min(m.width, width+box.GetHorizontalFrameSize())
	height = min(m.height, height+box.GetVerticalFrameSize())
	d.measuredAt = image.Pt(m.width, m.height)
	d.limit = image.Pt(m.width-width, m.height-height)
	handle := bloom.Layer(bloom.Space()).ID(confirmHandleID).Anchor(bloom.Start, bloom.Start).
		Size(width, 1).Transparent().On(on.Drag(d.drag))
	panel := bloom.Overlay(bloom.Box(box, body), handle)
	layer := s.modalLayer(panel).ID(confirmPanelID).Size(width, height)
	if p := d.position; p != nil {
		*p = d.clamp(*p)
		layer = layer.Anchor(bloom.Start, bloom.Start).Offset(p.X, p.Y)
	}
	return layer
}

func (d *confirmDialog) clamp(p image.Point) image.Point {
	p.X = max(0, min(p.X, d.limit.X))
	p.Y = max(0, min(p.Y, d.limit.Y))
	return p
}

func (d *confirmDialog) drag(e on.Event) tea.Cmd {
	switch e.Msg.(type) {
	case tea.MouseClickMsg:
		if !e.Done {
			d.grab = new(image.Pt(e.X, e.Y))
		}
	case tea.MouseReleaseMsg, tea.MouseMotionMsg:
		if d.grab != nil && (e.Screen != e.Start || d.position != nil) {
			d.position = new(d.clamp(e.Screen.Sub(*d.grab)))
		}
	}
	if e.Done {
		d.grab = nil
	}
	return nil
}
