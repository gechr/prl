# prl - GitHub Pull Request CLI

## Overview

`prl` is a Go CLI tool that searches, filters, displays, and acts on GitHub pull requests across an organization. It wraps `gh search prs` with opinionated defaults, rich terminal output (ANSI colors, OSC 8 hyperlinks, markdown-rendered titles), interactive multi-select for bulk actions, and Slack-formatted output.

**Install**: `go install github.com/gechr/prl@latest`
**Requires**: `gh` CLI installed and authenticated.

---

## Command Syntax

```text
prl [flags] [query...]
```

Positional `[query...]` arguments are free-text search terms joined with spaces and matched against the field specified by `--match` (default: title).

---

## Complete Flag Reference

### Filter Flags

| Flag                   | Short | Type   | Default | Description                                                                        |
| ---------------------- | ----- | ------ | ------- | ---------------------------------------------------------------------------------- |
| `--author <user>`      | `-a`  | CSV    | `@me`   | Filter by author(s), comma-separated. Use `all` for no author filter               |
| `--ci <status>`        |       | enum   |         | CI status: `success`/`s`, `failure`/`f`, `pending`/`p`                             |
| `--commenter <user>`   |       | CSV    |         | Filter by commenter                                                                |
| `--created <date>`     | `-c`  | date   |         | Filter by creation date                                                            |
| `--drift <duration>`   | `-d`  | drift  |         | Filter by gap between created and updated timestamps                               |
| `--filter <qualifier>` | `-f`  | string |         | Raw GitHub search qualifier (repeatable)                                           |
| `--involves <user>`    | `-I`  | CSV    |         | Filter by involvement (author, assignee, mentions, comments)                       |
| `--language <lang>`    | `-l`  | string |         | Filter by programming language                                                     |
| `--merged <date>`      | `-m`  | date   |         | Filter by merged date                                                              |
| `--no-bot`             | `-B`  | bool   | config  | Exclude bot authors (post-query filter)                                            |
| `--no-draft`           | `-D`  | bool   | false   | Exclude draft PRs                                                                  |
| `--review <status>`    | `-r`  | enum   |         | Review status: `none`/`n`, `required`/`r`, `approved`/`a`, `changes_requested`/`c` |
| `--requested <user>`   |       | CSV    |         | Filter by requested reviewer                                                       |
| `--reviewed-by <user>` |       | CSV    |         | Filter by who reviewed                                                             |
| `--state <state>`      | `-s`  | enum   | `open`  | PR state: `open`/`o`, `closed`/`c`, `merged`/`m`, `all`/`a`                        |
| `--team <slug>`        | `-t`  | string |         | Filter by team members (resolved from Terraform HCL)                               |
| `--topic <topic>`      | `-T`  | string |         | Filter by repo topic (resolved from Terraform HCL)                                 |
| `--updated <date>`     | `-u`  | date   |         | Filter by last updated date                                                        |

### Scope Flags

| Flag            | Short | Type   | Default | Description                                                                         |
| --------------- | ----- | ------ | ------- | ----------------------------------------------------------------------------------- |
| `--org <org>`   |       | CSV    | config  | GitHub organization(s), comma-separated (aliases: `organization`, `owner`). Use `all` for no org filter |
| `--repo <repo>` | `-R`  | string |         | Limit to specific repository (alias: `repository`)                                  |

### Display Flags

| Flag               | Short | Type   | Default | Description                                                                              |
| ------------------ | ----- | ------ | ------- | ---------------------------------------------------------------------------------------- |
| `--columns <cols>` |       | string |         | Custom table columns, comma-separated. Forces table output                               |
| `--copy`           | `-C`  | bool   | false   | Copy output to clipboard                                                                 |
| `--limit <n>`      | `-L`  | int    | 30      | Maximum results                                                                          |
| `--match <field>`  |       | string | `title` | Restrict search to field: `title`/`t`, `body`/`b`, `comments`/`c`                        |
| `--open`           | `-O`  | bool   | false   | Open each PR in browser. Forces URL output                                               |
| `--output <fmt>`   | `-o`  | enum   | `table` | Output format: `table`/`t`, `url`/`u`, `bullet`/`b`, `slack`/`s`, `json`/`j`             |
| `--reverse`        |       | bool   | false   | Show oldest first (at top). Default display is newest first                              |
| `--sort <field>`   |       | enum   | `name`  | Sort: `name`/`n`, `created`/`c`, `updated`/`u`. Table mode overrides `name` to `updated` |

### Action Flags

| Flag               | Short | Type   | Description                                               |
| ------------------ | ----- | ------ | --------------------------------------------------------- |
| `--approve`        |       | bool   | Approve selected PRs (interactive)                        |
| `--close`          |       | bool   | Close selected PRs (interactive)                          |
| `--comment <body>` |       | string | Add comment to PRs (interactive)                          |
| `--delete-branch`  |       | bool   | Delete branch on close (requires `--close`)               |
| `--mark-draft`     |       | bool   | Convert PR to draft (interactive, uses GraphQL)           |
| `--mark-ready`     |       | bool   | Mark PR ready for review (interactive, uses GraphQL)      |
| `--merge`          |       | bool   | Enable auto-merge with squash (interactive, uses GraphQL) |
| `--no-merge`       |       | bool   | Disable auto-merge (non-interactive, uses GraphQL)        |
| `--update`         |       | bool   | Update PR branch from base (interactive)                  |

### Mode Flags

| Flag    | Short | Description                                                        |
| ------- | ----- | ------------------------------------------------------------------ |
| `--dry` | `-n`  | Show search query without executing (aliases: `dry-run`, `dryrun`) |
| `--web` | `-w`  | Open GitHub search in browser                                      |
| `--yes` | `-y`  | Skip interactive confirmation                                      |

---

## Mutually Exclusive Flags

These flag combinations will error:

- `--merge` and `--no-merge`
- `--close` and `--approve`
- `--close` and `--merge`
- `--close` and `--update`
- `--mark-draft` and `--mark-ready`
- `--mark-draft` and `--close`
- `--mark-draft` and `--merge`
- `--author` and `--team`

**Dependency**: `--delete-branch` requires `--close`.

---

## Date Syntax

Relative durations with automatic operator flipping (user intent is preserved):

```text
2weeks      # since 2 weeks ago (>=)
>2weeks     # more than 2 weeks ago (older)
<3days      # less than 3 days ago (newer)
>=1month    # at least 1 month ago
today       # today (hardcoded >=)
yesterday   # yesterday
2024-01-15  # exact ISO date passthrough
```

**Units**: `m`/`min`/`mins`/`minute`/`minutes`, `h`/`hr`/`hrs`/`hour`/`hours`, `d`/`day`/`days`, `w`/`week`/`weeks`, `mo`/`month`/`months`, `y`/`year`/`years`

**Operator flipping**: `>2weeks` means "created more than 2 weeks ago" which translates to `created:<date-2-weeks-ago>` in the GitHub API. The tool handles this automatically.

---

## Drift Syntax

Drift measures the time gap between PR creation and last update:

```text
--drift 0           # never updated after creation (stale since open)
--drift 1week       # updated within 1 week of creation (default operator: <=)
--drift '>1week'    # lingering PRs (gap exceeds 1 week)
--drift '>=3days'   # gap is 3+ days
--drift '<5hours'   # gap is less than 5 hours
```

**Operators**: `<=` (default), `<`, `>=`, `>`, `=`, `==`
**Units**: same as date syntax, plus `s`/`sec`/`second`/`seconds`. Plain numbers are seconds.

Drift is a **post-query filter** (applied after GitHub API results are fetched).

---

## Table Columns

**Default**: `index`, `title`, `ref`, `created`, `updated`
**With team/multiple authors**: adds `author` column automatically

**All available columns**:

| Column    | Aliases    | Description                                                   |
| --------- | ---------- | ------------------------------------------------------------- |
| `index`   | `idx`, `i` | Row number (#1 = newest). Hidden in interactive mode          |
| `ref`     |            | `repo#number` or `org/repo#number` (hyperlinked)              |
| `repo`    |            | Repository name only (hyperlinked)                            |
| `org`     | `owner`    | Full `org/repo` name                                          |
| `number`  |            | `#<number>` (hyperlinked)                                     |
| `title`   |            | PR title (truncated to 80 chars, inline markdown rendered)    |
| `labels`  |            | Comma-separated label names                                   |
| `author`  |            | Author login (colorized, bots dimmed, departed strikethrough) |
| `state`   |            | `open` (blue), `merged` (magenta), `closed` (red)             |
| `created` |            | Relative time with color (green=recent, red=old)              |
| `updated` |            | Relative time with color                                      |
| `url`     |            | Full GitHub PR URL                                            |

Usage: `prl --columns title,ref,author,labels`

---

## Output Formats

| Format     | Flag                  | Description                                             |
| ---------- | --------------------- | ------------------------------------------------------- |
| **table**  | `-o table` or `-o t`  | Rich table with colors, alignment, hyperlinks (default) |
| **url**    | `-o url` or `-o u`    | One URL per line                                        |
| **bullet** | `-o bullet` or `-o b` | Markdown bullet list: `* <url>`                         |
| **json**   | `-o json` or `-o j`   | Pretty-printed JSON array                               |
| **slack**  | `-o slack` or `-o s`  | Slack-formatted with emoji headers and approval tiers   |

### Output Override Rules

These rules apply automatically (in priority order):

1. `--columns` always forces `table` output
1. `--open` forces `url` output
1. Action flags with interactive mode (no `--yes`) force `table`
1. Action flags without interactive (`--yes`) force `url`

---

## Interactive Mode

Triggered when action flags are set (`--approve`, `--close`, `--comment`, `--mark-draft`, `--mark-ready`, `--merge`, `--update`) AND `--yes` is NOT set.

Uses `charmbracelet/huh` multi-select UI:

- Arrow keys to navigate
- Space to toggle selection
- Enter to confirm
- Ctrl+C to cancel

**Note**: `--no-merge` does NOT trigger interactive mode despite being an action.

---

## Action Execution Phases

1. **Phase 1**: Comments (only if `--comment` without `--close`)
1. **Phase 2**: All other actions run in parallel per-PR. Within each PR, order is: update → close → approve → mark-draft → mark-ready → disable-auto-merge → enable-auto-merge
1. **Phase 3**: Open in browser (if `--open`)

### Implicit Behaviors

- `--approve` without `--review` automatically adds `-review:approved` to exclude already-approved PRs
- `--close` with `--comment` defers the comment to phase 2 (posted before closing)
- `--merge` uses SQUASH merge method (hardcoded)

---

## Configuration

**File**: `~/.config/prl/config.yaml`

```yaml
default:
  organizations:
    - my-org
  authors:
    - "@me"
  bots: true        # set to false to exclude bots by default
  limit: 50
  match: title      # title, body, or comments
  output: table     # table, url, bullet, slack, or json
  reverse: false    # true to show oldest first by default
  sort: updated     # name, created, or updated
  state: open       # open, closed, merged, or all

code_dir: ~/code/github/my-org
terraform_repository_dir: ~/code/github/my-org/tf-github
terraform_membership_dir: ~/code/github/my-org/tf-membership-v2
ignored_organizations:
  - archived-org

output:
  slack:
    skip_repos:
      - internal-tool
    two_approver_repos:
      - critical-service

team_aliases:
  ops: ops_team_full_name

authors:
  dependabot: Bot
  jdoe: Jane Doe
```

**Priority** (highest to lowest): CLI flags > `PRL_*` env vars > config file > hardcoded defaults

Setting `code_dir` automatically derives `terraform_repository_dir` (`<code_dir>/tf-github`) and `terraform_membership_dir` (`<code_dir>/tf-membership-v2`) unless they are set explicitly.

Top-level settings can be overridden via `PRL_*` environment variables (e.g. `PRL_CODE_DIR=~/code`). Nested keys under `default.*` and `output.*` cannot be set via env vars.

Pass `--debug` to log HTTP requests to stderr for debugging.

---

## Author Resolution

Two sources (HCL has higher priority):

1. **Terraform HCL** (`users.tf`): Maps `github_username` to `first_name + last_name`
1. **Config** (`authors` key): Fallback mapping

**Display rules**:

- Regular authors: colored (20-color rotating palette)
- Bots (suffix `[bot]`): dimmed, linked to `/apps/<name>`
- Departed users (config-only, not in HCL): strikethrough

---

## Team Resolution (via Terraform HCL)

`--team <name>` resolves team members from `groups_*.tf` files:

1. Finds `module` block with matching `name` attribute
1. Extracts `members` list (e.g., `local.users["username"]`)
1. Maps internal names to GitHub usernames via `users.tf`
1. Builds `(author:user1 OR author:user2 ...)` query

Team aliases in config map short names to full Terraform team names.

---

## Topic Resolution (via Terraform HCL)

`--topic <name>` resolves repos from `main.tf` / `sg2.tf`:

1. Finds `module` blocks with matching `topics` attribute
1. Special: `sg2` topic matches modules in `sg2_repos` local list
1. Builds `(repo:org/repo1 OR repo:org/repo2 ...)` query

---

## CSV Flag Semantics

**`--author` (AuthorFlag)**: First explicit use CLEARS the default (`@me`) and replaces. Subsequent uses APPEND.
**All other CSV flags** (`--commenter`, `--involves`, `--requested`, `--reviewed-by`): Always APPEND.

All CSV flags accept comma-separated values: `--author alice,bob`
The value `any` normalizes to `all` for all CSV and org flags.

---

## Query Construction

The tool builds a GitHub search query from flags:

```text
type:pr archived:false [state:X] [user:org] [author:X] [created:X] ...
```

Multiple values for the same qualifier use OR syntax:

```text
(author:alice OR author:bob)
```

The `--filter` flag passes raw qualifiers directly, enabling:

```text
prl -f "label:bug" -f "-label:wontfix"
prl -f 'assignee:alice'
```

---

## Post-Query Filters

These filters run after fetching API results (cannot be expressed in GitHub search):

- `--no-bot`: Removes authors ending with `[bot]` (case-insensitive)
- `--drift`: Filters by `abs(updatedAt - createdAt)` gap

---

## Sorting Behavior

- **Table mode**: `--sort name` is automatically overridden to `--sort updated`
- **Other modes**: Respect the user's sort choice
- `--reverse`: Flips display order. Default is newest at top; `--reverse` shows oldest at top
- Default sort direction: ascending (oldest first for dates, A-Z for names)

---

## Example Recipes

### Finding PRs

```sh
# Your open PRs (default)
prl

# All open PRs in the org
prl -a all

# PRs by a specific user
prl -a username

# PRs by multiple users
prl -a alice,bob

# PRs by a team (resolved from HCL)
prl --team backend

# All your PRs (open + closed + merged)
prl -s all

# Your merged PRs from the last month
prl -s merged -m 1month

# PRs in a specific repo
prl -R owner/repo -a all

# PRs with a search term in the title
prl fix authentication

# PRs with a search term in the body
prl --match body "breaking change"

# PRs created in the last 2 weeks
prl -a all -c 2weeks

# PRs not updated in over a month
prl -a all -u '>1month'

# PRs created more than 3 months ago that are still open
prl -a all -c '>3months'

# Stale PRs (never updated since creation)
prl -a all --drift 0

# Lingering PRs (large gap between creation and last update)
prl -a all --drift '>2weeks'

# PRs with failing CI
prl -a all --ci failure

# PRs awaiting your review
prl --requested @me -a all

# PRs you've reviewed
prl --reviewed-by @me -a all

# PRs with changes requested
prl -a all -r changes_requested

# PRs involving you (author, assignee, mentioned, commented)
prl -I @me -a all

# Draft PRs only (combine state filter)
prl -a all -f "draft:true"

# Exclude draft PRs
prl -a all -D

# Exclude bot PRs
prl -a all -B

# PRs with a specific label
prl -a all -f "label:bug"

# PRs without a specific label
prl -a all -f "-label:wontfix"

# Go language PRs
prl -a all -l go

# PRs in repos with a specific topic
prl -a all --topic infrastructure

# PRs across all orgs
prl -a all --org all
```

### Stale PR Discovery

```sh
# Find stale PRs by a user (open, old, never updated)
prl -a username --drift 0 -c '>2weeks'

# Find stale PRs raised by a user where CI is failing
prl -a username --ci failure -c '>1week'

# Find all stale open PRs in the org older than 1 month
prl -a all -c '>1month' --drift 0

# Find PRs where review was requested but not yet given
prl --requested username -a all -r none

# Find PRs approved but not merged
prl -a all -r approved

# Find PRs with changes requested that haven't been updated
prl -a all -r changes_requested -u '>1week'
```

### Output and Display

```sh
# JSON output for scripting
prl -a all -o json

# URL list (one per line, good for piping)
prl -a all -o url

# Markdown bullet list
prl -a all -o bullet

# Slack-formatted for posting to channels
prl -a all -o slack

# Copy URLs to clipboard
prl -a all -C

# Open all matching PRs in browser
prl -a all -O

# Custom table columns
prl -a all --columns title,ref,author,labels,state

# Show oldest PRs first
prl -a all --reverse

# Sort by creation date
prl -a all --sort created

# Limit results
prl -a all -L 10

# Dry run (see the query without executing)
prl -a all -c 2weeks -n

# Open GitHub search in browser
prl -a all -w
```

### Bulk Actions

```sh
# Approve selected PRs (interactive multi-select)
prl -a all --approve

# Approve all PRs without confirmation
prl -a all --approve -y

# Close selected PRs
prl -a all --close

# Close and delete branches
prl -a all --close --delete-branch

# Close with a comment
prl -a all --close --comment "Superseded by #456"

# Add a comment to PRs
prl -a all --comment "Please rebase"

# Enable auto-merge (squash) on your PRs
prl --merge

# Enable auto-merge without confirmation
prl --merge -y

# Disable auto-merge
prl --no-merge

# Update PR branches from base
prl --update

# Mark PRs as draft
prl -a all --mark-draft

# Mark PRs as ready for review
prl --mark-ready

# Approve and open in browser
prl -a all --approve -O
```

### Combined Workflows

```sh
# Find and approve team PRs with passing CI
prl --team backend --ci success --approve

# Find stale PRs by user, close them with a comment
prl -a username -c '>3months' --close --comment "Closing stale PR" --delete-branch

# Review team's PRs needing attention (changes requested, not updated recently)
prl --team frontend -r changes_requested -u '>3days'

# Get a Slack message of PRs needing review, excluding bots
prl -a all -B -o slack

# Find PRs across all repos for a topic, output as JSON
prl -a all --topic infrastructure -o json

# Dry run a complex query to verify
prl -a all --team ops -c 2weeks --ci failure -n
```

---

## Architecture Overview

```text
CLI Flags + Config
    |
    v
Validation & Normalization
    |
    v
Query Building (search.go)
    |
    +--[--dry]--> Print query, exit
    +--[--web]--> Open browser search, exit
    |
    v
GitHub Search API (paginated REST)
    |
    v
Post-Query Filters (bots, drift)
    |
    v
Sort & Render (table/url/bullet/json/slack)
    |
    +--[--copy]--> Clipboard
    +--[interactive]--> Multi-select UI --> Action Runner
    +--[--yes]--> Action Runner (all PRs)
    |
    v
Action Runner (REST + GraphQL)
    Phase 1: Comments
    Phase 2: Approve/Close/Merge/Update/Draft/Ready (parallel per-PR)
    Phase 3: Browser open
```

### Key Files

| File             | Purpose                                                      |
| ---------------- | ------------------------------------------------------------ |
| `main.go`        | Entry point, orchestrates the full pipeline                  |
| `cli.go`         | CLI struct, flag definitions, validation, normalization      |
| `config.go`      | Config loading (YAML + env vars + defaults)                  |
| `search.go`      | GitHub search query building, API execution, pagination      |
| `query.go`       | Date parsing, drift parsing, OR-qualifier construction       |
| `resolve.go`     | Terraform HCL parsing for teams, topics, users               |
| `output.go`      | Post-query filtering (bots, drift), sorting, render dispatch |
| `table.go`       | Table renderer, column definitions, alignment                |
| `authors.go`     | Author name resolution, color assignment, display styling    |
| `actions.go`     | ActionRunner, all PR mutations (REST + GraphQL)              |
| `interactive.go` | Multi-select UI using charmbracelet/huh                      |
| `slack.go`       | Slack output formatting with approval tiers                  |
| `github.go`      | REST and GraphQL API client initialization                   |
| `options.go`     | GitHub API client options (debug, auth)                      |
| `complete.go`    | Fish shell completion generation                             |
| `style.go`       | Lipgloss styles, column alignment, TTY detection             |
| `browser.go`     | Browser opening and clipboard operations                     |
| `types.go`       | Core data structures and enum types                          |
| `constant.go`    | Constants (limits, time units, defaults)                     |
| `util.go`        | Path expansion utility                                       |
| `help.go`        | Custom help printer with colored output                      |

### API Usage

- **REST** (always): Search queries, approve, comment, close, update branch, delete branch
- **GraphQL** (lazy, only when needed): mark-draft, mark-ready, enable/disable auto-merge
- **Authentication**: Delegated to `gh` CLI (`github.com/cli/go-gh/v2`)

---

## Tips and Tricks

1. **Use `-a all` for org-wide searches** - Default is `@me` (your PRs only)
1. **Combine date + drift for stale PR discovery** - e.g., `-c '>2weeks' --drift 0`
1. **Use `--filter` for anything not covered by flags** - Raw GitHub search qualifiers like `label:bug`, `-label:wontfix`, `assignee:user`
1. **Pipe URL output for scripting** - `prl -a all -o url | xargs -I{} gh pr view {}`
1. **Dry run complex queries first** - `prl -a all --team ops -c 2weeks -n` to verify the query
1. **Table sort override** - In table mode, `--sort name` automatically becomes `--sort updated` for more useful display
1. **Author column auto-shows** - When using `--team` or multiple authors, the author column appears automatically
1. **`--no-merge` is non-interactive** - Unlike other action flags, it executes without the multi-select UI
1. **Comments before close** - `--comment` with `--close` posts the comment before closing; without `--close`, comments execute in a separate phase
1. **Implicit approve filter** - `--approve` automatically excludes already-approved PRs unless `--review` is explicitly set
1. **Short flag values** - Most enum flags accept single-letter shortcuts: `-s m` for merged, `-o j` for JSON, `-r a` for approved
1. **Config file is optional** - Everything works with CLI flags alone; config just sets defaults
1. **Team aliases** - Configure short team names in config to avoid typing full Terraform team slugs
1. **`--columns` forces table** - Even if `-o url` is set, `--columns` overrides to table output
1. **Clipboard before interactive** - `--copy` captures output before the interactive selection UI appears
