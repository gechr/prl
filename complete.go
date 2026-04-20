package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gechr/clog"
)

const tab = "\t"

// handleComplete handles --complete=<kind> --shell=<shell> and prints completions to stdout.
func (p *prl) handleComplete(shell, kind string, cfg *Config) error {
	if shell != "fish" {
		return fmt.Errorf("unsupported shell %q (supported: fish)", shell)
	}

	var results []string

	switch kind {
	case "author":
		results = completeAuthors(cfg)
	case "team":
		results = completeTeams(cfg)
	case "repo":
		results = completeRepositories(cfg)
	case "topic":
		results = completeTopics(cfg)
	case "columns":
		results = p.completeColumns()
	case "slack-recipient":
		results = completeSlackRecipients(cfg)
	default:
		return fmt.Errorf("unknown completion type %q", kind)
	}

	for _, r := range results {
		fmt.Println(r)
	}
	return nil
}

// completeAuthors returns author completions as "username\tDisplay Name" lines.
// Tries plugin first, falls back to config authors.
func completeAuthors(cfg *Config) []string {
	var results []string
	seen := make(map[string]bool)
	bots := discoverBotAuthors(cfg)

	results = append(results, valueAtMe+tab+"Current user")
	seen[valueAtMe] = true
	results = append(results, "all"+tab+"All authors")
	seen["all"] = true

	if cfg == nil {
		return results
	}

	// Try plugin
	if pluginResults := tryPluginComplete(cfg, "users"); pluginResults != nil {
		for _, r := range pluginResults {
			val, desc, _ := strings.Cut(r, tab)
			normalized := normalizeBotAuthorValue(val, bots)
			if !seen[normalized] {
				seen[normalized] = true
				results = append(results, normalized+tab+desc)
			}
		}
	}

	// Add config authors as a fallback and supplement to plugin results.
	if len(cfg.Authors) > 0 {
		var configUsers []string
		for username := range cfg.Authors {
			configUsers = append(configUsers, username)
		}
		sort.Strings(configUsers)

		for _, username := range configUsers {
			name := cfg.Authors[username]
			if strings.EqualFold(name, BotName) {
				name += " 🤖"
			}
			if seen[username] {
				continue
			}
			seen[username] = true
			results = append(results, username+tab+name)
		}
	}

	return results
}

// completeTeams returns team name completions.
// Tries plugin first, falls back to config teams + team_aliases.
func completeTeams(cfg *Config) []string {
	if cfg == nil {
		return nil
	}

	if pluginResults := tryPluginComplete(cfg, "teams"); pluginResults != nil {
		seen := make(map[string]bool)
		var results []string

		for _, r := range pluginResults {
			val, _, _ := strings.Cut(r, tab)
			seen[val] = true
			results = append(results, r)
		}

		for alias, target := range cfg.TeamAliases {
			if !seen[alias] {
				seen[alias] = true
				results = append(results, alias+tab+target)
			}
		}

		sort.Strings(results)
		return results
	}

	// Fall back to config teams + aliases
	seen := make(map[string]bool)
	var results []string

	for team := range cfg.Teams {
		if !seen[team] {
			seen[team] = true
			results = append(results, team)
		}
	}

	for alias, target := range cfg.TeamAliases {
		if !seen[alias] {
			seen[alias] = true
			results = append(results, alias+tab+target)
		}
	}

	sort.Strings(results)
	return results
}

// completeRepositories returns repository name completions from the plugin.
func completeRepositories(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	return tryPluginComplete(cfg, "repos")
}

// completeTopics returns topic completions from the plugin.
func completeTopics(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	return tryPluginComplete(cfg, "topics")
}

// completeSlackRecipients returns Slack recipient completions from the plugin.
func completeSlackRecipients(cfg *Config) []string {
	if cfg == nil {
		return nil
	}

	return tryPluginComplete(cfg, "slack-recipients")
}

func tryPluginComplete(cfg *Config, kind string) []string {
	plug, err := discoverPlugin(cfg)
	if err != nil {
		clog.Debug().Err(err).Str("kind", kind).Msg("Skipping plugin completions")
		return nil
	}

	results, err := plug.Complete(kind)
	if err != nil {
		clog.Debug().Err(err).Str("kind", kind).Msg("Skipping plugin completions")
		return nil
	}

	return results
}

// completeColumns returns column name completions.
func (p *prl) completeColumns() []string {
	defs := p.allColumnDefs(tableLayout{})

	canonical := make(map[string]bool)
	var results []string

	for key, col := range defs {
		name := col.Name
		if name == "" {
			name = key
		}
		if canonical[name] {
			continue
		}
		canonical[name] = true
		results = append(results, name)
	}

	sort.Strings(results)
	return results
}
