package main

import (
	"strings"
	"testing"

	"bloomfx.dev/bloom"
	tea "charm.land/bubbletea/v2"
	"getsteep.dev/steep/reel"
	"getsteep.dev/steep/reel/vt"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestHelpSheetScrollsAndRefits(t *testing.T) {
	m := numberedListTestModel(3)
	m.width, m.height = 60, 12
	h := newTUIHarness(t, m)
	const controls = "↑/↓ scroll · Esc close"
	h.Key("?").Wait(tuiDescNavigate).Assert(controls).AssertNot(tuiDescHelp)

	first := h.Locate(reel.Contains(tuiDescNavigate))
	first.Wheel(vt.MouseWheelDown).WaitNot(tuiDescNavigate)
	h.Key("home").Wait(tuiDescNavigate).KeyDown().WaitNot(tuiDescNavigate)
	h.Key("end").Wait(tuiDescHelp).Assert(controls)
	h.Key("pgup").WaitNot(tuiDescHelp)
	h.Key("home").Wait(tuiDescNavigate)
	top := h.Locate(reel.Contains("█"))
	bottom := h.Locate(reel.Nth(-1, reel.Contains("┃")))
	top.LeftDrag(bottom).Wait(tuiDescHelp)
	h.KeyEsc().WaitNot(controls).Assert("❯ 1")
	h.Key("?").Wait(tuiDescNavigate).AssertNot(tuiDescHelp)

	h.Resize(130, 42).Wait("Press any key to dismiss").Assert(tuiDescHelp)
	first = h.Locate(reel.Contains(tuiDescNavigate))
	require.Greater(t, h.Locate(reel.Contains(tuiDescHelp)).X, first.X)
	h.Resize(60, 12).Wait(controls).Key("a").WaitNot(controls).Assert("❯ 1")
}

func TestHelpSheetWrapsEveryShortcut(t *testing.T) {
	m := numberedListTestModel(1)
	m.width, m.height = 23, 1
	screen := m.ensureScreen()
	m.help = &helpSheet{}
	var rows strings.Builder
	for {
		page := bloom.Overlay(nil, m.helpLayer())
		line := ansi.Strip(screen.root.Render(m.width, m.height, page))
		rows.WriteString(strings.TrimRight(line, " ┃█"))
		offset := m.help.offset
		m = m.updateHelp(tea.KeyPressMsg{Code: tea.KeyDown})
		if m.help.offset == offset {
			break
		}
	}
	var bindings strings.Builder
	for _, hint := range helpShortcuts() {
		bindings.WriteString(hint.Key + hint.Desc)
	}
	require.Equal(
		t,
		strings.Join(strings.Fields(bindings.String()), ""),
		strings.Join(strings.Fields(rows.String()), ""),
	)
}

func TestHelpSheetIgnoresKeyRelease(t *testing.T) {
	m := numberedListTestModel(1)
	help := tea.KeyPressMsg{Code: '?', Text: "?"}
	m, _ = updateModel(t, m, help)
	m, _ = updateModel(t, m, tea.KeyReleaseMsg(help))
	require.NotNil(t, m.help)
	approve := tea.KeyPressMsg{Code: 'a', Text: "a"}
	m, _ = updateModel(t, m, approve)
	m, _ = updateModel(t, m, tea.KeyReleaseMsg(approve))
	require.Nil(t, m.help)
	require.Empty(t, m.confirmAction)
}
