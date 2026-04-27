// Package wizard runs the first-run configuration flow: it discovers the
// current user's recent contribution repos (via the gh CLI), lets the user
// pick or type one, and produces a ready-to-save config.
package wizard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/eulercb/pr-merger/internal/config"
)

// Discoverer is the subset of gh.Client the wizard needs. Keeping it small
// makes the wizard testable without shelling out.
type Discoverer interface {
	CurrentUser(ctx context.Context) (string, error)
	RecentContributedRepos(ctx context.Context, limit int) ([]string, error)
}

// Discovery is the information the wizard collects up-front via GitHub. The
// UI layer uses this to pre-populate defaults.
type Discovery struct {
	Login string
	Repos []string
	Err   error
}

// Discover calls the GitHub API via the provided Discoverer. Errors are
// stored on the result rather than returned so the wizard can still prompt
// with sensible fallbacks when discovery fails (e.g. offline, gh not logged
// in).
func Discover(ctx context.Context, d Discoverer) Discovery {
	if d == nil {
		return Discovery{Err: errors.New("no discoverer provided")}
	}
	login, err := d.CurrentUser(ctx)
	if err != nil {
		return Discovery{Err: fmt.Errorf("current user: %w", err)}
	}
	repos, err := d.RecentContributedRepos(ctx, 10)
	if err != nil {
		return Discovery{Login: login, Err: fmt.Errorf("recent repos: %w", err)}
	}
	return Discovery{Login: login, Repos: repos}
}

// Answers holds raw user responses collected by the wizard UI.
type Answers struct {
	Repo            string
	FilterName      string
	AuthorRestrict  bool
	Login           string
	PollIntervalStr string
}

// Result is the outcome of running the wizard.
type Result struct {
	Config *config.Config
	Path   string
}

// ValidateRepo reports whether s is a well-formed "OWNER/NAME" slug. Used
// by both the wizard's live per-step validation and the final BuildConfig
// check so the two can't drift.
func ValidateRepo(s string) error {
	repo := strings.TrimSpace(s)
	if repo == "" {
		return errors.New("repo is required")
	}
	if strings.Count(repo, "/") != 1 {
		return fmt.Errorf("repo must be OWNER/NAME, got %q", repo)
	}
	parts := strings.Split(repo, "/")
	if parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("repo must be OWNER/NAME, got %q", repo)
	}
	return nil
}

// BuildConfig turns raw answers into a ready-to-save config. Returns a
// descriptive error for each invalid field so the UI can surface them.
func BuildConfig(a Answers) (*config.Config, error) {
	if err := ValidateRepo(a.Repo); err != nil {
		return nil, err
	}
	repo := strings.TrimSpace(a.Repo)

	name := strings.TrimSpace(a.FilterName)
	if name == "" {
		name = defaultFilterName(repo)
	}

	interval := 30
	if s := strings.TrimSpace(a.PollIntervalStr); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("poll interval must be a positive integer (seconds), got %q", s)
		}
		interval = v
	}

	filter := config.Filter{
		Name: name,
		Repo: repo,
	}
	if a.AuthorRestrict {
		login := strings.TrimSpace(a.Login)
		if login == "" {
			return nil, errors.New("cannot restrict to your PRs: GitHub login is unknown")
		}
		filter.Authors = []string{login}
	}

	return &config.Config{
		PollInterval: interval,
		Filters:      []config.Filter{filter},
	}, nil
}

// defaultFilterName synthesises a readable filter name from a repo slug.
// "toggl/ic-tribe" → "ic-tribe-automerge".
func defaultFilterName(repo string) string {
	slash := strings.LastIndex(repo, "/")
	base := repo
	if slash >= 0 {
		base = repo[slash+1:]
	}
	if base == "" {
		base = "auto"
	}
	return base + "-automerge"
}

// DefaultRepo picks the top recent contribution repo if any, otherwise "".
// Pure helper so tests can pin the choice rule.
func DefaultRepo(d Discovery) string {
	if len(d.Repos) == 0 {
		return ""
	}
	return d.Repos[0]
}

// RunResult captures the outcome of a headless run (used by tests and by
// non-interactive callers in the future).
type RunResult struct {
	Discovery Discovery
	Config    *config.Config
	Elapsed   time.Duration
}

// PrepareHeadless runs discovery and builds a config directly from answers.
// The TUI wizard uses the same helpers; this one is here so callers can run
// the full flow without a terminal (e.g. for tests).
func PrepareHeadless(ctx context.Context, d Discoverer, a Answers) (RunResult, error) {
	start := time.Now()
	disc := Discover(ctx, d)
	if a.Repo == "" {
		a.Repo = DefaultRepo(disc)
	}
	if a.AuthorRestrict && a.Login == "" {
		a.Login = disc.Login
	}
	cfg, err := BuildConfig(a)
	if err != nil {
		return RunResult{Discovery: disc, Elapsed: time.Since(start)}, err
	}
	return RunResult{Discovery: disc, Config: cfg, Elapsed: time.Since(start)}, nil
}
