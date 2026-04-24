# CLAUDE.md

Project-specific guidance for Claude Code agents working in this repository.

## What this project is

`pr-merger` is a local Go daemon that watches GitHub PRs with auto-merge
enabled and rebases them one at a time per repo, letting GitHub's own
auto-merge finish each PR once required checks pass. See `README.md` for
the user-facing overview.

## Build, test, and check commands

```bash
go build ./...        # compile every package
go vet ./...          # static analysis; must be clean
go test ./...         # run tests (add them before adding features)
go build -o ./pr-merger ./cmd/pr-merger   # build the CLI binary
```

Run a quick CLI smoke test without touching the real config at
`~/.config/pr-merger/config.yaml` by pointing `PR_MERGER_CONFIG` at a temp
file:

```bash
PR_MERGER_CONFIG=/tmp/pr-merger-test.yaml ./pr-merger filter list
```

CI runs `go vet`, `go build`, and `go test -race` on every push and PR. See
`.github/workflows/ci.yml`.

## Code layout

```
cmd/pr-merger/        CLI entry point (cobra). Default command launches the
                      TUI; subcommands: watch, status, filter add|list|remove,
                      config path, setup (wizard).
internal/config/      YAML-backed filter persistence. Config struct and
                      Filter type live here.
internal/gh/          Wrapper over the gh CLI + PRClient interface for
                      testability. Shells out to `gh pr list|view|merge` and
                      `gh api graphql`. PR and StatusCheck types in types.go;
                      PRState / icons in status.go; CurrentUser +
                      RecentContributedRepos in discover.go.
internal/prutil/      Shared helpers: MatchesAny/Matches (filter matching)
                      and Truncate (rune-aware). Used by watcher, TUI, and
                      the status command to avoid drift.
internal/tui/         Bubble Tea dashboard: per-repo panels with status
                      icons. Subscribes to watcher.Events.
internal/watcher/     One long-running goroutine per repo, each with its own
                      ticker and per-tick timeout. Publishes every
                      observation/action as a watcher.Event so subscribers
                      (TUI, log writer, tests) can react live.
internal/wizard/      First-run configuration wizard. Pure helpers in
                      wizard.go; Bubble Tea UI in tui.go.
```

## Conventions and guardrails

### Calling GitHub

- Always go through `internal/gh.Client`. Do **not** spawn `exec.Command("gh",
  ...)` directly from other packages.
- For actions the GitHub REST API doesn't expose cleanly (e.g. rebase-update),
  call the corresponding GraphQL mutation via `gh api graphql`. See
  `UpdateBranchRebase` as the example.
- Requesting new PR fields: add the field tag to `internal/gh.PullRequest`
  (or `StatusCheck`), then extend `prListFields` / `prViewFields` in
  `client.go` so the `gh pr list|view` calls actually ask for it. The two
  must stay in sync — forgetting either causes silent zero values.

### Watcher behavior

- The watcher enforces "one PR per repo per tick, oldest first." Don't break
  that invariant without an explicit ask.
- Every per-repo call in `iterate` must run under a timeout derived from
  `w.Interval`, so a hung `gh` invocation can't freeze the loop.
- After `GetPR`, always re-validate the PR is still eligible (`AutoMergeRequest
  != nil`, `!IsDraft`, still matches a filter). Users can disable auto-merge
  between `pr list` and `pr view`; the watcher must not rebase in that window.
- When classifying `mergeStateStatus`, keep a `default` arm that logs the
  unhandled state. GitHub introduces new states occasionally; we want
  diagnosable logs, not silent no-ops.
- When classifying `StatusCheck` conclusions, treat any completed conclusion
  that isn't an explicit success as failed. Never silently drop unknown
  conclusions — `summarizeChecks` depends on that.

### Filters and config

- `internal/config` owns persistence. Don't read or write the YAML file
  from elsewhere. Use `config.Load` / `config.Save` / `Config.FindFilter` /
  `Config.RemoveFilter`.
- A filter matches a PR only when **every** specified criterion matches.
  Empty criteria act as "any". A PR is eligible if it matches **at least
  one** filter.
- Filter matching and title truncation live in `internal/prutil`. Both the
  `watch` loop and the `status` command must use those helpers — do not
  reintroduce local copies.

### Go style

- Follow idiomatic Go: short receiver names, errors wrapped with
  `fmt.Errorf("...: %w", err)`, no `panic` for expected failures, no
  `init()` for side effects.
- Default to no comments. Add one only when the **why** is non-obvious
  (hidden constraint, subtle invariant, workaround, surprising behavior).
  Don't narrate the **what** — names and types do that.
- Avoid adding scaffolding for hypothetical future features. When we added
  an `Active()` accessor "for a future status command," review flagged its
  stale state — it got removed. Don't reintroduce that pattern.
- Prefer extending `internal/prutil` (or a new small internal package) over
  duplicating helpers between `cmd/pr-merger/main.go` and `internal/watcher`.

### Dependencies

- Runtime deps: `github.com/spf13/cobra` (CLI) and `gopkg.in/yaml.v3`
  (config). Don't add heavy dependencies (an HTTP client, a logging
  framework, etc.) without a concrete need — we lean on `gh` and the stdlib.
- Shell dep: the `gh` CLI must be on `PATH`, authenticated. Don't try to
  replicate auth handling in code.

## Scope discipline

- Only change what was asked. A fix for one concern shouldn't drag in
  unrelated refactors.
- Don't add error handling, fallbacks, or validation for scenarios that
  can't happen. Trust internal call sites; validate at true boundaries
  (user input in CLI flags, `gh` CLI output).
- Don't add a feature flag, backward-compat shim, or "util"/"common"
  package. If code wants a home, give it a specific one (like
  `internal/prutil`).

## When uncertain

If a request is ambiguous — especially about watcher semantics (ordering,
serialization, timeouts), GitHub API choice (REST vs. GraphQL), or adding
new top-level commands — ask before implementing. These are the shape of
the tool; don't reshape them on inference.
