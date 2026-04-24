package wizard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eulercb/pr-merger/internal/config"
)

// step identifies the current wizard screen.
type step int

const (
	stepRepo step = iota
	stepFilterName
	stepAuthor
	stepInterval
	stepConfirm
	stepDone
)

// Model is the Bubble Tea model for the wizard.
type Model struct {
	disc      Discovery
	inputs    [4]textinput.Model
	step      step
	authorYes bool
	err       string
	quit      bool

	// Cfg is populated when the wizard completes successfully.
	Cfg *config.Config
}

// NewModel builds a wizard model, pre-populating inputs from discovery.
func NewModel(disc Discovery) Model {
	repoIn := textinput.New()
	repoIn.Prompt = "> "
	repoIn.CharLimit = 128
	repoIn.Placeholder = "owner/repo"
	if def := DefaultRepo(disc); def != "" {
		repoIn.SetValue(def)
	}
	repoIn.Focus()

	nameIn := textinput.New()
	nameIn.Prompt = "> "
	nameIn.CharLimit = 64

	authorIn := textinput.New()
	authorIn.Prompt = "> "
	authorIn.CharLimit = 2
	authorIn.Placeholder = "y/N"

	intervalIn := textinput.New()
	intervalIn.Prompt = "> "
	intervalIn.CharLimit = 6
	intervalIn.SetValue("30")

	return Model{
		disc:   disc,
		inputs: [4]textinput.Model{repoIn, nameIn, authorIn, intervalIn},
	}
}

// Init satisfies tea.Model.
func (m Model) Init() tea.Cmd { return textinput.Blink }

// Update handles keyboard input and advances the wizard through steps.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quit = true
			return m, tea.Quit
		case "enter":
			return m.advance()
		}
	}

	// Route keys to the active input.
	if m.step >= 0 && int(m.step) < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.step], cmd = m.inputs[m.step].Update(msg)
		return m, cmd
	}
	return m, nil
}

// advance validates the current step and moves to the next. On stepConfirm,
// it builds the final config and exits the Bubble Tea loop.
func (m Model) advance() (tea.Model, tea.Cmd) {
	m.err = ""
	switch m.step {
	case stepRepo:
		repo := strings.TrimSpace(m.inputs[stepRepo].Value())
		if repo == "" {
			m.err = "repo is required"
			return m, nil
		}
		// Advance to filter-name; pre-fill with suggested default.
		m.inputs[stepFilterName].SetValue(defaultFilterName(repo))
		m.step = stepFilterName
		m.focus(stepFilterName)
	case stepFilterName:
		m.step = stepAuthor
		m.focus(stepAuthor)
	case stepAuthor:
		raw := strings.ToLower(strings.TrimSpace(m.inputs[stepAuthor].Value()))
		m.authorYes = raw == "y" || raw == "yes"
		m.step = stepInterval
		m.focus(stepInterval)
	case stepInterval:
		// Build config and show confirm.
		cfg, err := BuildConfig(Answers{
			Repo:            m.inputs[stepRepo].Value(),
			FilterName:      m.inputs[stepFilterName].Value(),
			AuthorRestrict:  m.authorYes,
			Login:           m.disc.Login,
			PollIntervalStr: m.inputs[stepInterval].Value(),
		})
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.Cfg = cfg
		m.step = stepConfirm
	case stepConfirm:
		m.step = stepDone
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) focus(s step) {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	if int(s) < len(m.inputs) {
		m.inputs[s].Focus()
	}
}

// View renders the wizard. Single-screen-per-step for simplicity.
func (m Model) View() string {
	if m.quit {
		return ""
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).
		Render("pr-merger setup")
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	var body string
	switch m.step {
	case stepRepo:
		prompt := "Which GitHub repo should pr-merger watch? (OWNER/NAME)"
		if len(m.disc.Repos) > 0 {
			prompt += "\n" + hint.Render(fmt.Sprintf("Suggested from your recent activity: %s", strings.Join(m.disc.Repos[:min(len(m.disc.Repos), 5)], ", ")))
		} else if m.disc.Err != nil {
			prompt += "\n" + hint.Render("(couldn't auto-detect recent repos: "+m.disc.Err.Error()+")")
		}
		body = prompt + "\n\n" + m.inputs[stepRepo].View()
	case stepFilterName:
		body = "Filter name (internal identifier):\n\n" + m.inputs[stepFilterName].View()
	case stepAuthor:
		q := "Only manage PRs you authored?"
		if m.disc.Login != "" {
			q += " (author: " + m.disc.Login + ")"
		}
		body = q + "\n\n" + m.inputs[stepAuthor].View()
	case stepInterval:
		body = "Poll interval in seconds [30]:\n\n" + m.inputs[stepInterval].View()
	case stepConfirm:
		body = "Review:\n\n" + renderConfig(m.Cfg) + "\n\n" +
			hint.Render("Press Enter to save, Esc to abort.")
	case stepDone:
		body = "Saved. Press any key."
	}

	out := title + "\n\n" + body
	if m.err != "" {
		out += "\n\n" + errStyle.Render(m.err)
	}
	out += "\n\n" + hint.Render("Enter: next · Esc: cancel")
	return out
}

func renderConfig(cfg *config.Config) string {
	if cfg == nil || len(cfg.Filters) == 0 {
		return "(empty)"
	}
	f := cfg.Filters[0]
	lines := []string{
		"repo:          " + f.Repo,
		"filter name:   " + f.Name,
		fmt.Sprintf("authors:       %s", strings.Join(f.Authors, ",")),
		fmt.Sprintf("poll interval: %ds", cfg.PollInterval),
	}
	return strings.Join(lines, "\n")
}
