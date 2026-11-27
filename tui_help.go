package main

import (
	"image"
	"strings"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/key"
)

type helpSheet struct {
	measuredAt             image.Point
	box                    lg.Style
	body, footer           bloom.Child
	width, footerHeight    int
	offset, visible, total int
	showBar                bool
}

func (h *helpSheet) scrollTo(offset int) tea.Cmd {
	h.offset = max(0, min(offset, h.total-h.visible))
	return nil
}

func (m tuiModel) updateHelp(k tea.KeyPressMsg) tuiModel {
	h := m.currentHelp()
	if h.total > h.visible {
		switch k.String() {
		case key.Down, tuiKeybindVimDown:
			h.scrollTo(h.offset + 1)
			return m
		case key.Up, tuiKeybindVimUp:
			h.scrollTo(h.offset - 1)
			return m
		case key.PageDown, key.CtrlF:
			h.scrollTo(h.offset + h.visible)
			return m
		case key.PageUp, key.CtrlB:
			h.scrollTo(h.offset - h.visible)
			return m
		case key.Home, tuiKeybindTop:
			h.scrollTo(0)
			return m
		case key.End, tuiKeybindBottom:
			h.scrollTo(h.total)
			return m
		}
	}
	m.help = nil
	if m.screen != nil && m.screen.pointer.last.Button == tea.MouseLeft {
		m.screen.resetRoot(m.reviewWidthMethod())
	}
	return m
}

// helpBody keeps the two-column reading order when both columns fit, including the scrollbar.
func (m tuiModel) helpBody(width, height int) bloom.Child {
	pairs := helpShortcuts()
	method := m.reviewWidthMethod()
	keyWidth := 0
	for _, p := range pairs {
		keyWidth = max(keyWidth, method.StringWidth(p.Key))
	}
	rows := make([]bloom.Child, len(pairs))
	for i, p := range pairs {
		pad := strings.Repeat(" ", keyWidth-method.StringWidth(p.Key))
		rows[i] = bloom.Text(
			pad + m.styles.helpKey.Render(p.Key) + "  " + m.styles.helpText.Render(p.Desc),
		)
	}
	const columnsPerSheet, gutter = 2, 4
	half := (len(rows) + columnsPerSheet - 1) / columnsPerSheet
	columns := bloom.Row(bloom.Column(rows[:half]...), bloom.Column(rows[half:]...)).Gap(gutter)
	fitWidth := width
	if half > height {
		fitWidth -= tuiScrollbarWidth
	}
	if bloom.Measure(0, 0, columns).W <= fitWidth {
		return columns
	}
	return bloom.Column(rows...)
}

// Measure on open and resize so scrolling can reuse the styled body. Input and
// View both come through here because resize can be followed immediately by a key.
func (m tuiModel) currentHelp() *helpSheet {
	h := m.help
	screen := image.Pt(m.width, m.height)
	if h.body != nil && h.measuredAt == screen {
		return h
	}
	h.measuredAt = screen
	h.box = m.styles.overlayBox.Padding(1, tuiConfirmPadX-1)
	if m.height < h.box.GetVerticalFrameSize()+2 ||
		m.width <= h.box.GetHorizontalFrameSize()+tuiScrollbarWidth {
		h.box = h.box.Padding(0)
	}
	if m.height < h.box.GetVerticalFrameSize()+2 ||
		m.width <= h.box.GetHorizontalFrameSize()+tuiScrollbarWidth {
		h.box = lg.NewStyle()
	}
	width := max(1, m.width-h.box.GetHorizontalFrameSize())
	height := max(1, m.height-h.box.GetVerticalFrameSize())
	footerRows := min(1, height-1)
	gap := min(1, height-footerRows-1)
	h.footerHeight = footerRows + gap
	bodyHeight := height - h.footerHeight
	h.body = m.helpBody(width, bodyHeight)
	size := bloom.Measure(width, 0, h.body)
	hint := "Press any key to dismiss"
	method := m.reviewWidthMethod()
	h.showBar = size.H > bodyHeight && width > tuiScrollbarWidth
	if size.H > bodyHeight {
		hint = "↑/↓ scroll · Esc close"
		if method.StringWidth(hint) > width {
			hint = "↑/↓ · Esc"
		}
	}
	if h.showBar {
		size = bloom.Measure(width-tuiScrollbarWidth, 0, h.body)
		size.W += tuiScrollbarWidth
	}
	h.total, h.visible = size.H, min(size.H, bodyHeight)
	h.scrollTo(h.offset)
	if method.StringWidth(hint) > width {
		hint = "Esc"
		if width < len(hint) {
			hint = "?"
		}
	}
	h.width = min(width, max(size.W, method.StringWidth(hint)))
	h.footer = bloom.Text(hint).
		NoWrap().
		Style(styleDismiss.Bold(true).Align(lg.Center).PaddingTop(gap))
	return h
}

func (m tuiModel) helpLayer() bloom.LayerNode {
	h := m.currentHelp()
	var viewport bloom.Child = bloom.Scroll(h.body, h.offset)
	if h.showBar {
		bar := bloom.Scrollbar(h.total, h.visible, h.offset).
			Style(reviewBarStyle()).
			OnScroll(h.scrollTo)
		viewport = bloom.Row(bloom.Fill(viewport), bloom.Fixed(tuiScrollbarWidth, bar))
	}
	panel := bloom.Column(bloom.Fixed(h.visible, viewport), bloom.Fixed(h.footerHeight, h.footer))
	return m.screen.modalLayer(bloom.Box(h.box, panel)).Size(
		h.width+h.box.GetHorizontalFrameSize(),
		h.visible+h.footerHeight+h.box.GetVerticalFrameSize(),
	)
}

func helpShortcuts() []key.Hint {
	pairs := []key.Hint{
		{Key: tuiKeysVimUpDown, Desc: tuiDescNavigate},
		{Key: tuiKeysJumpFirstLast, Desc: tuiDescJumpFirstLast},
		{Key: key.Enter, Desc: tuiDescShow},
		{Key: key.Space, Desc: tuiDescSelect},
		{Key: key.ShiftArrowsVertical, Desc: tuiDescExtendSelection},
		{Key: tuiKeybindSelectAll, Desc: tuiDescSelectAll},
		{Key: tuiKeybindInvertSelection, Desc: tuiDescInvertSelection},
		{Key: tuiKeybindFilter, Desc: tuiDescFilter},
		{Key: tuiKeybindApprove, Desc: tuiDescApprove},
		{Key: tuiKeybindApproveMerge, Desc: tuiDescApproveMerge},
		{Key: tuiKeybindApproveNoConfirm, Desc: tuiDescApproveNoConfirm},
		{Key: tuiKeybindDiff, Desc: tuiDescDiff},
		{Key: tuiKeybindDraftToggle, Desc: tuiDescDraftToggle},
		{Key: tuiKeybindMerge, Desc: tuiDescMerge},
		{Key: tuiKeybindForceMerge, Desc: tuiDescForceMerge},
		{Key: tuiKeybindClose, Desc: tuiDescClose},
		{Key: tuiKeybindUpdateBranch, Desc: tuiDescUpdateBranch},
		{Key: tuiKeybindUnassign, Desc: tuiDescUnassign},
		{Key: tuiKeybindUnassignNoConfirm, Desc: tuiDescUnassignNoConf},
		{Key: tuiKeybindOpen, Desc: tuiDescOpen},
		{Key: tuiKeybindCopyURL, Desc: tuiDescCopy},
		{Key: tuiKeybindSlack, Desc: tuiDescSendSlack},
		{Key: tuiKeybindSlackNoConfirm, Desc: tuiDescSendSlackNoConf},
		{Key: key.Tab, Desc: tuiDescCycleSortOrder},
		{Key: tuiKeybindOptions, Desc: tuiDescOptions},
		{Key: tuiKeybindCyclePreset + "/" + tuiKeybindCyclePresetBack, Desc: tuiDescCyclePreset},
		{
			Key:  tuiKeybindCycleTimePreset + "/" + tuiKeybindCycleTimePresetBack,
			Desc: tuiDescCycleTimePreset,
		},
		{Key: tuiKeybindClearPresets, Desc: tuiDescClearPresets},
		{Key: tuiKeybindToggleRefresh, Desc: tuiDescRefresh},
		{Key: tuiKeybindHelp, Desc: tuiDescHelp},
		{Key: tuiKeybindQuit, Desc: tuiDescQuit},
	}
	if hasAIReviewLauncher() {
		// Insert review before the last two entries (?, q).
		pairs = append(
			pairs[:len(pairs)-2],
			append(
				[]key.Hint{
					{Key: tuiKeybindReview, Desc: tuiDescReview},
					{Key: tuiKeybindReviewNoConfirm, Desc: tuiDescReviewNoConfirm},
					{Key: tuiKeybindCopilotReview, Desc: tuiDescCopilotReview},
				},
				pairs[len(pairs)-2:]...)...)
	}

	return pairs
}
