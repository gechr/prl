package main

import (
	"maps"
	"strings"

	"github.com/gechr/clog"
)

var defaultAuthorAliases = map[string]string{
	strings.ToLower(copilotReviewer): "Copilot",
}

// AuthorResolver resolves GitHub usernames to display names.
type AuthorResolver struct {
	names       map[string]string // lowercased username -> display name
	activeNames map[string]bool   // tracks which names came from helper (active users)
}

// NewAuthorResolver creates an AuthorResolver from config authors and optional plugin.
func NewAuthorResolver(cfg *Config) *AuthorResolver {
	if cfg == nil {
		cfg = &Config{}
	}

	r := &AuthorResolver{
		names:       make(map[string]string),
		activeNames: make(map[string]bool),
	}

	maps.Copy(r.names, defaultAuthorAliases)

	// Load config authors (lower priority)
	for username, displayName := range cfg.Authors {
		r.names[strings.ToLower(username)] = displayName
	}

	// Load plugin names (higher priority, marks as active)
	r.loadPluginNames(cfg)

	return r
}

// loadPluginNames loads author names from the plugin binary.
// Uses the "complete author" output which returns "username\tDisplay Name" lines.
func (r *AuthorResolver) loadPluginNames(cfg *Config) {
	plug, err := discoverPlugin(cfg)
	if err != nil {
		clog.Debug().Err(err).Msg("Skipping plugin author names")
		return
	}
	results, err := plug.Complete("author")
	if err != nil {
		clog.Debug().Err(err).Msg("Skipping plugin author names")
		return
	}
	if results == nil {
		return
	}

	for _, line := range results {
		val, desc, hasSep := strings.Cut(line, "\t")
		if val == valueAtMe || val == "all" {
			continue
		}
		lower := strings.ToLower(val)
		if hasSep && desc != "" {
			r.names[lower] = desc
		}
		r.activeNames[lower] = true
	}
}

// Resolve returns the display name for a GitHub username.
func (r *AuthorResolver) Resolve(username string) string {
	if name, ok := r.names[strings.ToLower(username)]; ok && name != "" {
		return name
	}
	return username
}

// IsActive returns true if the name came from the helper (active user).
// When no helper is available, all known authors are considered active.
func (r *AuthorResolver) IsActive(username string) bool {
	if len(r.activeNames) == 0 {
		// No helper - can't distinguish active from departed
		return true
	}
	return r.activeNames[strings.ToLower(username)]
}

// IsKnown returns true if the username has a display name mapping.
func (r *AuthorResolver) IsKnown(username string) bool {
	_, ok := r.names[strings.ToLower(username)]
	return ok
}

// isAuthorBot returns true if the username is a known bot reviewer.
func isAuthorBot(username string) bool {
	return strings.EqualFold(username, copilotReviewer)
}
