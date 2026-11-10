# prl

A Swiss Army knife for GitHub pull requests.

## Install

### macOS / Linux

```sh
brew install gechr/tap/prl
```

### Windows

```sh
scoop bucket add gechr https://github.com/gechr/scoop-bucket
scoop install gechr/prl
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

# GitHub URLs work too, with or without the scheme
prl -R https://github.com/owner/repo -a any
prl -R github.com/owner/repo -a any

# PRs with "fix" in the title/body/comments
prl fix

# Specific PRs by URL or owner/repo#N (search filters such as --state are ignored)
prl https://github.com/owner/repo/pull/1 https://github.com/owner/repo/pull/2 owner/other#3

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

# Watch with a custom refresh interval floor for this run
prl --watch --interval 30s

# Print only the total count
prl --count

# Break down the results by author
prl --group author

# Break down by repo, then author within each repo
prl --group repo,author

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

`--ci` takes a comma-separated list, and a leading `!` or `-` excludes a status: `--ci='!failure'` hides PRs with failing CI. Quote `!` in bash and zsh.

For flags that accept a GitHub user, `copilot` is an alias for `copilot-pull-request-reviewer[bot]`.

### Interactive Actions

| Flag                     | Short | Description                                  |
| ------------------------ | ----- | -------------------------------------------- |
| `--interactive`          | `-i`  | Launch the full-screen TUI browser           |
| `--interval`             |       | Override auto-refresh interval for TUI/watch |
| `--approve`              |       | Approve each PR                              |
| `--close`                |       | Close each PR                                |
| `--copilot`              |       | Request Copilot review on each PR            |
| `--delete-branch`        |       | Delete the branch after close                |
| `--comment`              |       | Add a comment to each PR                     |
| `--edit`                 | `-e`  | Edit title and body of each PR               |
| `--mark-draft`           |       | Convert each PR to draft                     |
| `--mark-ready`           |       | Mark each PR ready for review                |
| `--merge` / `--no-merge` |       | Toggle auto-merge                            |
| `--force-merge`          | `-M`  | Poll for checks, then force-merge            |
| `--unsubscribe`          | `-U`  | Remove review request and unsubscribe        |
| `--update`               |       | Update each PR branch from its base branch   |
| `--yes`                  | `-y`  | Skip the confirmation prompt                 |

### Actions

| Flag        | Short | Description                                                                                                                        |
| ----------- | ----- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `--clone`   |       | Clone unique repos from the results                                                                                                |
| `--copy`    | `-C`  | Copy rendered output to the clipboard                                                                                              |
| `--count`   | `-N`  | Print only the total result count                                                                                                  |
| `--group`   | `-G`  | Break down result counts by field(s): `author`, `repo`, `owner`, `state`, `draft`, `label` (comma-separated for nested breakdowns) |
| `--dry`     | `-n`  | Show the query without executing it                                                                                                |
| `--open`    | `-P`  | Open each PR in the browser                                                                                                        |
| `--web`     | `-w`  | Open the GitHub search page in the browser                                                                                         |
| `--send`    |       | Send PRs to Slack via plugin                                                                                                       |
| `--send-to` |       | Override the Slack recipient                                                                                                       |

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

- `prl -i`: browse PRs and diffs. `?`: all shortcuts.
- `Shift+O`: filters and stack visibility.
- `f/F`: status presets. `t/T`: time ranges. `Ctrl+X`: clear both.
- Left/Right or click a stack header: collapse/expand stacks.
- `a` in diff view: approve and advance.
- `--interval 30s`: slow auto-refresh.
- View preferences are saved between sessions.
- `tui.presets` and `tui.review`: configure presets and AI reviews.
- Reviews open in Herdr, Ghostty, iTerm2, Kitty, or Windows Terminal (WSL).
- Reviews use read-only/plan mode. **YOLO** allows unrestricted commands and resets each time. `Alt+R` always uses restricted mode.

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

Available columns: `index`, `owner`, `ref`, `repo`, `number`, `title`, `labels`, `author`, `state`, `review`, `created`, `updated`, `url`

The `review` column shows the PR's review decision using the same values as the
`--review` filter: `approved`, `changes_requested`, `required`, `dismissed`, or
`none` when nobody has been asked to review. It reuses the review decision
already fetched for the status column, so it costs no extra API calls (with
`--quick` it renders `unknown`).

```sh
prl --columns title,ref,author,labels
```

Set `default.columns` in `config.yaml` to change the default set. `--columns` overrides it, and unlike the flag a configured list leaves `default.output` alone.

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
  columns: []
  limit: 30
  match: title
  merge_method: squash
  output: table
  reverse: false
  sort: name
  state: open

# Options: auto, unicode, nerd
# auto selects nerd when NERD_FONTS is non-empty; otherwise it selects unicode.
icons: auto

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
      model: opus
      effort: high
    providers:
      claude:
        # Optional: override the available model/effort choices.
        # models: [sonnet, opus, fable]
        # efforts: [low, medium, high, max, auto]
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}
      codex:
        # Optional: override the available model/effort choices.
        # models: [gpt-6-astra, gpt-6.1-sol, gpt-6-sol, gpt-6-luna, gpt-5.6-sol, gpt-5.6-terra, gpt-5.6-luna, gpt-5.5]
        # efforts: [low, medium, high, xhigh, max, ultra]
        # ultra is supported by gpt-6-astra, gpt-5.6-sol, and gpt-5.6-terra.
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}
      gemini:
        # Optional: override the available model/effort choices.
        # models: [gemini-4-argon, gemini-3.8-flash, gemini-3.7-flash, gemini-3.6-flash, gemini-3.1-pro]
        # efforts:
        #   Gemini 3.1 Pro: [low, high]
        #   Gemini Flash and Argon: [low, medium, high]
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}
  filters: {}
  presets:
    filters:
      - name: Needs your review
        filter: approvable
        when: "!scope:self"
        color: blue
      - name: Waiting for CI
        filter: >-
          scope:self !draft ci:pending OR
          !mine !draft !approved !review:approved ci:pending
        color: "208"
    time:
      field: updated
      filters:
        - name: 10 minutes
          max_age: 10m
        - name: 1 hour
          max_age: 1h
        - name: 1 day
          max_age: 1d
        - name: 1 week
          max_age: 1w
        - name: 1 month
          max_age: 30d
        - name: 1 year
          max_age: 1y
  stacks:
    enabled: true
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
- `icons`: `auto` selects Nerd Font icons when `NERD_FONTS` is non-empty and Unicode icons otherwise; use `nerd` or `unicode` to choose explicitly
- `vcs`: controls whether `--clone` uses `git` or `jj`
- `tui.refresh`, `tui.sort`, `tui.filters`, `tui.stacks.enabled`: starting defaults you manage. Interactive changes in the TUI (toggling auto-refresh, sorting columns, applying filters) are **not** written back to `config.yaml`. They're saved to a separate state file at `$XDG_STATE_HOME/prl/state.yaml` (default `~/.local/state/prl/state.yaml`), which mirrors the `tui:` subtree and takes precedence over config. This keeps your version-controlled config stable while remembering your last session.
- `tui.presets.filters`: named filters that `f` cycles through in the TUI
  - `name`: shown in the TUI
  - `color`: a colour name such as `blue`, a hex value or an ANSI number
  - `when`: show the preset only when the search matches, e.g. `"!scope:self"`
    - Same syntax as `filter`, with search terms only (`scope:self`)
  - `filter`: terms combined as in GitHub search
    - Space, comma or `AND`: all must match
    - `OR`: either side matches; binds more loosely than `AND`
    - `(` `)`: group terms
    - `!` or `-`: negate a term or group; quote a filter that starts with `!`
    - Example: `(mine AND draft) OR approved`
  - Terms:
    - `mine`, `draft`, `ready`, `conflict`
    - `approved`: approved by you
    - `approvable`: not yours, not a draft, not yet approved, CI passing
    - `mergeable`: not a draft, approved, CI passing, no conflicts
    - `broken`: failing CI or merge conflicts
    - `blocked`: open, not a draft, and `broken` or changes requested
    - `outdated`: open, not a draft, and behind a base branch that requires it to be up to date
    - `scope:self`: the search lists only your PRs, as with the default `authors: ["@me"]`; never with `--team`
    - `ci:<status>`, `review:<decision>`
  - Defaults: Needs your review, Waiting for CI, Approved by you, Mergeable, Outdated and Blocked
    - Waiting for CI shows your PRs when you list only yours, and PRs you can approve once CI passes otherwise
    - Needs your review and Approved by you are hidden when you list only your PRs
- `tui.presets.time.field`: timestamp to filter, `updated` (default) or `created`
- `tui.presets.time.filters`: replaces the default time ranges with entries containing `name`, `max_age` and optional `color`. All Time is always available and is the initial selection. Durations use `gechr/x/human.ParseDuration`, supporting `10m`, `1h`, `1d`, `1w`, `30d`, `1y` and compounds such as `1w2d`. The default month is 30 days and year is 365 days. Filtering applies to the loaded results alongside other filters.
- `tui.filters.time`: initial time range by name, before a saved session takes precedence
- AI review placeholders: `{prNumber}`, `{repo}`, `{owner}`, `{ownerWithRepo}`, `{prURL}`, `{prRef}`, `{title}`
- Gemini reviews require the Antigravity CLI (`agy`). Model and effort selections map to its model slugs (for example, `gemini-3.8-flash` with `high` selects `gemini-3.8-flash-high`) and `--effort` flag.

## Plugins

External binaries (`prl-plugin-*`) that provide completion (`author`, `team`, `repo`, `topic`, `slack-recipient`), resolution (`team`, `topic`), and Slack sending. Set `plugin:` in config if multiple are on `PATH`.

## Development

```sh
make fmt
make lint
make test
make install
```
