package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/stretchr/testify/require"
)

func TestCurrentAIReviewLauncherHerdr(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WT_SESSION", "")
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr not in PATH")
	}

	t.Run("herdr session wins over the host emulator", func(t *testing.T) {
		t.Setenv(herdrEnvVar, "1")
		t.Setenv("KITTY_WINDOW_ID", "1")
		t.Setenv("TERM_PROGRAM", "ghostty")
		require.Equal(t, aiReviewLauncherHerdr, currentAIReviewLauncher())
	})

	t.Run("unset herdr env falls through", func(t *testing.T) {
		t.Setenv(herdrEnvVar, "")
		t.Setenv("KITTY_WINDOW_ID", "")
		t.Setenv("TERM_PROGRAM", "Apple_Terminal")
		require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
	})
}

func TestCurrentAIReviewLauncher(t *testing.T) {
	t.Setenv(herdrEnvVar, "")
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WT_SESSION", "")

	if !xos.IsDarwin() {
		t.Run("non-darwin always returns none", func(t *testing.T) {
			t.Setenv("KITTY_WINDOW_ID", "1")
			t.Setenv("TERM_PROGRAM", "ghostty")
			require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
		})
		return
	}

	t.Run("kitty via KITTY_WINDOW_ID", func(t *testing.T) {
		if _, err := exec.LookPath("kitty"); err != nil {
			t.Skip("kitty not in PATH")
		}
		t.Setenv("KITTY_WINDOW_ID", "1")
		t.Setenv("TERM_PROGRAM", "")
		require.Equal(t, aiReviewLauncherKitty, currentAIReviewLauncher())
	})
	t.Run("kitty takes precedence over TERM_PROGRAM", func(t *testing.T) {
		if _, err := exec.LookPath("kitty"); err != nil {
			t.Skip("kitty not in PATH")
		}
		t.Setenv("KITTY_WINDOW_ID", "2")
		t.Setenv("TERM_PROGRAM", "ghostty")
		require.Equal(t, aiReviewLauncherKitty, currentAIReviewLauncher())
	})

	t.Setenv("KITTY_WINDOW_ID", "") // ensure kitty not detected for remaining cases
	t.Setenv("TERM_PROGRAM", "ghostty")
	require.Equal(t, aiReviewLauncherGhostty, currentAIReviewLauncher())

	t.Setenv("TERM_PROGRAM", "iTerm.app")
	require.Equal(t, aiReviewLauncherITerm2, currentAIReviewLauncher())

	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
}

func TestCurrentAIReviewLauncherWSL(t *testing.T) {
	if !xos.IsLinux() {
		t.Skip("WSL detection requires Linux")
	}

	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	t.Setenv(herdrEnvVar, "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WT_SESSION", "test-session")

	t.Run("detection does not require Windows executables on PATH", func(t *testing.T) {
		require.Equal(t, aiReviewLauncherWindowsTerminal, currentAIReviewLauncher())
	})
	t.Run("WSL without Windows Terminal", func(t *testing.T) {
		t.Setenv("WT_SESSION", "")
		require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
	})
	t.Run("Windows Terminal without WSL", func(t *testing.T) {
		t.Setenv("WSL_DISTRO_NAME", "")
		require.Equal(t, aiReviewLauncherNone, currentAIReviewLauncher())
	})
	t.Run("Herdr takes precedence", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(binDir, "herdr"), []byte("#!/bin/sh\nexit 0\n"), 0o700))
		t.Setenv(herdrEnvVar, "1")
		require.Equal(t, aiReviewLauncherHerdr, currentAIReviewLauncher())
	})
}

func TestLaunchAIReviewWindowsTerminal(t *testing.T) {
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "cmd.exe"), []byte(`#!/bin/sh
printf '%s\000' "$@" > "$PRL_TEST_ARGS"
if [ "$PRL_TEST_FAIL" = 1 ]; then
  echo 'wt.exe unavailable' >&2
  exit 7
fi
`), 0o700))
	t.Setenv("PATH", binDir)
	t.Setenv("PRL_TEST_ARGS", argsFile)
	t.Setenv("PRL_TEST_FAIL", "")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu Dev")
	currentUser, err := user.Current()
	require.NoError(t, err)

	t.Run("direct wt without cmd", func(t *testing.T) {
		directDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(directDir, "wt.exe"), []byte(`#!/bin/sh
printf '%s\000' "$@" > "$PRL_TEST_ARGS"
`), 0o700))
		t.Setenv("PATH", directDir)
		err := launchAIReviewWindowsTerminal(t.Context(), "/tmp/review files/launch.sh", "repo#42")
		require.NoError(t, err)
		data, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		require.Equal(t, []string{
			"--window", "0", "new-tab", "--title", "repo#42",
			"wsl.exe", "--distribution", "Ubuntu Dev", "--user", currentUser.Username,
			"--exec", "/bin/sh", "/tmp/review files/launch.sh",
		}, strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"))
	})
	t.Run("unexecutable wt alias falls back to cmd", func(t *testing.T) {
		aliasDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(aliasDir, "wt.exe"), []byte("not an executable"), 0o700))
		t.Setenv("PATH", aliasDir+string(os.PathListSeparator)+binDir)
		require.NoError(t, launchAIReviewWindowsTerminal(t.Context(), "/tmp/launch.sh", "repo#42"))
		data, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(data), "/d\x00/v:off\x00/c\x00wt.exe\x00"))
	})
	t.Run("direct wt preserves cmd metacharacters as literal arguments", func(t *testing.T) {
		directDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(directDir, "wt.exe"), []byte(`#!/bin/sh
printf '%s\000' "$@" > "$PRL_TEST_ARGS"
`), 0o700))
		t.Setenv("PATH", directDir)
		const launchFile = `/tmp/review & (draft) %TEMP% ^|<>"!.sh`
		err := launchAIReviewWindowsTerminal(t.Context(), launchFile, "repo#42")
		require.NoError(t, err)
		data, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
		require.Equal(t, launchFile, args[len(args)-1])

		for _, separator := range []string{";", "\r", "\n", "\x00"} {
			t.Run(fmt.Sprintf("reject separator %q", separator), func(t *testing.T) {
				captureFile := filepath.Join(t.TempDir(), "args")
				t.Setenv("PRL_TEST_ARGS", captureFile)
				path := "/tmp/review" + separator + ".sh"
				err := launchAIReviewWindowsTerminal(t.Context(), path, "repo#42")
				require.EqualError(t, err,
					fmt.Sprintf("windows terminal: unsupported command character in argument %q", path))
				require.NoFileExists(t, captureFile)
			})
		}
	})
	t.Run("wt failure does not launch a duplicate through cmd", func(t *testing.T) {
		directDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(directDir, "wt.exe"), []byte("#!/bin/sh\necho 'direct failure' >&2\nexit 8\n"), 0o700))
		t.Setenv("PATH", directDir+string(os.PathListSeparator)+binDir)
		captureFile := filepath.Join(t.TempDir(), "args")
		t.Setenv("PRL_TEST_ARGS", captureFile)
		err := launchAIReviewWindowsTerminal(t.Context(), "/tmp/launch.sh", "repo#42")
		require.EqualError(t, err, "windows terminal: exit status 8: direct failure")
		require.NoFileExists(t, captureFile)
	})

	t.Run("same distro and user with Linux script path", func(t *testing.T) {
		err := launchAIReviewWindowsTerminal(t.Context(), "/tmp/review files/launch.sh", "repo#42")
		require.NoError(t, err)
		data, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		require.Equal(t, []string{
			"/d", "/v:off", "/c", "wt.exe", "--window", "0", "new-tab", "--title", "repo#42",
			"wsl.exe", "--distribution", "Ubuntu Dev", "--user", currentUser.Username,
			"--exec", "/bin/sh", "/tmp/review files/launch.sh",
		}, strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"))
	})
	t.Run("launcher failure reports stderr", func(t *testing.T) {
		t.Setenv("PRL_TEST_FAIL", "1")
		err := launchAIReviewWindowsTerminal(t.Context(), "/tmp/launch.sh", "repo#42")
		require.EqualError(t, err, "windows terminal: exit status 7: wt.exe unavailable")
	})
	t.Run("reject Windows command expansion", func(t *testing.T) {
		values := []string{
			"a&b", "a|b", "a<b", "a>b", "a^b", "a;b", "a(b", "a)b",
			`a"b`, "%TEMP%", "a\nb", "a\rb", "a\x00b",
		}
		for _, value := range values {
			t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
				captureFile := filepath.Join(t.TempDir(), "args")
				t.Setenv("PRL_TEST_ARGS", captureFile)
				t.Cleanup(func() { require.NoFileExists(t, captureFile) })
				require.EqualError(t,
					launchAIReviewWindowsTerminal(t.Context(), "/tmp/"+value, "repo#42"),
					fmt.Sprintf("windows terminal: unsupported command character in argument %q", "/tmp/"+value))
				require.EqualError(t,
					launchAIReviewWindowsTerminal(t.Context(), "/tmp/launch.sh", value),
					fmt.Sprintf("windows terminal: unsupported command character in argument %q", value))
				if strings.ContainsRune(value, '\x00') {
					return // Environment variables cannot contain NUL.
				}
				t.Setenv("WSL_DISTRO_NAME", value)
				require.EqualError(t,
					launchAIReviewWindowsTerminal(t.Context(), "/tmp/launch.sh", "repo#42"),
					fmt.Sprintf("windows terminal: unsupported command character in argument %q", value))
			})
		}
	})
}

func TestFindWSLCommandInterpreter(t *testing.T) {
	for _, tt := range []struct {
		name      string
		available string
		wantCalls []string
	}{
		{
			name: "PATH takes precedence", available: "cmd.exe",
			wantCalls: []string{"cmd.exe"},
		},
		{
			name: "default Windows mount without PATH", available: "/mnt/c/Windows/System32/cmd.exe",
			wantCalls: []string{"cmd.exe", "/mnt/c/Windows/System32/cmd.exe"},
		},
		{
			name:      "neither available",
			wantCalls: []string{"cmd.exe", "/mnt/c/Windows/System32/cmd.exe"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			interpreter, err := findWSLCommandInterpreter(func(name string) (string, error) {
				calls = append(calls, name)
				if name == tt.available {
					return name, nil
				}
				return "", exec.ErrNotFound
			})
			if tt.available == "" {
				require.ErrorIs(t, err, exec.ErrNotFound)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.available, interpreter)
			require.Equal(t, tt.wantCalls, calls)
		})
	}
}

func TestBuildWSLReviewCommand(t *testing.T) {
	shellPath := filepath.Join(t.TempDir(), "login shell")
	require.NoError(t, os.WriteFile(shellPath, []byte(`#!/bin/sh
test "$1" = -ilc || exit 12
export PRL_TEST_LOGIN=loaded
exec /bin/sh -c "$2"
`), 0o700))
	t.Setenv(shell.EnvShell, shellPath)
	t.Setenv("PRL_TEST_LOGIN", "")
	// Exercise nested quoting with spaces, quotes, metacharacters, and newlines.
	want := "it's a \"review\"; $HOME & %PATH%\nsecond line"
	command := `test "$PRL_TEST_LOGIN" = loaded && printf '%s' ` + shell.Quote(want)
	output, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", buildWSLReviewCommand(command)).CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Equal(t, want, string(output))

	t.Run("shell fallback", func(t *testing.T) {
		t.Setenv(shell.EnvShell, "")
		require.Equal(t, "/bin/sh -ilc "+shell.Quote("/bin/sh -c "+shell.Quote("true")), buildWSLReviewCommand("true"))
	})
}

func TestLaunchAIReviewWSLCleansFilesOnFailure(t *testing.T) {
	if !xos.IsLinux() {
		t.Skip("WSL detection requires Linux")
	}
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "cmd.exe"), []byte("#!/bin/sh\nexit 7\n"), 0o700))
	t.Setenv("PATH", binDir)
	t.Setenv(herdrEnvVar, "")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WT_SESSION", "test-session")
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)

	err := launchAIReview(testReviewPullRequest(), "test prompt", nil, reviewProviderCodex, "", "")
	require.EqualError(t, err, "windows terminal: exit status 7: ")
	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestBuildAIReviewAppleScriptGhosttyUsesNewTab(t *testing.T) {
	script, err := buildAIReviewAppleScript(aiReviewLauncherGhostty)

	require.NoError(t, err)
	require.Equal(t, `on run argv
	set shellCmd to item 1 of argv
	tell application "Ghostty"
	tell application "System Events" to tell process "Ghostty" to set frontmost to true
	set cfg to new surface configuration
	set initial input of cfg to shellCmd
	new tab in front window with configuration cfg
	end tell
end run`, script)
}

func TestBuildAIReviewAppleScriptITerm2UsesNewTab(t *testing.T) {
	script, err := buildAIReviewAppleScript(aiReviewLauncherITerm2)

	require.NoError(t, err)
	require.Equal(t, `on run argv
	set shellCmd to item 1 of argv
	tell application "iTerm2"
	activate
	tell current window
		set newTab to (create tab with default profile)
		tell current session of newTab
			write text " " & shellCmd
		end tell
	end tell
	end tell
end run`, script)
}

func TestBuildAIReviewAppleScriptUnsupported(t *testing.T) {
	_, err := buildAIReviewAppleScript(aiReviewLauncherNone)

	require.EqualError(t, err, `unsupported terminal ""`)
}

func TestPrepareAIReviewConfirmUsesYesNo(t *testing.T) {
	pr := testReviewPullRequest()
	m := tuiModel{}

	m = m.prepareAIReviewConfirm(pr, 0)

	require.Equal(t, "review", m.confirmAction)
	require.NotNil(t, m.confirmCmdFn)
	require.True(t, m.confirmHasInput)
	require.Equal(t, "Prompt", m.confirmInputLabel)
	require.Len(t, m.confirmOptions, 3)
	require.Equal(t, reviewProviderOptionLabel, m.confirmOptions[0].label)
	require.Equal(t, reviewModelOptionLabel, m.confirmOptions[1].label)
	require.Equal(t, reviewEffortOptionLabel, m.confirmOptions[2].label)
	require.Equal(t, string(defaultReviewProvider), m.selectedConfirmOptionValue(0))
	require.Equal(
		t,
		defaultReviewModel(nil, defaultReviewProvider),
		m.selectedConfirmOptionValue(1),
	)
	require.Equal(
		t,
		defaultReviewEffort(
			nil,
			defaultReviewProvider,
			defaultReviewModel(nil, defaultReviewProvider),
		),
		m.selectedConfirmOptionValue(2),
	)
	require.Equal(t, reviewPrompt(pr, nil, defaultReviewProvider), m.confirmInputValue)
}

func TestClaudeReviewDefaultsUseOpusHighAndIncludeFable(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: claudeReviewModelSonnet, value: claudeReviewModelSonnet},
			{label: claudeReviewModelOpus, value: claudeReviewModelOpus},
			{label: claudeReviewModelFable, value: claudeReviewModelFable},
		},
		reviewModelChoices(nil, reviewProviderClaude),
	)
	require.Equal(t, claudeReviewModelOpus, defaultReviewModel(nil, reviewProviderClaude))
	require.Equal(
		t,
		claudeReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderClaude, claudeReviewModelOpus),
	)
}

func TestBuildAIReviewCommandUsesSelectedModel(t *testing.T) {
	pr := testReviewPullRequest()
	const promptFile = "/tmp/prl-prompt.txt"
	promptExpr := fmt.Sprintf(`"$(/bin/cat %s)"`, shell.Quote(promptFile))
	cleanup := fmt.Sprintf("; rm -f %s", shell.Quote(promptFile))
	baseCmd := expectedAIReviewBaseCommand(pr)

	cmd := buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderClaude,
		claudeReviewModelSonnet,
		claudeReviewEffortHigh,
	)
	require.Equal(
		t,
		baseCmd+"claude --permission-mode plan --model=sonnet "+
			"--effort=high --system-prompt 'You are an expert code reviewer. "+
			"Be thorough, precise, and actionable.' "+promptExpr+cleanup,
		cmd,
	)

	cmd = buildAIReviewCommand(pr, promptFile, nil, reviewProviderClaude, "", "")
	require.Equal(
		t,
		baseCmd+"claude --permission-mode plan --model=opus "+
			"--effort=high --system-prompt 'You are an expert code reviewer. "+
			"Be thorough, precise, and actionable.' "+promptExpr+cleanup,
		cmd,
	)

	cmd = buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderCodex,
		codexReviewModel54Mini,
		codexReviewEffortXHigh,
	)
	require.Equal(
		t,
		baseCmd+"codex --sandbox read-only -m gpt-5.4-mini "+
			"-c model_reasoning_effort=xhigh "+promptExpr+cleanup,
		cmd,
	)

	cmd = buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderGemini,
		geminiReviewModel31Pro,
		"",
	)
	require.Equal(
		t,
		baseCmd+"/bin/rm -rf "+shell.Quote(aiReviewDir(pr, promptFile))+"/.gemini "+
			"&& /bin/mkdir -p "+shell.Quote(aiReviewDir(pr, promptFile))+"/.gemini "+
			`&& printf '%s' '{"modelConfigs":{"customAliases":{"prl-review":{"modelConfig":{"generateContentConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}},"model":"gemini-3.1-pro"}}}}}' > `+
			shell.Quote(aiReviewDir(pr, promptFile))+"/.gemini/settings.json "+
			"&& gemini --sandbox --approval-mode plan --model prl-review "+
			"--prompt-interactive "+promptExpr+cleanup,
		cmd,
	)
}

func TestBuildAIReviewCommandReadsPromptFromFile(t *testing.T) {
	pr := testReviewPullRequest()
	const promptFile = "/tmp/prl-prompt.txt"

	cmd := buildAIReviewCommand(
		pr,
		promptFile,
		nil,
		reviewProviderCodex,
		codexReviewModel54,
		codexReviewEffortMedium,
	)

	require.Equal(
		t,
		expectedAIReviewBaseCommand(pr)+
			`codex --sandbox read-only -m gpt-5.4 -c model_reasoning_effort=medium "$(/bin/cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`,
		cmd,
	)
}

func TestBuildAIReviewCommandUsesRevisionSpecificReviewDirectory(t *testing.T) {
	pr := testReviewPullRequest()
	pr.HeadSHA = "abc123"

	cacheHome, err := shell.CacheDir()
	require.NoError(t, err)
	require.Equal(
		t,
		filepath.Join(cacheHome, "prl", "reviews", "owner", "repo", "42", "abc123"),
		aiReviewDir(pr, "/tmp/prl-review-prompt.txt"),
	)
}

func TestSafeReviewPathComponentRejectsTraversal(t *testing.T) {
	require.Equal(t, "owner", safeReviewPathComponent("owner", "fallback"))
	require.Equal(t, "fallback", safeReviewPathComponent("..", "fallback"))
	require.Equal(t, "fallback", safeReviewPathComponent("../../tmp", "fallback"))
	require.Equal(t, "fallback", safeReviewPathComponent("owner/repo", "fallback"))
}

func expectedAIReviewBaseCommand(pr PullRequest) string {
	const promptFile = "/tmp/prl-prompt.txt"
	reviewDir := aiReviewDir(pr, promptFile)
	headGuard := ""
	if pr.HeadSHA != "" {
		headGuard = fmt.Sprintf(
			`test "$(git rev-parse HEAD)" = %s && `,
			shell.Quote(pr.HeadSHA),
		)
	}
	return fmt.Sprintf(
		"/usr/bin/trash %s 2>/dev/null; /bin/mkdir -p %s && cd %s && git clone --quiet --depth 1 %s . && git fetch origin refs/pull/%d/head:pr-%d --no-tags && git checkout pr-%d && %s",
		shell.Quote(reviewDir),
		shell.Quote(reviewDir),
		shell.Quote(reviewDir),
		shell.Quote("git@github.com:"+pr.Repository.NameWithOwner),
		pr.Number,
		pr.Number,
		pr.Number,
		headGuard,
	)
}

func TestWriteReviewPromptFilePreservesContent(t *testing.T) {
	prompt := "line one\n\nline two\nwith 'single quotes' and \"doubles\""
	path, err := writeReviewPromptFile(prompt)
	require.NoError(t, err)
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, prompt, string(got))
}

func TestWriteReviewLaunchFileQuarantinesShellCommand(t *testing.T) {
	const promptFile = "/tmp/prl-review-prompt.txt"
	const shellCmd = "printf '%s\\n' 'line one' 'line two'"

	path, err := writeReviewLaunchFile(shellCmd, promptFile)
	require.NoError(t, err)
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	cleanup := "/bin/rm -f " + shell.Quote(promptFile) + " " + shell.Quote(path)
	require.Equal(
		t,
		"#!/bin/sh\ntrap "+shell.Quote(cleanup)+" EXIT HUP INT TERM\n"+shellCmd+"\n",
		string(got),
	)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	err = exec.Command("/bin/sh", "-n", path).Run()
	require.NoError(t, err)
}

func TestDefaultAIReviewPromptUsesParagraphs(t *testing.T) {
	pr := testReviewPullRequest()

	prompt := reviewPrompt(pr, nil, reviewProviderClaude)
	require.Equal(
		t,
		fmt.Sprintf(
			`Perform a comprehensive code review of PR #%d in %s.

The PR branch is checked out.

First read the PR context with:
gh pr view %[1]d --repo %[2]s

Then get the diff with:
gh pr diff %[1]d --repo %[2]s

Focus on: correctness, edge cases, error handling, performance, readability, and style.

Be thorough but concise.`,
			pr.Number,
			pr.Repository.NameWithOwner,
		),
		prompt,
	)
}

func TestGeminiReviewHasEffortOptions(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, geminiReviewModel31Pro),
	)
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortOff, value: geminiReviewEffortOff},
			{label: geminiReviewEffort1024, value: geminiReviewEffort1024},
			{label: geminiReviewEffort8192, value: geminiReviewEffort8192},
			{label: geminiReviewEffort24576, value: geminiReviewEffort24576},
			{label: geminiReviewEffortDynamic, value: geminiReviewEffortDynamic},
		},
		reviewEffortChoices(nil, reviewProviderGemini, geminiReviewModelFlash),
	)
	require.True(t, reviewProviderHasEffort(nil, reviewProviderGemini, geminiReviewModel31Pro))
	require.True(t, reviewProviderHasEffort(nil, reviewProviderClaude, claudeReviewModelSonnet))
	require.True(t, reviewProviderHasEffort(nil, reviewProviderCodex, codexReviewModel54))
}

func TestGeminiPrepareAIReviewConfirmIncludesEffort(t *testing.T) {
	pr := testReviewPullRequest()
	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Default: TUIReviewDefaultConfig{
					Provider: string(reviewProviderGemini),
					Model:    geminiReviewModel31Pro,
				},
			},
		},
	}
	m := tuiModel{cfg: cfg}

	m = m.prepareAIReviewConfirm(pr, 0)

	require.Len(t, m.confirmOptions, 3)
	require.Equal(t, reviewProviderOptionLabel, m.confirmOptions[0].label)
	require.Equal(t, reviewModelOptionLabel, m.confirmOptions[1].label)
	require.Equal(t, reviewEffortOptionLabel, m.confirmOptions[2].label)
	require.Len(t, m.confirmOptionValues, 3)
}

func TestReviewPromptUsesConfigTemplate(t *testing.T) {
	pr := testReviewPullRequest()
	pr.Title = "Improve AI review prompts"

	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Providers: TUIReviewProvidersConfig{
					Claude: TUIReviewProviderConfig{
						Prompt: `Review PR {prNumber} in {ownerWithRepo}.
Repo: {repo}
Owner: {owner}
Ref: {prRef}
URL: {prURL}
Title: {title}`,
					},
				},
			},
		},
	}

	require.Equal(
		t,
		`Review PR 42 in owner/repo.
Repo: repo
Owner: owner
Ref: owner/repo#42
URL: https://github.com/owner/repo/pull/42
Title: Improve AI review prompts`,
		reviewPrompt(pr, cfg, reviewProviderClaude),
	)
}

func TestReviewConfigUsesConfiguredChoices(t *testing.T) {
	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Providers: TUIReviewProvidersConfig{
					Codex: TUIReviewProviderConfig{
						Models:  []string{"gpt-5.5", "gpt-5.5-mini"},
						Efforts: []string{"minimal", "deep"},
					},
				},
			},
		},
	}

	require.Equal(
		t,
		[]filterChoice{
			{label: "gpt-5.5", value: "gpt-5.5"},
			{label: "gpt-5.5-mini", value: "gpt-5.5-mini"},
		},
		reviewModelChoices(cfg, reviewProviderCodex),
	)
	require.Equal(t, "gpt-5.5", defaultReviewModel(cfg, reviewProviderCodex))
	require.Equal(
		t,
		[]filterChoice{
			{label: "minimal", value: "minimal"},
			{label: "deep", value: "deep"},
		},
		reviewEffortChoices(cfg, reviewProviderCodex, "gpt-5.5"),
	)
	require.Equal(t, "minimal", defaultReviewEffort(cfg, reviewProviderCodex, "gpt-5.5"))
}

func TestBuildAIReviewCommandUsesConfiguredFallbackChoices(t *testing.T) {
	pr := testReviewPullRequest()
	cfg := &Config{
		TUI: TUIConfig{
			Review: TUIReviewConfig{
				Providers: TUIReviewProvidersConfig{
					Codex: TUIReviewProviderConfig{
						Models:  []string{"gpt-5.5"},
						Efforts: []string{"deep"},
					},
				},
			},
		},
	}

	cmd := buildAIReviewCommand(pr, "/tmp/prl-prompt.txt", cfg, reviewProviderCodex, "", "")

	require.Equal(
		t,
		expectedAIReviewBaseCommand(pr)+
			`codex --sandbox read-only -m gpt-5.5 -c model_reasoning_effort=deep "$(/bin/cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`,
		cmd,
	)
}

func TestBuildAIReviewCommandUsesGeminiBudgetFor25Flash(t *testing.T) {
	pr := testReviewPullRequest()

	cmd := buildAIReviewCommand(
		pr,
		"/tmp/prl-prompt.txt",
		nil,
		reviewProviderGemini,
		geminiReviewModelFlash,
		geminiReviewEffort1024,
	)

	reviewDir := aiReviewDir(pr, "/tmp/prl-prompt.txt")
	require.Equal(
		t,
		expectedAIReviewBaseCommand(pr)+
			"/bin/rm -rf "+shell.Quote(reviewDir)+"/.gemini "+
			"&& /bin/mkdir -p "+shell.Quote(reviewDir)+"/.gemini "+
			`&& printf '%s' '{"modelConfigs":{"customAliases":{"prl-review":{"modelConfig":{"generateContentConfig":{"thinkingConfig":{"thinkingBudget":1024}},"model":"gemini-2.5-flash"}}}}}' > `+
			shell.Quote(reviewDir)+"/.gemini/settings.json "+
			`&& gemini --sandbox --approval-mode plan --model prl-review --prompt-interactive "$(/bin/cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`,
		cmd,
	)
}

func TestMatchesPatternTreatsPlainStringsAsExact(t *testing.T) {
	require.True(t, matchesPattern("sonnet", "sonnet"))
	require.False(t, matchesPattern("sonnet", "sonnet-4"))
}

func TestMatchesPatternTreatsWildcardsAsGlobs(t *testing.T) {
	require.True(t, matchesPattern("gemini-3*", "gemini-3-pro"))
	require.True(t, matchesPattern("gemini-*", "gemini-2.5-flash"))
	require.False(t, matchesPattern("gemini-3*", "gemini-2.5-flash"))
}

func TestClaudeEffortRulesUseGlobFallback(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: claudeReviewEffortLow, value: claudeReviewEffortLow},
			{label: claudeReviewEffortMedium, value: claudeReviewEffortMedium},
			{label: claudeReviewEffortHigh, value: claudeReviewEffortHigh},
			{label: claudeReviewEffortXHigh, value: claudeReviewEffortXHigh},
			{label: claudeReviewEffortMax, value: claudeReviewEffortMax},
			{label: claudeReviewEffortAuto, value: claudeReviewEffortAuto},
		},
		reviewEffortChoices(nil, reviewProviderClaude, "claude-3.7-sonnet"),
	)
	require.Equal(
		t,
		claudeReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderClaude, "claude-3.7-sonnet"),
	)
}

func TestClaudeSonnetAndFableDefaultToMediumEffort(t *testing.T) {
	require.Equal(
		t,
		claudeReviewEffortMedium,
		defaultReviewEffort(nil, reviewProviderClaude, claudeReviewModelSonnet),
	)
	require.Equal(
		t,
		claudeReviewEffortMedium,
		defaultReviewEffort(nil, reviewProviderClaude, claudeReviewModelFable),
	)
}

func TestCodexEffortRulesUseGlobFallback(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
		},
		reviewEffortChoices(nil, reviewProviderCodex, "gpt-5.5"),
	)
	require.Equal(
		t,
		codexReviewEffortXHigh,
		defaultReviewEffort(nil, reviewProviderCodex, "gpt-5.5"),
	)
}

func TestCodex56ModelsIncludeMaxEffort(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewModel54Mini, value: codexReviewModel54Mini},
			{label: codexReviewModel54, value: codexReviewModel54},
			{label: codexReviewModel55, value: codexReviewModel55},
			{label: codexReviewModel56Luna, value: codexReviewModel56Luna},
			{label: codexReviewModel56Terra, value: codexReviewModel56Terra},
			{label: codexReviewModel56Sol, value: codexReviewModel56Sol},
			{label: codexReviewModel6Astra, value: codexReviewModel6Astra},
		},
		reviewModelChoices(nil, reviewProviderCodex),
	)
	require.Equal(t, codexReviewModel6Astra, defaultReviewModel(nil, reviewProviderCodex))
	require.Equal(
		t,
		[]filterChoice{
			{label: codexReviewEffortLow, value: codexReviewEffortLow},
			{label: codexReviewEffortMedium, value: codexReviewEffortMedium},
			{label: codexReviewEffortHigh, value: codexReviewEffortHigh},
			{label: codexReviewEffortXHigh, value: codexReviewEffortXHigh},
			{label: codexReviewEffortMax, value: codexReviewEffortMax},
		},
		reviewEffortChoices(nil, reviewProviderCodex, codexReviewModel56Luna),
	)
	require.Equal(
		t,
		codexReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderCodex, codexReviewModel56Sol),
	)
}

func TestBuildAIReviewCommandUsesCodex6AstraByDefault(t *testing.T) {
	pr := testReviewPullRequest()
	cmd := buildAIReviewCommand(
		pr,
		"/tmp/prl-prompt.txt",
		nil,
		reviewProviderCodex,
		"",
		"",
	)

	require.Equal(
		t,
		expectedAIReviewBaseCommand(pr)+
			`codex --sandbox read-only -m gpt-6-astra -c model_reasoning_effort=high "$(/bin/cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`,
		cmd,
	)
}

func TestGeminiEffortRulesPreferSpecificGlobBeforeCatchAll(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortOff, value: geminiReviewEffortOff},
			{label: geminiReviewEffort1024, value: geminiReviewEffort1024},
			{label: geminiReviewEffort8192, value: geminiReviewEffort8192},
			{label: geminiReviewEffort24576, value: geminiReviewEffort24576},
			{label: geminiReviewEffortDynamic, value: geminiReviewEffortDynamic},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-2.5-flash-preview"),
	)
	require.Equal(
		t,
		geminiReviewEffortDynamic,
		defaultReviewEffort(nil, reviewProviderGemini, "gemini-2.5-flash-preview"),
	)
}

func TestGemini3EffortRulesDistinguishProAndFlash(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-3.1-pro-preview"),
	)
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortMinimal, value: geminiReviewEffortMinimal},
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-3.5-flash"),
	)
}

func TestGeminiEffortRulesUseCatchAllGlob(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini-2.0-pro"),
	)
	require.Equal(
		t,
		geminiReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderGemini, "gemini-2.0-pro"),
	)
}

func TestGeminiEffortRulesUseExactMatchForBareGemini(t *testing.T) {
	require.Equal(
		t,
		[]filterChoice{
			{label: geminiReviewEffortLow, value: geminiReviewEffortLow},
			{label: geminiReviewEffortMedium, value: geminiReviewEffortMedium},
			{label: geminiReviewEffortHigh, value: geminiReviewEffortHigh},
		},
		reviewEffortChoices(nil, reviewProviderGemini, "gemini"),
	)
	require.Equal(
		t,
		geminiReviewEffortHigh,
		defaultReviewEffort(nil, reviewProviderGemini, "gemini"),
	)
	require.True(t, reviewProviderHasEffort(nil, reviewProviderGemini, "gemini"))
}

func TestCodexUltraEffortCommands(t *testing.T) {
	for _, model := range []string{codexReviewModel6Astra, codexReviewModel56Sol, codexReviewModel56Terra} {
		t.Run(model, func(t *testing.T) {
			require.True(t, isValidReviewModel(nil, reviewProviderCodex, model))
			require.True(
				t,
				isValidReviewEffort(nil, reviewProviderCodex, model, codexReviewEffortUltra),
			)
			pr := testReviewPullRequest()
			cmd := buildAIReviewCommand(pr, "/tmp/prl-prompt.txt", nil,
				reviewProviderCodex, model, codexReviewEffortUltra)
			require.Equal(t, expectedAIReviewBaseCommand(pr)+
				"codex --sandbox read-only -m "+model+
				` -c model_reasoning_effort=ultra "$(/bin/cat /tmp/prl-prompt.txt)"; rm -f /tmp/prl-prompt.txt`, cmd)
		})
	}
	for _, model := range []string{codexReviewModel56Luna, codexReviewModel55, codexReviewModel54} {
		require.False(
			t,
			isValidReviewEffort(nil, reviewProviderCodex, model, codexReviewEffortUltra),
		)
	}
}
