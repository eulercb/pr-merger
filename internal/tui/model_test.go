package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
	"github.com/eulercb/pr-merger/internal/watcher"
)

func newTestModel() Model {
	return NewModel(&config.Config{
		PollInterval: 30,
		Filters: []config.Filter{
			{Name: "b", Repo: "org/b"},
			{Name: "a", Repo: "org/a"},
			{Name: "a2", Repo: "org/a"}, // duplicate repo shouldn't create two panels
		},
	})
}

func TestNewModel_RepoOrderIsSortedAndUnique(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	assert.Equal(t, []string{"org/a", "org/b"}, m.repos)
	assert.Len(t, m.states, 2)
}

func TestApplyEvent_Snapshot(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	now := time.Now()
	pr := gh.PullRequest{Number: 42, Title: "x"}

	m.applyEvent(watcher.Event{
		Repo: "org/a",
		Kind: watcher.EventSnapshot,
		At:   now,
		PRs:  []gh.PullRequest{pr},
	})

	st := m.states["org/a"]
	require.NotNil(t, st)
	assert.Len(t, st.PRs, 1)
	assert.Equal(t, now, st.LastUpdate)
}

func TestApplyEvent_UnknownRepoIsRegistered(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.applyEvent(watcher.Event{Repo: "new/repo", Kind: watcher.EventSnapshot})
	assert.Contains(t, m.repos, "new/repo")
	require.NotNil(t, m.states["new/repo"])
}

func TestApplyEvent_ErrorClearsOnNextSnapshot(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.applyEvent(watcher.Event{Repo: "org/a", Kind: watcher.EventError, Message: "boom"})
	require.Equal(t, "boom", m.states["org/a"].LastErr)

	m.applyEvent(watcher.Event{Repo: "org/a", Kind: watcher.EventSnapshot})
	assert.Empty(t, m.states["org/a"].LastErr)
}

func TestApplyEvent_RebasedSetsMessage(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	pr := &gh.PullRequest{Number: 7, Title: "rebase me"}
	m.applyEvent(watcher.Event{
		Repo: "org/a", Kind: watcher.EventRebased, Head: pr, Message: "rebased #7",
	})
	st := m.states["org/a"]
	assert.Equal(t, "rebased #7", st.LastMsg)
	require.NotNil(t, st.Head)
	assert.Equal(t, 7, st.Head.Number)
}

func TestApplyEvent_CursorResetsWhenBeyondQueue(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.states["org/a"].Cursor = 5 // stale cursor from earlier frame

	m.applyEvent(watcher.Event{
		Repo: "org/a", Kind: watcher.EventSnapshot,
		PRs: []gh.PullRequest{{Number: 1}, {Number: 2}},
	})
	assert.Equal(t, 0, m.states["org/a"].Cursor)
}

// TestApplyEvent_EmptySnapshotClearsStaleHead guards the Copilot-flagged
// regression: after a queue drains, the status bar must stop showing the
// PR we were acting on last tick.
func TestApplyEvent_EmptySnapshotClearsStaleHead(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	prev := &gh.PullRequest{Number: 42, Title: "old head"}
	m.states["org/a"].Head = prev
	m.states["org/a"].LastMsg = "rebased #42"

	m.applyEvent(watcher.Event{Repo: "org/a", Kind: watcher.EventSnapshot, PRs: nil})

	assert.Nil(t, m.states["org/a"].Head, "Head must be cleared when queue drains")
	assert.Empty(t, m.states["org/a"].LastMsg, "stale rebase message should be cleared too")
}

func TestUpdate_QuitKey(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	mm, ok := updated.(Model)
	require.True(t, ok)
	assert.True(t, mm.Quit())
	assert.NotNil(t, cmd)
}

func TestUpdate_NextPrevRepo(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	require.Equal(t, 0, m.active)

	// Tab → next.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	assert.Equal(t, 1, m.active)

	// Tab wraps.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	assert.Equal(t, 0, m.active)

	// Shift+Tab wraps backwards.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(Model)
	assert.Equal(t, 1, m.active)
}

func TestUpdate_CursorNavigationWithinPanel(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	m.states["org/a"].PRs = []gh.PullRequest{{Number: 1}, {Number: 2}, {Number: 3}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	assert.Equal(t, 1, m.states["org/a"].Cursor)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = updated.(Model)
	assert.Equal(t, 0, m.states["org/a"].Cursor)

	// k at position 0 wraps to end.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = updated.(Model)
	assert.Equal(t, 2, m.states["org/a"].Cursor)
}

func TestUpdate_WindowSize(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)
	assert.Equal(t, 120, m.width)
	assert.Equal(t, 40, m.height)
}

func TestUpdate_HelpToggle(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	assert.False(t, m.showHelp)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = updated.(Model)
	assert.True(t, m.showHelp)
}

func TestView_LoadingBeforeSize(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	assert.Equal(t, "Loading...", m.View())
}

func TestView_RendersAfterSize(t *testing.T) {
	t.Parallel()
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)
	rendered := m.View()
	assert.NotEmpty(t, rendered)
	assert.Contains(t, rendered, "pr-merger")
}

func TestFmtDuration(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "just now", fmtDuration(100*time.Millisecond))
	assert.Equal(t, "5s ago", fmtDuration(5*time.Second))
	assert.Equal(t, "2m ago", fmtDuration(2*time.Minute))
	assert.Equal(t, "3h ago", fmtDuration(3*time.Hour))
}
