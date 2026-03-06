package main

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/gechr/clib/prompt"
	"github.com/gechr/clog"
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

// editTheme returns a huh theme for the edit form.
// Field titles are pink; the editable text is green when focused.
func editTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Base = lipgloss.NewStyle()
	t.Blurred.Base = lipgloss.NewStyle()

	// Field titles: pink (212).
	t.Focused.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("212")).
		Bold(true)
	t.Blurred.Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("242"))

	// Editable text.
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().Reverse(true)
	t.Focused.TextInput.Text = lipgloss.NewStyle().Foreground(lipgloss.Color("48"))
	t.Blurred.TextInput.Cursor = lipgloss.NewStyle().Reverse(true)
	t.Blurred.TextInput.Text = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))

	return t
}

// editHeaderStyle is the style for the PR ref header in the edit form.
var editHeaderStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("208")).
	Bold(true)

// interactiveEdit presents a sequential edit form for each PR's title and body.
// For each PR it fetches the body on demand, shows a huh form, and PATCHes if changed.
func interactiveEdit(actions *ActionRunner, prs []PullRequest) error {
	theme := editTheme()

	for _, pr := range prs {
		owner, repo := prOwnerRepo(pr)

		body, err := actions.fetchPRBody(owner, repo, pr.Number)
		if err != nil {
			return fmt.Errorf("fetching body for %s: %w", pr.URL, err)
		}

		title := pr.Title
		origTitle := title
		origBody := body

		fmt.Println(editHeaderStyle.Render(pr.Ref()))

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Title").
					Prompt("").
					Value(&title).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("title cannot be empty")
						}
						return nil
					}),
				huh.NewText().
					Title("Body").
					Value(&body).
					Lines(editBodyLines),
			),
		).WithTheme(theme)

		if err := form.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil
			}
			return fmt.Errorf("edit form for %s: %w", pr.URL, err)
		}

		if title == origTitle && body == origBody {
			clog.Debug().
				Link("pr", pr.URL, pr.Ref()).
				Msg("No changes")
			continue
		}

		if err := actions.updatePR(owner, repo, pr.Number, title, body); err != nil {
			return fmt.Errorf("updating %s: %w", pr.URL, err)
		}
		clog.Info().
			Link("pr", pr.URL, pr.Ref()).
			Str("title", truncateTitle(title)).
			Msg("Updated")
	}
	return nil
}
