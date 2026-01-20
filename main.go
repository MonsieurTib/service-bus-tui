package main

import (
	"log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/popandcode/asb-tui/internal/app"
)

func main() {
	f, err := tea.LogToFile("debug.log", "debug")
	if err != nil {
		log.Fatalf("failed to create debug log: %v", err)
	}
	defer f.Close()

	p := tea.NewProgram(app.NewRootModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("error running program: %v", err)
	}
}
