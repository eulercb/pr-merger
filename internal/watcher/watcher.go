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

// Watcher monitors PRs across the configured filters and, one at a time per
// repo, rebases a PR that has auto-merge enabled and is behind its base.
type Watcher struct {
	Client   *gh.Client
	Filters  []config.Filter
	Interval time.Duration
	Logger   *log.Logger
}

func New(client *gh.Client, filters []config.Filter, interval time.Duration, logger *log.Logger) *Watcher {
	if logger == nil {
		logger = log.Default()
	}
	return &Watcher{
		Client:   client,
		Filters:  filters,
		Interval: interval,
		Logger:   logger,
	}
}

// Run loops until ctx is cancelled, polling each configured repo on every tick.
func (w *Watcher) Run(ctx context.Context) error {
	if len(w.Filters) == 0 {
		return fmt.Errorf("no filters configured")
	}
	if w.Interval <= 0 {
		return fmt.Errorf("poll interval must be positive, got %s", w.Interval)
	}
	w.Logger.Printf("watcher started: %d filter(s), poll every %s", len(w.Filters), w.Interval)

	tick := time.NewTicker(w.Interval)
	defer tick.Stop()

	// Run one iteration immediately.
	w.iterate(ctx)

	for {
		select {
		case <-ctx.Done():
			w.Logger.Printf("watcher stopping: %v", ctx.Err())
			return nil
		case <-tick.C:
			w.iterate(ctx)
		}
	}
}

// iterate fans out across repos (one goroutine per repo) so slow repos don't
// block others. Inside a repo the processing is strictly serial, and each
// per-repo call is bounded by Interval so a hung gh invocation can't stall
// the whole watcher.
func (w *Watcher) iterate(ctx context.Context) {
	byRepo := groupFiltersByRepo(w.Filters)

	var wg sync.WaitGroup
	for repo, filters := range byRepo {
		wg.Add(1)
		go func(repo string, filters []config.Filter) {
			defer wg.Done()
			repoCtx, cancel := context.WithTimeout(ctx, w.Interval)
			defer cancel()
			if err := w.processRepo(repoCtx, repo, filters); err != nil {
				w.Logger.Printf("[%s] iteration error: %v", repo, err)
			}
		}(repo, filters)
	}
	wg.Wait()
}

func groupFiltersByRepo(filters []config.Filter) map[string][]config.Filter {
	out := map[string][]config.Filter{}
	for _, f := range filters {
		out[f.Repo] = append(out[f.Repo], f)
	}
	return out
}

// processRepo picks at most one PR per repo per tick: the oldest queued PR
// that matches any filter, has auto-merge on, and is either BEHIND (rebase it)
// or BLOCKED/UNSTABLE with running checks (just report status).
func (w *Watcher) processRepo(ctx context.Context, repo string, filters []config.Filter) error {
	prs, err := w.Client.ListOpenPRs(ctx, repo)
	if err != nil {
		return err
	}

	queue := filterQueue(prs, filters)
	if len(queue) == 0 {
		w.Logger.Printf("[%s] no auto-merge PRs match filters", repo)
		return nil
	}

	// Oldest first (FIFO).
	sort.Slice(queue, func(i, j int) bool {
		return queue[i].CreatedAt.Before(queue[j].CreatedAt)
	})

	head := queue[0]

	// Refetch with status-check rollup for accurate state.
	full, err := w.Client.GetPR(ctx, repo, head.Number)
	if err != nil {
		return fmt.Errorf("view PR #%d: %w", head.Number, err)
	}

	// Re-validate eligibility: the user may have disabled auto-merge,
	// marked the PR as draft, or changed labels between list and view.
	if full.AutoMergeRequest == nil {
		w.Logger.Printf("[%s] PR #%d no longer has auto-merge enabled; skipping", repo, full.Number)
		return nil
	}
	if full.IsDraft {
		w.Logger.Printf("[%s] PR #%d is now a draft; skipping", repo, full.Number)
		return nil
	}
	if !prutil.MatchesAny(*full, filters) {
		w.Logger.Printf("[%s] PR #%d no longer matches any filter; skipping", repo, full.Number)
		return nil
	}

	w.Logger.Printf("[%s] head PR #%d %q (state=%s, mergeable=%s)",
		repo, full.Number, prutil.Truncate(full.Title, 60), full.MergeStateStatus, full.Mergeable)

	switch full.MergeStateStatus {
	case "BEHIND":
		if full.Mergeable == "CONFLICTING" {
			w.Logger.Printf("[%s] PR #%d has conflicts; skipping rebase", repo, full.Number)
			return nil
		}
		w.Logger.Printf("[%s] rebasing PR #%d", repo, full.Number)
		if err := w.Client.UpdateBranchRebase(ctx, full.ID); err != nil {
			return fmt.Errorf("rebase PR #%d: %w", full.Number, err)
		}
	case "BLOCKED", "UNSTABLE", "UNKNOWN":
		// Waiting on checks, reviews, or required statuses.
		summarizeChecks(w.Logger, repo, full)
	case "CLEAN", "HAS_HOOKS":
		// Ready — auto-merge will fire on GitHub's side. Nothing to do.
		w.Logger.Printf("[%s] PR #%d clean, waiting for GitHub auto-merge", repo, full.Number)
	case "DIRTY":
		w.Logger.Printf("[%s] PR #%d has merge conflicts; human action required", repo, full.Number)
	default:
		// Unknown/new state from GitHub. Log so the operator can tell what
		// happened instead of silently no-oping.
		w.Logger.Printf("[%s] PR #%d has unhandled merge state status %q (mergeable=%s); treating as unknown",
			repo, full.Number, full.MergeStateStatus, full.Mergeable)
	}
	return nil
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
