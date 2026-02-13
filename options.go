package main

import (
	"os"

	"github.com/cli/go-gh/v2/pkg/api"
)

// clientOption configures GitHub API clients.
type clientOption func(*api.ClientOptions)

// withDebug enables HTTP request logging to stderr.
func withDebug(enabled bool) clientOption {
	return func(opts *api.ClientOptions) {
		if enabled {
			opts.Log = os.Stderr
		}
	}
}
