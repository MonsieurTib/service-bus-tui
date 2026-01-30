package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/popandcode/asb-tui/internal/azure"
	"github.com/popandcode/asb-tui/internal/styles"
)

var (
	detailHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(styles.Primary)

	detailLabelStyle = lipgloss.NewStyle().
				Foreground(styles.Muted)

	detailSeparator = lipgloss.NewStyle().
			Foreground(styles.Muted).
			Render("────────────────────")
)

type MessageDetailModel struct {
	viewport viewport.Model
	message  *azure.MessageInfo
	width    int
	height   int
	ready    bool
}

func NewMessageDetailModel() *MessageDetailModel {
	return &MessageDetailModel{}
}

func (m *MessageDetailModel) SetMessage(msg *azure.MessageInfo) {
	m.message = msg
	m.rebuildContent()
}

func (m *MessageDetailModel) SetSize(width, height int) {
	m.width = width
	m.height = height

	if !m.ready {
		m.viewport = viewport.New(width, height)
		m.viewport.Style = lipgloss.NewStyle()
		m.ready = true
	} else {
		m.viewport.Width = width
		m.viewport.Height = height
	}

	m.rebuildContent()
}

func (m *MessageDetailModel) Update(msg tea.Msg) tea.Cmd {
	if !m.ready {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "down", "j":
			m.viewport.LineDown(1)
		case "up", "k":
			m.viewport.LineUp(1)
		}
	}

	return nil
}

func (m *MessageDetailModel) ViewContent() string {
	if m.message == nil {
		return styles.Subtle.Render("No message selected")
	}

	if !m.ready {
		return ""
	}

	return m.viewport.View()
}

func (m *MessageDetailModel) rebuildContent() {
	if m.message == nil || !m.ready {
		return
	}

	var b strings.Builder

	b.WriteString(detailHeaderStyle.Render("Properties"))
	b.WriteString("\n")
	b.WriteString(detailSeparator)
	b.WriteString("\n")

	writeField(&b, "Message ID", m.message.MessageID)
	writeField(&b, "Sequence #", fmt.Sprintf("%d", m.message.SequenceNumber))
	writeField(&b, "Subject", m.message.Subject)
	writeField(&b, "Enqueued", m.message.EnqueuedTime.Format("2006-01-02 15:04:05"))
	writeField(&b, "Content-Type", m.message.ContentType)

	if len(m.message.Properties) > 0 {
		b.WriteString("\n")
		b.WriteString(detailHeaderStyle.Render("Custom Properties"))
		b.WriteString("\n")
		b.WriteString(detailSeparator)
		b.WriteString("\n")

		keys := make([]string, 0, len(m.message.Properties))
		for k := range m.message.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			writeField(&b, k, fmt.Sprintf("%v", m.message.Properties[k]))
		}
	}

	b.WriteString("\n")
	b.WriteString(detailHeaderStyle.Render("Body"))
	b.WriteString("\n")
	b.WriteString(detailSeparator)
	b.WriteString("\n")

	body := styles.FormatJSONBody([]byte(m.message.Body))
	b.WriteString(body)

	m.viewport.SetContent(b.String())
	m.viewport.GotoTop()
}

func writeField(b *strings.Builder, label, value string) {
	if value == "" {
		value = "-"
	}
	b.WriteString(detailLabelStyle.Render(fmt.Sprintf("%-15s", label+":")))
	b.WriteString(" ")
	b.WriteString(value)
	b.WriteString("\n")
}
