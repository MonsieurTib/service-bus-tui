package app

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const appTitle = "service-bus-tui"

func SetTerminalTitle(title string) {
	_, _ = os.Stdout.WriteString(terminalTitleSequence(title))
}

func SetTerminalTitleCmd(title string) tea.Cmd {
	return func() tea.Msg {
		SetTerminalTitle(title)
		return nil
	}
}

func TerminalTitle(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return appTitle
	}
	return appTitle + " - " + namespace
}

func terminalTitleSequence(title string) string {
	safeTitle := strings.NewReplacer("\x1b", "", "\x07", "", "\n", " ", "\r", " ").Replace(title)
	return "\x1b]0;" + safeTitle + "\x07\x1b]2;" + safeTitle + "\x07"
}
