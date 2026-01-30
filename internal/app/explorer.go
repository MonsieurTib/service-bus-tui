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
	PaneDetail
)

// ExplorerModel "orchestrates" the namespace tree, messages panel, and detail panel.
type ExplorerModel struct {
	namespace     *NamespaceModel
	messages      *MessagesModel
	detail        *MessageDetailModel
	activePane    Pane
	width         int
	height        int
	namespaceName string
	prevCursor    int
}

func NewExplorerModel(namespaceName string, client *azure.ServiceBusClient) *ExplorerModel {
	return &ExplorerModel{
		namespace:     NewNamespaceModel(namespaceName, client),
		messages:      NewMessagesModel(client),
		detail:        NewMessageDetailModel(),
		activePane:    PaneNamespace,
		namespaceName: namespaceName,
		prevCursor:    -1,
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
		m.detail.SetSize(m.detailWidth()-2, m.contentHeight())

	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			m.switchPane()
			return m, nil
		}

		switch m.activePane {
		case PaneNamespace:
			var nsModel tea.Model
			nsModel, cmd := m.namespace.Update(msg)
			m.namespace = nsModel.(*NamespaceModel)
			cmds = append(cmds, cmd)

		case PaneMessages:
			var msgsModel tea.Model
			msgsModel, cmd := m.messages.Update(msg)
			m.messages = msgsModel.(*MessagesModel)
			cmds = append(cmds, cmd)
			m.syncDetailWithCursor()

		case PaneDetail:
			m.detail.Update(msg)
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
			m.prevCursor = -1
			m.syncDetailWithCursor()
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

func (m *ExplorerModel) switchPane() {
	switch m.activePane {
	case PaneNamespace:
		if !m.messages.isEmpty {
			m.activePane = PaneMessages
			m.messages.SetFocused(true)
		}
	case PaneMessages:
		if !m.messages.isEmpty && m.messages.SelectedMessage() != nil {
			m.activePane = PaneDetail
			m.messages.SetFocused(false)
		} else {
			m.activePane = PaneNamespace
			m.messages.SetFocused(false)
		}
	case PaneDetail:
		m.activePane = PaneNamespace
	}
}

func (m *ExplorerModel) syncDetailWithCursor() {
	selected := m.messages.SelectedMessage()
	if selected == nil {
		return
	}
	cursor := m.messages.table.Cursor()
	if cursor != m.prevCursor {
		m.prevCursor = cursor
		m.detail.SetMessage(selected)
	}
}

func (m *ExplorerModel) View() string {
	var s strings.Builder

	s.WriteString(styles.Subtle.Render("Namespace: " + m.namespaceName))
	s.WriteString("\n")

	treeWidth := m.namespaceWidth()
	messagesWidth := m.messagesWidth()
	detailWidth := m.detailWidth()
	contentHeight := m.contentHeight()

	treeContent := m.namespace.ViewContent()
	treeContent = padToHeight(treeContent, contentHeight)

	messagesContent := m.messages.ViewContent()
	messagesContent = padToHeight(messagesContent, contentHeight)

	detailContent := m.detail.ViewContent()
	detailContent = padToHeight(detailContent, contentHeight)

	treeBorderColor := styles.Muted
	messagesBorderColor := styles.Muted
	detailBorderColor := styles.Muted
	switch m.activePane {
	case PaneNamespace:
		treeBorderColor = styles.Primary
	case PaneMessages:
		messagesBorderColor = styles.Primary
	case PaneDetail:
		detailBorderColor = styles.Primary
	}

	treeStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(treeBorderColor).
		Width(treeWidth - 2)

	messagesStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(messagesBorderColor).
		Width(messagesWidth - 2)

	detailStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(detailBorderColor).
		Width(detailWidth - 2)

	leftPane := treeStyle.Render(treeContent)
	middlePane := messagesStyle.Render(messagesContent)
	rightPane := detailStyle.Render(detailContent)

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPane, middlePane, rightPane))
	s.WriteString("\n")

	s.WriteString(styles.Subtle.Render("tab: switch pane • ↑↓/jk: navigate • ctrl+c: quit"))
	s.WriteString("\n")

	return s.String()
}

func (m *ExplorerModel) contentHeight() int {
	// Reserve: Namespace header (1) + Footer (1) + borders (2) + extra (1)
	reserved := 5
	h := max(m.height-reserved, 3)
	return h
}

func (m *ExplorerModel) namespaceWidth() int {
	pct := 15
	w := max(m.width*pct/100, pct)
	return w
}

func (m *ExplorerModel) messagesWidth() int {
	w := max(m.width-m.namespaceWidth()-m.detailWidth(), 30)
	return w
}

func (m *ExplorerModel) detailWidth() int {
	w := max(m.width*30/100, 30)
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
