package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
)

// editField tracks which field is focused in the edit TUI.
type editField int

const (
	editFieldTitle editField = iota
	editFieldBody
	editFieldCount
)

// editStyles holds all styles for the edit TUI.
type editStyles struct {
	header      lg.Style
	label       lg.Style
	dimLabel    lg.Style
	focusedText lg.Style
	blurredText lg.Style
	helpText    lg.Style
	helpKey     lg.Style
	counter     lg.Style
	dirty       lg.Style
}

func newEditStyles() editStyles {
	return editStyles{
		header:      lg.NewStyle().Foreground(lg.Color("208")).Bold(true),
		label:       lg.NewStyle().Foreground(lg.Color("212")).Bold(true),
		dimLabel:    lg.NewStyle().Foreground(lg.Color("242")),
		focusedText: lg.NewStyle().Foreground(lg.Color("48")),
		blurredText: lg.NewStyle().Foreground(lg.Color("242")),
		helpText:    lg.NewStyle().Foreground(lg.Color("242")),
		helpKey:     lg.NewStyle().Foreground(lg.Color("248")),
		counter:     lg.NewStyle().Foreground(lg.Color("242")),
		dirty:       lg.NewStyle().Foreground(lg.Color("226")).Bold(true),
	}
}

// editEntry holds the state for a single PR being edited.
type editEntry struct {
	ref          string
	origTitle    string
	origBody     string
	title        textinput.Model
	body         textarea.Model
	bodyFetched  bool
	bodyFetchErr error
	bodyFetching bool
	focus        editField
}

// bodyFetchedMsg is sent when a PR body has been fetched from the API.
type bodyFetchedMsg struct {
	index int
	body  string
	err   error
}

// bodyFetchFunc fetches the body for a PR at the given index.
type bodyFetchFunc func(index int) (string, error)

// editModel is the Bubble Tea model for the multi-PR edit TUI.
type editModel struct {
	entries   []editEntry
	current   int
	styles    editStyles
	fetchBody bodyFetchFunc

	submitted bool
	aborted   bool
}

// editResult holds the outcome for a single PR edit.
type editResult struct {
	Ref     string
	Title   string
	Body    string
	Changed bool
}

func newTextInput(styles editStyles, value string) textinput.Model {
	tiStyles := textinput.DefaultDarkStyles()
	tiStyles.Focused.Text = styles.focusedText
	tiStyles.Blurred.Text = styles.blurredText
	tiStyles.Cursor.Shape = tea.CursorBar
	tiStyles.Cursor.Blink = true
	tiStyles.Cursor.Color = lg.Color("48")

	ti := textinput.New()
	ti.Prompt = ""
	ti.SetWidth(editWidth)
	ti.SetValue(value)
	ti.SetStyles(tiStyles)
	ti.SetVirtualCursor(false)
	return ti
}

func newTextArea(styles editStyles, value string) textarea.Model {
	taStyles := textarea.DefaultDarkStyles()
	taStyles.Focused.Text = styles.focusedText
	taStyles.Focused.CursorLine = lg.NewStyle().Foreground(lg.Color("252"))
	taStyles.Blurred.Text = styles.blurredText
	taStyles.Cursor.Shape = tea.CursorBar
	taStyles.Cursor.Blink = true
	taStyles.Cursor.Color = lg.Color("48")

	ta := textarea.New()
	ta.Prompt = ""
	ta.SetWidth(editWidth)
	ta.SetValue(value)
	ta.ShowLineNumbers = false
	ta.SetHeight(editBodyLines)
	ta.SetStyles(taStyles)
	ta.SetVirtualCursor(false)
	return ta
}

type editPR struct {
	Ref   string
	Title string
}

func newEditModel(prs []editPR, fetchBody bodyFetchFunc) editModel {
	styles := newEditStyles()

	entries := make([]editEntry, len(prs))
	for i, pr := range prs {
		ti := newTextInput(styles, pr.Title)
		ta := newTextArea(styles, "")

		if i == 0 {
			ti.Focus()
		} else {
			ti.Blur()
		}
		ta.Blur()

		entries[i] = editEntry{
			ref:       pr.Ref,
			origTitle: pr.Title,
			title:     ti,
			body:      ta,
			focus:     editFieldTitle,
		}
	}

	return editModel{
		entries:   entries,
		styles:    styles,
		fetchBody: fetchBody,
	}
}

func (m editModel) Init() tea.Cmd {
	// Fetch body for the first PR.
	if len(m.entries) > 0 {
		return m.fetchBodyCmd(0)
	}
	return nil
}

func (m editModel) fetchBodyCmd(index int) tea.Cmd {
	fetch := m.fetchBody
	return func() tea.Msg {
		body, err := fetch(index)
		return bodyFetchedMsg{index: index, body: body, err: err}
	}
}

func (m editModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bodyFetchedMsg:
		e := &m.entries[msg.index]
		e.bodyFetching = false
		if msg.err != nil {
			e.bodyFetchErr = msg.err
		} else {
			e.bodyFetched = true
			e.origBody = msg.body
			e.body.SetValue(msg.body)
		}
		return m, nil

	case tea.WindowSizeMsg:
		e := &m.entries[m.current]
		e.title.SetWidth(msg.Width)
		e.body.SetWidth(msg.Width)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "tab", "shift+tab":
			return m.cycleField(msg.String())
		case "ctrl+s":
			m.submitted = true
			return m, tea.Quit
		case "ctrl+n":
			return m.navigate(1)
		case "ctrl+p":
			return m.navigate(-1)
		}
	}

	return m.updateFocused(msg)
}

func (m editModel) navigate(delta int) (tea.Model, tea.Cmd) {
	next := m.current + delta
	if next < 0 || next >= len(m.entries) {
		return m, nil
	}

	// Blur current entry.
	cur := &m.entries[m.current]
	cur.title.Blur()
	cur.body.Blur()

	m.current = next

	// Focus the title of the new entry.
	e := &m.entries[m.current]
	e.focus = editFieldTitle
	cmd := e.title.Focus()
	e.body.Blur()

	// Always clear screen when switching PRs to avoid overlapping views.
	cmds := []tea.Cmd{cmd, tea.ClearScreen}

	// Fetch body if not yet fetched.
	if !e.bodyFetched && !e.bodyFetching && e.bodyFetchErr == nil {
		e.bodyFetching = true
		cmds = append(cmds, m.fetchBodyCmd(m.current))
	}

	return m, tea.Batch(cmds...)
}

func (m editModel) cycleField(key string) (tea.Model, tea.Cmd) {
	e := &m.entries[m.current]

	if key == "tab" {
		e.focus = (e.focus + 1) % editFieldCount
	} else {
		e.focus = (e.focus - 1 + editFieldCount) % editFieldCount
	}

	var cmd tea.Cmd
	switch e.focus { //nolint:exhaustive // editFieldCount is a sentinel
	case editFieldTitle:
		cmd = e.title.Focus()
		e.body.Blur()
	case editFieldBody:
		e.title.Blur()
		cmd = e.body.Focus()
	}
	return m, cmd
}

func (m editModel) updateFocused(msg tea.Msg) (tea.Model, tea.Cmd) {
	e := &m.entries[m.current]
	var cmd tea.Cmd
	switch e.focus { //nolint:exhaustive // editFieldCount is a sentinel
	case editFieldTitle:
		e.title, cmd = e.title.Update(msg)
	case editFieldBody:
		e.body, cmd = e.body.Update(msg)
	}
	return m, cmd
}

func (m editModel) View() tea.View {
	e := &m.entries[m.current]
	var b strings.Builder

	// Header: repo#123* (1/3) - yellow asterisk if edited.
	b.WriteString(m.styles.header.Render(e.ref))
	if e.title.Value() != e.origTitle || (e.bodyFetched && e.body.Value() != e.origBody) {
		b.WriteString(m.styles.dirty.Render("*"))
	}
	if len(m.entries) > 1 {
		b.WriteString(" ")
		b.WriteString(m.styles.counter.Render(
			fmt.Sprintf("(%d/%d)", m.current+1, len(m.entries)),
		))
	}
	b.WriteString("\n\n")

	// Title field
	titleLabel := m.styles.label
	if e.focus != editFieldTitle {
		titleLabel = m.styles.dimLabel
	}
	b.WriteString(titleLabel.Render("Title"))
	b.WriteString("\n")

	titleView := e.title.View()
	titleLines := strings.Count(titleView, "\n") + 1
	b.WriteString(titleView)
	b.WriteString("\n\n")

	// Body field
	bodyLabel := m.styles.label
	if e.focus != editFieldBody {
		bodyLabel = m.styles.dimLabel
	}
	b.WriteString(bodyLabel.Render("Body"))
	b.WriteString("\n")

	switch {
	case e.bodyFetching:
		b.WriteString(m.styles.blurredText.Render("Loading…"))
	case e.bodyFetchErr != nil:
		b.WriteString(m.styles.blurredText.Render(fmt.Sprintf("Error: %v", e.bodyFetchErr)))
	default:
		b.WriteString(e.body.View())
	}
	b.WriteString("\n\n")

	// Help
	b.WriteString(m.renderHelp())

	v := tea.NewView(b.String())

	// Attach the real cursor from the focused field.
	if !e.bodyFetching && e.bodyFetchErr == nil {
		switch e.focus { //nolint:exhaustive // editFieldCount is a sentinel
		case editFieldTitle:
			if cur := e.title.Cursor(); cur != nil {
				cur.Y += editTitleYOffset
				v.Cursor = cur
			}
		case editFieldBody:
			if cur := e.body.Cursor(); cur != nil {
				cur.Y += editBodyYOffset + titleLines
				v.Cursor = cur
			}
		}
	}

	return v
}

func (m editModel) renderHelp() string {
	pairs := []struct{ key, desc string }{
		{"tab", "switch field"},
		{"ctrl+s", "save all"},
		{"esc", "cancel"},
	}
	if len(m.entries) > 1 {
		pairs = append([]struct{ key, desc string }{
			{"ctrl+n", "next"},
			{"ctrl+p", "prev"},
		}, pairs...)
	}
	var parts []string
	for _, p := range pairs {
		parts = append(parts,
			m.styles.helpKey.Render(p.key)+" "+m.styles.helpText.Render(p.desc),
		)
	}
	return strings.Join(parts, m.styles.helpText.Render(" · "))
}

func (m editModel) results() []editResult {
	results := make([]editResult, len(m.entries))
	for i, e := range m.entries {
		title := e.title.Value()
		body := e.body.Value()
		results[i] = editResult{
			Ref:     e.ref,
			Title:   title,
			Body:    body,
			Changed: title != e.origTitle || body != e.origBody,
		}
	}
	return results
}

// runEditTUI launches the multi-PR edit TUI and returns results for all PRs.
func runEditTUI(prs []editPR, fetchBody bodyFetchFunc) ([]editResult, bool, error) {
	m := newEditModel(prs, fetchBody)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, false, fmt.Errorf("edit TUI: %w", err)
	}
	em, ok := final.(editModel)
	if !ok {
		return nil, false, fmt.Errorf("edit TUI: unexpected model type")
	}
	if em.aborted || !em.submitted {
		return nil, false, nil
	}
	return em.results(), true, nil
}
