package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/form"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/pill"
)

func (m *tuiModel) confirmUsesDialog() bool {
	return m.confirmAction != ""
}

func (m *tuiModel) showInfo(prompt string) {
	if m.confirmHasInput && m.screen != nil && m.screen.dialog != nil {
		if d := m.screen.dialog; d.form != nil {
			*m = m.rememberConfirmDraft(d.form.Value(len(d.options)))
		}
	}
	*m = m.clearConfirm()
	m.confirmAction, m.confirmPrompt = tuiActionInfo, prompt
}

func (m *tuiModel) syncConfirmDialog() {
	if !m.confirmUsesDialog() && m.screen == nil {
		return
	}
	s := m.ensureScreen()
	if s.dialog != nil {
		if s.dialog.action == m.confirmAction && s.dialog.prompt == m.confirmPrompt {
			return
		}
		s.focus.Pop()
		s.dialog = nil
		if s.pointer.last.Button == tea.MouseLeft {
			// A later release belongs to the old dialog, even if another opens in its place.
			s.resetRoot(m.reviewWidthMethod())
		}
	}
	if !m.confirmUsesDialog() {
		return
	}
	d := &confirmDialog{action: m.confirmAction, prompt: m.confirmPrompt}
	s.dialog = d
	s.focus.Push(confirmPanelID)
	switch {
	case m.confirmHasInput:
		d.options = m.confirmOptions
		values := make([]string, len(d.options))
		for i := range d.options {
			values[i] = m.selectedConfirmOptionValue(i)
		}
		if m.confirmAction == tuiActionReview && len(values) > 0 {
			d.provider = normalizeReviewProvider(values[0])
		}
		input := m.confirmInputValue
		if draft, ok := m.confirmDraft(); ok {
			input = draft
		}
		d.form = new(m.newConfirmFormModel(d.options, values, input))
	case m.confirmCmd == nil && m.confirmCmdFn == nil:
		d.info = true
		s.focus.Set(confirmOKID)
	default:
		s.focus.Set(confirmYesID)
	}
}

func (m *tuiModel) newConfirmFormModel(
	defs []filterOptionDef,
	values []string,
	inputValue string,
) form.Model {
	boldAccent := styleAccent.Bold(true)
	dimAccent := styleAccent.Faint(true)
	fields := make([]form.FieldSpec, 0, len(defs)+1)
	for i, def := range defs {
		initial := ""
		if i < len(values) {
			initial = values[i]
		}
		spec := form.FieldSpec{
			Label:   def.label,
			Initial: initial,
			Options: confirmChoiceLabels(def.choices),
		}
		if m.confirmAction == tuiActionReview {
			spec.Notify = true
			spec.RenderValue = confirmValueRenderer(def.label)
		}
		fields = append(fields, spec)
	}
	label := m.confirmInputLabel
	if label == "" {
		label = "Comment"
	}
	fields = append(fields, form.FieldSpec{
		Label:       label,
		Placeholder: m.confirmInputPlaceholder,
		Initial:     inputValue,
		Multiline:   true,
		Optional:    m.confirmAction != tuiActionUnapprove,
	})
	return form.New(form.Config{
		Title:  m.confirmPrompt + nl,
		Fields: fields,
		Width:  m.confirmInputWidth(),
		Styles: form.Styles{
			Title: styleText,
			Chevrons: pill.Chevrons{
				Left:  activeIcons.ChevronLeft,
				Right: activeIcons.ChevronRight,
			},
			Label:         dimAccent.Bold(true),
			LabelFocused:  boldAccent,
			Border:        dimAccent.Bold(true),
			BorderFocused: boldAccent,
			HintKey:       m.styles.helpKey,
			HintText:      m.styles.helpText,
			Question:      styleWarning,
			Error:         styleDanger,
		},
	})
}

// Review cycle values render in meaningful colors: providers in their brand
// colors, models and efforts on the statusline's capability scale. Built per
// call so hex colors track the active background; ANSI indexes 1-4 are
// terminal-themed and identical in both modes.
func reviewProviderValueStyles() map[string]lg.Style {
	ld := lg.LightDark(!paletteIsLight)
	return map[string]lg.Style{
		string(reviewProviderClaude): lg.NewStyle().
			Foreground(ld(lg.Color("#c15f3c"), lg.Color("#da7756"))),
		string(reviewProviderCodex): lg.NewStyle().
			Foreground(ld(lg.Color("#7a55c8"), lg.Color("#9b77dc"))),
		string(reviewProviderGemini): lg.NewStyle().
			Foreground(ld(lg.Color("#1a73e8"), lg.Color("#4796e3"))),
	}
}

func reviewEffortValueStyles() map[string]lg.Style {
	ld := lg.LightDark(!paletteIsLight)
	return map[string]lg.Style{
		claudeReviewEffortLow:    lg.NewStyle().Foreground(lg.Color("2")),
		claudeReviewEffortMedium: lg.NewStyle().Foreground(lg.Color("3")),
		claudeReviewEffortHigh: lg.NewStyle().
			Foreground(ld(lg.Color("#cc7000"), lg.Color("#ff8c00"))),
		claudeReviewEffortXHigh: lg.NewStyle().Foreground(lg.Color("1")),
		claudeReviewEffortMax: lg.NewStyle().
			Foreground(ld(lg.Color("#d70000"), lg.Color("#ff0000"))),
		codexReviewEffortUltra: lg.NewStyle().Bold(true).
			Foreground(ld(lg.Color("#d70000"), lg.Color("#ff0000"))),
	}
}

// Yolo reads as danger when on, dim when off.
func reviewYoloValueStyles() map[string]lg.Style {
	ld := lg.LightDark(!paletteIsLight)
	return map[string]lg.Style{
		reviewYoloNo: lg.NewStyle().Faint(true),
		reviewYoloYes: lg.NewStyle().Bold(true).
			Foreground(ld(lg.Color("#d70000"), lg.Color("#ff0000"))),
	}
}

func reviewModelValueStyles() map[string]lg.Style {
	ld := lg.LightDark(!paletteIsLight)
	return map[string]lg.Style{
		claudeReviewModelSonnet:  lg.NewStyle().Foreground(lg.Color("2")),
		claudeReviewModelOpus:    lg.NewStyle().Foreground(lg.Color("3")),
		claudeReviewModelFable:   lg.NewStyle().Foreground(lg.Color("1")),
		codexReviewModel62Sol:    lg.NewStyle().Foreground(lg.Color("1")),
		codexReviewModel62Astra:  lg.NewStyle().Bold(true).Foreground(lg.Color("1")),
		codexReviewModel61Terra:  lg.NewStyle().Foreground(ld(lg.Color("130"), lg.Color("208"))),
		codexReviewModel56Luna:   lg.NewStyle().Foreground(lg.Color("3")),
		codexReviewModel56Terra:  lg.NewStyle().Foreground(ld(lg.Color("130"), lg.Color("208"))),
		codexReviewModel56Sol:    lg.NewStyle().Foreground(lg.Color("1")),
		codexReviewModel6Sol:     lg.NewStyle().Foreground(lg.Color("1")),
		codexReviewModel6Luna:    lg.NewStyle().Foreground(lg.Color("3")),
		codexReviewModel61Sol:    lg.NewStyle().Foreground(lg.Color("1")),
		codexReviewModel6Astra:   lg.NewStyle().Bold(true).Foreground(lg.Color("1")),
		geminiReviewModel36Flash: lg.NewStyle().Foreground(lg.Color("2")),
		geminiReviewModel37Flash: lg.NewStyle().Foreground(lg.Color("2")),
		geminiReviewModel38Flash: lg.NewStyle().Foreground(lg.Color("2")),
		geminiReviewModel4Argon:  lg.NewStyle().Bold(true).Foreground(lg.Color("1")),
		geminiReviewModel31Pro:   lg.NewStyle().Foreground(lg.Color("1")),
	}
}

// confirmValueRenderer returns the display styler for a review cycle field,
// or nil for fields whose values carry no color.
func confirmValueRenderer(label string) func(string) string {
	var styles map[string]lg.Style
	switch label {
	case reviewProviderOptionLabel:
		styles = reviewProviderValueStyles()
	case reviewModelOptionLabel:
		styles = reviewModelValueStyles()
	case reviewEffortOptionLabel:
		styles = reviewEffortValueStyles()
	case reviewYoloOptionLabel:
		styles = reviewYoloValueStyles()
	default:
		return nil
	}
	return func(v string) string {
		if s, ok := styles[v]; ok {
			return s.Render(v)
		}
		return v
	}
}

func confirmChoiceLabels(choices []filterChoice) []string {
	labels := make([]string, len(choices))
	for i, choice := range choices {
		labels[i] = choice.label
	}
	return labels
}

func (m *tuiModel) syncReviewDialogForm(d *confirmDialog) {
	const requiredOptions = 2 // provider and model
	if len(d.options) < requiredOptions {
		return
	}
	fm := d.form
	provider := normalizeReviewProvider(fm.Value(0))
	if provider == reviewProviderUnknown {
		provider = configuredReviewProvider(m.cfg)
	}
	model := normalizeReviewModel(m.cfg, provider, fm.Value(1))
	effort := ""
	if row := reviewOptionRow(d.options, reviewEffortOptionLabel); row >= 0 {
		effort = normalizeReviewEffort(m.cfg, provider, model, fm.Value(row))
	}
	// Yolo never persisted: carry across rebuilds only.
	yolo := reviewYoloNo
	if row := reviewOptionRow(d.options, reviewYoloOptionLabel); row >= 0 {
		yolo = normalizeReviewYolo(fm.Value(row))
	}
	// Remember the navigated combination immediately - the next dialog opens
	// here even if this one is cancelled. Explicit config keys still win when
	// the state is re-applied on startup.
	if m.cfg != nil {
		m.cfg.TUI.Review.Default = TUIReviewDefaultConfig{
			Provider: string(provider),
			Model:    model,
			Effort:   effort,
		}
		if err := saveTUIState(m.cfg); err != nil {
			warnStateSaveErr(err, "review defaults")
		}
	}
	next := reviewConfirmOptions(m.cfg, provider, model)
	promptValue := fm.Value(len(d.options))
	if m.confirmReviewPR != nil {
		oldPrompt := reviewPrompt(*m.confirmReviewPR, m.cfg, d.provider)
		if promptValue == oldPrompt {
			promptValue = reviewPrompt(*m.confirmReviewPR, m.cfg, provider)
		}
	}
	values := []string{string(provider), model}
	if reviewOptionRow(next, reviewEffortOptionLabel) >= 0 {
		values = append(values, effort)
	}
	if reviewOptionRow(next, reviewYoloOptionLabel) >= 0 {
		values = append(values, yolo)
	}
	if len(next) != len(d.options) {
		*fm = m.newConfirmFormModel(next, values, promptValue)
		d.options = next
		d.provider = provider
		return
	}
	for i, def := range next {
		fm.SetOptions(i, confirmChoiceLabels(def.choices))
		fm.SetValue(i, values[i])
	}
	fm.SetValue(len(next), promptValue)
	d.options = next
	d.provider = provider
}

func (m tuiModel) updateConfirmDialog(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.screen == nil || m.screen.dialog == nil {
		return m, nil
	}
	d := m.screen.dialog
	if d.form == nil {
		return m.updateConfirmButtons(msg)
	}
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		d.manualScroll = false
		if keyMsg.String() == key.AltEnter {
			msg = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
		}
	}
	if click, ok := msg.(tea.MouseClickMsg); ok {
		m.screen.focusFormAt(click)
		return m, nil
	}
	cmd, result, _ := d.form.Update(msg)
	switch result {
	case form.EventNone, form.EventEditor, form.EventConfirmDiscard:
		// These forms edit inline and save drafts immediately on cancellation.
	case form.EventChanged:
		if m.confirmAction == tuiActionReview {
			m.syncReviewDialogForm(d)
		}
	case form.EventSubmit:
		if !m.confirmFits() {
			return m, cmd
		}
		model, actionCmd := m.confirmAcceptWithSubmission(d.submission())
		return model, tea.Batch(cmd, actionCmd)
	case form.EventCancel:
		m = m.rememberConfirmDraft(d.form.Value(len(d.options)))
		model, dismissCmd := m.confirmDismiss()
		return model, tea.Batch(cmd, dismissCmd)
	}
	return m, cmd
}

func (m tuiModel) updateConfirmButtons(msg tea.Msg) (tea.Model, tea.Cmd) {
	s := m.screen
	d := s.dialog
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case key.Y, "Y":
			if d.info {
				return m.confirmDismiss()
			}
			d.pressed = confirmYesID
		case tuiKeybindConfirmNo, "N", tuiKeybindQuit, key.Esc:
			return m.confirmDismiss()
		case key.Space:
			if d.info {
				return m.confirmDismiss()
			}
		case key.Enter:
			if _, handled := s.focus.Update(k, s.root.Frame()); !handled {
				// The frame may not have been drawn yet.
				d.pressed = s.focus.Current()
			}
		case key.Left, "h", key.ShiftTab, key.Right, "l", key.Tab:
			// Both directions wrap in a two-button row.
			if s.focus.Next(s.root.Frame()) {
				break
			}
			switch s.focus.Current() {
			case confirmYesID:
				s.focus.Set(confirmNoID)
			case confirmNoID:
				s.focus.Set(confirmYesID)
			}
		}
	}
	switch d.pressed {
	case confirmYesID:
		d.pressed = ""
		if !m.confirmFits() {
			return m, nil
		}
		return m.confirmAccept()
	case confirmNoID, confirmOKID:
		return m.confirmDismiss()
	default:
		return m, nil
	}
}

func (m tuiModel) confirmDraft() (string, bool) {
	key, ok := m.confirmDraftKey()
	if !ok {
		return "", false
	}
	draft, ok := m.confirmDrafts[key]
	return draft, ok
}

func (m tuiModel) rememberConfirmDraft(input string) tuiModel {
	key, ok := m.confirmDraftKey()
	if !ok {
		return m
	}
	if m.confirmDrafts == nil {
		m.confirmDrafts = make(map[confirmDraftKey]string)
	}
	if input == "" {
		delete(m.confirmDrafts, key)
	} else {
		m.confirmDrafts[key] = input
	}
	return m
}

func (m tuiModel) forgetConfirmDraft() tuiModel {
	key, ok := m.confirmDraftKey()
	if ok {
		delete(m.confirmDrafts, key)
	}
	return m
}

func (m tuiModel) confirmDraftKey() (confirmDraftKey, bool) {
	if !m.confirmHasInput || m.confirmAction == "" || m.confirmURL == "" {
		return confirmDraftKey{}, false
	}
	return confirmDraftKey{action: m.confirmAction, url: m.confirmURL}, true
}

func (d *confirmDialog) submission() confirmSubmission {
	submission := confirmSubmission{Input: strings.TrimSpace(d.form.Value(len(d.options)))}
	if len(d.options) > 0 {
		submission.Options = make(map[string]string, len(d.options))
		for i, opt := range d.options {
			submission.Options[opt.label] = confirmChoiceValue(opt.choices, d.form.Value(i))
		}
	}
	return submission
}

func confirmChoiceValue(choices []filterChoice, label string) string {
	for _, choice := range choices {
		if choice.label == label {
			return choice.value
		}
	}
	return label
}
