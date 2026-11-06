package main

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/overlay"
	"github.com/stretchr/testify/require"
)

func filterClickTestModel() tuiModel {
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2), approvalTestPR(3))
	m.cfg = &Config{}
	m.width, m.height = 120, 30
	m.showOptions = true
	m.optionsPicker = m.newFilterPicker()
	return m
}

// Locate the exact displayed word in its labelled row, including repeated
// choices such as show/hide and the custom CI expression.
func filterChoiceButton(t *testing.T, m tuiModel, row filterRow, choice string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View().Content), nl) {
		if !slices.Contains(strings.Fields(line), filterOptionDefs[row].label) {
			continue
		}
		index := strings.Index(line, " "+choice+" ")
		require.NotEqual(t, -1, index, "choice is visible in its row")
		return ansi.StringWidth(line[:index+1]), y
	}
	t.Fatal("filter row is not visible")
	return 0, 0
}

func TestOptionsOverlayClickSelectsEveryChoice(t *testing.T) {
	for row, def := range filterOptionDefs {
		for choiceIndex, choice := range def.choices {
			t.Run(def.label+"/"+choice.label, func(t *testing.T) {
				m := filterClickTestModel()
				original := m.currentFilterValues()
				x, y := filterChoiceButton(t, m, filterRow(row), choice.label)
				for _, clickX := range []int{x, x + ansi.StringWidth(choice.label) - 1} {
					m.optionsPicker.IsReset[row] = true
					var cmd tea.Cmd
					m, cmd = updateModel(
						t,
						m,
						tea.MouseClickMsg{X: clickX, Y: y, Button: tea.MouseLeft},
					)
					require.Nil(t, cmd)
					require.True(t, m.showOptions)
					require.Equal(t, row, m.optionsPicker.Cursor)
					require.Equal(t, choiceIndex, m.optionsPicker.Values[row])
					require.False(t, m.optionsPicker.IsReset[row])
					require.Equal(
						t,
						original,
						m.currentFilterValues(),
						"clicks stage choices until apply",
					)
				}
			})
		}
	}
}

func TestOptionsOverlayClickChangesCLIRows(t *testing.T) {
	m := filterClickTestModel()
	m.cli.State, m.cli.stateExplicit = valueMerged, true
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Cursor = int(filterRowReview)
	x, y := filterChoiceButton(t, m, filterRowState, valueClosed)
	m, cmd := updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	require.Nil(t, cmd)
	require.Equal(
		t,
		filterChoiceIndex(filterRowState, valueClosed),
		m.optionsPicker.Values[filterRowState],
	)
	require.Equal(t, int(filterRowState), m.optionsPicker.Cursor)
	require.True(t, m.showOptions)
}

func TestOptionsOverlayClickCustomCIAtDifferentWidths(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := filterClickTestModel()
		m.width = width
		m.cli.CI = "pending,!success"
		m.optionsPicker = m.newFilterPicker()
		m.optionsPicker.Values[filterRowCI] = 0
		x, y := filterChoiceButton(t, m, filterRowCI, "pending,!success")
		m, cmd := updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		require.Nil(t, cmd)
		require.Equal(
			t,
			len(m.optionsPicker.Rows[filterRowCI].Choices)-1,
			m.optionsPicker.Values[filterRowCI],
		)
	}
}

func TestOptionsOverlayIgnoresOtherClicks(t *testing.T) {
	m := filterClickTestModel()
	m.cursor = 1
	m.optionsPicker.Cursor = int(filterRowReview)
	before := slices.Clone(m.optionsPicker.Values)
	x, y := filterChoiceButton(t, m, filterRowState, valueOpen)
	originX, originY := overlay.Origin(m.renderOptionsOverlay(), m.width, m.height, overlay.Center)
	for _, msg := range []tea.MouseClickMsg{
		{X: x, Y: y, Button: tea.MouseRight},
		{X: x + len(valueOpen), Y: y, Button: tea.MouseLeft},
		{X: originX, Y: originY, Button: tea.MouseLeft}, // the dialog's own border
	} {
		m, _ = updateModel(t, m, msg)
		require.Equal(t, before, m.optionsPicker.Values)
		require.Equal(t, int(filterRowReview), m.optionsPicker.Cursor)
		require.Equal(t, 1, m.cursor, "modal clicks do not reach the list underneath")
		require.True(t, m.showOptions)
	}
}

func TestOptionsOverlayClickedChoiceAppliesOrCancels(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := filterClickTestModel()
	x, y := filterChoiceButton(t, m, filterRowCI, ciStatusFailure)
	m, _ = updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m, cmd := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd, "CI filtering is local")
	require.False(t, m.showOptions)
	require.Equal(t, ciStatusFailure, m.cli.CI)
	st, err := loadTUIState()
	require.NoError(t, err)
	require.Equal(t, ciStatusFailure, st.Filters.CI)

	m = filterClickTestModel()
	x, y = filterChoiceButton(t, m, filterRowState, valueClosed)
	m, _ = updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m, cmd = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, cmd)
	require.False(t, m.showOptions)
	require.Equal(t, valueOpen, m.cli.State)
}

func TestOptionTagsUseDotDividers(t *testing.T) {
	m := filterClickTestModel()
	m.cli.NoBot = false
	m.cli.CI, m.cli.Review = ciStatusFailure, valueReviewFilterRequired
	text := "┤ ci:fail · review:required │ All Time ▾ │ All PRs ▾ ├─"
	expected := strings.Repeat("─", m.width-ansi.StringWidth(text)) + text
	require.Equal(t, expected, ansi.Strip(m.renderListSeparator()))
}

func TestOptionsOverlayOnlyEscDiscardsStagedChoices(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	clickTags := func(m tuiModel) tea.Msg {
		start, _ := m.filterTagsBounds()
		require.False(t, m.optionsDialogAt(start, m.listSeparatorY()), "tags are exposed")
		return tea.MouseClickMsg{X: start, Y: m.listSeparatorY(), Button: tea.MouseLeft}
	}
	for _, tc := range []struct {
		name    string
		msg     func(tuiModel) tea.Msg
		applied bool
	}{
		{"q", func(tuiModel) tea.Msg { return tea.KeyPressMsg{Code: 'q', Text: "q"} }, true},
		{"tags", clickTags, true},
		{"outside", func(tuiModel) tea.Msg {
			return tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}
		}, true},
		{"esc", func(tuiModel) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEscape} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := filterClickTestModel()
			x, y := filterChoiceButton(t, m, filterRowCI, ciStatusFailure)
			m, _ = updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			m, _ = updateModel(t, m, tc.msg(m))
			require.False(t, m.showOptions)
			want := ""
			if tc.applied {
				want = ciStatusFailure
			}
			require.Equal(t, want, m.cli.CI)
		})
	}
}

func TestOptionsOverlayCoversTagsUnderneath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	covered := 0
	for height := 12; height <= 22; height++ {
		m := filterClickTestModel()
		m.width, m.height, m.header = 80, height, "PR"
		m.cli.State, m.cli.stateExplicit = valueClosed, true
		m.optionsPicker = m.newFilterPicker()
		start, end := m.filterTagsBounds()
		y := m.listSeparatorY()
		for x := start; x < end; x++ {
			if !m.optionsDialogAt(x, y) {
				continue
			}
			if _, _, ok := m.filterChoiceAt(x, y); ok {
				continue
			}
			covered++
			after, _ := updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			require.True(
				t,
				after.showOptions,
				"height=%d x=%d hits the dialog, not the tags",
				height,
				x,
			)
		}
	}
	require.Positive(t, covered, "some heights put the dialog over the tags")
}
