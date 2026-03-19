package main

import (
	"fmt"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/clog"
	"github.com/gechr/prl/internal/prompt"
)

// prlHuhTheme implements huh.Theme with prl's custom styling.
type prlHuhTheme struct{}

func (prlHuhTheme) Theme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)

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
			Display:  row.Display,
			Value:    row,
			Selected: true,
		}
	}

	return prompt.MultiSelect(header, items, prlHuhTheme{}, maxSelectHeight, true)
}

// interactiveEdit presents an edit TUI for the selected PRs with ctrl+n/ctrl+p navigation.
// Bodies are fetched lazily. Changes are only submitted on ctrl+s.
func interactiveEdit(actions *ActionRunner, prs []PullRequest) error {
	editPRs := make([]editPR, len(prs))
	for i, pr := range prs {
		editPRs[i] = editPR{Ref: pr.Ref(), Title: pr.Title}
	}

	fetchBody := func(index int) (string, error) {
		pr := prs[index]
		owner, repo := prOwnerRepo(pr)
		return actions.fetchPRBody(owner, repo, pr.Number)
	}

	results, submitted, err := runEditTUI(editPRs, fetchBody)
	if err != nil {
		return err
	}
	if !submitted {
		return nil
	}

	for i, result := range results {
		pr := prs[i]
		if !result.Changed {
			clog.Debug().
				Link("pr", pr.URL, pr.Ref()).
				Msg("No changes")
			continue
		}

		owner, repo := prOwnerRepo(pr)
		if err := actions.updatePR(owner, repo, pr.Number, result.Title, result.Body); err != nil {
			return fmt.Errorf("updating %s: %w", pr.URL, err)
		}
		clog.Info().
			Link("pr", pr.URL, pr.Ref()).
			Str("title", truncateTitle(result.Title)).
			Msg("Updated")
	}
	return nil
}
