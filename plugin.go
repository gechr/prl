package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gechr/clog"
)

const pluginTimeout = 5 * time.Second

var (
	errNoPluginAvailable    = errors.New("no plugin available")
	errPluginNotImplemented = errors.New("plugin not implemented")
)

// Plugin wraps an external plugin binary for completions and resolution.
// A nil Plugin is safe to use - all methods return fallback values.
type Plugin struct {
	path string
}

var (
	pluginMu    sync.Mutex
	pluginCache = make(map[pluginCacheKey]pluginDiscoveryResult)
)

type pluginCacheKey struct {
	configured string
	pathEnv    string
}

type pluginCandidate struct {
	name string
	path string
}

type pluginDiscoveryResult struct {
	plugin *Plugin
	err    error
}

// discoverPlugin finds the plugin binary. It checks the config first,
// then looks for any prl-plugin-* binary on PATH. Results are cached per config+PATH.
func discoverPlugin(cfg *Config) (*Plugin, error) {
	key := pluginCacheKey{pathEnv: os.Getenv("PATH")}
	if cfg != nil {
		key.configured = cfg.Plugin
	}

	pluginMu.Lock()
	if cached, ok := pluginCache[key]; ok {
		pluginMu.Unlock()
		return normalizeDiscoveredPlugin(cached.plugin, cached.err)
	}
	pluginMu.Unlock()

	plug, err := discoverPluginUncached(cfg)

	pluginMu.Lock()
	pluginCache[key] = pluginDiscoveryResult{plugin: plug, err: err}
	pluginMu.Unlock()

	return normalizeDiscoveredPlugin(plug, err)
}

func normalizeDiscoveredPlugin(plug *Plugin, err error) (*Plugin, error) {
	if errors.Is(err, errNoPluginAvailable) {
		return &Plugin{}, nil
	}
	return plug, err
}

func discoverPluginUncached(cfg *Config) (*Plugin, error) {
	// 1. Explicit config
	if cfg != nil && cfg.Plugin != "" {
		path, err := resolveConfiguredPluginPath(cfg.Plugin)
		if err != nil {
			return nil, err
		}
		clog.Debug().Str("path", path).Msg("Using configured plugin")
		return &Plugin{path: path}, nil
	}

	// 2. Convention: look for prl-plugin-* on PATH
	candidates := discoverPluginsOnPATH()
	switch len(candidates) {
	case 0:
		return nil, errNoPluginAvailable
	case 1:
		clog.Debug().Str("path", candidates[0].path).Msg("Discovered plugin on PATH")
		return &Plugin{path: candidates[0].path}, nil
	default:
		paths := make([]string, len(candidates))
		for i, candidate := range candidates {
			paths[i] = candidate.path
		}
		return nil, fmt.Errorf(
			"multiple prl-plugin-* plugins found on PATH (%s); set plugin in config",
			strings.Join(paths, ", "),
		)
	}
}

func resolveConfiguredPluginPath(configured string) (string, error) {
	if filepath.IsAbs(configured) {
		info, err := os.Stat(configured)
		if err != nil {
			return "", fmt.Errorf("configured plugin %q not found: %w", configured, err)
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("configured plugin %q is not executable", configured)
		}
		return configured, nil
	}

	for _, candidate := range configuredPluginCandidates(configured) {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("configured plugin %q not found", configured)
}

func configuredPluginCandidates(configured string) []string {
	if strings.ContainsRune(configured, os.PathSeparator) ||
		strings.HasPrefix(configured, "prl-plugin-") {
		return []string{configured}
	}
	return []string{"prl-plugin-" + configured, configured}
}

func discoverPluginsOnPATH() []pluginCandidate {
	pathDirs := filepath.SplitList(os.Getenv("PATH"))
	seen := make(map[string]pluginCandidate)

	for _, dir := range pathDirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "prl-plugin-") || entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue
			}

			full := filepath.Join(dir, name)
			canonical := canonicalPluginPath(full)
			seen[canonical] = pluginCandidate{name: name, path: full}
		}
	}

	candidates := make([]pluginCandidate, 0, len(seen))
	for _, candidate := range seen {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].name == candidates[j].name {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].name < candidates[j].name
	})

	return candidates
}

func canonicalPluginPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// Complete calls the plugin for tab completions.
// Returns nil if no plugin is available or the kind is not implemented.
func (p *Plugin) Complete(kind string) ([]string, error) {
	if p == nil || p.path == "" {
		return nil, nil
	}

	out, err := p.run("complete", kind)
	if err != nil {
		if errors.Is(err, errPluginNotImplemented) {
			return nil, nil
		}
		return nil, fmt.Errorf("plugin complete %s: %w", kind, err)
	}

	return parsePluginLines(out), nil
}

// Resolve calls the plugin for runtime resolution.
// Returns nil, nil if no plugin is available or the kind is not implemented (exit 1).
// Returns nil, error if the plugin fails (exit 2+).
func (p *Plugin) Resolve(kind, value string) ([]string, error) {
	if p == nil || p.path == "" {
		return nil, nil
	}

	out, err := p.run("resolve", kind, value)
	if err != nil {
		if errors.Is(err, errPluginNotImplemented) {
			return nil, nil
		}
		return nil, fmt.Errorf("plugin resolve %s %s: %w", kind, value, err)
	}

	return parsePluginLines(out), nil
}

// ResolveTeam resolves a team name to GitHub usernames.
// Tries the plugin first, then falls back to config teams.
func (p *Plugin) ResolveTeam(team string, cfg *Config) ([]string, error) {
	members, err := p.Resolve("team", team)
	if err != nil {
		return nil, err
	}
	if members != nil {
		return members, nil
	}

	// Fall back to config teams
	if cfg != nil {
		if m, ok := cfg.Teams[team]; ok {
			return m, nil
		}
	}

	return nil, nil
}

// ResolveTopic resolves a topic to repo names via the plugin.
// No config fallback - topics require a plugin.
func (p *Plugin) ResolveTopic(topic string) ([]string, error) {
	if p == nil || p.path == "" {
		return nil, errNoPluginAvailable
	}

	out, err := p.run("resolve", "topic", topic)
	if err != nil {
		if errors.Is(err, errPluginNotImplemented) {
			return nil, errPluginNotImplemented
		}
		return nil, fmt.Errorf("plugin resolve topic %s: %w", topic, err)
	}

	return parsePluginLines(out), nil
}

// run executes the plugin binary with the given arguments.
// Returns stdout on success. For exit code 1 (not implemented), it returns
// errPluginNotImplemented. For other failures, it returns an error with
// stderr context when available.
func (p *Plugin) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.path, args...) //nolint:gosec // path from plugin discovery
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	clog.Debug().Str("cmd", p.path).Strs("args", args).Msg("Running plugin")

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			clog.Warn().Str("cmd", p.path).Strs("args", args).Msg("Plugin timed out")
			return "", fmt.Errorf("plugin timed out after %s", pluginTimeout)
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			clog.Debug().Str("cmd", p.path).Strs("args", args).Msg("Plugin: not implemented")
			return "", errPluginNotImplemented
		}

		msg := strings.TrimSpace(stderr.String())
		clog.Warn().
			Str("cmd", p.path).
			Strs("args", args).
			Str("stderr", msg).
			Msg("Plugin failed")
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}

	return stdout.String(), nil
}

// Slack pipes JSON PRs to the plugin's slack subcommand.
// The plugin handles formatting, routing, and sending.
func (p *Plugin) Slack(prsJSON []byte, sendTo string) error {
	if p == nil || p.path == "" {
		return fmt.Errorf("--send requires a plugin (no prl-plugin-* binary found)")
	}

	args := []string{"slack"}
	if sendTo != "" {
		args = append(args, "--to", sendTo)
	}

	const slackTimeout = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), slackTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.path, args...) //nolint:gosec // path from plugin discovery
	cmd.Stdin = bytes.NewReader(prsJSON)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	clog.Debug().Str("cmd", p.path).Strs("args", args).Msg("Running plugin slack")

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("plugin slack: %s", msg)
		}
		return fmt.Errorf("plugin slack: %w", err)
	}

	return nil
}

// pluginSlackSend marshals PRs as JSON and sends them to Slack via the plugin.
func pluginSlackSend(cfg *Config, sendTo string, prs []PullRequest) error {
	prsJSON, err := json.Marshal(prs)
	if err != nil {
		return fmt.Errorf("marshalling PRs: %w", err)
	}

	plug, err := discoverPlugin(cfg)
	if err != nil {
		return err
	}
	return plug.Slack(prsJSON, sendTo)
}

// parsePluginLines splits plugin output into non-empty lines.
func parsePluginLines(output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	var lines []string
	for line := range strings.SplitSeq(output, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
