package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/popandcode/asb-tui/internal/azure"
	"github.com/popandcode/asb-tui/internal/styles"
)

type MessagesModel struct {
	client           *azure.ServiceBusClient
	topicName        string
	subscriptionName string
	isDeadLetter     bool
	messages         []azure.MessageInfo
	table            table.Model
	spinner          spinner.Model
	isLoading        bool
	errMsg           string
	width            int
	height           int
	isEmpty          bool
}

type MessagesLoadedMsg struct {
	Messages []azure.MessageInfo
}

func NewMessagesModel(client *azure.ServiceBusClient) *MessagesModel {
	s := spinner.New()
	s.Spinner = spinner.MiniDot

	columns := []table.Column{
		{Title: "Seq#", Width: 8},
		{Title: "Message ID", Width: 20},
		{Title: "Subject", Width: 20},
		{Title: "Enqueued", Width: 20},
		{Title: "Body (preview)", Width: 30},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows([]table.Row{}),
		table.WithFocused(false),
		table.WithHeight(10),
	)

	tableStyle := table.DefaultStyles()
	tableStyle.Header = tableStyle.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(styles.Muted).
		BorderBottom(true).
		Bold(true)
	tableStyle.Selected = tableStyle.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(tableStyle)

	return &MessagesModel{
		client:  client,
		spinner: s,
		table:   t,
		isEmpty: true,
	}
}

func (m *MessagesModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *MessagesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var spinnerCmd tea.Cmd
	m.spinner, spinnerCmd = m.spinner.Update(msg)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if !m.isEmpty && !m.isLoading {
			var tableCmd tea.Cmd
			m.table, tableCmd = m.table.Update(msg)
			return m, tableCmd
		}

	case MessagesLoadedMsg:
		m.isLoading = false
		m.messages = msg.Messages
		m.updateTableRows()

	case ErrorMsg:
		m.isLoading = false
		m.errMsg = string(msg)
	}

	if m.isLoading {
		return m, spinnerCmd
	}

	return m, nil
}

func (m *MessagesModel) LoadMessages(topicName, subscriptionName string, isDeadLetter bool) tea.Cmd {
	m.topicName = topicName
	m.subscriptionName = subscriptionName
	m.isDeadLetter = isDeadLetter
	m.isLoading = true
	m.isEmpty = false
	m.errMsg = ""
	m.messages = nil
	m.updateTableRows()

	return tea.Batch(
		m.spinner.Tick,
		m.loadMessagesCmd(),
	)
}

func (m *MessagesModel) SetSize(width, height int) {
	m.width = width
	m.height = height

	tableHeight := max(height-4, 5)
	m.table.SetHeight(tableHeight)

	m.updateColumnWidths()
}

func (m *MessagesModel) SetFocused(focused bool) {
	if focused {
		m.table.Focus()
	} else {
		m.table.Blur()
	}
}

func (m *MessagesModel) updateColumnWidths() {
	if m.width <= 0 {
		return
	}

	available := m.width - 10

	columns := []table.Column{
		{Title: "Seq#", Width: min(8, available/5)},
		{Title: "Message ID", Width: min(24, available/5)},
		{Title: "Subject", Width: min(20, available/5)},
		{Title: "Enqueued", Width: min(20, available/5)},
		{Title: "Body (preview)", Width: max(20, available-72)},
	}
	m.table.SetColumns(columns)
}

func (m *MessagesModel) updateTableRows() {
	var rows []table.Row
	for _, msg := range m.messages {
		bodyPreview := truncateString(msg.Body, 50)
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", msg.SequenceNumber),
			truncateString(msg.MessageID, 20),
			truncateString(msg.Subject, 20),
			msg.EnqueuedTime.Format("2006-01-02 15:04:05"),
			bodyPreview,
		})
	}
	m.table.SetRows(rows)
}

func (m *MessagesModel) View() string {
	return m.ViewContent()
}

func (m *MessagesModel) ViewContent() string {
	if m.isEmpty {
		return styles.Subtle.Render("Select a message node and press Enter")
	}

	if m.isLoading {
		return m.spinner.View() + " " + styles.Subtle.Render("Loading messages...")
	}

	if m.errMsg != "" {
		return styles.Error.Render("Error: " + m.errMsg)
	}

	if len(m.messages) == 0 {
		return styles.Subtle.Render("No messages found")
	}

	return m.table.View()
}

func (m *MessagesModel) loadMessagesCmd() tea.Cmd {
	client := m.client
	topicName := m.topicName
	subscriptionName := m.subscriptionName
	isDeadLetter := m.isDeadLetter

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		messages, err := client.PeekMessages(ctx, topicName, subscriptionName, isDeadLetter, 100)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to peek messages: %v", err))
		}

		return MessagesLoadedMsg{Messages: messages}
	}
}

func truncateString(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")

	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
