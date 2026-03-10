package main

import (
	"strings"
)

// AuthorResolver resolves GitHub usernames to display names.
type AuthorResolver struct {
	names    map[string]string // lowercased username -> display name
	hclNames map[string]bool   // tracks which names came from HCL
}

// NewAuthorResolver creates an AuthorResolver from config authors and HCL sources.
func NewAuthorResolver(cfg *Config) *AuthorResolver {
	r := &AuthorResolver{
		names:    make(map[string]string),
		hclNames: make(map[string]bool),
	}

	// Load config authors first (lower priority)
	for username, displayName := range cfg.Authors {
		r.names[strings.ToLower(username)] = displayName
	}

	// Load HCL names (higher priority, overwrites config entries)
	r.loadHCLNames(cfg)

	return r
}

// loadHCLNames loads names from HCL users.tf (overwrites config author entries).
func (r *AuthorResolver) loadHCLNames(cfg *Config) {
	hclNames, err := parseUsersHCLNames(cfg)
	if err != nil {
		return
	}
	for ghUser, name := range hclNames {
		lower := strings.ToLower(ghUser)
		r.names[lower] = name
		r.hclNames[lower] = true
	}
}

// Resolve returns the display name for a GitHub username.
func (r *AuthorResolver) Resolve(username string) string {
	if name, ok := r.names[strings.ToLower(username)]; ok && name != "" {
		return name
	}
	return username
}

// IsHCL returns true if the name came from HCL (vs config-only = departed).
func (r *AuthorResolver) IsHCL(username string) bool {
	return r.hclNames[strings.ToLower(username)]
}

// IsKnown returns true if the username has a display name mapping.
func (r *AuthorResolver) IsKnown(username string) bool {
	_, ok := r.names[strings.ToLower(username)]
	return ok
}
