package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/popandcode/asb-tui/internal/azure"
	"github.com/popandcode/asb-tui/internal/styles"
)

type Pane int

const (
	PaneNamespace Pane = iota
	PaneMessages
)

// ExplorerModel "orchestrates" the namespace tree and messages panel.
type ExplorerModel struct {
	namespace     *NamespaceModel
	messages      *MessagesModel
	activePane    Pane
	width         int
	height        int
	namespaceName string
}

func NewExplorerModel(namespaceName string, client *azure.ServiceBusClient) *ExplorerModel {
	return &ExplorerModel{
		namespace:     NewNamespaceModel(namespaceName, client),
		messages:      NewMessagesModel(client),
		activePane:    PaneNamespace,
		namespaceName: namespaceName,
	}
}

func (m *ExplorerModel) Init() tea.Cmd {
	return tea.Batch(
		m.namespace.Init(),
		m.messages.Init(),
	)
}

func (m *ExplorerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		_, nsCmd := m.namespace.Update(tea.WindowSizeMsg{
			Width:  m.namespaceWidth() - 2,
			Height: m.contentHeight(),
		})
		cmds = append(cmds, nsCmd)

		m.messages.SetSize(m.messagesWidth()-2, m.contentHeight())

	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			if !m.messages.isEmpty {
				if m.activePane == PaneNamespace {
					m.activePane = PaneMessages
					m.messages.SetFocused(true)
				} else {
					m.activePane = PaneNamespace
					m.messages.SetFocused(false)
				}
			}
			return m, nil
		}

		if m.activePane == PaneNamespace {
			var nsModel tea.Model
			nsModel, cmd := m.namespace.Update(msg)
			m.namespace = nsModel.(*NamespaceModel)
			cmds = append(cmds, cmd)
		} else {
			var msgsModel tea.Model
			msgsModel, cmd := m.messages.Update(msg)
			m.messages = msgsModel.(*MessagesModel)
			cmds = append(cmds, cmd)
		}

	case SubscriptionMessagesSelectedMsg:
		cmd := m.messages.LoadMessages(msg.TopicName, msg.SubscriptionName, msg.IsDeadLetter)
		cmds = append(cmds, cmd)

	case MessagesLoadedMsg:
		var msgsModel tea.Model
		msgsModel, msgsCmd := m.messages.Update(msg)
		m.messages = msgsModel.(*MessagesModel)
		cmds = append(cmds, msgsCmd)

		if len(msg.Messages) > 0 {
			m.activePane = PaneMessages
			m.messages.SetFocused(true)
		}

	default:
		var nsModel tea.Model
		nsModel, nsCmd := m.namespace.Update(msg)
		m.namespace = nsModel.(*NamespaceModel)
		cmds = append(cmds, nsCmd)

		var msgsModel tea.Model
		msgsModel, msgsCmd := m.messages.Update(msg)
		m.messages = msgsModel.(*MessagesModel)
		cmds = append(cmds, msgsCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *ExplorerModel) View() string {
	var s strings.Builder

	s.WriteString(styles.Subtle.Render("Namespace: " + m.namespaceName))
	s.WriteString("\n")

	treeWidth := m.namespaceWidth()
	messagesWidth := m.messagesWidth()
	contentHeight := m.contentHeight()

	treeContent := m.namespace.ViewContent()
	treeContent = padToHeight(treeContent, contentHeight)

	messagesContent := m.messages.ViewContent()
	messagesContent = padToHeight(messagesContent, contentHeight)

	treeBorderColor := styles.Muted
	messagesBorderColor := styles.Muted
	if m.activePane == PaneNamespace {
		treeBorderColor = styles.Primary
	} else {
		messagesBorderColor = styles.Primary
	}

	treeStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(treeBorderColor).
		Width(treeWidth - 2)

	messagesStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(messagesBorderColor).
		Width(messagesWidth - 2)

	leftPane := treeStyle.Render(treeContent)
	rightPane := messagesStyle.Render(messagesContent)

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane))
	s.WriteString("\n")

	s.WriteString(styles.Subtle.Render("tab: switch pane • ↑↓/jk: navigate • ←→/hl/enter: expand • ctrl+c: quit"))
	s.WriteString("\n")

	return s.String()
}

func (m *ExplorerModel) contentHeight() int {
	// Reserve: Namespace header (1) + Footer (1) + borders (2) + extra (1)
	reserved := 5
	h := m.height - reserved
	if h < 3 {
		h = 3
	}
	return h
}

func (m *ExplorerModel) namespaceWidth() int {
	w := m.width * 30 / 100
	if w < 20 {
		w = 20
	}
	return w
}

func (m *ExplorerModel) messagesWidth() int {
	w := m.width - m.namespaceWidth() - 4 // 4 for borders (2+2)
	if w < 30 {
		w = 30
	}
	return w
}

func padToHeight(content string, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}
