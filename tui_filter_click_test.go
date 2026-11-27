package main

import (
	"image"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func filterClickTestModel() tuiModel {
	m := approvalTestModel("", approvalTestPR(1), approvalTestPR(2), approvalTestPR(3))
	m.cfg = &Config{}
	m.width, m.height = 120, 30
	m.openOptions()
	return m
}

// Locate the exact displayed word in its labelled row, including repeated
// choices such as show/hide and the custom CI expression.
func filterChoiceButton(
	t *testing.T,
	h *steep.Harness,
	row filterRow,
	choice string,
) steep.Location {
	t.Helper()
	label := reel.Contains(filterOptionDefs[row].label + " ")
	at := h.Locate(reel.After(label, reel.Contains(" "+choice+" "))).Offset(1, 0)
	require.Equal(t, h.Locate(label).Y, at.Y)
	return at
}

func waitFilterChoice(t *testing.T, h *steep.Harness, row filterRow, choice string) {
	t.Helper()
	at := filterChoiceButton(t, h, row, choice)
	waitCellStyle(t, h, at, func(style reel.CellStyle) bool { return style.Attrs&vt.AttrBold != 0 })
}

func waitFilterRow(h *steep.Harness, row filterRow) {
	h.WaitFor(reel.Matches(regexp.MustCompile("❯ +" + filterOptionDefs[row].label + "  ")))
}

func TestOptionsOverlayClickSelectsEveryChoice(t *testing.T) {
	m := filterClickTestModel()
	original := m.currentFilterValues()
	h := newTUIHarness(t, m)
	for row, def := range filterOptionDefs {
		for _, lastCell := range []bool{false, true} {
			for _, choice := range def.choices {
				at := filterChoiceButton(t, h, filterRow(row), choice.label)
				if lastCell {
					at = at.Offset(ansi.StringWidth(choice.label)-1, 0)
				}
				at.LeftClick()
				waitFilterRow(h, filterRow(row))
				waitFilterChoice(t, h, filterRow(row), choice.label)
			}
		}
	}
	h.KeyEsc().WaitNot("enter apply").Key("O").Wait("enter apply")
	for row, index := range original {
		waitFilterChoice(t, h, filterRow(row), filterOptionDefs[row].choices[index].label)
	}
}

func TestOptionsOverlayClickChangesCLIRows(t *testing.T) {
	m := filterClickTestModel()
	m.cli.State, m.cli.stateExplicit = valueMerged, true
	m.optionsPicker = m.newFilterPicker()
	m.optionsPicker.Cursor = int(filterRowReview)
	h := newTUIHarness(t, m)
	filterChoiceButton(t, h, filterRowState, valueClosed).LeftClick()
	waitFilterRow(h, filterRowState)
	waitFilterChoice(t, h, filterRowState, valueClosed)
}

func TestOptionsOverlayClickCustomCIAtDifferentWidths(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := filterClickTestModel()
			m.width = width
			m.cli.CI = "pending,!success"
			m.optionsPicker = m.newFilterPicker()
			m.optionsPicker.Values[filterRowCI] = 0
			h := newTUIHarness(t, m)
			filterChoiceButton(t, h, filterRowCI, m.cli.CI).LeftClick()
			waitFilterRow(h, filterRowCI)
			waitFilterChoice(t, h, filterRowCI, m.cli.CI)
		})
	}
}

func TestOptionsOverlayIgnoresOtherClicks(t *testing.T) {
	m := filterClickTestModel()
	m.cursor = 1
	m.optionsPicker.Cursor = int(filterRowReview)
	h := newTUIHarness(t, m)
	open := filterChoiceButton(t, h, filterRowState, valueOpen)
	h.RightClick(open.X, open.Y)
	open.Offset(len(valueOpen), 0).LeftClick()
	h.Locate(reel.Contains("╭")).LeftClick()
	h.KeyLeft()
	waitFilterRow(h, filterRowReview)
	waitFilterChoice(t, h, filterRowReview, valueReviewFilterNone)
	waitFilterChoice(t, h, filterRowState, valueOpen)
	h.KeyEsc().WaitNot("enter apply").Assert("❯ 2")
}

func TestOptionsOverlayClickedChoiceAppliesOrCancels(t *testing.T) {
	h := newTUIHarness(t, filterClickTestModel())
	filterChoiceButton(t, h, filterRowCI, ciStatusFailure).LeftClick().Key("enter").
		WaitNot("enter apply").Assert("ci:fail")
	st, err := loadTUIState()
	require.NoError(t, err)
	require.Equal(t, ciStatusFailure, st.Filters.CI)

	h.Key("O").Wait("enter apply")
	filterChoiceButton(
		t,
		h,
		filterRowState,
		valueClosed,
	).LeftClick().
		KeyEsc().
		WaitNot("enter apply")
	h.Key("O").Wait("enter apply")
	waitFilterChoice(t, h, filterRowState, valueOpen)
}

func TestOptionTagsUseDotDividers(t *testing.T) {
	m := filterClickTestModel()
	m.cli.NoBot = false
	m.cli.CI, m.cli.Review = ciStatusFailure, valueReviewFilterRequired
	text := "┤ ci:fail · review:required │ All Time ▾ │ All PRs ▾ ├─"
	expected := strings.Repeat("─", m.width-ansi.StringWidth(text)) + text
	require.Equal(t, expected, testListSeparator(&m))
}

func TestOptionsOverlayOnlyEscDiscardsStagedChoices(t *testing.T) {
	for _, exit := range []string{"q", "O", "tags", "outside", "esc"} {
		t.Run(exit, func(t *testing.T) {
			h := newTUIHarness(t, filterClickTestModel())
			filterChoiceButton(t, h, filterRowCI, ciStatusFailure).LeftClick()
			switch exit {
			case "tags":
				h.Locate(reel.Contains("bots:hide")).LeftClick()
			case "outside":
				h.LeftClick(0, 0)
			default:
				h.Key(exit)
			}
			h.WaitNot("enter apply")
			if exit == "esc" {
				h.AssertNot("ci:fail")
			} else {
				h.Assert("ci:fail")
			}
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
		m.View()
		rect, ok := m.screen.root.Frame().Rect(optionsModalID)
		require.True(t, ok)
		start, end := testControlBounds(m, filterTagsID)
		y := m.listSeparatorY()
		for x := start; x < end; x++ {
			if !image.Pt(x, y).In(rect) {
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
