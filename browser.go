package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"
	xos "github.com/gechr/x/os"
)

// openBrowser opens the given URLs in the default browser.
func openBrowser(urls ...string) error {
	for _, url := range urls {
		cmd, err := browserCommand(url, runtime.GOOS, inWSL())
		if err != nil {
			return err
		}
		if err := runDesktopCommand(cmd); err != nil {
			return err
		}
	}
	return nil
}

// browserCommand selects the platform launcher without starting it.
func browserCommand(url, platform string, wsl bool) (*exec.Cmd, error) {
	if platform == xos.PlatformWindows || wsl {
		return desktopPowerShellCommand("Start-Process -FilePath", url, wsl)
	}
	name := "open"
	if platform == xos.PlatformLinux {
		name = "xdg-open"
	}
	return exec.CommandContext(context.Background(), name, url), nil
}

// copyToClipboard copies text to the system clipboard.
func copyToClipboard(text string) error {
	if !inWSL() {
		return clipboard.WriteAll(text)
	}
	cmd, err := desktopPowerShellCommand("Set-Clipboard -Value", text, true)
	if err != nil {
		return err
	}
	return runDesktopCommand(cmd)
}

func inWSL() bool {
	return xos.IsLinux() && (os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "")
}

// desktopPowerShellCommand prepares a trusted PowerShell action with UTF-8 stdin.
// URLs and clipboard contents remain data, never PowerShell code.
func desktopPowerShellCommand(action, text string, wsl bool) (*exec.Cmd, error) {
	name, err := findDesktopPowerShell(exec.LookPath, wsl)
	if err != nil {
		return nil, fmt.Errorf("windows desktop integration requires powershell.exe: %w", err)
	}
	script := "$ErrorActionPreference = 'Stop'; " +
		"[Console]::InputEncoding = [System.Text.Encoding]::UTF8; " +
		action + " ([Console]::In.ReadToEnd())"
	cmd := exec.CommandContext(context.Background(), name,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Stdin = strings.NewReader(text)
	return cmd, nil
}

func findDesktopPowerShell(lookPath func(string) (string, error), wsl bool) (string, error) {
	name, err := lookPath("powershell.exe")
	if err != nil && wsl {
		// WSL can disable appending Windows directories to PATH.
		return lookPath("/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe")
	}
	return name, err
}

func runDesktopCommand(cmd *exec.Cmd) error {
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("%s: %w: %s", cmd.Path, err, detail)
	}
	return fmt.Errorf("%s: %w", cmd.Path, err)
}
