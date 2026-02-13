package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gechr/clib/shell"
	"github.com/gechr/clog"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const envPrefix = "PRL_"

// Configuration key constants.
const (
	keyAuthors              = "authors"
	keyCodeDir              = "code_dir"
	keyDefaultAuthors       = "default.authors"
	keyDefaultBots          = "default.bots"
	keyDefaultLimit         = "default.limit"
	keyDefaultMatch         = "default.match"
	keyDefaultMergeMethod   = "default.merge_method"
	keyDefaultOrganizations = "default.organizations"
	keyDefaultOutput        = "default.output"
	keyDefaultReverse       = "default.reverse"
	keyDefaultSort          = "default.sort"
	keyDefaultState         = "default.state"
	keyIgnoredOrganizations = "ignored_organizations"
	keySlackRecipients      = "output.slack.recipients"
	keySlackSkipRepos       = "output.slack.skip_repos"
	keySlackTwoApprover     = "output.slack.two_approver_repos"
	keyTeamAliases          = "team_aliases"
	keyTerraformMemberDir   = "terraform_membership_dir"
	keyTerraformRepoDir     = "terraform_repository_dir"
	keyVCS                  = "vcs"
)

// Defaults holds default values that can be overridden by CLI flags.
type Defaults struct {
	Authors       []string `koanf:"authors"`
	Bots          bool     `koanf:"bots"`
	Limit         int      `koanf:"limit"`
	Match         string   `koanf:"match"`
	MergeMethod   string   `koanf:"merge_method"`
	Organizations []string `koanf:"organizations"`
	Output        string   `koanf:"output"`
	Reverse       bool     `koanf:"reverse"`
	Sort          string   `koanf:"sort"`
	State         string   `koanf:"state"`
}

// OutputConfig holds output format configuration.
type OutputConfig struct {
	Slack SlackOutputConfig `koanf:"slack"`
}

// slackRecipients maps a Slack recipient (#channel, @user, or email) to a list
// of <org>/<repo> patterns (or "*" for the default) that route to it.
type slackRecipients map[string][]string

// SlackOutputConfig holds Slack output format configuration.
type SlackOutputConfig struct {
	Recipients       slackRecipients `koanf:"recipients"`
	SkipRepos        []string        `koanf:"skip_repos"`
	TwoApproverRepos []string        `koanf:"two_approver_repos"`
}

// Config holds all prl configuration.
type Config struct {
	// Defaults
	Default Defaults `koanf:"default"`

	// Clone settings
	VCS string `koanf:"vcs"`

	// Directory paths
	CodeDir                string `koanf:"code_dir"`
	TerraformRepositoryDir string `koanf:"terraform_repository_dir"`
	TerraformMemberDir     string `koanf:"terraform_membership_dir"`

	// Organization exclusion
	IgnoredOrganizations []string `koanf:"ignored_organizations"`

	// Output format configuration
	Output OutputConfig `koanf:"output"`

	// Team aliases
	TeamAliases map[string]string `koanf:"team_aliases"`

	// Author display names (github_username -> Display Name)
	Authors map[string]string `koanf:"authors"`
}

func defaultConfig() map[string]any {
	return map[string]any{
		keyAuthors:              map[string]string{},
		keyVCS:                  vcsGit,
		keyCodeDir:              "",
		keyDefaultAuthors:       []string{"@me"},
		keyDefaultBots:          true,
		keyDefaultLimit:         defaultLimit,
		keyDefaultMatch:         "title",
		keyDefaultMergeMethod:   "squash",
		keyDefaultOrganizations: []string{},
		keyDefaultOutput:        valueTable,
		keyDefaultReverse:       false,
		keyDefaultSort:          valueName,
		keyDefaultState:         valueOpen,
		keyIgnoredOrganizations: []string{},
		keySlackRecipients:      slackRecipients{},
		keySlackSkipRepos:       []string{},
		keySlackTwoApprover:     []string{},
		keyTeamAliases:          map[string]string{},
		keyTerraformMemberDir:   "",
		keyTerraformRepoDir:     "",
	}
}

// loadConfig loads configuration with the following priority:
// 1. Hardcoded defaults
// 2. YAML file at ~/.config/prl/config.yaml (optional)
// 3. PRL_* env vars
func loadConfig() (*Config, error) {
	k := koanf.New(".")

	// 1. Hardcoded defaults
	if err := k.Load(confmap.Provider(defaultConfig(), "."), nil); err != nil {
		return nil, fmt.Errorf("loading defaults: %w", err)
	}

	// 2. YAML config file (optional)
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		clog.Debug().Err(homeErr).Msg("Failed to determine home directory")
	}
	if home != "" {
		configPath := filepath.Join(home, ".config", "prl", "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			if err := k.Load(file.Provider(configPath), yaml.Parser()); err != nil {
				return nil, fmt.Errorf("loading config file %s: %w", configPath, err)
			}
		}
	}

	// 3. PRL_* env vars
	if err := k.Load(env.Provider(envPrefix, ".", func(s string) string {
		return strings.ToLower(strings.TrimPrefix(s, envPrefix))
	}), nil); err != nil {
		return nil, fmt.Errorf("loading environment variables: %w", err)
	}

	// Re-derive dependent paths if code_dir was set but terraform dirs were not.
	codeDir := shell.ExpandPath(k.String(keyCodeDir))
	if codeDir != "" {
		deriveTerraformDir(k, keyTerraformRepoDir, filepath.Join(codeDir, "tf-github"))
		deriveTerraformDir(k, keyTerraformMemberDir, filepath.Join(codeDir, "tf-membership-v2"))
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	// Validate defaults
	if cfg.Default.Limit <= 0 {
		return nil, fmt.Errorf("default.limit must be > 0, got %d", cfg.Default.Limit)
	}
	if _, ok := parseOutputFormat(cfg.Default.Output); !ok {
		return nil, fmt.Errorf("invalid default.output %q", cfg.Default.Output)
	}
	if _, ok := parseSortField(cfg.Default.Sort); !ok {
		return nil, fmt.Errorf("invalid default.sort %q", cfg.Default.Sort)
	}
	if _, ok := parsePRState(cfg.Default.State); !ok {
		return nil, fmt.Errorf("invalid default.state %q", cfg.Default.State)
	}
	if cfg.Default.Match != "" {
		switch cfg.Default.Match {
		case "title", "body", "comments":
		default:
			return nil, fmt.Errorf(
				"invalid default.match %q (expected title, body, or comments)",
				cfg.Default.Match,
			)
		}
	}

	// Expand ~ and $ENV in directory paths
	cfg.CodeDir = shell.ExpandPath(cfg.CodeDir)
	cfg.TerraformRepositoryDir = shell.ExpandPath(cfg.TerraformRepositoryDir)
	cfg.TerraformMemberDir = shell.ExpandPath(cfg.TerraformMemberDir)

	// Validate slack repo lists are in org/repo format
	for _, repo := range cfg.Output.Slack.SkipRepos {
		if strings.Count(repo, "/") != 1 {
			return nil, fmt.Errorf(
				"output.slack.skip_repos: %q must be in <org>/<repo> format",
				repo,
			)
		}
	}
	for _, repo := range cfg.Output.Slack.TwoApproverRepos {
		if strings.Count(repo, "/") != 1 {
			return nil, fmt.Errorf(
				"output.slack.two_approver_repos: %q must be in <org>/<repo> format",
				repo,
			)
		}
	}
	normalizedRecipients := make(slackRecipients, len(cfg.Output.Slack.Recipients))
	for channel, repos := range cfg.Output.Slack.Recipients {
		for _, repo := range repos {
			if repo != "*" && strings.Count(repo, "/") != 1 {
				return nil, fmt.Errorf(
					"output.slack.recipients: repo %q must be \"*\" or in <org>/<repo> format",
					repo,
				)
			}
		}
		normalizedRecipients[normalizeSlackChannel(channel)] = repos
	}
	cfg.Output.Slack.Recipients = normalizedRecipients

	// Validate VCS
	switch strings.ToLower(cfg.VCS) {
	case vcsGit, vcsJJ:
		cfg.VCS = strings.ToLower(cfg.VCS)
	default:
		return nil, fmt.Errorf("invalid vcs %q (expected %q or %q)", cfg.VCS, vcsGit, vcsJJ)
	}

	return &cfg, nil
}

// deriveTerraformDir sets a terraform directory key to a default value if it is not already set.
func deriveTerraformDir(k *koanf.Koanf, key, defaultPath string) {
	if k.String(key) != "" {
		return
	}
	if err := k.Load(confmap.Provider(map[string]any{key: defaultPath}, "."), nil); err != nil {
		clog.Debug().Err(err).Str("key", key).Msg("Failed to set derived terraform dir")
	}
}

// resolveTeamAlias resolves a team alias to its full name.
func (c *Config) resolveTeamAlias(team string) string {
	lower := strings.ToLower(team)
	if full, ok := c.TeamAliases[lower]; ok {
		return full
	}
	return team
}
