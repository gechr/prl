package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestDesktopActionFailures(t *testing.T) {
	fakeDesktopCommands(t)
	t.Setenv("PRL_TEST_FAIL", "1")
	for _, view := range []struct {
		name string
		kind tuiView
	}{
		{name: "list", kind: tuiViewList},
		{name: "detail", kind: tuiViewDetail},
		{name: "diff", kind: tuiViewDiff},
	} {
		for _, action := range []struct {
			name  string
			label string
			key   tea.KeyPressMsg
		}{
			{"open", "Open", tea.KeyPressMsg{Code: 'o', Text: "o"}},
			{"copy", "Copy", tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}},
		} {
			t.Run(view.name+"/"+action.name, func(t *testing.T) {
				pr := testReviewPullRequest()
				key := makePRKey(pr)
				m := tuiModel{
					view:      view.kind,
					items:     []PRRowModel{{PR: pr}},
					rows:      []TableRow{{Item: PRRowModel{PR: pr}}},
					removed:   make(prKeys),
					selected:  prKeys{key: true},
					detailKey: key,
					diffKey:   key,
					styles:    newTuiStyles(),
				}
				var model tea.Model
				var handled bool
				if view.kind == tuiViewList {
					model, _, handled = m.updateListActions(action.key)
				} else {
					model, _, handled = m.handleViewAction(action.key)
				}
				require.True(t, handled)
				result, ok := model.(tuiModel)
				require.True(t, ok)
				require.True(t, result.flash.Err, "failed desktop actions must not report success")
				require.Equal(t, fmt.Sprintf(
					"%s failed: %s: exit status 7: desktop action failed",
					action.label,
					filepath.Join(os.Getenv("PATH"), "powershell.exe"),
				), result.flash.Msg)
				require.Equal(t, prKeys{key: true}, result.selected, "retain selection for retry")
			})
		}
	}
}
