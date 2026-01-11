package styles

import (
	"github.com/charmbracelet/lipgloss"
)

const (
	colorPrimary    = "205"
	colorSecondary  = "62"
	colorSuccess    = "42"
	colorError      = "196"
	colorWarning    = "220"
	colorMuted      = "8"
	colorWhite      = "255"
	colorDarkGray   = "235"
)

var (
	Primary    = lipgloss.Color(colorPrimary)
	Secondary  = lipgloss.Color(colorSecondary)
	Success    = lipgloss.Color(colorSuccess)
	ErrorColor = lipgloss.Color(colorError)
	Warning    = lipgloss.Color(colorWarning)
	Muted      = lipgloss.Color(colorMuted)

	Title = lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true).
		MarginBottom(1)

	Label = lipgloss.NewStyle().
		Foreground(Secondary).
		Bold(true)

	Input = lipgloss.NewStyle().
		Foreground(lipgloss.Color(colorWhite)).
		Background(lipgloss.Color(colorDarkGray)).
		Padding(0, 1)

	Selected = lipgloss.NewStyle().
		Foreground(Success).
		Bold(true)

	Subtle = lipgloss.NewStyle().
		Foreground(Muted)

	Error = lipgloss.NewStyle().
		Foreground(ErrorColor).
		Bold(true)

	PaneActive = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Padding(1, 1)

	PaneInactive = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Muted).
		Padding(1, 1)
)
