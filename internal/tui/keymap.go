package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines keyboard shortcuts for the TUI. Kept minimal and
// single-layer to avoid conflicts with terminal shortcuts.
type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	NextRepo key.Binding
	PrevRepo key.Binding
	Quit     key.Binding
	Help     key.Binding
}

// DefaultKeyMap returns the default bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		NextRepo: key.NewBinding(
			key.WithKeys("tab", "right", "l"),
			key.WithHelp("tab", "next repo"),
		),
		PrevRepo: key.NewBinding(
			key.WithKeys("shift+tab", "left", "h"),
			key.WithHelp("shift+tab", "prev repo"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
	}
}
