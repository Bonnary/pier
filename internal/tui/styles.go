package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	pendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	activeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	logBoxStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8"))

	// selectedStyle marks a checked item in a multi-select Picker.
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	// helpStyle renders the bottom-of-picker help line.
	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)
