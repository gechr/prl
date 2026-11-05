package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/stretchr/testify/require"
)

func helpClickTestModel(count int) tuiModel {
	prs := make([]PullRequest, count)
	for i := range prs {
		prs[i] = approvalTestPR(i + 1)
	}
	m := approvalTestModel("", prs...)
	m.cfg = &Config{}
	m.header = "PR"
	return m
}

// footerLines returns the plain help lines and the screen row of the first.
func footerLines(m tuiModel) ([]string, int) {
	top := m.listSeparatorY() + 1
	return strings.Split(ansi.Strip(m.View().Content), nl)[top:], top
}

// Clicking anywhere on a "key desc" hint acts like pressing its key.
func TestHelpHintClickPressesItsKey(t *testing.T) {
	for _, tc := range []struct {
		label string
		check func(tuiModel) bool
	}{
		{"Options", func(m tuiModel) bool { return m.showOptions }},
		{"? " + tuiHelpHelp, func(m tuiModel) bool { return m.showHelp }},
	} {
		t.Run(tc.label, func(t *testing.T) {
			m := helpClickTestModel(3)
			lines, top := footerLines(m)
			y, start := -1, 0
			for i, line := range lines {
				if before, _, ok := strings.Cut(line, tc.label); ok {
					y, start = top+i, ansi.StringWidth(before)
					break
				}
			}
			require.NotEqual(t, -1, y, "hint is drawn")
			for _, x := range []int{start, start + ansi.StringWidth(tc.label) - 1} {
				after, _ := updateModel(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				require.True(t, tc.check(after), "x=%d", x)
				require.Equal(t, 0, after.cursor)
			}
		})
	}
}

func TestHelpHintsDroppedForStatusAreNotButtons(t *testing.T) {
	// 30 rows overflow the viewport, so the footer shows a scroll position
	// that can push trailing hints off the help line.
	m := helpClickTestModel(30)
	m.height = 12
	dropped := 0
	// 47, 72 and 140 drop a hint for the status, 80 and 120 do not.
	for _, width := range []int{47, 72, 80, 120, 140} {
		m.width = width
		lines, top := footerLines(m)
		footer := strings.Join(lines, nl)
		for _, hint := range m.listHelpPairs() {
			if !strings.Contains(footer, helpHintText(m, hint)) {
				dropped++
			}
		}
		for i, line := range lines {
			for x := range width {
				hint, ok := m.listHelpHintAt(x, top+i)
				if !ok {
					continue
				}
				// The clicked cell must sit inside the hint's drawn text.
				text := helpHintText(m, hint)
				idx := strings.Index(line, text)
				require.NotEqual(t, -1, idx, "width=%d x=%d hits undrawn %q", width, x, hint.Key)
				start := ansi.StringWidth(line[:idx])
				require.GreaterOrEqual(t, x, start, "width=%d x=%d", width, x)
				require.Less(t, x, start+ansi.StringWidth(text), "width=%d x=%d", width, x)
			}
		}
	}
	require.Positive(t, dropped, "some width drops a hint for the status")
}

// helpHintText is the plain "key desc" text the help bar draws for hint.
func helpHintText(m tuiModel, hint key.Hint) string {
	return strings.TrimPrefix(ansi.Strip(m.helpBar([]key.Hint{hint}).Render()), " ")
}

// Status text that happens to start where a dropped hint sat must not act as
// that hint, even when it starts with the hint's key.
func TestHelpStatusCannotImpersonateDroppedHint(t *testing.T) {
	m := helpClickTestModel(30)
	m.width, m.height = 80, 20
	plain := strings.Split(ansi.Strip(m.helpBar(m.listHelpPairs()).Render()), nl)
	last := plain[len(plain)-1]
	before, _, ok := strings.Cut(last, tuiHelpQuit)
	require.True(t, ok, "quit is the last hint")
	x := ansi.StringWidth(before)
	m.flash.Msg = "q" + strings.Repeat("x", m.width-2-x)
	y := m.listSeparatorY() + len(plain)
	require.Equal(t, "q", ansi.Cut(screenLine(m, y), x, x+1), "status sits where quit was")
	_, hit := m.listHelpHintAt(x, y)
	require.False(t, hit)
}

// screenLine returns the plain text of screen row y.
func screenLine(m tuiModel, y int) string {
	return strings.Split(ansi.Strip(m.View().Content), nl)[y]
}
