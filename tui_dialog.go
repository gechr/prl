package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	"github.com/gechr/primer/button"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/form"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/pill"
	"github.com/gechr/primer/scrollbar"
)

// confirmUsesDialog reports whether a confirmation is pending. Every flavor -
// buttons, information, and forms - renders through the primer dialog stack.
func (m *tuiModel) confirmUsesDialog() bool {
	return m.confirmAction != ""
}

// syncConfirmDialog reconciles the dialog stack with the pending confirmation
// after every Update: a freshly set-up confirmation opens its dialog, and one
// cleared by any other path closes it. The stack is created
// lazily so literal test models without one stay valid.
func (m *tuiModel) syncConfirmDialog() {
	if m.confirmUsesDialog() {
		if m.dialogs == nil {
			m.dialogs = dialog.New(m.newConfirmFrame())
		}
		if !m.dialogs.Active() {
			m.dialogs.Push(m.newConfirmDialog())
		}
		return
	}
	for m.dialogs != nil && m.dialogs.Active() {
		m.dialogs.Pop()
	}
}

// newConfirmFrame gives every dialog the existing modal chrome and scrollbar
// colors while primer owns sizing, clipping, and placement.
func (m *tuiModel) newConfirmFrame() dialog.Frame {
	return dialog.NewFrame(dialog.FrameConfig{
		Styles: dialog.Styles{
			Box:      m.styles.overlayBox.Padding(1, tuiConfirmPadX-1),
			HintKey:  m.styles.helpKey,
			HintText: m.styles.helpText,
			Scrollbar: scrollbar.Styles{
				Thumb: lg.NewStyle().Foreground(colorAccent),
				Track: lg.NewStyle().Foreground(colorAccent).Faint(true),
			},
		},
	})
}

func (m *tuiModel) newConfirmDialog() dialog.Dialog {
	switch {
	case m.confirmHasInput:
		return m.newConfirmFormDialog()
	case m.confirmCmd == nil && m.confirmCmdFn == nil:
		return dialog.NewInfo(m.confirmPrompt, button.Button{
			Label: "OK",
			Focused: pillButtonStyle(lg.NewStyle().
				Background(colorTitle).
				Foreground(colorBlack).
				Bold(true).
				Padding(0, 1), colorTitle, false),
			Blurred: pillButtonStyle(
				lg.NewStyle().Foreground(colorTitle).Padding(0, 1),
				colorTitle,
				true,
			),
		})
	default:
		return m.newConfirmButtonsDialog()
	}
}

// newConfirmButtonsDialog builds the No/Yes dialog for the pending plain
// confirm, keeping the legacy key contract: y accepts, n/q (and esc) decline,
// and enter presses the focused button, which starts on Yes.
func (m *tuiModel) newConfirmButtonsDialog() dialog.Dialog {
	return dialog.NewConfirmButtons(
		m.confirmPrompt,
		dialog.ConfirmButton{
			Button: button.Button{
				Label:   "No",
				Focused: m.styles.confirmNo,
				Blurred: m.styles.confirmNoDim,
			},
			Keys: []string{tuiKeybindConfirmNo, "N", tuiKeybindQuit},
		},
		dialog.ConfirmButton{
			Button: button.Button{
				Label:   "Yes",
				Focused: m.styles.confirmYes,
				Blurred: m.styles.confirmYesDim,
			},
			Accept:  true,
			Keys:    []string{"y", "Y"},
			Default: true,
		},
	)
}

func (m *tuiModel) newConfirmFormDialog() dialog.Dialog {
	defs := append([]filterOptionDef(nil), m.confirmOptions...)
	values := make([]string, len(defs))
	for i := range defs {
		values[i] = m.selectedConfirmOptionValue(i)
	}
	owner := *m
	previousProvider := reviewProviderUnknown
	if len(values) > 0 {
		previousProvider = normalizeReviewProvider(values[0])
	}
	inputValue := m.confirmInputValue
	if draft, ok := m.confirmDraft(); ok {
		inputValue = draft
	}
	var d *dialog.Form
	formOptions := []dialog.FormOption{}
	if activeIcons.PillLeft != "" {
		formOptions = append(formOptions, dialog.WithNerdFonts())
	}
	d = dialog.NewForm(
		owner.newConfirmFormModel(defs, values, inputValue),
		func(ev form.EventKind) {
			if ev == form.EventChanged && owner.confirmAction == tuiActionReview {
				owner.syncReviewDialogForm(d, &defs, &previousProvider)
			}
		},
		formOptions...,
	)
	return d
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
		Optional:    true,
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
// colors, models and efforts on the statusline's capability scale.
var (
	reviewProviderValueStyles = map[string]lg.Style{
		string(reviewProviderClaude): lg.NewStyle().Foreground(lg.Color("#da7756")),
		string(reviewProviderCodex):  lg.NewStyle().Foreground(lg.Color("#9b77dc")),
		string(reviewProviderGemini): lg.NewStyle().Foreground(lg.Color("#4796e3")),
	}
	reviewEffortValueStyles = map[string]lg.Style{
		claudeReviewEffortLow:    lg.NewStyle().Foreground(lg.Color("2")),
		claudeReviewEffortMedium: lg.NewStyle().Foreground(lg.Color("3")),
		claudeReviewEffortHigh:   lg.NewStyle().Foreground(lg.Color("#ff8c00")),
		claudeReviewEffortXHigh:  lg.NewStyle().Foreground(lg.Color("1")),
		claudeReviewEffortMax:    lg.NewStyle().Foreground(lg.Color("#ff0000")),
		// Gemini-only efforts: minimal sits below low, and the thinking
		// budgets ramp like the named levels, with off dimmed and dynamic in
		// the statusline's fallback purple.
		geminiReviewEffortMinimal: lg.NewStyle().Foreground(lg.Color("4")),
		geminiReviewEffortOff:     lg.NewStyle().Faint(true),
		geminiReviewEffort1024:    lg.NewStyle().Foreground(lg.Color("2")),
		geminiReviewEffort8192:    lg.NewStyle().Foreground(lg.Color("3")),
		geminiReviewEffort24576:   lg.NewStyle().Foreground(lg.Color("1")),
		geminiReviewEffortDynamic: lg.NewStyle().Foreground(lg.Color("#c4b5fd")),
	}
	reviewModelValueStyles = map[string]lg.Style{
		claudeReviewModelSonnet: lg.NewStyle().Foreground(lg.Color("2")),
		claudeReviewModelOpus:   lg.NewStyle().Foreground(lg.Color("3")),
		claudeReviewModelFable:  lg.NewStyle().Foreground(lg.Color("1")),
		codexReviewModel54Mini:  lg.NewStyle().Foreground(lg.Color("4")),
		codexReviewModel54:      lg.NewStyle().Foreground(lg.Color("2")),
		codexReviewModel55:      lg.NewStyle().Foreground(lg.Color("2")),
		codexReviewModel56Luna:  lg.NewStyle().Foreground(lg.Color("3")),
		codexReviewModel56Terra: lg.NewStyle().Foreground(lg.Color("208")),
		codexReviewModel56Sol:   lg.NewStyle().Foreground(lg.Color("1")),
		geminiReviewModelFlash:  lg.NewStyle().Foreground(lg.Color("2")),
		geminiReviewModel31Pro:  lg.NewStyle().Foreground(lg.Color("1")),
	}
)

// confirmValueRenderer returns the display styler for a review cycle field,
// or nil for fields whose values carry no color.
func confirmValueRenderer(label string) func(string) string {
	var styles map[string]lg.Style
	switch label {
	case reviewProviderOptionLabel:
		styles = reviewProviderValueStyles
	case reviewModelOptionLabel:
		styles = reviewModelValueStyles
	case reviewEffortOptionLabel:
		styles = reviewEffortValueStyles
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

func (m *tuiModel) syncReviewDialogForm(
	d *dialog.Form,
	defs *[]filterOptionDef,
	previousProvider *reviewProvider,
) {
	if d == nil || len(*defs) < 2 {
		return
	}
	fm := d.Model()
	provider := normalizeReviewProvider(fm.Value(0))
	if provider == reviewProviderUnknown {
		provider = configuredReviewProvider(m.cfg)
	}
	model := normalizeReviewModel(m.cfg, provider, fm.Value(1))
	effort := ""
	if len(*defs) > reviewEffortOptionRow {
		effort = normalizeReviewEffort(m.cfg, provider, model, fm.Value(reviewEffortOptionRow))
	}
	next := reviewConfirmOptions(m.cfg, provider, model)
	promptValue := fm.Value(len(*defs))
	if m.confirmReviewPR != nil {
		oldPrompt := reviewPrompt(*m.confirmReviewPR, m.cfg, *previousProvider)
		if promptValue == oldPrompt {
			promptValue = reviewPrompt(*m.confirmReviewPR, m.cfg, provider)
		}
	}
	values := []string{string(provider), model}
	if len(next) > reviewEffortOptionRow {
		values = append(values, effort)
	}
	if len(next) != len(*defs) {
		*fm = m.newConfirmFormModel(next, values, promptValue)
		*defs = next
		*previousProvider = provider
		return
	}
	for i, def := range next {
		fm.SetOptions(i, confirmChoiceLabels(def.choices))
		fm.SetValue(i, values[i])
	}
	fm.SetValue(len(next), promptValue)
	*defs = next
	*previousProvider = provider
}

// updateConfirmDialog routes a message into the confirm dialog and completes
// the confirmation when the dialog resolves: an accepted confirm runs the
// pending command with the progress flash, a declined one just clears.
func (m tuiModel) updateConfirmDialog(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.dialogs == nil || !m.dialogs.Active() {
		return m, nil
	}
	if _, ok := m.dialogs.Top().(*dialog.Info); ok {
		if key, ok := msg.(tea.KeyPressMsg); ok {
			switch key.String() {
			case "q", "y", "Y", "n", "N":
				return m.confirmDismiss()
			}
		}
	}
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && keyMsg.String() == key.AltEnter {
		msg = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	cmd, popped, res := m.dialogs.Update(msg)
	if popped == nil {
		return m, cmd
	}
	if res == dialog.ResultSubmit {
		if f, ok := popped.(*dialog.Form); ok {
			model, actionCmd := m.confirmAcceptWithSubmission(m.dialogFormSubmission(f))
			return model, tea.Batch(cmd, actionCmd)
		}
		model, actionCmd := m.confirmAccept()
		return model, tea.Batch(cmd, actionCmd)
	}
	if f, ok := popped.(*dialog.Form); ok {
		m = m.rememberConfirmDraft(m.dialogFormInput(f))
	}
	model, dismissCmd := m.confirmDismiss()
	return model, tea.Batch(cmd, dismissCmd)
}

func (m tuiModel) dialogFormInput(d *dialog.Form) string {
	values := d.Model().Values()
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
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

func (m tuiModel) dialogFormSubmission(d *dialog.Form) confirmSubmission {
	values := d.Model().Values()
	if len(values) == 0 {
		return confirmSubmission{}
	}
	submission := confirmSubmission{Input: strings.TrimSpace(values[len(values)-1])}
	if len(values) == 1 {
		return submission
	}
	submission.Options = make(map[string]string, len(values)-1)
	for i, selected := range values[:len(values)-1] {
		label := ""
		if i < len(m.confirmOptions) {
			label = m.confirmOptions[i].label
			selected = confirmChoiceValue(m.confirmOptions[i].choices, selected)
		} else if m.confirmAction == tuiActionReview && i == reviewEffortOptionRow {
			label = reviewEffortOptionLabel
		}
		if label != "" {
			submission.Options[label] = selected
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

// viewConfirmDialog composites the confirm dialog over the backdrop.
func (m tuiModel) viewConfirmDialog(backdrop string) string {
	if m.dialogs == nil {
		return backdrop
	}
	return m.dialogs.View(backdrop, m.width, m.height)
}
