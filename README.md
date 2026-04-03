# prl

Search, filter, display, and act on GitHub pull requests across an owner (organization or user).

`prl` wraps `gh search prs` with opinionated defaults, rich terminal output (ANSI colors, OSC 8 hyperlinks, markdown-rendered titles), interactive multi-select for bulk actions, and optional plugin-based completion, resolution, and Slack sending.

## Install

```text
go install github.com/gechr/prl@latest
```

Requires [`gh`](https://cli.github.com/) to be installed and authenticated.

### Quickstart

```sh
prl --init
```

This creates `~/.config/prl/config.yaml` with default settings. Edit it to set a default owner.

## Usage

```text
prl [flags] [query...]
```

### Examples

```sh
# Your open PRs (default)
prl

# All open PRs for an owner
prl -a all

# PRs created in the last 2 weeks, sorted by creation date
prl -a all -c 2weeks --sort created

# PRs with "fix" in the title
prl fix

# PRs that haven't been updated since creation
prl -a all --drift 0

# Approve selected PRs interactively
prl -a all --approve

# Auto-merge your PRs
prl --merge -y

# Send your PRs to Slack via a plugin
prl --send

# Dry run: show the gh command without executing
prl -a all -n

# JSON output
prl -a all -o json
```

## Flags

### Filters

| Flag            | Short | Description                                                    |
| --------------- | ----- | -------------------------------------------------------------- |
| `--owner`       | `-O`  | GitHub owner(s), comma-separated                               |
| `--repo`        | `-R`  | Limit to specific repo                                         |
| `--filter`      | `-f`  | Raw GitHub search qualifier (repeatable)                       |
| `--match`       |       | Restrict search to field (default: title)                      |
| `--author`      | `-a`  | Filter by author(s), comma-separated. Default: `@me`           |
| `--commenter`   |       | Filter by commenter                                            |
| `--no-bot`      | `-B`  | Exclude bot authors (post-fetch)                               |
| `--draft`       |       | Show only draft PRs (negatable: `--no-draft`)                  |
| `--team`        | `-t`  | Filter by team authors (via plugin or `teams` config)          |
| `--involves`    | `-I`  | Filter by involvement                                          |
| `--requested`   |       | Filter by requested reviewer                                   |
| `--reviewed-by` |       | Filter by reviewer                                             |
| `--ci`          |       | Filter by CI status (success/failure/pending)                  |
| `--language`    | `-l`  | Filter by language                                             |
| `--review`      | `-r`  | Filter by review status                                        |
| `--state`       | `-s`  | PR state: open/closed/merged/all (default: open)               |
| `--topic`       | `-T`  | Filter by repo topic (via plugin)                              |
| `--created`     | `-c`  | Filter by creation date (e.g. `2weeks`, `today`, `2024-01-01`) |
| `--drift`       | `-d`  | Filter by created-to-updated gap (e.g. `0`, `1week`, `>3days`) |
| `--updated`     | `-u`  | Filter by last updated date                                    |
| `--merged`      | `-m`  | Filter by merged date                                          |
| `--archived`    |       | Include archived repos                                         |

### Interactive

| Flag              | Short | Description                                  |
| ----------------- | ----- | -------------------------------------------- |
| `--approve`       |       | Approve each PR                              |
| `--close`         |       | Close each PR                                |
| `--delete-branch` |       | Delete branch after close (requires --close) |
| `--comment`       |       | Add a comment to each PR                     |
| `--mark-draft`    |       | Convert each PR to draft                     |
| `--mark-ready`    |       | Mark each PR as ready for review             |
| `--merge`         |       | Enable auto-merge (squash) on each PR        |
| `--no-merge`      |       | Disable auto-merge on each PR                |
| `--update`        |       | Update each PR branch from base branch       |
| `--yes`           | `-y`  | Skip confirmation                            |

### Actions

| Flag        | Short | Description                                           |
| ----------- | ----- | ----------------------------------------------------- |
| `--copy`    | `-C`  | Copy output to clipboard                              |
| `--dry`     | `-n`  | Show search query without executing                   |
| `--open`    | `-O`  | Open each PR in browser                               |
| `--web`     | `-w`  | Open GitHub search in browser                         |
| `--send`    |       | Send PRs to Slack via plugin                          |
| `--send-to` |       | Override Slack recipient (`#channel`, `@user`, email) |

### Output

| Flag        | Short | Description                                   |
| ----------- | ----- | --------------------------------------------- |
| `--columns` |       | Custom table columns (forces table output)    |
| `--limit`   | `-L`  | Maximum results (default: 30)                 |
| `--output`  | `-o`  | Output format: table/url/bullet/json/repo     |
| `--reverse` |       | Show oldest first (top)                       |
| `--sort`    |       | Sort by: name/created/updated (default: name) |

### Miscellaneous

| Flag      | Short | Description                                     |
| --------- | ----- | ----------------------------------------------- |
| `--debug` |       | Log HTTP requests to stderr                     |
| `-h`      |       | Print short help                                |
| `--help`  |       | Print long help with examples                   |

## Date Syntax

Relative durations with automatic operator flipping:

```text
2weeks      # since 2 weeks ago (>=)
>2weeks     # more than 2 weeks ago
<3days      # less than 3 days ago
1y6mo       # compound: 1 year and 6 months ago
1d12h       # compound: 1 day and 12 hours ago
today       # today (hardcoded >=)
yesterday   # yesterday
2024-01-15  # exact ISO date
```

Units: `m`/`min`/`mins`/`minute`/`minutes`, `h`/`hr`/`hrs`/`hour`/`hours`, `d`/`day`/`days`, `w`/`week`/`weeks`, `mo`/`month`/`months`, `y`/`year`/`years`

Compound durations combine multiple units in descending order (e.g. `1y6mo`, `2w3d`, `1d12h30m`).

## Drift

Drift measures the gap between a PR's creation and last update. The default operator is `<=` (within).

```text
--drift 0         # never updated after creation
--drift 1week     # updated within 1 week of creation
--drift '>1week'  # lingering PRs (updated more than 1 week after creation)
```

Additional units for drift: `s`/`sec`/`secs`/`second`/`seconds`

## Table Columns

Default columns: `index`, `title`, `ref`, `created`, `updated` (plus `author` when using `--team` or multiple authors).

Available columns: `index`, `owner`, `ref`, `repo`, `number`, `title`, `labels`, `author`, `state`, `created`, `updated`, `url`

```sh
prl --columns title,ref,author,labels
```

Note: in table mode, the default sort (`name`) is automatically overridden to `updated`. Explicitly passing `--sort name` is honored.

## Configuration

Config file at `~/.config/prl/config.yaml`:

```yaml
default:
  owners:
    - my-org
  authors:
    - "@me"
  bots: true        # set to false to exclude bots by default
  limit: 50
  match: title      # title, body, or comments
  output: table     # table, url, bullet, json, or repo
  reverse: false    # true to show oldest first by default
  sort: updated     # name, created, or updated
  state: open       # open, closed, merged, or all

plugin: ""

tui:
  review:
    default:
      provider: claude
      model: opus
    providers:
      claude:
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}
      codex:
        prompt: |
          Review PR #{prNumber} in {ownerWithRepo}.

          URL: {prURL}

ignored_owners:
  - archived-org

team_aliases:
  ops: ops_team_full_name

teams:
  ops_team_full_name:
    - alice
    - bob

authors:
  dependabot: Bot
  jdoe: Jane Doe
  asmith: Alice Smith
```

Available AI review prompt placeholders: `{prNumber}`, `{repo}`, `{owner}`, `{ownerWithRepo}`, `{prURL}`, `{prRef}`, `{title}`.

Top-level settings can be overridden via `PRL_*` environment variables.

## Author Name Resolution

Authors can be resolved to display names from two sources:

1. **Plugin**: `complete author` output from the configured plugin
1. **Config** (`authors`): Fallback mapping in `config.yaml`

In table output, authors are colorized with a 20-color palette, bots are dimmed, and config-only users are shown with strikethrough when the plugin marks active users.
