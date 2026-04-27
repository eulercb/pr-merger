// Package watcher monitors GitHub pull requests across configured repos and,
// per repo, rebases one at a time so GitHub's native auto-merge can finish
// each PR. One long-running goroutine owns each repo; repos never share work.
package watcher

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
	"github.com/eulercb/pr-merger/internal/prutil"
)

// EventKind classifies each event the watcher publishes.
type EventKind int

const (
	// EventSnapshot reports the current set of eligible PRs for a repo
	// after a poll. Consumed by the TUI to refresh per-repo panels.
	EventSnapshot EventKind = iota
	// EventRebased signals that the head PR was rebased this tick.
	EventRebased
	// EventWaiting signals the head PR is waiting on CI / reviews /
	// auto-merge — no action taken.
	EventWaiting
	// EventConflict signals a queued PR has merge conflicts and needs
	// human attention. The watcher does not act on it and walks past it
	// to the next eligible PR in the same tick, so a stuck head doesn't
	// freeze the rest of the queue.
	EventConflict
	// EventError signals a poll or rebase failure. Message carries detail.
	EventError
)

// Event is a per-repo notification emitted on every watcher action or
// observation. Events flow through a buffered channel so a slow consumer
// can fall behind by a few ticks without blocking the per-repo goroutine.
// Consumers must still eventually drain the channel; when the buffer is
// full and the run context is cancelled the event is dropped and logged.
type Event struct {
	Repo    string
	Kind    EventKind
	At      time.Time
	Message string
	// PRs is the current eligible queue for the repo (FIFO).
	// Populated on EventSnapshot.
	PRs []gh.PullRequest
	// Head is the PR we acted on or inspected, with statusCheckRollup
	// included. nil when the queue is empty.
	Head *gh.PullRequest
}

// Watcher orchestrates one goroutine per repo, each with its own poll ticker,
// so repos never block each other and a hung gh invocation is scoped to a
// single repo.
type Watcher struct {
	Client   gh.PRClient
	Filters  []config.Filter
	Interval time.Duration
	Logger   *log.Logger

	// Events receives every observation/action. The channel is buffered
	// (defaultEventBuffer) so consumers can fall behind by a few ticks
	// without immediately blocking a per-repo goroutine; once the buffer
	// fills up, sends apply backpressure and emit() will drop-and-log an
	// event if the run-level context is cancelled while waiting. The
	// watcher closes this channel on shutdown so consumers can range
	// over it.
	Events chan Event

	// now is swappable in tests.
	now func() time.Time
}

// defaultEventBuffer is how many events one repo can queue up before the
// channel blocks. Large enough that a few slow UI renders don't cause
// drops; small enough that genuine consumer starvation is visible.
const defaultEventBuffer = 64

// New builds a watcher. Callers can read w.Events for live updates.
func New(client gh.PRClient, filters []config.Filter, interval time.Duration, logger *log.Logger) *Watcher {
	if logger == nil {
		logger = log.Default()
	}
	return &Watcher{
		Client:   client,
		Filters:  filters,
		Interval: interval,
		Logger:   logger,
		Events:   make(chan Event, defaultEventBuffer),
		now:      time.Now,
	}
}

// Run starts one goroutine per repo and blocks until ctx is cancelled. It
// closes Events before returning so consumers can range over the channel.
func (w *Watcher) Run(ctx context.Context) error {
	if len(w.Filters) == 0 {
		return fmt.Errorf("no filters configured")
	}
	if w.Interval <= 0 {
		return fmt.Errorf("poll interval must be positive, got %s", w.Interval)
	}

	byRepo := groupFiltersByRepo(w.Filters)
	w.Logger.Printf("watcher started: %d repo(s), %d filter(s), poll every %s",
		len(byRepo), len(w.Filters), w.Interval)

	var wg sync.WaitGroup
	for repo, filters := range byRepo {
		wg.Add(1)
		go func(repo string, filters []config.Filter) {
			defer wg.Done()
			w.runRepo(ctx, repo, filters)
		}(repo, filters)
	}
	wg.Wait()
	close(w.Events)
	w.Logger.Printf("watcher stopped")
	return nil
}

// runRepo is the per-repo long-running loop. Each repo has its own ticker so
// slow repos never delay faster ones.
func (w *Watcher) runRepo(ctx context.Context, repo string, filters []config.Filter) {
	tick := time.NewTicker(w.Interval)
	defer tick.Stop()

	// Run one iteration immediately.
	w.tickRepo(ctx, repo, filters)

	for {
		select {
		case <-ctx.Done():
			w.Logger.Printf("[%s] stopping: %v", repo, ctx.Err())
			return
		case <-tick.C:
			w.tickRepo(ctx, repo, filters)
		}
	}
}

// tickRepo runs a single bounded iteration for one repo. The gh calls get a
// per-tick timeout (so a hung invocation can't stall the loop); event emits
// get the run-level context so a slow tick doesn't cause snapshots to be
// silently dropped just because the tick expired.
func (w *Watcher) tickRepo(runCtx context.Context, repo string, filters []config.Filter) {
	callCtx, cancel := context.WithTimeout(runCtx, w.Interval)
	defer cancel()
	if err := w.processRepo(runCtx, callCtx, repo, filters); err != nil {
		w.Logger.Printf("[%s] iteration error: %v", repo, err)
		w.emit(runCtx, Event{Repo: repo, Kind: EventError, At: w.now(), Message: err.Error()})
	}
}

// emit sends on Events. It only gives up when the run-level context is
// cancelled (shutdown). Dropped events are logged so the operator can see
// backpressure instead of silent loss.
func (w *Watcher) emit(ctx context.Context, ev Event) {
	select {
	case w.Events <- ev:
	case <-ctx.Done():
		w.Logger.Printf("[%s] dropping event kind=%d: %v", ev.Repo, ev.Kind, ctx.Err())
	}
}

func groupFiltersByRepo(filters []config.Filter) map[string][]config.Filter {
	out := map[string][]config.Filter{}
	for _, f := range filters {
		out[f.Repo] = append(out[f.Repo], f)
	}
	return out
}

// processRepo picks at most one PR per repo per tick to act on: the oldest
// queued PR that matches any filter, has auto-merge on, and isn't blocked on
// merge conflicts. PRs with conflicts are walked past so a stuck head doesn't
// freeze the queue; each one is reported via EventConflict so the operator
// still sees it.
//
// emitCtx is the run-level context used for publishing events; callCtx has
// the per-tick timeout and scopes every gh invocation.
func (w *Watcher) processRepo(emitCtx, callCtx context.Context, repo string, filters []config.Filter) error {
	prs, err := w.Client.ListOpenPRs(callCtx, repo)
	if err != nil {
		return err
	}

	queue := filterQueue(prs, filters)
	sort.Slice(queue, func(i, j int) bool {
		return queue[i].CreatedAt.Before(queue[j].CreatedAt)
	})

	if len(queue) == 0 {
		w.Logger.Printf("[%s] no auto-merge PRs match filters", repo)
		w.emit(emitCtx, Event{Repo: repo, Kind: EventSnapshot, At: w.now(), PRs: nil})
		return nil
	}

	// Walk oldest-first, refetching each candidate. Drop PRs that became
	// ineligible between list and view; emit a conflict event for each
	// conflicted PR and keep scanning. Stop at the first actionable PR.
	// Conflicts are emitted before the terminal snapshot so the snapshot
	// remains the source of truth for the repo's head.
	var head *gh.PullRequest
	for i := 0; i < len(queue); {
		candidate := queue[i]
		full, err := w.Client.GetPR(callCtx, repo, candidate.Number)
		if err != nil {
			return fmt.Errorf("view PR #%d: %w", candidate.Number, err)
		}
		if full.AutoMergeRequest == nil {
			w.Logger.Printf("[%s] PR #%d no longer has auto-merge enabled; skipping", repo, full.Number)
			queue = append(queue[:i], queue[i+1:]...)
			continue
		}
		if full.IsDraft {
			w.Logger.Printf("[%s] PR #%d is now a draft; skipping", repo, full.Number)
			queue = append(queue[:i], queue[i+1:]...)
			continue
		}
		if !prutil.MatchesAny(*full, filters) {
			w.Logger.Printf("[%s] PR #%d no longer matches any filter; skipping", repo, full.Number)
			queue = append(queue[:i], queue[i+1:]...)
			continue
		}
		// Splice the refetched PR back so the snapshot reflects fresh state.
		queue[i] = *full
		if isConflicted(full) {
			w.Logger.Printf("[%s] PR #%d has conflicts; advancing to next eligible PR", repo, full.Number)
			w.emit(emitCtx, Event{Repo: repo, Kind: EventConflict, At: w.now(), Head: full,
				Message: conflictMessage(full)})
			i++
			continue
		}
		head = full
		break
	}

	w.emit(emitCtx, Event{Repo: repo, Kind: EventSnapshot, At: w.now(), PRs: queue, Head: head})

	if head == nil {
		return nil
	}

	w.Logger.Printf("[%s] head PR #%d %q (state=%s, mergeable=%s)",
		repo, head.Number, prutil.Truncate(head.Title, 60), head.MergeStateStatus, head.Mergeable)

	switch head.MergeStateStatus {
	case "BEHIND":
		w.Logger.Printf("[%s] rebasing PR #%d", repo, head.Number)
		if err := w.Client.UpdateBranchRebase(callCtx, head.ID); err != nil {
			return fmt.Errorf("rebase PR #%d: %w", head.Number, err)
		}
		w.emit(emitCtx, Event{Repo: repo, Kind: EventRebased, At: w.now(), Head: head,
			Message: fmt.Sprintf("rebased #%d", head.Number)})
	case "BLOCKED", "UNSTABLE", "UNKNOWN":
		summarizeChecks(w.Logger, repo, head)
		w.emit(emitCtx, Event{Repo: repo, Kind: EventWaiting, At: w.now(), Head: head,
			Message: "waiting on checks/reviews"})
	case "CLEAN", "HAS_HOOKS":
		w.Logger.Printf("[%s] PR #%d clean, waiting for GitHub auto-merge", repo, head.Number)
		w.emit(emitCtx, Event{Repo: repo, Kind: EventWaiting, At: w.now(), Head: head,
			Message: "clean; waiting for GitHub auto-merge"})
	default:
		// Unknown/new state from GitHub. Log and surface so the operator
		// can tell what happened instead of silently no-oping.
		w.Logger.Printf("[%s] PR #%d has unhandled merge state status %q (mergeable=%s); treating as unknown",
			repo, head.Number, head.MergeStateStatus, head.Mergeable)
		w.emit(emitCtx, Event{Repo: repo, Kind: EventWaiting, At: w.now(), Head: head,
			Message: "unhandled merge state " + head.MergeStateStatus})
	}
	return nil
}

// isConflicted reports whether the PR has merge conflicts that prevent
// either a rebase or a clean merge. DIRTY is GitHub's explicit conflict
// state; BEHIND + CONFLICTING means a rebase would conflict.
func isConflicted(pr *gh.PullRequest) bool {
	if pr.MergeStateStatus == "DIRTY" {
		return true
	}
	return pr.MergeStateStatus == "BEHIND" && pr.Mergeable == "CONFLICTING"
}

func conflictMessage(pr *gh.PullRequest) string {
	if pr.MergeStateStatus == "DIRTY" {
		return "dirty; human action required"
	}
	return "conflicts; cannot rebase"
}

// filterQueue returns PRs that have auto-merge enabled and match at least one
// filter from the provided list (all filters here share the same repo).
func filterQueue(prs []gh.PullRequest, filters []config.Filter) []gh.PullRequest {
	var out []gh.PullRequest
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		if pr.AutoMergeRequest == nil {
			continue
		}
		if !prutil.MatchesAny(pr, filters) {
			continue
		}
		out = append(out, pr)
	}
	return out
}

func summarizeChecks(logger *log.Logger, repo string, pr *gh.PullRequest) {
	var pending, failed, passed int
	var firstFailure string
	for _, c := range pr.StatusCheckRoll {
		switch {
		case c.Failed():
			failed++
			if firstFailure == "" {
				firstFailure = c.Display()
			}
		case c.Passed():
			passed++
		case c.Pending():
			pending++
		}
	}
	msg := fmt.Sprintf("[%s] PR #%d checks: %d passed, %d pending, %d failed",
		repo, pr.Number, passed, pending, failed)
	if firstFailure != "" {
		msg += " (first failure: " + firstFailure + ")"
	}
	logger.Print(msg)
}
