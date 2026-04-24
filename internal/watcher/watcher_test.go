package watcher

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
)

// fakeClient is a scripted gh.PRClient for tests. All fields are goroutine-safe
// via the mutex so per-repo goroutines can hammer it concurrently.
type fakeClient struct {
	mu sync.Mutex

	listOpenPRsResults map[string][]gh.PullRequest
	listOpenPRsErr     error
	getPRResults       map[string]*gh.PullRequest // key: "repo#num"
	getPRErr           error
	rebaseErr          error

	rebaseCalls []string // PR node IDs passed to UpdateBranchRebase
	listCalls   []string // repos passed to ListOpenPRs
	getCalls    []string // "repo#num"
}

func (f *fakeClient) ListOpenPRs(_ context.Context, repo string) ([]gh.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, repo)
	if f.listOpenPRsErr != nil {
		return nil, f.listOpenPRsErr
	}
	return append([]gh.PullRequest(nil), f.listOpenPRsResults[repo]...), nil
}

func (f *fakeClient) GetPR(_ context.Context, repo string, number int) (*gh.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := prKey(repo, number)
	f.getCalls = append(f.getCalls, key)
	if f.getPRErr != nil {
		return nil, f.getPRErr
	}
	pr := f.getPRResults[key]
	if pr == nil {
		return nil, errors.New("not found: " + key)
	}
	// Return a copy so callers can't mutate our fixture.
	cp := *pr
	return &cp, nil
}

func (f *fakeClient) UpdateBranchRebase(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebaseCalls = append(f.rebaseCalls, id)
	return f.rebaseErr
}

func prKey(repo string, num int) string {
	return repo + "#" + itoa(num)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func autoMergePR(num int, created time.Time, opts ...func(*gh.PullRequest)) gh.PullRequest {
	pr := gh.PullRequest{
		ID:               "node-" + itoa(num),
		Number:           num,
		Title:            "PR " + itoa(num),
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "BEHIND",
		Author:           gh.Actor{Login: "alice"},
		AutoMergeRequest: &gh.AutoMergeReq{MergeMethod: "squash"},
		CreatedAt:        created,
	}
	for _, opt := range opts {
		opt(&pr)
	}
	return pr
}

func newTestWatcher(t *testing.T, client gh.PRClient, filters []config.Filter) *Watcher {
	t.Helper()
	w := New(client, filters, 100*time.Millisecond, log.New(io.Discard, "", 0))
	// Buffered channel so tests that don't drain don't deadlock the watcher.
	w.Events = make(chan Event, 32)
	w.now = func() time.Time { return time.Unix(0, 0) }
	return w
}

func TestGroupFiltersByRepo(t *testing.T) {
	t.Parallel()
	filters := []config.Filter{
		{Name: "a", Repo: "org/a"},
		{Name: "b", Repo: "org/b"},
		{Name: "a2", Repo: "org/a", Authors: []string{"x"}},
	}
	got := groupFiltersByRepo(filters)
	require.Len(t, got, 2)
	assert.Len(t, got["org/a"], 2)
	assert.Len(t, got["org/b"], 1)
}

func TestFilterQueue_DropsDraftsAndNonAutoMerge(t *testing.T) {
	t.Parallel()
	now := time.Now()
	prs := []gh.PullRequest{
		autoMergePR(1, now),                                                 // eligible
		autoMergePR(2, now, func(p *gh.PullRequest) { p.IsDraft = true }),   // draft
		autoMergePR(3, now, func(p *gh.PullRequest) { p.AutoMergeRequest = nil }), // no auto-merge
	}
	filters := []config.Filter{{Name: "f", Repo: "org/r"}}
	got := filterQueue(prs, filters)
	require.Len(t, got, 1)
	assert.Equal(t, 1, got[0].Number)
}

func TestDropByNumber(t *testing.T) {
	t.Parallel()
	now := time.Now()
	queue := []gh.PullRequest{autoMergePR(1, now), autoMergePR(2, now), autoMergePR(3, now)}
	out := dropByNumber(queue, 2)
	require.Len(t, out, 2)
	assert.Equal(t, 1, out[0].Number)
	assert.Equal(t, 3, out[1].Number)
}

func TestProcessRepo_RebasesBehindHead(t *testing.T) {
	t.Parallel()
	now := time.Now()
	pr1 := autoMergePR(1, now.Add(-2*time.Hour)) // oldest
	pr2 := autoMergePR(2, now.Add(-1*time.Hour))

	fc := &fakeClient{
		listOpenPRsResults: map[string][]gh.PullRequest{
			"org/r": {pr2, pr1}, // not yet sorted
		},
		getPRResults: map[string]*gh.PullRequest{
			prKey("org/r", 1): &pr1,
		},
	}
	w := newTestWatcher(t, fc, []config.Filter{{Name: "f", Repo: "org/r"}})

	err := w.processRepo(context.Background(), "org/r", w.Filters)
	require.NoError(t, err)

	// It must have rebased the oldest PR (FIFO).
	require.Len(t, fc.rebaseCalls, 1)
	assert.Equal(t, "node-1", fc.rebaseCalls[0])

	// Confirm the event sequence: snapshot, then rebased.
	events := drainEvents(w.Events)
	require.GreaterOrEqual(t, len(events), 2)
	assert.Equal(t, EventSnapshot, events[0].Kind)
	assert.Equal(t, EventRebased, events[len(events)-1].Kind)
}

func TestProcessRepo_SkipsRebaseOnConflict(t *testing.T) {
	t.Parallel()
	now := time.Now()
	pr := autoMergePR(1, now, func(p *gh.PullRequest) { p.Mergeable = "CONFLICTING" })
	fc := &fakeClient{
		listOpenPRsResults: map[string][]gh.PullRequest{"org/r": {pr}},
		getPRResults:       map[string]*gh.PullRequest{prKey("org/r", 1): &pr},
	}
	w := newTestWatcher(t, fc, []config.Filter{{Name: "f", Repo: "org/r"}})

	require.NoError(t, w.processRepo(context.Background(), "org/r", w.Filters))
	assert.Empty(t, fc.rebaseCalls, "must not rebase when CONFLICTING")

	events := drainEvents(w.Events)
	assert.Contains(t, kinds(events), EventConflict)
}

func TestProcessRepo_SkipsWhenAutoMergeDisabledBetweenListAndView(t *testing.T) {
	t.Parallel()
	now := time.Now()
	listed := autoMergePR(1, now)
	viewed := listed
	viewed.AutoMergeRequest = nil // disabled after list
	fc := &fakeClient{
		listOpenPRsResults: map[string][]gh.PullRequest{"org/r": {listed}},
		getPRResults:       map[string]*gh.PullRequest{prKey("org/r", 1): &viewed},
	}
	w := newTestWatcher(t, fc, []config.Filter{{Name: "f", Repo: "org/r"}})

	require.NoError(t, w.processRepo(context.Background(), "org/r", w.Filters))
	assert.Empty(t, fc.rebaseCalls, "must not rebase when auto-merge was disabled")
}

func TestProcessRepo_CleanEmitsWaiting(t *testing.T) {
	t.Parallel()
	now := time.Now()
	pr := autoMergePR(1, now, func(p *gh.PullRequest) { p.MergeStateStatus = "CLEAN" })
	fc := &fakeClient{
		listOpenPRsResults: map[string][]gh.PullRequest{"org/r": {pr}},
		getPRResults:       map[string]*gh.PullRequest{prKey("org/r", 1): &pr},
	}
	w := newTestWatcher(t, fc, []config.Filter{{Name: "f", Repo: "org/r"}})

	require.NoError(t, w.processRepo(context.Background(), "org/r", w.Filters))
	assert.Empty(t, fc.rebaseCalls)

	events := drainEvents(w.Events)
	assert.Contains(t, kinds(events), EventWaiting)
}

func TestProcessRepo_UnknownStateLogsAndEmits(t *testing.T) {
	t.Parallel()
	now := time.Now()
	pr := autoMergePR(1, now, func(p *gh.PullRequest) { p.MergeStateStatus = "NEW_STATE_FROM_GITHUB" })
	fc := &fakeClient{
		listOpenPRsResults: map[string][]gh.PullRequest{"org/r": {pr}},
		getPRResults:       map[string]*gh.PullRequest{prKey("org/r", 1): &pr},
	}
	w := newTestWatcher(t, fc, []config.Filter{{Name: "f", Repo: "org/r"}})

	require.NoError(t, w.processRepo(context.Background(), "org/r", w.Filters))
	events := drainEvents(w.Events)
	assert.Contains(t, kinds(events), EventWaiting)
}

func TestProcessRepo_NoEligiblePRsEmitsEmptySnapshot(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{
		listOpenPRsResults: map[string][]gh.PullRequest{"org/r": {}},
	}
	w := newTestWatcher(t, fc, []config.Filter{{Name: "f", Repo: "org/r"}})

	require.NoError(t, w.processRepo(context.Background(), "org/r", w.Filters))
	events := drainEvents(w.Events)
	require.Len(t, events, 1)
	assert.Equal(t, EventSnapshot, events[0].Kind)
	assert.Empty(t, events[0].PRs)
}

func TestRun_PerRepoGoroutines(t *testing.T) {
	t.Parallel()
	now := time.Now()
	pr := autoMergePR(1, now, func(p *gh.PullRequest) { p.MergeStateStatus = "CLEAN" })

	fc := &fakeClient{
		listOpenPRsResults: map[string][]gh.PullRequest{
			"org/a": {pr},
			"org/b": {pr},
		},
		getPRResults: map[string]*gh.PullRequest{
			prKey("org/a", 1): &pr,
			prKey("org/b", 1): &pr,
		},
	}
	w := newTestWatcher(t, fc, []config.Filter{
		{Name: "a", Repo: "org/a"},
		{Name: "b", Repo: "org/b"},
	})
	w.Interval = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	var events []Event
	for ev := range w.Events {
		events = append(events, ev)
	}
	require.NoError(t, <-done)

	repos := map[string]bool{}
	for _, ev := range events {
		repos[ev.Repo] = true
	}
	assert.True(t, repos["org/a"])
	assert.True(t, repos["org/b"])
}

func TestRun_RejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	w := newTestWatcher(t, &fakeClient{}, nil)
	err := w.Run(context.Background())
	require.Error(t, err)

	w2 := newTestWatcher(t, &fakeClient{}, []config.Filter{{Name: "f", Repo: "o/r"}})
	w2.Interval = 0
	err = w2.Run(context.Background())
	require.Error(t, err)
}

func drainEvents(ch chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func kinds(events []Event) []EventKind {
	out := make([]EventKind, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Kind)
	}
	return out
}
