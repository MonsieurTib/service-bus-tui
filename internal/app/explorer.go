package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MonsieurTib/service-bus-tui/internal/azure"
	"github.com/MonsieurTib/service-bus-tui/internal/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Pane int

const (
	PaneNamespace Pane = iota
	PaneMessages
	PaneDetail
)

// ExplorerModel "orchestrates" the namespace tree, messages panel, and detail panel.
type ExplorerModel struct {
	namespace      *NamespaceModel
	messages       *MessagesModel
	detail         *MessageDetailModel
	client         *azure.ServiceBusClient
	activePane     Pane
	width          int
	height         int
	namespaceName  string
	prevCursor     int
	showHelp       bool
	resendOverlay  *ResendOverlayModel
	sendOverlay    *SendOverlayModel
	sessionOverlay *SessionOverlayModel
}

type sessionRequirementResolvedMsg struct {
	entityName       string
	isDeadLetter     bool
	requiresSessions bool
	err              error
}

func NewExplorerModel(namespaceName string, client *azure.ServiceBusClient) *ExplorerModel {
	return &ExplorerModel{
		namespace:     NewNamespaceModel(namespaceName, client),
		messages:      NewMessagesModel(client),
		detail:        NewMessageDetailModel(),
		client:        client,
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

	if m.sessionOverlay != nil {
		switch msg := msg.(type) {
		case SessionOverlayDismissedMsg:
			m.sessionOverlay = nil
			return m, nil
		case SessionOverlayConfirmedMsg:
			m.sessionOverlay = nil
			cmd := m.messages.LoadMessages(msg.EntityName, msg.IsDeadLetter, msg.SessionOpts)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		case tea.KeyMsg:
			cmd := m.sessionOverlay.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		default:
			cmd := m.sessionOverlay.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	if m.sendOverlay != nil {
		switch msg := msg.(type) {
		case SendDismissedMsg:
			m.sendOverlay = nil
			return m, nil
		case tea.KeyMsg:
			cmd := m.sendOverlay.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		default:
			cmd := m.sendOverlay.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	if m.sendOverlay != nil || m.resendOverlay != nil || m.sessionOverlay != nil {
		if _, ok := msg.(tea.MouseMsg); ok {
			return m, tea.Batch(cmds...)
		}
	}

	if m.resendOverlay != nil {
		switch msg := msg.(type) {
		case ResendDismissedMsg:
			m.resendOverlay = nil
			return m, nil
		case ResendProgressMsg:
			cmd := m.resendOverlay.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		case tea.KeyMsg:
			cmd := m.resendOverlay.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		default:
			cmd := m.resendOverlay.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

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
		if m.resendOverlay != nil {
			return m, tea.Batch(cmds...)
		}

		// Help panel toggle
		if msg.String() == "?" {
			m.showHelp = !m.showHelp
			return m, nil
		}
		if m.showHelp {
			if msg.String() == "esc" {
				m.showHelp = false
			}
			return m, nil
		}

		switch msg.String() {
		case "tab":
			m.switchPane(1)
			return m, nil
		case "shift+tab":
			m.switchPane(-1)
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
			if cmd := m.detail.Update(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case tea.MouseMsg:
		switch m.activePane {
		case PaneNamespace:
			m.namespace.HandleMouse(msg)
		case PaneMessages:
			if cmd := m.messages.HandleMouse(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			m.syncDetailWithCursor()
		case PaneDetail:
			m.detail.HandleMouse(msg)
		}

	case ResendRequestedMsg:
		if msg.EditableMessage != nil {
			overlay := NewSendOverlayModelFromMessage(msg.Destination, m.client, *msg.EditableMessage)
			m.sendOverlay = overlay
			cmds = append(cmds, overlay.Init())
			return m, tea.Batch(cmds...)
		}
		overlay := NewResendOverlayModel(msg.Messages, msg.Destination, m.client)
		m.resendOverlay = overlay
		cmds = append(cmds, overlay.Init())
		return m, tea.Batch(cmds...)

	case SendRequestedMsg:
		overlay := NewSendOverlayModel(msg.Destination, m.client)
		m.sendOverlay = overlay
		cmds = append(cmds, overlay.Init())
		return m, tea.Batch(cmds...)

	case MessagesSelectedMsg:
		cmd := m.resolveSessionRequirementCmd(msg.EntityName, msg.IsDeadLetter)
		cmds = append(cmds, cmd)

	case sessionRequirementResolvedMsg:
		if msg.err != nil {
			var msgsModel tea.Model
			msgsModel, msgsCmd := m.messages.Update(ErrorMsg(fmt.Sprintf("failed to determine session mode: %v", msg.err)))
			m.messages = msgsModel.(*MessagesModel)
			cmds = append(cmds, msgsCmd)
			return m, tea.Batch(cmds...)
		}

		if msg.requiresSessions {
			overlay := NewSessionOverlayModel(msg.entityName, msg.isDeadLetter)
			m.sessionOverlay = overlay
			cmds = append(cmds, overlay.Init())
			return m, tea.Batch(cmds...)
		}

		cmd := m.messages.LoadMessages(msg.entityName, msg.isDeadLetter, azure.PeekSessionOptions{Kind: azure.PeekSessionNone})
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

	case ErrorMsg:
		var msgsModel tea.Model
		msgsModel, msgsCmd := m.messages.Update(msg)
		m.messages = msgsModel.(*MessagesModel)
		cmds = append(cmds, msgsCmd)

	case CopyBodyRequestedMsg:
		_, _ = CopyTextToClipboard(msg.Body)

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

func (m *ExplorerModel) switchPane(step int) {
	if step != 1 && step != -1 {
		return
	}

	order := []Pane{PaneNamespace, PaneMessages, PaneDetail}
	current := m.activePaneIndex(order)
	if current < 0 {
		return
	}

	for i := 1; i < len(order); i++ {
		next := (current + (step * i) + len(order)) % len(order)
		candidate := order[next]
		if m.isPaneAvailable(candidate) {
			m.setActivePane(candidate)
			return
		}
	}
}

func (m *ExplorerModel) activePaneIndex(order []Pane) int {
	for i, pane := range order {
		if pane == m.activePane {
			return i
		}
	}
	return -1
}

func (m *ExplorerModel) isPaneAvailable(pane Pane) bool {
	switch pane {
	case PaneNamespace:
		return true
	case PaneMessages:
		return !m.messages.isEmpty
	case PaneDetail:
		return !m.messages.isEmpty && m.messages.SelectedMessage() != nil
	default:
		return false
	}
}

func (m *ExplorerModel) setActivePane(pane Pane) {
	m.activePane = pane
	m.messages.SetFocused(pane == PaneMessages)
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
	if m.showHelp {
		return renderHelp(m.width, m.height)
	}

	var s strings.Builder

	header := "Namespace: " + m.namespaceName
	if selectedPath := m.namespace.SelectedPath(m.namespaceName); selectedPath != "" {
		header = selectedPath
	}

	s.WriteString(styles.Subtle.Copy().Padding(0, 1).Render(header))
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

	s.WriteString(m.footerHints())
	s.WriteString("\n")

	view := s.String()

	// Composite resend overlay on top of the explorer view
	if m.resendOverlay != nil {
		return m.resendOverlay.View(m.width, m.height)
	}
	if m.sendOverlay != nil {
		return m.sendOverlay.View(m.width, m.height)
	}
	if m.sessionOverlay != nil {
		return m.sessionOverlay.View(m.width, m.height)
	}

	return view
}

func (m *ExplorerModel) resolveSessionRequirementCmd(entityName string, isDeadLetter bool) tea.Cmd {
	client := m.client

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		requiresSessions, err := client.EntityRequiresSession(ctx, entityName)
		return sessionRequirementResolvedMsg{
			entityName:       entityName,
			isDeadLetter:     isDeadLetter,
			requiresSessions: requiresSessions,
			err:              err,
		}
	}
}

func (m *ExplorerModel) footerHints() string {
	base := "tab/shift+tab: switch pane • ↑↓/jk: navigate • ?: help • ctrl+c: quit"
	if m.activePane == PaneNamespace {
		base = "tab/shift+tab: switch pane • ↑↓/jk: navigate • S: send • ctrl+r: refresh • ?: help • ctrl+c: quit"
	}
	if m.activePane == PaneMessages && !m.messages.isEmpty {
		base = "tab/shift+tab: switch pane • space: select • R: resend/edit sel/current • ctrl+y: copy body • ctrl+r: refresh • ?: help • ctrl+c: quit"
	}
	if m.activePane == PaneDetail {
		base = "tab/shift+tab: switch pane • ↑↓/jk: scroll • ctrl+y: copy body • ?: help • ctrl+c: quit"
	}
	return styles.Subtle.Render(base)
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
