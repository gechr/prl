package main

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/gechr/clib/prompt"
)

// prlTheme returns a huh theme matching the original prl gum choose styling.
func prlTheme() *huh.Theme {
	t := huh.ThemeBase()

	// Remove the thick left border that ThemeBase adds.
	t.Focused.Base = lipgloss.NewStyle()
	t.Blurred.Base = lipgloss.NewStyle()

	// Header: orange bold (--header.foreground=208 --header.bold).
	t.Focused.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("208")).
		Bold(true)

	// Cursor: ❯ in hot pink (--cursor='❯ ' --cursor.foreground=212).
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().
		SetString("❯ ").
		Foreground(lipgloss.Color("212"))

	// Unselected prefix: ○ (--unselected-prefix='○ ' --cursor-prefix='○ ').
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().
		SetString("○ ")

	// Selected prefix: ● in spring green (--selected-prefix='● ').
	t.Focused.SelectedPrefix = lipgloss.NewStyle().
		SetString("● ").
		Foreground(lipgloss.Color("48"))

	// Selected option text: spring green (--selected.foreground=48).
	t.Focused.SelectedOption = lipgloss.NewStyle().
		Foreground(lipgloss.Color("48"))

	return t
}

// interactiveSelect presents a multi-select UI for PR selection.
// Returns selected TableRows, or prompt.ErrCancelled if user cancels.
func interactiveSelect(rows []TableRow, header string) ([]TableRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	items := make([]prompt.SelectItem[TableRow], len(rows))
	for i, row := range rows {
		items[i] = prompt.SelectItem[TableRow]{
			Display: row.Display,
			Value:   row,
		}
	}

	return prompt.MultiSelect(header, items, prlTheme(), maxSelectHeight)
}
