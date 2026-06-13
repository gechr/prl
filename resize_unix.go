//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize registers ch to receive terminal-resize notifications. On Unix
// the kernel delivers SIGWINCH whenever the controlling terminal's size
// changes.
func notifyResize(ch chan os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}
