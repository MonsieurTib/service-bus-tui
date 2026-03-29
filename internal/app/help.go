package app

import (
	"strings"

	"github.com/MonsieurTib/service-bus-tui/internal/styles"
	"github.com/charmbracelet/lipgloss"
)

type helpSection struct {
	title    string
	bindings []helpBinding
}

type helpBinding struct {
	key         string
	description string
}

func helpSections() []helpSection {
	return []helpSection{
		{
			title: "General",
			bindings: []helpBinding{
				{"tab/shift+tab", "Switch pane"},
				{"esc", "Go back"},
				{"?", "Toggle help"},
				{"ctrl+c", "Quit"},
			},
		},
		{
			title: "Navigation",
			bindings: []helpBinding{
				{"up/k", "Move up"},
				{"down/j", "Move down"},
				{"g/Home", "Go to top"},
				{"G/End", "Go to bottom"},
			},
		},
		{
			title: "Tree",
			bindings: []helpBinding{
				{"right/l/enter", "Expand / select"},
				{"left/h", "Collapse"},
				{"S", "Send to topic/queue"},
			},
		},
		{
			title: "Messages",
			bindings: []helpBinding{
				{"space", "Toggle select"},
				{"R", "Resend sel/current (1=edit)"},
				{"ctrl+n", "Next page"},
				{"ctrl+p", "Previous page"},
				{"f/pgdn", "Page down"},
				{"b/pgup", "Page up"},
				{"u/ctrl+u", "Half page up"},
				{"d/ctrl+d", "Half page down"},
			},
		},
	}
}

func renderHelp(width, height int) string {
	sections := helpSections()

	titleStyle := lipgloss.NewStyle().
		Foreground(styles.Primary).
		Bold(true).
		MarginBottom(1)

	sectionTitleStyle := lipgloss.NewStyle().
		Foreground(styles.Secondary).
		Bold(true).
		MarginTop(1)

	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Bold(true).
		Width(16)

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	var content strings.Builder

	content.WriteString(titleStyle.Render("Keybindings"))
	content.WriteString("\n")

	for _, section := range sections {
		content.WriteString(sectionTitleStyle.Render(section.title))
		content.WriteString("\n")

		for _, b := range section.bindings {
			line := keyStyle.Render(b.key) + descStyle.Render(b.description)
			content.WriteString(line)
			content.WriteString("\n")
		}
	}

	content.WriteString("\n")
	content.WriteString(styles.Subtle.Render("Press ? or esc to close"))

	panelWidth := min(50, width-4)
	panelHeight := min(30, height-4)

	panelStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Primary).
		Padding(1, 2).
		Width(panelWidth).
		Height(panelHeight).
		Background(lipgloss.Color("235"))

	panel := panelStyle.Render(content.String())

	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		panel,
	)
}
