package main

import (
	"fmt"
	"sort"
	"strings"
)

// handleComplete handles --complete=<kind> --shell=<shell> and prints completions to stdout.
func (p *prl) handleComplete(shell, kind string, cfg *Config) error {
	if shell != "fish" {
		return fmt.Errorf("unsupported shell %q (supported: fish)", shell)
	}

	var (
		err     error
		results []string
	)

	switch kind {
	case "author":
		results, err = completeAuthors(cfg)
	case "team":
		results, err = completeTeams(cfg)
	case "repo":
		results, err = completeRepositories(cfg)
	case "topic":
		results, err = completeTopics(cfg)
	case "columns":
		results = p.completeColumns()
	case "slack-recipient":
		results, err = completeSlackRecipients(cfg)
	default:
		return fmt.Errorf("unknown completion type %q", kind)
	}
	if err != nil {
		return err
	}

	for _, r := range results {
		fmt.Println(r)
	}
	return nil
}

// completeAuthors returns author completions as "username\tDisplay Name" lines.
// Tries plugin first, falls back to config authors.
func completeAuthors(cfg *Config) ([]string, error) {
	var results []string

	results = append(results, valueAtMe+"\tCurrent user")
	results = append(results, "all\tAll authors")

	if cfg == nil {
		return results, nil
	}

	// Try plugin
	plug, err := discoverPlugin(cfg)
	if err != nil {
		return nil, err
	}
	pluginResults, err := plug.Complete("author")
	if err != nil {
		return nil, err
	}
	if pluginResults != nil {
		for _, r := range pluginResults {
			val, _, _ := strings.Cut(r, "\t")
			if val != valueAtMe && val != "all" {
				results = append(results, r)
			}
		}
		return results, nil
	}

	// Fall back to config authors
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
			results = append(results, username+"\t"+name)
		}
	}

	return results, nil
}

// completeTeams returns team name completions.
// Tries plugin first, falls back to config teams + team_aliases.
func completeTeams(cfg *Config) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}

	plug, err := discoverPlugin(cfg)
	if err != nil {
		return nil, err
	}
	pluginResults, err := plug.Complete("team")
	if err != nil {
		return nil, err
	}
	if pluginResults != nil {
		seen := make(map[string]bool)
		var results []string

		for _, r := range pluginResults {
			val, _, _ := strings.Cut(r, "\t")
			seen[val] = true
			results = append(results, r)
		}

		for alias, target := range cfg.TeamAliases {
			if !seen[alias] {
				seen[alias] = true
				results = append(results, alias+"\t"+target)
			}
		}

		sort.Strings(results)
		return results, nil
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
			results = append(results, alias+"\t"+target)
		}
	}

	sort.Strings(results)
	return results, nil
}

// completeRepositories returns repository name completions from the plugin.
func completeRepositories(cfg *Config) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	plug, err := discoverPlugin(cfg)
	if err != nil {
		return nil, err
	}
	return plug.Complete("repo")
}

// completeTopics returns topic completions from the plugin.
func completeTopics(cfg *Config) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	plug, err := discoverPlugin(cfg)
	if err != nil {
		return nil, err
	}
	return plug.Complete("topic")
}

// completeSlackRecipients returns Slack recipient completions from the plugin.
func completeSlackRecipients(cfg *Config) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	plug, err := discoverPlugin(cfg)
	if err != nil {
		return nil, err
	}
	return plug.Complete("slack-recipient")
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
