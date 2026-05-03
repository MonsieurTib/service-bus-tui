package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MonsieurTib/service-bus-tui/internal/azure"
	"github.com/MonsieurTib/service-bus-tui/internal/styles"
	"github.com/MonsieurTib/service-bus-tui/internal/table"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

const pageSize = 100

type pageDirection int

const (
	pageInitial pageDirection = iota
	pageNext
	pagePrev
)

var (
	nextPageKey     = key.NewBinding(key.WithKeys("ctrl+n"))
	prevPageKey     = key.NewBinding(key.WithKeys("ctrl+p"))
	toggleSelectKey = key.NewBinding(key.WithKeys(" "))
	resendKey       = key.NewBinding(key.WithKeys("R"))
	refreshKey      = key.NewBinding(key.WithKeys("ctrl+r"))
	copyBodyKey     = key.NewBinding(key.WithKeys("ctrl+y"))
)

type MessagesModel struct {
	client       *azure.ServiceBusClient
	entityName   string // e.g. "topic/subscription" or "queue"
	isDeadLetter bool
	sessionOpts  azure.PeekSessionOptions
	messages     []azure.MessageInfo
	table        table.Model
	spinner      spinner.Model
	isLoading    bool
	errMsg       string
	width        int
	height       int
	isEmpty      bool

	// pagination
	currentPage        int     // 0-indexed
	pageStartSequences []int64 // first sequence number of each loaded page
	hasMore            bool    // true when last batch returned exactly pageSize messages
	totalMessages      int64   // total message count from runtime properties, -1 if unknown
}

type MessagesLoadedMsg struct {
	Messages  []azure.MessageInfo
	Direction pageDirection
}

type messageCountMsg struct {
	Count int64 // -1 if unknown/error
}

type ResendRequestedMsg struct {
	Messages        []azure.MessageInfo
	Destination     string // topic or queue name
	EditableMessage *azure.MessageInfo
}

type ResendProgressMsg struct {
	Sent  int
	Total int
	Err   error
	Done  bool
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
	tableStyle.Marked = tableStyle.Marked.
		Foreground(lipgloss.Color("42")).
		Background(lipgloss.Color("236")).
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
			// Selection and resend
			switch {
			case key.Matches(msg, toggleSelectKey):
				m.table.ToggleSelect(m.table.Cursor())
				m.table.MoveDown(1)
				return m, nil
			case key.Matches(msg, resendKey):
				if cmd := m.requestResend(); cmd != nil {
					return m, cmd
				}
				return m, nil
			case key.Matches(msg, refreshKey):
				return m, m.LoadMessages(m.entityName, m.isDeadLetter, m.sessionOpts)
			case key.Matches(msg, copyBodyKey):
				if cmd := m.requestCopyBody(); cmd != nil {
					return m, cmd
				}
				return m, nil
			}

			// Explicit page navigation shortcuts
			switch {
			case key.Matches(msg, nextPageKey):
				if cmd := m.loadNextPage(); cmd != nil {
					return m, cmd
				}
			case key.Matches(msg, prevPageKey):
				if cmd := m.loadPrevPage(); cmd != nil {
					return m, cmd
				}
			}

			rowCount := len(m.table.Rows())
			if rowCount > 0 {
				isDown := msg.String() == "down" || msg.String() == "j"
				isUp := msg.String() == "up" || msg.String() == "k"

				if isDown && m.table.Cursor() == rowCount-1 && m.hasMore {
					if cmd := m.loadNextPage(); cmd != nil {
						return m, cmd
					}
				}
				if isUp && m.table.Cursor() == 0 && m.currentPage > 0 {
					if cmd := m.loadPrevPage(); cmd != nil {
						return m, cmd
					}
				}
			}

			var tableCmd tea.Cmd
			m.table, tableCmd = m.table.Update(msg)
			return m, tableCmd
		}

	case MessagesLoadedMsg:
		m.isLoading = false
		m.messages = msg.Messages
		m.hasMore = len(msg.Messages) == pageSize
		m.updateTableRows()

		switch msg.Direction {
		case pageInitial, pageNext:
			m.table.SetCursor(0)
		case pagePrev:
			m.table.SetCursor(len(m.table.Rows()) - 1)
		}

		if len(msg.Messages) > 0 {
			startSeq := msg.Messages[0].SequenceNumber
			if m.currentPage >= len(m.pageStartSequences) {
				m.pageStartSequences = append(m.pageStartSequences, startSeq)
			} else {
				m.pageStartSequences[m.currentPage] = startSeq
			}
		}

	case messageCountMsg:
		m.totalMessages = msg.Count

	case ErrorMsg:
		m.isLoading = false
		m.errMsg = string(msg)
	}

	if m.isLoading {
		return m, spinnerCmd
	}

	return m, nil
}

func (m *MessagesModel) loadNextPage() tea.Cmd {
	if !m.hasMore || m.isLoading || len(m.messages) == 0 {
		return nil
	}

	m.currentPage++
	lastSeq := m.messages[len(m.messages)-1].SequenceNumber
	fromSeq := lastSeq + 1

	m.isLoading = true
	m.errMsg = ""

	return tea.Batch(
		m.spinner.Tick,
		m.loadMessagesCmd(&fromSeq, pageNext),
	)
}

func (m *MessagesModel) loadPrevPage() tea.Cmd {
	if m.currentPage <= 0 || m.isLoading {
		return nil
	}

	m.currentPage--
	fromSeq := m.pageStartSequences[m.currentPage]

	m.isLoading = true
	m.errMsg = ""

	return tea.Batch(
		m.spinner.Tick,
		m.loadMessagesCmd(&fromSeq, pagePrev),
	)
}

func (m *MessagesModel) LoadMessages(entityName string, isDeadLetter bool, sessionOpts azure.PeekSessionOptions) tea.Cmd {
	m.entityName = entityName
	m.isDeadLetter = isDeadLetter
	m.sessionOpts = sessionOpts
	m.isLoading = true
	m.isEmpty = false
	m.errMsg = ""
	m.messages = nil
	m.currentPage = 0
	m.pageStartSequences = nil
	m.hasMore = false
	m.totalMessages = -1
	m.updateTableRows()

	return tea.Batch(
		m.spinner.Tick,
		m.loadMessagesCmd(nil, pageInitial),
		m.fetchMessageCountCmd(),
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

func (m *MessagesModel) HandleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.isEmpty || m.isLoading {
		return nil
	}

	rowCount := len(m.table.Rows())
	if rowCount == 0 {
		return nil
	}

	switch msg.Type {
	case tea.MouseWheelUp:
		if m.table.Cursor() == 0 && m.currentPage > 0 {
			return m.loadPrevPage()
		}
		m.table.MoveUp(1)
	case tea.MouseWheelDown:
		if m.table.Cursor() == rowCount-1 && m.hasMore {
			return m.loadNextPage()
		}
		m.table.MoveDown(1)
	}

	return nil
}

func (m *MessagesModel) SelectedMessage() *azure.MessageInfo {
	if len(m.messages) == 0 {
		return nil
	}
	cursor := m.table.Cursor()
	if cursor >= 0 && cursor < len(m.messages) {
		return &m.messages[cursor]
	}
	return nil
}

func (m *MessagesModel) SelectedMessages() []azure.MessageInfo {
	indices := m.table.SelectedIndices()
	msgs := make([]azure.MessageInfo, 0, len(indices))
	for _, i := range indices {
		if i < len(m.messages) {
			msgs = append(msgs, m.messages[i])
		}
	}
	return msgs
}

func (m *MessagesModel) requestResend() tea.Cmd {
	msgs := m.SelectedMessages()
	if len(msgs) == 0 {
		selected := m.SelectedMessage()
		if selected == nil {
			return nil
		}
		msgs = []azure.MessageInfo{*selected}
	}
	dest := extractDestination(m.entityName)
	var editableMessage *azure.MessageInfo
	if len(msgs) == 1 {
		msg := msgs[0]
		editableMessage = &msg
	}
	return func() tea.Msg {
		return ResendRequestedMsg{
			Messages:        msgs,
			Destination:     dest,
			EditableMessage: editableMessage,
		}
	}
}

func (m *MessagesModel) requestCopyBody() tea.Cmd {
	selected := m.SelectedMessage()
	if selected == nil {
		return nil
	}
	body := selected.Body
	return func() tea.Msg {
		return CopyBodyRequestedMsg{Body: body}
	}
}

func extractDestination(entityName string) string {
	if parts := strings.SplitN(entityName, "/", 2); len(parts) == 2 {
		return parts[0]
	}
	return entityName
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

	if len(m.messages) > 0 {
		m.updateTableRows()
	}
}

func (m *MessagesModel) updateTableRows() {
	var rows []table.Row
	bodyColWidth := m.getBodyColumnWidth()

	for _, msg := range m.messages {
		bodyPreview := styles.FormatJSONCell([]byte(msg.Body), bodyColWidth)

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

func (m *MessagesModel) getBodyColumnWidth() int {
	if m.width <= 0 {
		return 30
	}
	available := m.width - 10
	return max(20, available-72)
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
		wrapped := wordwrap.String(m.errMsg, max(m.width-4, 10))
		return styles.Error.Render(wrapped)
	}

	if len(m.messages) == 0 {
		return styles.Subtle.Render("No messages found")
	}

	tableView := m.table.View()

	// Status line: selection count + page indicator
	var statusParts []string
	if selCount := m.table.SelectionCount(); selCount > 0 {
		statusParts = append(statusParts, styles.Selected.Render(fmt.Sprintf("%d selected", selCount)))
	}
	if pageInfo := m.buildPageIndicator(); pageInfo != "" {
		statusParts = append(statusParts, pageInfo)
	}

	if len(statusParts) > 0 {
		tableView += "\n" + strings.Join(statusParts, "  ")
	}

	return tableView
}

func (m *MessagesModel) buildPageIndicator() string {
	// Only show if there's pagination context (not on the only page)
	if m.currentPage == 0 && !m.hasMore {
		return ""
	}

	var totalPages string
	if m.totalMessages > 0 {
		tp := (m.totalMessages + pageSize - 1) / pageSize
		totalPages = fmt.Sprintf("%d", tp)
	} else if !m.hasMore {
		// We're on the last page, so we know the total
		totalPages = fmt.Sprintf("%d", m.currentPage+1)
	} else {
		totalPages = "?"
	}

	parts := []string{
		fmt.Sprintf("Page %d/%s", m.currentPage+1, totalPages),
	}

	var nav []string
	if m.hasMore {
		nav = append(nav, "Ctrl+N next")
	}
	if m.currentPage > 0 {
		nav = append(nav, "Ctrl+P prev")
	}
	if len(nav) > 0 {
		parts = append(parts, strings.Join(nav, " · "))
	}

	indicator := styles.Subtle.Render(strings.Join(parts, " · "))

	indicatorStyle := lipgloss.NewStyle().
		MarginTop(1).
		MarginRight(2)

	if m.width > 0 {
		indicatorStyle = indicatorStyle.Width(m.width - 2).Align(lipgloss.Right)
	}

	return indicatorStyle.Render(indicator)
}

func (m *MessagesModel) fetchMessageCountCmd() tea.Cmd {
	client := m.client
	entityName := m.entityName
	isDeadLetter := m.isDeadLetter

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		count, err := client.GetMessageCount(ctx, entityName, isDeadLetter)
		if err != nil {
			return messageCountMsg{Count: -1}
		}
		return messageCountMsg{Count: count}
	}
}

func (m *MessagesModel) loadMessagesCmd(fromSequenceNumber *int64, direction pageDirection) tea.Cmd {
	client := m.client
	entityName := m.entityName
	isDeadLetter := m.isDeadLetter
	sessionOpts := m.sessionOpts

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		messages, err := client.PeekMessages(ctx, entityName, isDeadLetter, pageSize, fromSequenceNumber, sessionOpts)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to peek messages: %v", err))
		}

		return MessagesLoadedMsg{
			Messages:  messages,
			Direction: direction,
		}
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
