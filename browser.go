package main

import (
	"context"
	"os/exec"

	"github.com/atotto/clipboard"
	xos "github.com/gechr/x/os"
)

// openBrowser opens the given URLs in the default browser.
func openBrowser(urls ...string) error {
	name := "open"
	if xos.IsLinux() {
		name = "xdg-open"
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
