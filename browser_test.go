package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"
	"github.com/stretchr/testify/require"
)

func fakeDesktopCommands(t *testing.T) (string, string) {
	t.Helper()
	if !xos.IsLinux() {
		t.Skip("WSL routing requires Linux")
	}
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	inputFile := filepath.Join(t.TempDir(), "input")
	script := `#!/bin/sh
printf '%s\000' "$@" >> "$PRL_TEST_ARGS"
/bin/cat >> "$PRL_TEST_INPUT"
if [ "$PRL_TEST_FAIL" = 1 ]; then
  echo 'desktop action failed' >&2
  exit 7
fi
if [ "$PRL_TEST_FAIL" = silent ]; then
  exit 7
fi
`
	for _, name := range []string{"powershell.exe", "xdg-open", "open"} {
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700))
	}
	t.Setenv("PATH", binDir)
	t.Setenv("PRL_TEST_ARGS", argsFile)
	t.Setenv("PRL_TEST_INPUT", inputFile)
	t.Setenv("PRL_TEST_FAIL", "")
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WSL_INTEROP", "")
	t.Setenv("WT_SESSION", "")
	return argsFile, inputFile
}

func TestOpenBrowserWSL(t *testing.T) {
	argsFile, inputFile := fakeDesktopCommands(t)
	urls := []string{
		"https://github.com/owner/repo/pull/42",
		"https://github.com/search?q=is%3Apr&text='quoted';$value&unicode=é",
	}
	require.NoError(t, openBrowser(urls...))
	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	const wantArgs = "-NoLogo\x00-NoProfile\x00-NonInteractive\x00-STA\x00-Command\x00" +
		"$ErrorActionPreference = 'Stop'; [Console]::InputEncoding = [System.Text.Encoding]::UTF8; " +
		"Start-Process -FilePath ([Console]::In.ReadToEnd())\x00"
	require.Equal(t, strings.Repeat(wantArgs, len(urls)), string(args))
	input, err := os.ReadFile(inputFile)
	require.NoError(t, err)
	require.Equal(t, strings.Join(urls, ""), string(input))
}

func TestOpenBrowserLinux(t *testing.T) {
	argsFile, _ := fakeDesktopCommands(t)
	t.Setenv("WSL_DISTRO_NAME", "")
	const url = "https://github.com/owner/repo/pull/42"
	require.NoError(t, openBrowser(url))
	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, url+"\x00", string(args))
}

func TestOpenBrowserNoURLs(t *testing.T) {
	argsFile, _ := fakeDesktopCommands(t)
	require.NoError(t, openBrowser())
	_, err := os.Stat(argsFile)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestBrowserCommand(t *testing.T) {
	argsFile, _ := fakeDesktopCommands(t)
	const url = "https://github.com/search?q=is%3Apr&text='quoted';$value"
	powerShellArgs := []string{
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-STA",
		"-Command",
		"$ErrorActionPreference = 'Stop'; [Console]::InputEncoding = [System.Text.Encoding]::UTF8; " +
			"Start-Process -FilePath ([Console]::In.ReadToEnd())",
	}
	for _, tc := range []struct {
		name     string
		platform string
		wsl      bool
		program  string
		args     []string
		stdin    string
	}{
		{
			name: "macOS", platform: xos.PlatformDarwin,
			program: "open", args: []string{url},
		},
		{
			name: "Linux", platform: xos.PlatformLinux,
			program: "xdg-open", args: []string{url},
		},
		{
			name: "Windows", platform: xos.PlatformWindows,
			program: "powershell.exe", args: powerShellArgs, stdin: url,
		},
		{
			name: "WSL", platform: xos.PlatformLinux, wsl: true,
			program: "powershell.exe", args: powerShellArgs, stdin: url,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := browserCommand(url, tc.platform, tc.wsl)
			require.NoError(t, err)
			require.NoError(t, cmd.Err)
			require.Equal(t, filepath.Join(os.Getenv("PATH"), tc.program), cmd.Path)
			require.Equal(t, tc.args, cmd.Args[1:])
			if tc.stdin == "" {
				require.Nil(t, cmd.Stdin)
			} else {
				input, readErr := io.ReadAll(cmd.Stdin)
				require.NoError(t, readErr)
				require.Equal(t, tc.stdin, string(input))
			}
			_, err = os.Stat(argsFile)
			require.ErrorIs(t, err, os.ErrNotExist, "building a command must not execute it")
		})
	}
}

func TestBrowserCommandMissingPowerShell(t *testing.T) {
	fakeDesktopCommands(t)
	t.Setenv("PATH", t.TempDir())
	cmd, err := browserCommand("https://github.com/owner/repo/pull/42", xos.PlatformWindows, false)
	require.Nil(t, cmd)
	require.ErrorIs(t, err, exec.ErrNotFound)
	lookupErr := &exec.Error{Name: "powershell.exe", Err: exec.ErrNotFound}
	require.EqualError(
		t,
		err,
		"windows desktop integration requires powershell.exe: "+lookupErr.Error(),
	)
}

func TestCopyToClipboardWSL(t *testing.T) {
	argsFile, inputFile := fakeDesktopCommands(t)
	const text = "PR #42: café \u4e16\u754c 🐱\nsecond line\n'\";$value & %PATH%"
	require.NoError(t, copyToClipboard(text))
	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	const wantArgs = "-NoLogo\x00-NoProfile\x00-NonInteractive\x00-STA\x00-Command\x00" +
		"$ErrorActionPreference = 'Stop'; [Console]::InputEncoding = [System.Text.Encoding]::UTF8; " +
		"Set-Clipboard -Value ([Console]::In.ReadToEnd())\x00"
	require.Equal(t, wantArgs, string(args))
	input, err := os.ReadFile(inputFile)
	require.NoError(t, err)
	require.Equal(t, text, string(input))
}

func TestDesktopWSLInteropDetection(t *testing.T) {
	_, inputFile := fakeDesktopCommands(t)
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "/run/WSL/123_interop")
	require.NoError(t, copyToClipboard("interop-only"))
	input, err := os.ReadFile(inputFile)
	require.NoError(t, err)
	require.Equal(t, "interop-only", string(input))
}

func TestFindDesktopPowerShell(t *testing.T) {
	const fallback = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
	for _, tc := range []struct {
		name      string
		wsl       bool
		available string
		wantCalls []string
	}{
		{"WSL PATH", true, "powershell.exe", []string{"powershell.exe"}},
		{"WSL fallback", true, fallback, []string{"powershell.exe", fallback}},
		{"WSL missing", true, "", []string{"powershell.exe", fallback}},
		{"native PATH", false, "powershell.exe", []string{"powershell.exe"}},
		{"native missing", false, "", []string{"powershell.exe"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			lookPath := func(name string) (string, error) {
				calls = append(calls, name)
				if name == tc.available {
					return name, nil
				}
				return "", exec.ErrNotFound
			}
			path, err := findDesktopPowerShell(lookPath, tc.wsl)
			if tc.available == "" {
				require.ErrorIs(t, err, exec.ErrNotFound)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.available, path)
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestDesktopCommandFailures(t *testing.T) {
	fakeDesktopCommands(t)
	t.Setenv("PRL_TEST_FAIL", "1")
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"open", func() error { return openBrowser("https://github.com/owner/repo/pull/42") }},
		{"copy", func() error { return copyToClipboard("text") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			require.EqualError(t, err, fmt.Sprintf("%s: exit status 7: desktop action failed",
				filepath.Join(os.Getenv("PATH"), "powershell.exe")))
		})
	}
}

func TestOpenBrowserStopsAfterFailure(t *testing.T) {
	_, inputFile := fakeDesktopCommands(t)
	t.Setenv("PRL_TEST_FAIL", "1")
	const first = "https://github.com/owner/repo/pull/42"
	err := openBrowser(first, "https://github.com/owner/repo/pull/43")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 7, exitErr.ExitCode())
	input, err := os.ReadFile(inputFile)
	require.NoError(t, err)
	require.Equal(t, first, string(input))
}

func TestRunDesktopCommandSilentFailure(t *testing.T) {
	fakeDesktopCommands(t)
	t.Setenv("PRL_TEST_FAIL", "silent")
	cmd, err := desktopPowerShellCommand("Set-Clipboard -Value", "text", true)
	require.NoError(t, err)
	err = runDesktopCommand(cmd)
	require.EqualError(t, err, cmd.Path+": exit status 7")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 7, exitErr.ExitCode())
}
