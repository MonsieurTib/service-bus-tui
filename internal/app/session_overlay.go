package app

import (
	"fmt"
	"strings"

	"github.com/MonsieurTib/service-bus-tui/internal/azure"
	"github.com/MonsieurTib/service-bus-tui/internal/styles"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type SessionOverlayDismissedMsg struct{}

type SessionOverlayConfirmedMsg struct {
	EntityName   string
	IsDeadLetter bool
	SessionOpts  azure.PeekSessionOptions
}

type SessionOverlayModel struct {
	entityName   string
	isDeadLetter bool
	input        textinput.Model
	errMsg       string
}

func NewSessionOverlayModel(entityName string, isDeadLetter bool) *SessionOverlayModel {
	input := textinput.New()
	input.Placeholder = "session-id"
	input.Prompt = ""
	input.CharLimit = 260
	input.Width = 36
	input.Focus()

	return &SessionOverlayModel{
		entityName:   entityName,
		isDeadLetter: isDeadLetter,
		input:        input,
	}
}

func (m *SessionOverlayModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *SessionOverlayModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *SessionOverlayModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return func() tea.Msg { return SessionOverlayDismissedMsg{} }
	case "enter":
		id := strings.TrimSpace(m.input.Value())
		if id == "" {
			m.errMsg = "session id required"
			return nil
		}
		return m.confirmByID(id)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *SessionOverlayModel) confirmByID(id string) tea.Cmd {
	return func() tea.Msg {
		return SessionOverlayConfirmedMsg{
			EntityName:   m.entityName,
			IsDeadLetter: m.isDeadLetter,
			SessionOpts: azure.PeekSessionOptions{
				Kind:      azure.PeekSessionByID,
				SessionID: id,
			},
		}
	}
}

func (m *SessionOverlayModel) View(width, height int) string {
	var body strings.Builder

	titleStyle := lipgloss.NewStyle().Foreground(styles.Primary).Bold(true).MarginBottom(1)
	body.WriteString(titleStyle.Render("Session Mode"))
	body.WriteString("\n")
	body.WriteString(styles.Subtle.Render(fmt.Sprintf("Entity: %s", m.entityName)))
	body.WriteString("\n\n")

	body.WriteString("Enter session id:\n\n")
	body.WriteString(renderTextInputField(m.input.View(), true, m.input.Width))
	if m.errMsg != "" {
		body.WriteString("\n")
		body.WriteString(styles.Error.Render(m.errMsg))
	}
	hint := styles.Subtle.Render("enter: confirm - esc: cancel")

	panelWidth := min(58, width-4)
	panelHeight := min(16, height-4)
	content := body.String()
	if hint != "" {
		contentHeight := lipgloss.Height(content)
		hintHeight := lipgloss.Height(hint)
		spacerLines := max(panelHeight-contentHeight-hintHeight, 1)
		content += strings.Repeat("\n", spacerLines) + hint
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Primary).
		Padding(1, 2).
		Width(panelWidth).
		Height(panelHeight).
		Render(content)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
