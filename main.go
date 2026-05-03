package main

import (
	"log"

	"github.com/MonsieurTib/service-bus-tui/internal/app"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	app.SetTerminalTitle(app.TerminalTitle(""))
	if err := app.InitClipboard(); err != nil {
		log.Printf("clipboard init failed, using terminal fallback: %v", err)
	}

	f, err := tea.LogToFile("debug.log", "debug")
	if err != nil {
		log.Fatalf("failed to create debug log: %v", err)
	}
	defer f.Close()

	p := tea.NewProgram(
		app.NewRootModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		log.Fatalf("error running program: %v", err)
	}
}
