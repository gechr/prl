# prl

A Swiss Army knife for GitHub pull requests.

## Install

### Homebrew

```sh
brew install gechr/tap/prl
```

### Go

```sh
go install github.com/gechr/prl@latest
```

Requires [`gh`](https://cli.github.com/) (installed and authenticated). Go `1.26+` if building from source.

## Quickstart

```sh
prl --init
```

This writes `~/.config/prl/config.yaml`. Edit it to set defaults such as owners, authors, output mode, plugin, and TUI behavior.

## Examples

Everything is flags plus optional free-text query terms - no subcommands.

```sh
# Your open PRs
prl

# All PRs across states
prl -s all

# PRs for a specific repo
prl --repo owner/repo

# PRs with "fix" in the title/body/comments
prl fix

# PRs created in the last 2 weeks
prl -c 2weeks

# PRs that have never been updated after creation
prl --drift 0

# Open an interactive TUI browser
prl --interactive

# Open the TUI with a slower refresh cadence for this run
prl --interactive --interval 30s

# Refresh results continuously
prl --watch

# Print only the total count
prl --count

# Approve selected PRs
prl --approve

# Enable auto-merge for your PRs
prl --merge -y

# Clone matching repos
prl --clone -y

# Send your PRs to Slack via plugin
prl --send

# Dry run: show the query without executing it
prl --dry

# JSON output
prl -o json
```

## Flags

### Filters

| Flag                     | Short | Description                                                 |
| ------------------------ | ----- | ----------------------------------------------------------- |
| `--owner`                | `-O`  | Limit to GitHub owner(s), comma-separated                   |
| `--repo`                 | `-R`  | Limit to a specific repo                                    |
| `--filter`               | `-f`  | Raw GitHub search qualifier (repeatable)                    |
| `--match`                |       | Restrict text search to `title`, `body`, or `comments`      |
| `--author`               | `-a`  | Filter by author(s), comma-separated                        |
| `--commenter`            |       | Filter by commenter                                         |
| `--no-bot`               | `-B`  | Exclude bot authors after fetch                             |
| `--team`                 | `-t`  | Filter by team authors via plugin or config                 |
| `--involves`             | `-I`  | Filter by involvement                                       |
| `--requested`            |       | Filter by requested reviewer                                |
| `--closed-by`            |       | Filter by who closed the PR                                 |
| `--merged-by`            |       | Filter by who merged the PR                                 |
| `--reviewed-by`          |       | Filter by reviewer                                          |
| `--ci`                   |       | Filter by CI status: `success`, `failure`, `pending`        |
| `--comments`             |       | Filter by comment count, for example `>5` or `10..20`       |
| `--language`             | `-l`  | Filter by language                                          |
| `--review`               | `-r`  | Filter by review status                                     |
| `--state`                | `-s`  | Filter by state: `open`, `closed`, `ready`, `merged`, `all` |
| `--topic`                | `-T`  | Filter by repo topic via plugin                             |
| `--created`              | `-c`  | Filter by creation date                                     |
| `--drift`                | `-d`  | Filter by gap between creation and last update              |
| `--updated`              | `-u`  | Filter by last updated date                                 |
| `--merged`               | `-m`  | Filter by merged date                                       |
| `--archived`             |       | Include archived repos                                      |
| `--draft` / `--no-draft` |       | Filter by draft state                                       |

### Interactive Actions

| Flag                     | Short | Description                                |
| ------------------------ | ----- | ------------------------------------------ |
| `--interactive`          | `-i`  | Launch the full-screen TUI browser         |
| `--interval`             |       | Override TUI auto-refresh interval         |
| `--approve`              |       | Approve each PR                            |
| `--close`                |       | Close each PR                              |
| `--copilot`              |       | Request Copilot review on each PR          |
| `--delete-branch`        |       | Delete the branch after close              |
| `--comment`              |       | Add a comment to each PR                   |
| `--edit`                 | `-e`  | Edit title and body of each PR             |
| `--mark-draft`           |       | Convert each PR to draft                   |
| `--mark-ready`           |       | Mark each PR ready for review              |
| `--merge` / `--no-merge` |       | Toggle auto-merge                          |
| `--force-merge`          | `-M`  | Poll for checks, then force-merge          |
| `--unsubscribe`          | `-U`  | Remove review request and unsubscribe      |
| `--update`               |       | Update each PR branch from its base branch |
| `--yes`                  | `-y`  | Skip the confirmation prompt               |

### Actions

| Flag        | Short | Description                                |
| ----------- | ----- | ------------------------------------------ |
| `--clone`   |       | Clone unique repos from the results        |
| `--copy`    | `-C`  | Copy rendered output to the clipboard      |
| `--count`   | `-N`  | Print only the total result count          |
| `--dry`     | `-n`  | Show the query without executing it        |
| `--open`    | `-P`  | Open each PR in the browser                |
| `--web`     | `-w`  | Open the GitHub search page in the browser |
| `--send`    |       | Send PRs to Slack via plugin               |
| `--send-to` |       | Override the Slack recipient               |

### Output

| Flag        | Short | Description                                 |
| ----------- | ----- | ------------------------------------------- |
| `--watch`   | `-W`  | Refresh output periodically                 |
| `--exit-0`  | `-0`  | Exit immediately when there are no results  |
| `--columns` |       | Custom table columns                        |
| `--limit`   | `-L`  | Maximum number of results                   |
| `--output`  | `-o`  | `table`, `url`, `bullet`, `json`, or `repo` |
| `--reverse` |       | Show oldest first                           |
| `--sort`    |       | Sort by `name`, `created`, or `updated`     |

### Miscellaneous

| Flag        | Short | Description                                         |
| ----------- | ----- | --------------------------------------------------- |
| `--init`    |       | Write the default config file                       |
| `--color`   |       | `auto`, `always`, or `never`                        |
| `--debug`   |       | Log HTTP requests to stderr                         |
| `--quick`   | `-Q`  | Skip enrichment such as merge status and auto-merge |
| `--verbose` | `-v`  | Enable verbose logging                              |
| `-h`        |       | Print short help                                    |
| `--help`    |       | Print long help with examples                       |

## TUI

`--interactive` opens a full-screen browser for inspecting PRs, filtering, and triggering actions. Use `--interval <duration>` to slow the per-run auto-refresh cadence, subject to the existing minimum interval enforced from the current result count. Configurable AI review launchers are available through `tui.review.*` settings in `config.yaml`.

## Date Syntax

```text
2weeks      # since 2 weeks ago (>=)
>2weeks     # more than 2 weeks ago
<3days      # less than 3 days ago
1y6mo       # compound: 1 year and 6 months ago
1d12h       # compound: 1 day and 12 hours ago
today       # today
yesterday   # yesterday
2024-01-15  # exact ISO date
```

Units: `m`/`min`/`mins`/`minute`/`minutes`, `h`/`hr`/`hrs`/`hour`/`hours`, `d`/`day`/`days`, `w`/`week`/`weeks`, `mo`/`month`/`months`, `y`/`year`/`years`

## Drift

Gap between PR creation and last update. Default operator is `<=`.

```text
--drift 0         # never updated after creation
--drift 1week     # updated within 1 week of creation
--drift '>1week'  # updated more than 1 week after creation
```

Also supports seconds: `s`, `sec`, `secs`, `second`, `seconds`.

## Table Columns

Default columns: `index`, `title`, `ref`, `created`, `updated`.

`author` is added automatically when multiple authors are in play (e.g. `--team`).

Available columns: `index`, `owner`, `ref`, `repo`, `number`, `title`, `labels`, `author`, `state`, `created`, `updated`, `url`

```sh
prl --columns title,ref,author,labels
```

In table mode, sort defaults to `updated` unless `--sort` is set explicitly.

## Configuration

Lives at `~/.config/prl/config.yaml`. Overridden by `PRL_*` environment variables.

```yaml
default:
  owners:
    - my-org
  authors:
    - "@me"
  bots: true
  limit: 30
  match: title
  merge_method: squash
  output: table
  reverse: false
  sort: name
  state: open

vcs: git

spinner:
  style: dots
  colors: ["39", "45", "51"]

tui:
  refresh:
    enabled: true
  review:
    # Optional: limit or reorder the available review providers.
    # enabled: [claude, codex, gemini]
    default:
      provider: claude
      model: sonnet
      effort: medium
    providers:
      claude:
        # Optional: override the available model/effort choices.
        # models: [sonnet, opus]
        # efforts: [low, medium, high, max, auto]
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}
      codex:
        # Optional: override the available model/effort choices.
        # models: [gpt-5.4, gpt-5.4-mini, gpt-5.3-codex]
        # efforts: [low, medium, high, xhigh]
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}
      gemini:
        # Optional: override the available model/effort choices.
        # models: [gemini-3.1-pro, gemini-3-pro, gemini-2.5-flash]
        # efforts:
        #   Gemini 3: [low, medium, high]
        #   Gemini 2.5 Flash budgets: [0, 1024, 8192, 24576, dynamic]
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}
  filters: {}
  sort: {}

plugin: ""

ignored_owners: []

team_aliases:
  ops: my-org/ops

teams:
  my-org/ops:
    - alice
    - bob

authors:
  dependabot: Bot
  jdoe: Jane Doe
```

- `plugin`: if empty, auto-discovers `prl-plugin-*` on `PATH`
- `vcs`: controls whether `--clone` uses `git` or `jj`
- AI review placeholders: `{prNumber}`, `{repo}`, `{owner}`, `{ownerWithRepo}`, `{prURL}`, `{prRef}`, `{title}`
- Gemini review effort uses provider-specific semantics:
  `Gemini 3` maps effort to `thinkingLevel`, while `gemini-2.5-flash` maps effort to `thinkingBudget`

## Plugins

External binaries (`prl-plugin-*`) that provide completion (`author`, `team`, `repo`, `topic`, `slack-recipient`), resolution (`team`, `topic`), and Slack sending. Set `plugin:` in config if multiple are on `PATH`.

## Development

```sh
make fmt
make lint
make test
make install
```
