package tui

import "github.com/charmbracelet/lipgloss"

// Color palette — kept minimal; the TUI leans on adaptive foreground colors
// so it renders on both light and dark terminals without configuration.
var (
	primary  = lipgloss.Color("205")
	success  = lipgloss.Color("42")
	warning  = lipgloss.Color("214")
	danger   = lipgloss.Color("196")
	muted    = lipgloss.Color("244")
	accent   = lipgloss.Color("39")
	inactive = lipgloss.Color("240")
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(primary).Padding(0, 1)
	panelBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(inactive).
			Padding(0, 1)
	panelBorderFocused = panelBorder.BorderForeground(accent)
	panelTitle         = lipgloss.NewStyle().Bold(true).Foreground(accent)
	mutedText          = lipgloss.NewStyle().Foreground(muted)
	okText             = lipgloss.NewStyle().Foreground(success)
	warnText           = lipgloss.NewStyle().Foreground(warning)
	dangerText         = lipgloss.NewStyle().Foreground(danger)
	statusBar          = lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
	helpBar            = lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
)
