package main

import (
	"fmt"
	"strings"

	"github.com/gechr/clog"
	xmaps "github.com/gechr/x/maps"
	xslices "github.com/gechr/x/slices"
)

const tab = "\t"

// completionValue returns the value part of a "value\tdescription" completion line.
func completionValue(line string) string {
	val, _, _ := strings.Cut(line, tab)
	return val
}

// handleComplete handles --complete=<kind> --shell=<shell> and prints completions to stdout.
func (p *prl) handleComplete(shell, kind string, cfg *Config) error {
	if shell != "fish" {
		return fmt.Errorf("unsupported shell %q (supported: fish)", shell)
	}

	var results []string

	switch kind {
	case colAuthor:
		results = completeAuthors(cfg)
	case "team":
		results = completeTeams(cfg)
	case valueRepo:
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
	bots := discoverBotAuthors(cfg)

	results := []string{
		valueAtMe + tab + "Current user",
		"all" + tab + "All authors",
	}

	if cfg == nil {
		return results
	}

	// Try plugin
	for _, r := range tryPluginComplete(cfg, valueUsers) {
		val, desc, _ := strings.Cut(r, tab)
		results = append(results, normalizeBotAuthorValue(val, bots)+tab+desc)
	}

	// Add config authors as a fallback and supplement to plugin results.
	for _, username := range xmaps.KeysNatural(cfg.Authors) {
		name := cfg.Authors[username]
		if strings.EqualFold(name, BotName) {
			name += " 🤖"
		}
		results = append(results, username+tab+name)
	}

	return xslices.UniqueFunc(results, completionValue)
}

// completeTeams returns team name completions.
// Tries plugin first, falls back to config teams + team_aliases.
func completeTeams(cfg *Config) []string {
	if cfg == nil {
		return nil
	}

	results := tryPluginComplete(cfg, "teams")
	if results == nil {
		// Fall back to config teams
		results = xmaps.Keys(cfg.Teams)
	}

	for alias, target := range cfg.TeamAliases {
		results = append(results, alias+tab+target)
	}

	results = xslices.UniqueFunc(results, completionValue)
	xslices.SortNatural(results)
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

	var results []string
	for key, col := range defs {
		name := col.Name
		if name == "" {
			name = key
		}
		results = append(results, name)
	}

	results = xslices.Unique(results)
	xslices.SortNatural(results)
	return results
}
