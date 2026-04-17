package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gechr/clog"
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
	keyAuthors               = "authors"
	keyDefaultAuthors        = "default.authors"
	keyDefaultBots           = "default.bots"
	keyDefaultLimit          = "default.limit"
	keyDefaultMatch          = "default.match"
	keyDefaultMergeMethod    = "default.merge_method"
	keyDefaultOwners         = "default.owners"
	keyDefaultOutput         = "default.output"
	keyDefaultReverse        = "default.reverse"
	keyDefaultSort           = "default.sort"
	keyDefaultState          = "default.state"
	keyPlugin                = "plugin"
	keyIgnoredOwners         = "ignored_owners"
	keyTeamAliases           = "team_aliases"
	keyTeams                 = "teams"
	keySpinnerStyle          = "spinner.style"
	keySpinnerColors         = "spinner.colors"
	keyTUIAutoRefresh        = "tui.refresh.enabled"
	keyTUIReviewDefaultEff   = "tui.review.default.effort"
	keyTUIReviewDefaultModel = "tui.review.default.model"
	keyTUIReviewDefaultProv  = "tui.review.default.provider"
	keyTUIReviewClaudePrompt = "tui.review.providers.claude.prompt"
	keyTUIReviewCodexPrompt  = "tui.review.providers.codex.prompt"
	keyTUIReviewGeminiPrompt = "tui.review.providers.gemini.prompt"
	keyTUIFilterArchived     = "tui.filters.archived"
	keyTUIFilterBots         = "tui.filters.bots"
	keyTUIFilterCI           = "tui.filters.ci"
	keyTUIFilterDraft        = "tui.filters.draft"
	keyTUIFilterReview       = "tui.filters.review"
	keyTUIFilterState        = "tui.filters.state"
	keyTUIScreenRepair       = "tui.screen_repair"
	keyTUISortKey            = "tui.sort.key"
	keyTUISortOrder          = "tui.sort.order"
	keyVCS                   = "vcs"
)

// Defaults holds default values that can be overridden by CLI flags.
type Defaults struct {
	Authors     []string `koanf:"authors"`
	Bots        bool     `koanf:"bots"`
	Limit       int      `koanf:"limit"`
	Match       string   `koanf:"match"`
	MergeMethod string   `koanf:"merge_method"`
	Owners      []string `koanf:"owners"`
	Output      string   `koanf:"output"`
	Reverse     bool     `koanf:"reverse"`
	Sort        string   `koanf:"sort"`
	State       string   `koanf:"state"`
}

// SpinnerConfig holds spinner style configuration.
type SpinnerConfig struct {
	Style  string   `koanf:"style"`
	Colors []string `koanf:"colors"`
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

type TUIReviewProviderConfig struct {
	Prompt  string   `koanf:"prompt"`
	Models  []string `koanf:"models"`
	Efforts []string `koanf:"efforts"`
}

type TUIReviewProvidersConfig struct {
	Claude TUIReviewProviderConfig `koanf:"claude"`
	Codex  TUIReviewProviderConfig `koanf:"codex"`
	Gemini TUIReviewProviderConfig `koanf:"gemini"`
}

type TUIReviewDefaultConfig struct {
	Provider string `koanf:"provider"`
	Model    string `koanf:"model"`
	Effort   string `koanf:"effort"`
}

type TUIReviewConfig struct {
	Default   TUIReviewDefaultConfig   `koanf:"default"`
	Enabled   []string                 `koanf:"enabled"`
	Providers TUIReviewProvidersConfig `koanf:"providers"`
}

func (c TUIReviewConfig) providerConfig(provider reviewProvider) TUIReviewProviderConfig {
	switch provider {
	case reviewProviderCodex:
		return c.Providers.Codex
	case reviewProviderGemini:
		return c.Providers.Gemini
	case reviewProviderClaude, reviewProviderUnknown:
		return c.Providers.Claude
	}
	return c.Providers.Claude
}

// TUIConfig holds TUI-specific configuration.
type TUIConfig struct {
	AutoRefresh  TUIAutoRefreshConfig `koanf:"refresh"`
	Review       TUIReviewConfig      `koanf:"review"`
	Filters      TUIFiltersConfig     `koanf:"filters"`
	Sort         TUISortConfig        `koanf:"sort"`
	ScreenRepair bool                 `koanf:"screen_repair"`
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

	// Plugin binary for completions and resolution (name or path).
	// If empty, prl auto-discovers prl-plugin-* on PATH.
	Plugin string `koanf:"plugin"`

	// Owner exclusion
	IgnoredOwners []string `koanf:"ignored_owners"`

	// Team aliases
	TeamAliases map[string]string `koanf:"team_aliases"`

	// Teams maps team names to lists of GitHub usernames.
	Teams map[string][]string `koanf:"teams"`

	// Author display names (github_username -> Display Name)
	Authors map[string]string `koanf:"authors"`
}

func defaultConfig() map[string]any {
	return map[string]any{
		keyAuthors:            map[string]string{},
		keyVCS:                vcsGit,
		keyDefaultAuthors:     []string{valueAtMe},
		keyDefaultBots:        true,
		keyDefaultLimit:       defaultLimit,
		keyDefaultMatch:       "title",
		keyDefaultMergeMethod: "squash",
		keyDefaultOwners:      []string{},
		keyDefaultOutput:      valueTable,
		keyDefaultReverse:     false,
		keyDefaultSort:        valueName,
		keyDefaultState:       valueOpen,
		keyPlugin:             "",
		keyIgnoredOwners:      []string{},
		keyTeamAliases:        map[string]string{},
		keyTeams:              map[string][]string{},
		keySpinnerStyle:       defaultSpinner,
		keyTUIAutoRefresh:     true,
		keyTUIReviewDefaultEff: defaultReviewEffort(
			nil,
			defaultReviewProvider,
			defaultReviewModel(nil, defaultReviewProvider),
		),
		keyTUIReviewDefaultProv:  string(defaultReviewProvider),
		keyTUIReviewDefaultModel: defaultReviewModel(nil, defaultReviewProvider),
		keyTUIReviewClaudePrompt: defaultReviewPromptTemplate(reviewProviderClaude),
		keyTUIReviewCodexPrompt:  defaultReviewPromptTemplate(reviewProviderCodex),
		keyTUIReviewGeminiPrompt: defaultReviewPromptTemplate(reviewProviderGemini),
		keyTUIScreenRepair:       false,
		keyTUIFilterArchived:     nil,
		keyTUIFilterBots:         nil,
		keyTUIFilterCI:           "",
		keyTUIFilterDraft:        nil,
		keyTUIFilterReview:       "",
		keyTUIFilterState:        "",
		keyTUISortKey:            "",
		keyTUISortOrder:          "",
		keySpinnerColors:         defaultSpinnerColors,
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
	if strings.EqualFold(k.String(keyDefaultOutput), "slack") {
		clog.Warn().Msg(
			`default.output=slack has been removed; using "table" (use --send to post via plugin)`,
		)
		if err := k.Set(keyDefaultOutput, valueTable); err != nil {
			return nil, fmt.Errorf("migrating default.output: %w", err)
		}
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
	if len(cfg.TUI.Review.Enabled) > 0 {
		normalized := make([]string, 0, len(cfg.TUI.Review.Enabled))
		seen := map[string]struct{}{}
		for _, raw := range cfg.TUI.Review.Enabled {
			provider := normalizeReviewProvider(raw)
			if provider == reviewProviderUnknown {
				return nil, fmt.Errorf("invalid tui.review.enabled provider %q", raw)
			}
			name := string(provider)
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			normalized = append(normalized, name)
		}
		cfg.TUI.Review.Enabled = normalized
	}
	provider := normalizeReviewProvider(cfg.TUI.Review.Default.Provider)
	if provider == reviewProviderUnknown {
		return nil, fmt.Errorf(
			"invalid tui.review.default.provider %q",
			cfg.TUI.Review.Default.Provider,
		)
	}
	if !isChoiceValue(reviewProviderChoices(&cfg), string(provider)) &&
		cfg.TUI.Review.Default.Provider == string(defaultReviewProvider) {
		provider = configuredReviewProvider(&cfg)
	}
	if !isChoiceValue(reviewProviderChoices(&cfg), string(provider)) {
		return nil, fmt.Errorf(
			"invalid tui.review.default.provider %q",
			cfg.TUI.Review.Default.Provider,
		)
	}
	cfg.TUI.Review.Default.Provider = string(provider)

	for _, provider := range []reviewProvider{
		reviewProviderClaude,
		reviewProviderCodex,
		reviewProviderGemini,
	} {
		pc := cfg.TUI.Review.providerConfig(provider)
		if len(pc.Models) == 1 && pc.Models[0] == "" {
			pc.Models = nil
		}
		if len(pc.Efforts) == 1 && pc.Efforts[0] == "" {
			pc.Efforts = nil
		}
		switch provider {
		case reviewProviderClaude:
			cfg.TUI.Review.Providers.Claude.Models = pc.Models
			cfg.TUI.Review.Providers.Claude.Efforts = pc.Efforts
		case reviewProviderCodex:
			cfg.TUI.Review.Providers.Codex.Models = pc.Models
			cfg.TUI.Review.Providers.Codex.Efforts = pc.Efforts
		case reviewProviderGemini:
			cfg.TUI.Review.Providers.Gemini.Models = pc.Models
			cfg.TUI.Review.Providers.Gemini.Efforts = pc.Efforts
		case reviewProviderUnknown:
			// The provider list above excludes unknown values.
		}
	}

	model := cfg.TUI.Review.Default.Model
	if model == "" {
		model = defaultReviewModel(&cfg, provider)
	}
	if !isValidReviewModel(&cfg, provider, model) &&
		model == defaultReviewModel(nil, defaultReviewProvider) {
		model = defaultReviewModel(&cfg, provider)
	}
	if !isValidReviewModel(&cfg, provider, model) {
		return nil, fmt.Errorf(
			"invalid tui.review.default.model %q for provider %q",
			cfg.TUI.Review.Default.Model,
			provider,
		)
	}
	cfg.TUI.Review.Default.Model = model

	effort := cfg.TUI.Review.Default.Effort
	if effort == "" {
		effort = defaultReviewEffort(&cfg, provider, model)
	}
	if effort != "" && !isValidReviewEffort(&cfg, provider, model, effort) &&
		effort == defaultReviewEffort(
			nil,
			defaultReviewProvider,
			defaultReviewModel(nil, defaultReviewProvider),
		) {
		effort = defaultReviewEffort(&cfg, provider, model)
	}
	if effort != "" && !isValidReviewEffort(&cfg, provider, model, effort) {
		return nil, fmt.Errorf(
			"invalid tui.review.default.effort %q for provider %q model %q",
			cfg.TUI.Review.Default.Effort,
			provider,
			model,
		)
	}
	cfg.TUI.Review.Default.Effort = effort

	if err := validateReviewPromptTemplate(cfg.TUI.Review.Providers.Claude.Prompt); err != nil {
		return nil, fmt.Errorf("invalid tui.review.providers.claude.prompt: %w", err)
	}
	if err := validateReviewPromptTemplate(cfg.TUI.Review.Providers.Codex.Prompt); err != nil {
		return nil, fmt.Errorf("invalid tui.review.providers.codex.prompt: %w", err)
	}
	if err := validateReviewPromptTemplate(cfg.TUI.Review.Providers.Gemini.Prompt); err != nil {
		return nil, fmt.Errorf("invalid tui.review.providers.gemini.prompt: %w", err)
	}

	// Validate VCS
	switch strings.ToLower(cfg.VCS) {
	case vcsGit, vcsJJ:
		cfg.VCS = strings.ToLower(cfg.VCS)
	default:
		return nil, fmt.Errorf("invalid vcs %q (expected %q or %q)", cfg.VCS, vcsGit, vcsJJ)
	}

	return &cfg, nil
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
	base := strings.TrimRight(f.String(), nl)
	sectionBody := strings.TrimRight(string(section), nl)
	out := sectionBody
	if base != "" {
		out = base + nl + nl + sectionBody
	}

	//nolint:gosec // config file, not sensitive
	return os.WriteFile(cp, []byte(withSingleTrailingNewline(out)), 0o644)
}

func marshalYAMLValue(value any) (string, error) {
	encoded, err := goyaml.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(encoded), nl), nil
}

func withSingleTrailingNewline(content string) string {
	return strings.TrimRight(content, nl) + nl
}

func indentBlock(content string, indent int) string {
	prefix := strings.Repeat(" ", indent)
	lines := strings.Split(content, nl)
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, nl)
}

// mergeIntoAncestor finds the top-level ancestor of a dotted key in the YAML
// AST, unmarshals it, deep-sets the missing suffix, re-marshals only that
// top-level subtree, and splices it back via ReplaceWithReader. The rest of
// the document's comments and formatting are left untouched.
//
// We only replace at depth=1 (top-level keys) because ReplaceWithReader does
// not re-indent replacement content to match the column offset of the replaced
// node - operating on nested nodes produces progressively wrong indentation.
func mergeIntoAncestor(f *goyamlast.File, key string, value any) bool {
	parts := strings.Split(key, ".")
	if len(parts) < 2 { //nolint:mnd // need at least key + one ancestor
		return false
	}

	ancestorPath, pErr := goyaml.PathString("$." + parts[0])
	if pErr != nil {
		return false
	}
	if _, aErr := ancestorPath.ReadNode(f); aErr != nil {
		return false
	}

	// Top-level ancestor exists. Unmarshal the full document, set the target
	// key, extract the top-level ancestor's subtree, and replace only that node.
	var docMap map[string]any
	if uErr := goyaml.Unmarshal([]byte(f.String()), &docMap); uErr != nil {
		return false
	}
	if docMap == nil {
		docMap = make(map[string]any)
	}

	// Deep-set the full dotted path into the decoded document.
	cur := docMap
	for i, seg := range parts {
		if i == len(parts)-1 {
			cur[seg] = value
		} else {
			if next, ok := cur[seg].(map[string]any); ok {
				cur = next
			} else {
				next := make(map[string]any)
				cur[seg] = next
				cur = next
			}
		}
	}

	ancestorMap, ok := docMap[parts[0]].(map[string]any)
	if !ok {
		return false
	}

	updated, mErr := goyaml.Marshal(ancestorMap)
	if mErr != nil {
		return false
	}
	if rErr := ancestorPath.ReplaceWithReader(
		f,
		strings.NewReader(string(updated)),
	); rErr != nil {
		return false
	}
	return true
}

var defaultConfigYAML = func() string {
	const promptBlockIndent = 10

	return fmt.Sprintf(`# prl configuration
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

  # Limit searches to specific GitHub owners (organizations or users).
  # Examples:
  #   owners: ["my-org"]
  #   owners: ["org-a", "org-b"]
  owners: []

  # Output format for results.
  # Options: table, url, bullet, json, repo
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

  review:
    # Optional: limit or reorder the available review providers.
    # enabled: [claude, codex, gemini]
    default:
      # Default AI review provider and model.
      # Providers: claude, codex, gemini
      provider: claude
      model: sonnet
      effort: medium

    providers:
      claude:
        # Optional overrides for the available model/effort choices.
        # If omitted, prl uses the built-in Claude review options.
        # models: [sonnet, opus]
        # efforts: [low, medium, high, xhigh, max, auto]
        # Default prompt for Claude AI review.
        # Available placeholders:
        #   %[10]s
        prompt: |
%[9]s

      codex:
        # Optional overrides for the available model/effort choices.
        # If omitted, prl uses the built-in Codex review options.
        # models: [gpt-5.4, gpt-5.4-mini, gpt-5.3-codex]
        # efforts: [low, medium, high, xhigh]
        # Default prompt for Codex AI review.
        # Available placeholders:
        #   %[10]s
        prompt: |
%[11]s

      gemini:
        # Optional overrides for the available model/effort choices.
        # If omitted, prl uses the built-in Gemini review options.
        # models: [gemini-3.1-pro, gemini-3-pro, gemini-2.5-flash]
        # efforts:
        #   Gemini 3: [low, medium, high]
        #   Gemini 2.5 Flash budgets: [0, 1024, 8192, 24576, dynamic]
        # Default prompt for Gemini AI review.
        # Available placeholders:
        #   %[10]s
        prompt: |
%[12]s

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

# Plugin binary for completions and resolution.
# If set, prl invokes this binary for --author/--team/-R/--topic completions
# and for resolving --team to GitHub usernames.
# If omitted, prl auto-discovers any prl-plugin-* binary on PATH.
# Example: plugin: acme
plugin: ""

# Owners to exclude from results.
# Example: ignored_owners: ["archived-org", "old-org"]
ignored_owners: []

# Map GitHub usernames to display names for prettier output.
# Example:
#   authors:
#     octocat: Mona Lisa
#     hubot: Hubot Bot
authors: {}

# Map team names to lists of GitHub usernames for --team resolution.
# Example:
#   teams:
#     ops: [alice, bob, charlie]
#     frontend: [dave, eve]
teams: {}

# Short aliases for team slugs, usable with --team.
# Example:
#   team_aliases:
#     fe: my-org/frontend
#     be: my-org/backend
team_aliases: {}
`,
		valueAtMe,
		defaultLimit,
		valueTable,
		valueName,
		valueOpen,
		vcsGit,
		defaultSpinner,
		`"`+strings.Join(defaultSpinnerColors, `", "`)+`"`,
		indentBlock(defaultReviewPromptTemplate(reviewProviderClaude), promptBlockIndent),
		"`{prNumber}`, `{repo}`, `{owner}`, `{ownerWithRepo}`, `{prURL}`, `{prRef}`, `{title}`",
		indentBlock(defaultReviewPromptTemplate(reviewProviderCodex), promptBlockIndent),
		indentBlock(defaultReviewPromptTemplate(reviewProviderGemini), promptBlockIndent),
	)
}()

func initConfig() error {
	cp, err := configPath()
	if err != nil {
		return fmt.Errorf("determining config path: %w", err)
	}

	if _, err := os.Stat(cp); err == nil {
		clog.Warn().Path("path", cp).Msg("Config already exists")
		return errOK
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
