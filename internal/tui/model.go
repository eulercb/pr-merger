// Package tui implements the Bubble Tea terminal UI for pr-merger. It shows
// one panel per repo with the current auto-merge queue and live per-PR
// status icons. Updates arrive through the watcher's event channel, so the
// UI reflects actions as the watcher takes them.
package tui

import (
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
	"github.com/eulercb/pr-merger/internal/prutil"
	"github.com/eulercb/pr-merger/internal/watcher"
)

// RepoState is the rendered snapshot for one repo panel.
type RepoState struct {
	Repo       string
	PRs        []gh.PullRequest
	Head       *gh.PullRequest
	LastUpdate time.Time
	LastMsg    string
	LastErr    string
	Cursor     int
}

// Model is the top-level Bubble Tea model.
type Model struct {
	keys   KeyMap
	cfg    *config.Config
	repos  []string
	states map[string]*RepoState

	width, height int
	active        int // index into repos
	showHelp      bool
	quit          bool
}

// NewModel builds a model pre-populated with one RepoState per configured
// repo so the TUI renders something sensible before the first watcher tick.
func NewModel(cfg *config.Config) Model {
	repos := repoOrder(cfg)
	states := make(map[string]*RepoState, len(repos))
	for _, r := range repos {
		states[r] = &RepoState{Repo: r}
	}
	return Model{
		keys:   DefaultKeyMap(),
		cfg:    cfg,
		repos:  repos,
		states: states,
	}
}

func repoOrder(cfg *config.Config) []string {
	seen := map[string]bool{}
	var repos []string
	for _, f := range cfg.Filters {
		if !seen[f.Repo] {
			seen[f.Repo] = true
			repos = append(repos, f.Repo)
		}
	}
	sort.Strings(repos)
	return repos
}

// Init does nothing: events arrive via WatcherEventMsg on the main program's
// event loop (see cmd/pr-merger/main.go).
func (m Model) Init() tea.Cmd { return nil }

// WatcherEventMsg wraps a watcher.Event so the main program can forward
// events from the watcher's channel into the Bubble Tea event loop.
type WatcherEventMsg watcher.Event

// Update applies key events and watcher events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case WatcherEventMsg:
		m.applyEvent(watcher.Event(msg))
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(m.keys.Quit, msg):
		m.quit = true
		return m, tea.Quit
	case keyMatches(m.keys.Help, msg):
		m.showHelp = !m.showHelp
	case keyMatches(m.keys.NextRepo, msg):
		if len(m.repos) > 0 {
			m.active = (m.active + 1) % len(m.repos)
		}
	case keyMatches(m.keys.PrevRepo, msg):
		if len(m.repos) > 0 {
			m.active = (m.active - 1 + len(m.repos)) % len(m.repos)
		}
	case keyMatches(m.keys.Down, msg):
		m.moveCursor(1)
	case keyMatches(m.keys.Up, msg):
		m.moveCursor(-1)
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if len(m.repos) == 0 {
		return
	}
	st := m.states[m.repos[m.active]]
	if st == nil || len(st.PRs) == 0 {
		return
	}
	st.Cursor = (st.Cursor + delta + len(st.PRs)) % len(st.PRs)
}

// applyEvent merges a watcher event into the matching repo state.
func (m *Model) applyEvent(ev watcher.Event) {
	st, ok := m.states[ev.Repo]
	if !ok {
		// Event for a repo we don't know about yet — register it.
		st = &RepoState{Repo: ev.Repo}
		m.states[ev.Repo] = st
		m.repos = append(m.repos, ev.Repo)
		sort.Strings(m.repos)
	}
	st.LastUpdate = ev.At
	switch ev.Kind {
	case watcher.EventSnapshot:
		st.PRs = ev.PRs
		switch {
		case ev.Head != nil:
			st.Head = ev.Head
		case len(ev.PRs) == 0:
			// Queue drained — don't keep rendering a stale head PR in
			// the status bar after the last matching PR was merged or
			// filtered out.
			st.Head = nil
			st.LastMsg = ""
		}
		if st.Cursor >= len(st.PRs) {
			st.Cursor = 0
		}
		st.LastErr = ""
	case watcher.EventRebased, watcher.EventWaiting, watcher.EventConflict:
		if ev.Head != nil {
			st.Head = ev.Head
		}
		st.LastMsg = ev.Message
		st.LastErr = ""
	case watcher.EventError:
		st.LastErr = ev.Message
	}
}

// keyMatches returns true if msg's key is one of binding's keys.
func keyMatches(b keyBindingLike, msg tea.KeyMsg) bool {
	for _, k := range b.Keys() {
		if msg.String() == k {
			return true
		}
	}
	return false
}

// keyBindingLike is a tiny interface so handleKey stays decoupled from the
// concrete bubbles/key.Binding type (which is fine to use directly, but this
// makes it trivial to test).
type keyBindingLike interface {
	Keys() []string
}

// Quit reports whether the user requested quit. Exposed so the caller
// managing the watcher knows to cancel its context.
func (m Model) Quit() bool { return m.quit }

// fmtDuration formats a duration as "5s ago" / "2m ago" for the status line.
func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// titleFor renders the truncated PR title for a given width; delegated to
// prutil.Truncate so the TUI, status command, and log output all agree.
func titleFor(pr gh.PullRequest, width int) string {
	if width <= 0 {
		return ""
	}
	return prutil.Truncate(pr.Title, width)
}

// clamp is a tiny helper used by view code.
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// headerFor returns a one-line label for a repo.
func headerFor(repo string, active bool) string {
	style := panelTitle
	if !active {
		style = lipgloss.NewStyle().Foreground(muted).Bold(true)
	}
	return style.Render(repo)
}

// repoWidth returns how wide each repo panel should be given m.width.
func (m Model) repoWidth() int {
	if m.width <= 0 || len(m.repos) == 0 {
		return 40
	}
	// Each panel has at least a few columns of padding / border.
	w := m.width / len(m.repos)
	return clamp(w, 24, 120)
}
