package main

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/gechr/clib/table"
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

// renderAuthor renders a styled author string for table display.
func renderAuthor(pr PullRequest, ctx *table.RenderContext, resolver *AuthorResolver) string {
	login := pr.Author.Login
	isBot := strings.HasSuffix(strings.ToLower(login), BotSuffix)

	// Resolve display name
	displayName := resolver.Resolve(login)

	// Bot name handling
	botBaseName := login
	if isBot {
		botBaseName = strings.TrimSuffix(login, BotSuffix)
		if before, ok := strings.CutSuffix(displayName, BotSuffix); ok {
			strippedName := before
			resolved := resolver.Resolve(strippedName)
			if resolved != strippedName {
				displayName = resolved
			} else {
				displayName = strippedName
			}
		}
	}

	if !ctx.Ansi.Terminal() {
		return displayName
	}

	// Assign color (stable per author across all rows)
	color := ctx.AssignEntityColor(login)
	style := lipgloss.NewStyle().Foreground(color)
	if isBot {
		style = style.Faint(true)
	} else if resolver.IsKnown(login) && !resolver.IsHCL(login) {
		style = style.Strikethrough(true)
	}

	// Hyperlink
	var url string
	if isBot {
		url = "https://github.com/apps/" + botBaseName
	} else {
		url = "https://github.com/" + login
	}

	return ctx.Hyperlink(url, style.Render(displayName))
}
