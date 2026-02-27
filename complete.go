package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

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
func completeAuthors(cfg *Config) []string {
	var results []string

	results = append(results, "@me\tCurrent user")
	results = append(results, "all\tAll authors")

	if cfg == nil {
		return results
	}

	// HCL names (active users)
	nameMap, err := parseUsersHCLNames(cfg)
	if err == nil {
		usernames := make([]string, 0, len(nameMap))
		for gh := range nameMap {
			usernames = append(usernames, gh)
		}
		sort.Strings(usernames)

		for _, gh := range usernames {
			results = append(results, gh+"\t"+nameMap[gh])
		}
	}

	// Config authors (departed/extra mappings)
	if len(cfg.Authors) > 0 {
		var configUsers []string
		for username := range cfg.Authors {
			configUsers = append(configUsers, username)
		}
		sort.Strings(configUsers)

		hclSet := make(map[string]bool, len(nameMap))
		for gh := range nameMap {
			hclSet[gh] = true
		}

		for _, username := range configUsers {
			if !hclSet[strings.ToLower(username)] {
				name := cfg.Authors[username]
				if strings.EqualFold(name, BotName) {
					name += " 🤖"
				} else {
					name += " 💀"
				}
				results = append(results, username+"\t"+name)
			}
		}
	}

	return results
}

// completeTeams returns team name completions.
func completeTeams(cfg *Config) []string {
	if cfg == nil || cfg.TerraformMemberDir == "" {
		return nil
	}

	modules, err := parseGroupModules(cfg.TerraformMemberDir)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var results []string

	for _, m := range modules {
		if !seen[m.Name] {
			seen[m.Name] = true
			results = append(results, m.Name)
		}
	}

	// Add team aliases
	for alias, target := range cfg.TeamAliases {
		if !seen[alias] {
			seen[alias] = true
			results = append(results, alias+"\t"+target)
		}
	}

	sort.Strings(results)
	return results
}

// completeRepositories returns repository name completions.
func completeRepositories(cfg *Config) []string {
	if cfg == nil || cfg.TerraformRepositoryDir == "" {
		return nil
	}

	pattern := filepath.Join(cfg.TerraformRepositoryDir, "*.tf")
	tfFiles, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	modules, err := parseRepoModules(tfFiles)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var results []string

	for _, m := range modules {
		if !seen[m.Name] {
			seen[m.Name] = true
			results = append(results, m.Name)
		}
	}

	sort.Strings(results)
	return results
}

// completeTopics returns topic completions.
func completeTopics(cfg *Config) []string {
	if cfg == nil || cfg.TerraformRepositoryDir == "" {
		return nil
	}

	var files []string
	for _, f := range []string{"main.tf", "sg2.tf"} {
		files = append(files, filepath.Join(cfg.TerraformRepositoryDir, f))
	}

	modules, err := parseRepoModules(files)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	seen["sg2"] = true // Always include sg2 as a special topic

	for _, m := range modules {
		topicsAttr, ok := m.Body.Attributes["topics"]
		if !ok {
			continue
		}
		srcRange := topicsAttr.SrcRange
		topicsSrc := string(m.Src[srcRange.Start.Byte:srcRange.End.Byte])
		for part := range strings.SplitSeq(topicsSrc, `"`) {
			topic := strings.TrimSpace(part)
			if topic != "" && !strings.ContainsAny(topic, "[]={},\n\t ") {
				seen[topic] = true
			}
		}
	}

	var results []string
	for topic := range seen {
		results = append(results, topic)
	}

	sort.Strings(results)
	return results
}

// completeSlackRecipients returns Slack recipient completions: channel names
// from the recipients config, plus email addresses from users.tf.
func completeSlackRecipients(cfg *Config) []string {
	if cfg == nil {
		return nil
	}

	var results []string

	// Channel completions from recipients config.
	for channel := range cfg.Output.Slack.Recipients {
		// Strip the leading "#" - normalizeSlackChannel adds it automatically.
		// Keep "@" for user mentions since that prefix is meaningful.
		display := strings.TrimPrefix(channel, "#")
		results = append(results, display+"\tChannel")
	}

	// Email completions from users.tf (e.g. "george@figment.io\tGeorge Henderson").
	{
		emailMap, err := parseUsersHCLEmails(cfg)
		nameMap, _ := parseUsersHCLNames(cfg)
		if err == nil {
			usernames := make([]string, 0, len(emailMap))
			for gh := range emailMap {
				usernames = append(usernames, gh)
			}
			sort.Strings(usernames)
			for _, gh := range usernames {
				email := emailMap[gh]
				desc := nameMap[gh]
				if desc == "" {
					desc = email
				}
				results = append(results, email+"\t"+desc)
			}
		}
	}

	sort.Strings(results)
	return results
}

// completeColumns returns column name completions.
func (p *prl) completeColumns() []string {
	defs := p.allColumnDefs("", nil)

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
