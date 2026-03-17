package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gechr/clog"
	"github.com/gechr/prl/internal/shell"
	goyaml "github.com/goccy/go-yaml"
	goyamlast "github.com/goccy/go-yaml/ast"
	goyamlparser "github.com/goccy/go-yaml/parser"
	koanfyaml "github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const envPrefix = "PRL_"

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "prl", "config.yaml"), nil
}

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
	keySpinnerStyle         = "spinner.style"
	keySpinnerColors        = "spinner.colors"
	keyTUIAutoRefresh       = "tui.refresh.enabled"
	keyTUIFilterArchived    = "tui.filters.archived"
	keyTUIFilterBots        = "tui.filters.bots"
	keyTUIFilterCI          = "tui.filters.ci"
	keyTUIFilterDraft       = "tui.filters.draft"
	keyTUIFilterReview      = "tui.filters.review"
	keyTUIFilterState       = "tui.filters.state"
	keyTUISortKey           = "tui.sort.key"
	keyTUISortOrder         = "tui.sort.order"
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

// SpinnerConfig holds spinner style configuration.
type SpinnerConfig struct {
	Style  string   `koanf:"style"`
	Colors []string `koanf:"colors"`
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

// TUIAutoRefreshConfig holds auto-refresh settings.
type TUIAutoRefreshConfig struct {
	Enabled bool `koanf:"enabled"`
}

// TUISortConfig holds sort settings persisted between TUI runs.
type TUISortConfig struct {
	Key   string `koanf:"key"`
	Order string `koanf:"order"`
}

// TUIFiltersConfig holds persisted filter overrides for TUI mode.
type TUIFiltersConfig struct {
	State    string `koanf:"state"`
	Draft    *bool  `koanf:"draft"`
	Bots     *bool  `koanf:"bots"`
	Archived *bool  `koanf:"archived"`
	CI       string `koanf:"ci"`
	Review   string `koanf:"review"`
}

// TUIConfig holds TUI-specific configuration.
type TUIConfig struct {
	AutoRefresh TUIAutoRefreshConfig `koanf:"refresh"`
	Filters     TUIFiltersConfig     `koanf:"filters"`
	Sort        TUISortConfig        `koanf:"sort"`
}

// Config holds all prl configuration.
type Config struct {
	// Defaults
	Default Defaults `koanf:"default"`

	// Clone settings
	VCS string `koanf:"vcs"`

	// Spinner style
	Spinner SpinnerConfig `koanf:"spinner"`

	// TUI settings
	TUI TUIConfig `koanf:"tui"`

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
		keyDefaultAuthors:       []string{valueAtMe},
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
		keySpinnerStyle:         defaultSpinner,
		keyTUIAutoRefresh:       true,
		keyTUIFilterArchived:    nil,
		keyTUIFilterBots:        nil,
		keyTUIFilterCI:          "",
		keyTUIFilterDraft:       nil,
		keyTUIFilterReview:      "",
		keyTUIFilterState:       "",
		keyTUISortKey:           "",
		keyTUISortOrder:         "",
		keySpinnerColors:        defaultSpinnerColors,
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
	if cp, cpErr := configPath(); cpErr != nil {
		clog.Debug().Err(cpErr).Msg("Failed to determine config path")
	} else if _, statErr := os.Stat(cp); statErr == nil {
		if err := k.Load(file.Provider(cp), koanfyaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading config file %s: %w", cp, err)
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

	// Validate tui.filters.*
	if cfg.TUI.Filters.State != "" {
		if _, ok := parsePRState(cfg.TUI.Filters.State); !ok {
			return nil, fmt.Errorf("invalid tui.filters.state %q", cfg.TUI.Filters.State)
		}
	}
	if cfg.TUI.Filters.CI != "" {
		if _, ok := parseCIStatus(cfg.TUI.Filters.CI); !ok {
			return nil, fmt.Errorf("invalid tui.filters.ci %q", cfg.TUI.Filters.CI)
		}
	}
	if cfg.TUI.Filters.Review != "" {
		switch cfg.TUI.Filters.Review {
		case valueReviewFilterNone,
			valueReviewFilterRequired,
			valueReviewFilterApproved,
			valueReviewFilterChanges:
		default:
			return nil, fmt.Errorf("invalid tui.filters.review %q", cfg.TUI.Filters.Review)
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

// saveConfigKey reads the config file, sets a dotted key (e.g. "tui.refresh.enabled")
// to the given value, and writes it back preserving comments and formatting.
func saveConfigKey(key string, value any) error {
	cp, err := configPath()
	if err != nil {
		return err
	}

	// Resolve symlinks so we write to the actual file.
	if resolved, evalErr := filepath.EvalSymlinks(cp); evalErr == nil {
		cp = resolved
	}

	// Read existing file, or start empty.
	data, err := os.ReadFile(cp)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config: %w", err)
	}

	// Parse into an AST to preserve comments/formatting.
	f, err := goyamlparser.ParseBytes(data, goyamlparser.ParseComments)
	if err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}

	// Build the path from the dotted key.
	path, pathErr := goyaml.PathString("$." + key)
	if pathErr != nil {
		return fmt.Errorf("building path: %w", pathErr)
	}
	// Try to read the existing value - if it fails, the key doesn't exist yet.
	_, readErr := path.ReadNode(f)
	if readErr == nil {
		// Key exists - replace in-place.
		replacement, marshalErr := marshalYAMLValue(value)
		if marshalErr != nil {
			return fmt.Errorf("marshalling key %s: %w", key, marshalErr)
		}
		if replaceErr := path.ReplaceWithReader(
			f,
			strings.NewReader(replacement),
		); replaceErr != nil {
			return fmt.Errorf("replacing key %s: %w", key, replaceErr)
		}

		//nolint:gosec // config file, not sensitive
		return os.WriteFile(cp, []byte(withSingleTrailingNewline(f.String())), 0o644)
	}

	if merged := mergeIntoAncestor(f, key, value); merged {
		//nolint:gosec // config file, not sensitive
		return os.WriteFile(cp, []byte(withSingleTrailingNewline(f.String())), 0o644)
	}

	// No ancestor found - append as a new top-level section.
	parts := strings.Split(key, ".")
	nested := make(map[string]any)
	cur := nested
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
		} else {
			next := make(map[string]any)
			cur[p] = next
			cur = next
		}
	}
	section, mErr := goyaml.Marshal(nested)
	if mErr != nil {
		return fmt.Errorf("marshalling new key: %w", mErr)
	}
	base := strings.TrimRight(f.String(), "\n")
	sectionBody := strings.TrimRight(string(section), "\n")
	out := sectionBody
	if base != "" {
		out = base + "\n\n" + sectionBody
	}

	//nolint:gosec // config file, not sensitive
	return os.WriteFile(cp, []byte(withSingleTrailingNewline(out)), 0o644)
}

func marshalYAMLValue(value any) (string, error) {
	encoded, err := goyaml.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(encoded), "\n"), nil
}

func withSingleTrailingNewline(content string) string {
	return strings.TrimRight(content, "\n") + "\n"
}

// mergeIntoAncestor finds the top-level ancestor of a dotted key in the YAML
// AST, unmarshals it, deep-sets the missing suffix, re-marshals only that
// top-level subtree, and splices it back via ReplaceWithReader. The rest of
// the document's comments and formatting are left untouched.
//
// We only replace at depth=1 (top-level keys) because ReplaceWithReader does
// not re-indent replacement content to match the column offset of the replaced
// node — operating on nested nodes produces progressively wrong indentation.
func mergeIntoAncestor(f *goyamlast.File, key string, value any) bool {
	parts := strings.Split(key, ".")
	for depth := len(parts) - 1; depth > 0; depth-- {
		prefix := strings.Join(parts[:depth], ".")
		ancestorPath, pErr := goyaml.PathString("$." + prefix)
		if pErr != nil {
			continue
		}
		if _, aErr := ancestorPath.ReadNode(f); aErr != nil {
			continue
		}

		// Ancestor exists. Rewrite the full document with the missing nested key
		// populated. This is less surgical than AST replacement, but it avoids
		// indentation bugs when introducing a new child mapping under an existing
		// ancestor (for example, adding tui.filters.* under an existing tui block).
		var existing map[string]any
		if uErr := goyaml.Unmarshal([]byte(f.String()), &existing); uErr != nil {
			return false
		}
		if existing == nil {
			existing = make(map[string]any)
		}

		// Deep-set the full dotted path into the decoded document.
		suffix := parts
		cur := existing
		for i, seg := range suffix {
			if i == len(suffix)-1 {
				cur[seg] = value
			} else {
				next := make(map[string]any)
				cur[seg] = next
				cur = next
			}
		}

		updated, mErr := goyaml.Marshal(existing)
		if mErr != nil {
			return false
		}

		parsed, pErr := goyamlparser.ParseBytes(updated, goyamlparser.ParseComments)
		if pErr != nil {
			return false
		}
		*f = *parsed
		return true
	}
	return false
}

var defaultConfigYAML = fmt.Sprintf(`# prl configuration
# See: prl --help

# Default query parameters applied when the corresponding flag is not set.
default:
  # Default authors to search for.
  # Use "%[1]s" for the authenticated user, or specify GitHub usernames.
  # Examples:
  #   authors: ["%[1]s"]
  #   authors: ["octocat", "hubot"]
  authors:
    - "%[1]s"

  # Whether to include PRs from bot accounts (e.g. dependabot, renovate).
  bots: true

  # Maximum number of results to return.
  limit: %[2]d

  # Restrict text search to a specific field.
  # Options: title, body, comments
  match: title

  # Merge method used for auto-merge.
  # Options: squash, merge, rebase
  merge_method: squash

  # Limit searches to specific GitHub organizations.
  # Examples:
  #   organizations: ["my-org"]
  #   organizations: ["org-a", "org-b"]
  organizations: []

  # Output format for results.
  # Options: table, url, bullet, slack, json, repo
  output: %[3]s

  # Show oldest results first (at the top).
  reverse: false

  # Sort order for results.
  # Options: name, created, updated
  sort: %[4]s

  # Filter by PR state.
  # Options: open, closed, merged, all
  state: %[5]s

# TUI (interactive browse) settings.
tui:
  refresh:
    # Automatically refresh results in the background.
    enabled: true

  # Persisted filter overrides for TUI mode.
  # Set via the filter menu (alt+f) in the TUI.
  # filters:
  #   state: merged
  #   draft: false
  #   bots: false

  sort:
    # Persisted sort column and direction.
    # Set by clicking column headers in the TUI.
    # key: title
    # order: asc

# VCS used for --clone.
# Options: git, jj
vcs: %[6]s

# Spinner displayed while fetching data.
spinner:
  # Animation style.
  # Options: dots, stars
  style: %[7]s

  # Colors for spinner frames (256-color palette).
  # Each frame cycles through these colors in order.
  colors: [%[8]s]

# Base directory for code repositories.
# Example: code_dir: ~/code/github
code_dir: ""

# Organizations to exclude from results.
# Example: ignored_organizations: ["archived-org", "old-org"]
ignored_organizations: []

# Map GitHub usernames to display names for prettier output.
# Example:
#   authors:
#     octocat: Mona Lisa
#     hubot: Hubot Bot
authors: {}

# Short aliases for team slugs, usable with --team.
# Example:
#   team_aliases:
#     fe: my-org/frontend
#     be: my-org/backend
team_aliases: {}

# Slack output configuration for --send.
output:
  slack:
    # Map GitHub usernames to Slack recipients.
    # Example:
    #   recipients:
    #     octocat: "@mona"
    #     hubot: "#bots"
    recipients: {}

    # Repos to exclude from Slack output.
    # Example: skip_repos: ["my-org/noisy-repo"]
    skip_repos: []

    # Repos that require two approvals (highlighted in Slack output).
    # Example: two_approver_repos: ["my-org/critical-service"]
    two_approver_repos: []
`,
	valueAtMe,
	defaultLimit,
	valueTable,
	valueName,
	valueOpen,
	vcsGit,
	defaultSpinner,
	`"`+strings.Join(defaultSpinnerColors, `", "`)+`"`,
)

func initConfig() error {
	cp, err := configPath()
	if err != nil {
		return fmt.Errorf("determining config path: %w", err)
	}

	if _, err := os.Stat(cp); err == nil {
		clog.Warn().Path("path", cp).Msg("Config already exists")
		return errFatal
	}

	if err := os.MkdirAll(
		filepath.Dir(cp),
		0o755,
	); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(cp, []byte(defaultConfigYAML), 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	clog.Info().Path("path", cp).Msg("Initialized default config")
	return nil
}

// resolveTeamAlias resolves a team alias to its full name.
func (c *Config) resolveTeamAlias(team string) string {
	lower := strings.ToLower(team)
	if full, ok := c.TeamAliases[lower]; ok {
		return full
	}
	return team
}
