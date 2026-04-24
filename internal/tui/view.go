package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/eulercb/pr-merger/internal/gh"
)

// View renders the current TUI frame.
func (m Model) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 {
		return "Loading..."
	}
	if m.showHelp {
		return m.renderHelp()
	}
	return m.renderDashboard()
}

func (m Model) renderDashboard() string {
	title := titleStyle.Render(fmt.Sprintf(" pr-merger · %d repo(s) · poll %ds ",
		len(m.repos), m.cfg.PollInterval))
	titleGap := m.width - lipgloss.Width(title)
	if titleGap < 0 {
		titleGap = 0
	}
	titleBar := title + strings.Repeat(" ", titleGap)

	var panels []string
	if len(m.repos) == 0 {
		panels = append(panels, mutedText.Render("  (no repos configured)"))
	} else {
		width := m.repoWidth()
		for i, repo := range m.repos {
			st := m.states[repo]
			panels = append(panels, m.renderPanel(repo, st, width, i == m.active))
		}
	}

	content := lipgloss.JoinHorizontal(lipgloss.Top, panels...)
	status := m.renderStatusBar()
	help := m.renderHelpBar()
	return strings.Join([]string{titleBar, content, status, help}, "\n")
}

func (m Model) renderPanel(repo string, st *RepoState, width int, active bool) string {
	border := panelBorder
	if active {
		border = panelBorderFocused
	}
	header := headerFor(repo, active)

	var meta string
	if st != nil && !st.LastUpdate.IsZero() {
		meta = mutedText.Render("  " + fmtDuration(time.Since(st.LastUpdate)))
	} else {
		meta = mutedText.Render("  (never)")
	}

	lines := []string{header + meta}
	if st == nil || len(st.PRs) == 0 {
		if st != nil && st.LastErr != "" {
			lines = append(lines, dangerText.Render("  error: "+st.LastErr))
		} else {
			lines = append(lines, mutedText.Render("  no eligible PRs"))
		}
	} else {
		// Reserve inner width for the title column. Size the PR number
		// column to the widest number currently in the queue so five- and
		// six-digit numbers (common in big monorepos) don't shift the
		// title column and break alignment.
		iconCol := 3
		numCol := prNumberColumnWidth(st.PRs)
		innerTitleWidth := width - iconCol - numCol - 4
		if innerTitleWidth < 10 {
			innerTitleWidth = 10
		}
		for i, pr := range st.PRs {
			lines = append(lines, renderPRLine(pr, numCol, innerTitleWidth, active && i == st.Cursor, i == 0))
		}
	}

	if st != nil && st.LastMsg != "" {
		lines = append(lines, mutedText.Render("  "+st.LastMsg))
	}

	body := strings.Join(lines, "\n")
	return border.Width(width - 2).Render(body)
}

// prNumberColumnWidth returns the width needed to left-align all PR numbers
// in a queue, e.g. "#42   " / "#12345". Minimum 6 (`#9999 `) so the column
// doesn't shrink below a comfortable default on small queues.
func prNumberColumnWidth(prs []gh.PullRequest) int {
	const minWidth = 6
	max := 0
	for _, pr := range prs {
		if pr.Number > max {
			max = pr.Number
		}
	}
	digits := 1
	for n := max; n >= 10; n /= 10 {
		digits++
	}
	// Leading '#' + digits + trailing space for breathing room.
	w := 1 + digits + 1
	if w < minWidth {
		return minWidth
	}
	return w
}

func renderPRLine(pr gh.PullRequest, numWidth, titleWidth int, selected, isHead bool) string {
	state := gh.Classify(&pr)
	icon := state.Icon()

	var iconStyle lipgloss.Style
	switch state {
	case gh.StateClean:
		iconStyle = okText
	case gh.StateChecksFailed, gh.StateConflict:
		iconStyle = dangerText
	case gh.StateBehind, gh.StateChecksPending, gh.StateBlocked:
		iconStyle = warnText
	case gh.StateDraft:
		iconStyle = mutedText
	default:
		iconStyle = mutedText
	}

	// numWidth already accounts for '#' + digits + trailing space; the
	// format string pads the numeric portion only.
	numericWidth := numWidth - 2
	if numericWidth < 1 {
		numericWidth = 1
	}
	number := fmt.Sprintf("#%-*d", numericWidth, pr.Number)
	title := titleFor(pr, titleWidth)

	prefix := " "
	if isHead {
		prefix = "▸"
	}
	line := fmt.Sprintf("%s %s %s %s", prefix, iconStyle.Render(icon), number, title)
	if selected {
		return lipgloss.NewStyle().Reverse(true).Render(line)
	}
	return line
}

func (m Model) renderStatusBar() string {
	if len(m.repos) == 0 {
		return statusBar.Render("no repos")
	}
	active := m.repos[m.active]
	st := m.states[active]
	if st == nil {
		return statusBar.Render(active)
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("repo: %s", active))
	parts = append(parts, fmt.Sprintf("queue: %d", len(st.PRs)))
	if st.Head != nil {
		state := gh.Classify(st.Head)
		parts = append(parts, fmt.Sprintf("head: #%d %s", st.Head.Number, state.Label()))
	}
	if st.LastMsg != "" {
		parts = append(parts, st.LastMsg)
	}
	if st.LastErr != "" {
		parts = append(parts, dangerText.Render("error: "+st.LastErr))
	}
	return statusBar.Render(strings.Join(parts, " · "))
}

func (m Model) renderHelpBar() string {
	keys := []string{"tab:next", "j/k:move", "?:help", "q:quit"}
	return helpBar.Render(strings.Join(keys, "  "))
}

func (m Model) renderHelp() string {
	lines := []string{
		titleStyle.Render(" pr-merger · help "),
		"",
		"  tab / shift+tab    switch repo panel",
		"  j / k or ↓ / ↑     move cursor within a repo",
		"  ?                  toggle this help",
		"  q / ctrl+c         quit",
		"",
		mutedText.Render("  Status icons:"),
		"    " + okText.Render(gh.StateClean.Icon()) + "  clean · auto-merge pending",
		"    " + warnText.Render(gh.StateBehind.Icon()) + "  behind · rebase queued",
		"    " + warnText.Render(gh.StateChecksPending.Icon()) + "  checks running",
		"    " + warnText.Render(gh.StateBlocked.Icon()) + "  blocked on review",
		"    " + dangerText.Render(gh.StateChecksFailed.Icon()) + "  checks failed",
		"    " + dangerText.Render(gh.StateConflict.Icon()) + "  merge conflicts",
		"    " + mutedText.Render(gh.StateDraft.Icon()) + "  draft",
		"",
		mutedText.Render("Press ? again to return."),
	}
	return strings.Join(lines, "\n")
}
