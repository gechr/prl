package prompt

import (
	"errors"
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/huh/v2"
)

// ErrCancelled is returned when the user cancels an interactive selection.
var ErrCancelled = errors.New("cancelled")

// SelectItem pairs a display string with a value of type T.
type SelectItem[T any] struct {
	Display  string
	Value    T
	Selected bool
}

// selectPadding is the extra rows the huh multi-select adds for chrome (title row).
// Help text is rendered by the form outside the field, so it is not included here.
const selectPadding = 1

// selectHeight returns the clamped height for the multi-select widget.
func selectHeight(maxHeight, itemCount int) int {
	return min(maxHeight, itemCount+selectPadding)
}

// buildOptions converts a slice of SelectItem into huh.Option values
// whose underlying value is the item's index.
func buildOptions[T any](items []SelectItem[T]) []huh.Option[int] {
	opts := make([]huh.Option[int], len(items))
	for i, item := range items {
		opt := huh.NewOption(item.Display, i)
		if item.Selected {
			opt = opt.Selected(true)
		}
		opts[i] = opt
	}
	return opts
}

// collectValues maps selected indices back to item values.
func collectValues[T any](indices []int, items []SelectItem[T]) []T {
	result := make([]T, len(indices))
	for i, idx := range indices {
		result[i] = items[idx].Value
	}
	return result
}

// MultiSelect presents a multi-select UI and returns the selected values.
// Returns ErrCancelled if the user cancels.
func MultiSelect[T any](
	title string,
	items []SelectItem[T],
	theme huh.Theme,
	maxHeight int,
	showHelp bool,
) ([]T, error) {
	if len(items) == 0 {
		return nil, nil
	}

	opts := buildOptions(items)

	var selected []int

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[int]().
				Title(title).
				Options(opts...).
				Value(&selected).
				Filterable(false).
				Height(selectHeight(maxHeight, len(items))),
		),
	)

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit"))
	km.MultiSelect.Toggle = key.NewBinding(
		key.WithKeys("space", "x"),
		key.WithHelp("space", "toggle"),
	)

	form = form.WithTheme(theme).WithShowHelp(showHelp).WithKeyMap(km)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, fmt.Errorf("%w: %w", ErrCancelled, err)
		}
		return nil, err
	}

	return collectValues(selected, items), nil
}
