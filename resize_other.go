//go:build !unix

package main

import "os"

// notifyResize is a no-op on platforms without a SIGWINCH equivalent (e.g.
// Windows), where terminal-resize signals are not delivered.
func notifyResize(ch chan os.Signal) {}
