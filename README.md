# pr-merger

A small local daemon that lands a stack of approved, auto-merge-enabled GitHub
PRs for you — rebasing them one at a time and letting GitHub's native
auto-merge carry each PR the rest of the way once required checks pass.

When your org can't use merge queues, keeping a stack of reviewed PRs landing
means repeatedly clicking "Update with rebase", waiting for CI, merging, and
moving to the next one. `pr-merger` automates that loop against any repo and
set of filters you configure.

## How it works

On each tick the watcher:

1. Lists open PRs in each configured repo (`gh pr list`).
2. Keeps only PRs that have auto-merge enabled and match at least one
   configured filter (authors, labels, base branch).
3. Picks the **oldest** matching PR per repo (FIFO).
4. Refetches it with `gh pr view` for accurate `mergeStateStatus` and
   status-check rollup, and re-validates it still qualifies (auto-merge
   still on, not a draft, still matches the filter).
5. Takes one action based on `mergeStateStatus`:
   - `BEHIND` → rebase the branch via the `updatePullRequestBranch`
     GraphQL mutation with `updateMethod: REBASE` (the exact action
     behind the "Update with rebase" button).
   - `BLOCKED`, `UNSTABLE`, `UNKNOWN` → log a check summary and wait.
   - `CLEAN`, `HAS_HOOKS` → do nothing; GitHub's own auto-merge will
     fire once checks land.
   - `DIRTY` → log that human action is required (conflicts).
   - anything else → log as unhandled and treat as unknown.

Processing is strictly one-per-repo per tick: rebasing PR #2 while PR #1 is
still ahead in the queue would just put #2 out of date again. Across repos
the work fans out in parallel goroutines, each bounded by the poll interval.

## Install

```bash
go install github.com/eulercb/pr-merger/cmd/pr-merger@latest
```

or, from a checkout:

```bash
go build -o ./pr-merger ./cmd/pr-merger
```

### Requirements

- Go 1.24+ (to build).
- [`gh`](https://cli.github.com/) on `PATH`, authenticated to the repos you
  want to watch. `pr-merger` shells out to `gh` for every GitHub call.

## Usage

### 1. Add one or more filters

```bash
pr-merger filter add \
  --name my-stack \
  --repo owner/repo \
  --author "$(gh api user -q .login)"

# Optional: restrict to PRs with all listed labels and a specific base
pr-merger filter add \
  --name release-train \
  --repo owner/repo \
  --label ready-to-merge \
  --label release-train \
  --base main
```

Filters are stored in YAML at `~/.config/pr-merger/config.yaml` (override
with `PR_MERGER_CONFIG=/path/to/config.yaml`).

Additional filter commands:

```bash
pr-merger filter list
pr-merger filter remove my-stack
pr-merger config path
```

### 2. Enable auto-merge on the PRs

`pr-merger` does **not** enable auto-merge for you — that's your explicit
opt-in per PR. Enable it via the GitHub UI ("Enable auto-merge") or `gh`:

```bash
gh pr merge <number> --auto --rebase
```

### 3. Run the watch loop

```bash
pr-merger watch
# Or override the poll interval on the command line:
pr-merger watch --interval 15s
```

The watcher runs in the foreground and prints what it's doing. `Ctrl-C`
stops it cleanly.

### One-shot status

To inspect what the watcher would see without actually running the loop:

```bash
pr-merger status
```

## Configuration

Example `config.yaml`:

```yaml
poll_interval: 30          # seconds; overridable per-run with --interval
filters:
  - name: my-stack
    repo: owner/repo
    authors: [eulercb]
  - name: release-train
    repo: owner/repo
    labels: [ready-to-merge, release-train]
    base: main
```

A PR matches a filter when every specified criterion matches. Empty
criteria act as "any" — e.g. a filter with no `authors` accepts any author.
A PR is eligible if it matches **at least one** filter.

## Layout

```
cmd/pr-merger/        CLI (cobra) entry point
internal/config/      YAML-backed filter persistence
internal/gh/          Wrapper around the gh CLI + PR/CheckRun types
internal/prutil/      Shared helpers: filter matching + rune-aware truncate
internal/watcher/     Poll loop: groups by repo, picks oldest, rebases
```

## Limitations / non-goals

- **Polling, not webhooks.** `pr-merger` runs locally and has no public
  endpoint, so it polls `gh` on an interval rather than subscribing to
  GitHub events.
- **Up to 500 open PRs per repo** are considered per tick. The realistic
  target is a single user's auto-merge-enabled stack, which stays well
  under that; repos with more than 500 open PRs may miss some.
- **No local git clone.** Rebases use GitHub's own rebase-update API — no
  `git push --force-with-lease` required.
- **Does not enable auto-merge.** You opt in per PR; the watcher only
  manages the rebase/wait/merge loop once you have.
