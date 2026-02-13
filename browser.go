package main

import (
	"context"
	"os/exec"
	"runtime"

	"github.com/atotto/clipboard"
)

// openBrowser opens the given URLs in the default browser.
func openBrowser(urls ...string) error {
	var name string
	switch runtime.GOOS {
	case "linux":
		name = "xdg-open"
	default:
		name = "open"
	}
	for _, url := range urls {
		if err := exec.CommandContext(context.Background(), name, url).Run(); err != nil {
			return err
		}
	}
	return nil
}

// copyToClipboard copies text to the system clipboard.
func copyToClipboard(text string) error {
	return clipboard.WriteAll(text)
}
