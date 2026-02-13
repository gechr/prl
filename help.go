package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	clib "github.com/gechr/clib/cli/kong"
	"github.com/gechr/clib/help"
)

// helpPrinter returns a Kong HelpPrinter that renders colored help output.
func (p *prl) helpPrinter(cfg *Config) kong.HelpPrinter {
	renderer := help.NewRenderer(p.theme)

	defaultAuthor := formatCSV(cfg.Default.Authors)
	if defaultAuthor == "" {
		defaultAuthor = valueAtMe
	}

	return clib.HelpPrinterFunc(renderer,
		clib.NodeSectionsFunc(clib.WithArguments(&CLI{})),
		help.WithFlagDefault("owner", formatCSV(cfg.Default.Owners)),
		help.WithFlagDefault("author", defaultAuthor),
		help.WithFlagDefault("limit", fmt.Sprintf("%d", cfg.Default.Limit)),
		help.WithHelpFlags("Print short help", "Print long help with examples"),
		help.WithLongHelp(os.Args,
			buildExamplesSection(),
			p.buildConfigurationSection(cfg),
		),
	)
}

func buildExamplesSection() help.Section {
	return help.Section{
		Title: "Examples",
		Content: []help.Content{
			help.Examples{
				{
					Comment: "List your open PRs",
					Command: "prl",
				},
				{
					Comment: "List all your PRs (open + closed + merged)",
					Command: "prl -s all",
				},
				{
					Comment: "List your open PRs in a specific repo",
					Command: "prl --repo owner/repo",
				},
				{
					Comment: "Search your open PRs matching 'golangci-lint'",
					Command: "prl golangci-lint",
				},
			},
		},
	}
}

func (p *prl) buildConfigurationSection(cfg *Config) help.Section {
	pluginName := cfg.Plugin
	if pluginName == "" {
		pluginName = "(auto-discover prl-plugin-* on PATH)"
	}

	return help.Section{
		Title: "Configuration",
		Content: []help.Content{
			help.Text("  " +
				p.theme.Magenta.Bold(true).Render("plugin") +
				"  External binary for completions and resolution" +
				p.theme.DimDefault(pluginName)),
			help.Text("  A plugin binary enables smart filtering:"),
			help.FlagGroup{
				{
					Long: "author",
					Desc: "Show real names via plugin or config authors",
				},
				{
					Long: "team",
					Desc: "Resolve team members via plugin or config teams",
				},
				{
					Long: "topic",
					Desc: "Resolve repo topics via plugin",
				},
			},
		},
	}
}
