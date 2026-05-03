package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MonsieurTib/service-bus-tui/internal/azure"
	"github.com/MonsieurTib/service-bus-tui/internal/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type sendState int

const (
	sendStateCompose sendState = iota
	sendStateSending
	sendStateDone
	sendStateError
)

type sendField int

const (
	fieldMessageID sendField = iota
	fieldSessionID
	fieldSubject
	fieldCorrelationID
	fieldTimeToLive
	fieldContentType
	fieldCustomContentType
	fieldProperties
	fieldBody
)

type messageProperty struct {
	Key   string
	Value string
}

type SendRequestedMsg struct {
	Destination string
}

type SendDismissedMsg struct{}

type sendCompletedMsg struct {
	err error
}

type SendOverlayModel struct {
	state               sendState
	focusedField        sendField
	destination         string
	client              *azure.ServiceBusClient
	messageIDInput      textinput.Model
	sessionIDInput      textinput.Model
	correlationIDInput  textinput.Model
	subjectInput        textinput.Model
	timeToLiveInput     textinput.Model
	bodyInput           textarea.Model
	customTypeInput     textinput.Model
	contentTypeOptions  []string
	selectedContentType int
	errMsg              string
	spinner             spinner.Model
	metaInputWidth      int
	bodyInputWidth      int
	bodyInputHeight     int
	properties          []messageProperty
	selectedPropertyRow int
	selectedPropertyCol int
	propertyKeyInput    textinput.Model
	propertyValueInput  textinput.Model
}

func NewSendOverlayModel(destination string, client *azure.ServiceBusClient) *SendOverlayModel {
	s := spinner.New()
	s.Spinner = spinner.MiniDot

	subjectInput := textinput.New()
	subjectInput.Placeholder = "Optional subject"
	subjectInput.Prompt = ""

	messageIDInput := textinput.New()
	messageIDInput.Placeholder = "Auto-generated if empty"
	messageIDInput.Prompt = ""

	sessionIDInput := textinput.New()
	sessionIDInput.Placeholder = "Optional session ID"
	sessionIDInput.Prompt = ""

	correlationIDInput := textinput.New()
	correlationIDInput.Placeholder = "Optional correlation ID"
	correlationIDInput.Prompt = ""

	timeToLiveInput := textinput.New()
	timeToLiveInput.Placeholder = "Optional TTL (for example 5m, 1h30m)"
	timeToLiveInput.Prompt = ""

	customTypeInput := textinput.New()
	customTypeInput.Placeholder = "application/vnd.my-app+json"
	customTypeInput.Prompt = ""

	propertyKeyInput := textinput.New()
	propertyKeyInput.Placeholder = "key"
	propertyKeyInput.Prompt = ""

	propertyValueInput := textinput.New()
	propertyValueInput.Placeholder = "value"
	propertyValueInput.Prompt = ""

	bodyInput := textarea.New()
	bodyInput.Placeholder = "Message body"
	bodyInput.ShowLineNumbers = false
	bodyInput.Prompt = ""
	bodyInput.CharLimit = 0
	bodyInput.SetHeight(10)
	bodyInput.FocusedStyle.Base = bodyInput.FocusedStyle.Base.Background(lipgloss.Color(""))
	bodyInput.BlurredStyle.Base = bodyInput.BlurredStyle.Base.Background(lipgloss.Color(""))
	bodyInput.FocusedStyle.CursorLine = bodyInput.FocusedStyle.CursorLine.Background(lipgloss.Color(""))
	bodyInput.BlurredStyle.CursorLine = bodyInput.BlurredStyle.CursorLine.Background(lipgloss.Color(""))
	bodyInput.Focus()

	m := &SendOverlayModel{
		state:              sendStateCompose,
		focusedField:       fieldMessageID,
		destination:        destination,
		client:             client,
		messageIDInput:     messageIDInput,
		sessionIDInput:     sessionIDInput,
		correlationIDInput: correlationIDInput,
		subjectInput:       subjectInput,
		timeToLiveInput:    timeToLiveInput,
		bodyInput:          bodyInput,
		customTypeInput:    customTypeInput,
		contentTypeOptions: []string{
			"application/json",
			"text/plain",
			"other",
		},
		selectedContentType: 0,
		spinner:             s,
		properties:          []messageProperty{{}},
		selectedPropertyRow: 0,
		selectedPropertyCol: 0,
		propertyKeyInput:    propertyKeyInput,
		propertyValueInput:  propertyValueInput,
	}
	m.syncInputsFromSelectedProperty()
	m.setFocusedField(fieldMessageID)
	return m
}

func NewSendOverlayModelFromMessage(destination string, client *azure.ServiceBusClient, message azure.MessageInfo) *SendOverlayModel {
	m := NewSendOverlayModel(destination, client)

	m.messageIDInput.SetValue(strings.TrimSpace(message.MessageID))
	m.sessionIDInput.SetValue(strings.TrimSpace(message.SessionID))
	m.correlationIDInput.SetValue(strings.TrimSpace(message.CorrelationID))
	m.subjectInput.SetValue(strings.TrimSpace(message.Subject))
	m.bodyInput.SetValue(message.Body)
	if message.TimeToLive > 0 {
		m.timeToLiveInput.SetValue(message.TimeToLive.String())
	}

	contentType := strings.TrimSpace(message.ContentType)
	switch contentType {
	case "application/json":
		m.selectedContentType = 0
	case "text/plain":
		m.selectedContentType = 1
	case "":
		m.selectedContentType = 0
	default:
		m.selectedContentType = 2
		m.customTypeInput.SetValue(contentType)
	}

	m.properties = buildPropertyRows(message.Properties)
	m.selectedPropertyRow = 0
	m.selectedPropertyCol = 0
	m.syncInputsFromSelectedProperty()
	m.setFocusedField(fieldBody)

	return m
}

func buildPropertyRows(properties map[string]any) []messageProperty {
	if len(properties) == 0 {
		return []messageProperty{{}}
	}

	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rows := make([]messageProperty, 0, len(keys)+1)
	for _, key := range keys {
		rows = append(rows, messageProperty{
			Key:   key,
			Value: fmt.Sprint(properties[key]),
		})
	}

	return append(rows, messageProperty{})
}

func (m *SendOverlayModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *SendOverlayModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case sendCompletedMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.state = sendStateError
			return nil
		}
		m.state = sendStateDone
		m.errMsg = ""
		return nil
	default:
		if m.state == sendStateSending {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return cmd
		}
	}

	return nil
}

func (m *SendOverlayModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch m.state {
	case sendStateSending:
		return nil
	case sendStateDone, sendStateError:
		switch msg.String() {
		case "esc", "enter":
			return func() tea.Msg { return SendDismissedMsg{} }
		}
		return nil
	case sendStateCompose:
		switch msg.String() {
		case "esc":
			return func() tea.Msg { return SendDismissedMsg{} }
		case "tab":
			if m.focusedField == fieldProperties {
				m.syncSelectedPropertyFromInputs()
				if m.movePropertyCell(1) {
					m.errMsg = ""
					return nil
				}
			}
			m.moveFocus(1)
			m.errMsg = ""
			return nil
		case "shift+tab":
			if m.focusedField == fieldProperties {
				m.syncSelectedPropertyFromInputs()
				if m.movePropertyCell(-1) {
					m.errMsg = ""
					return nil
				}
			}
			m.moveFocus(-1)
			m.errMsg = ""
			return nil
		case "ctrl+s":
			return m.startSending()
		case "ctrl+y":
			_, err := CopyTextToClipboard(m.bodyInput.Value())
			if err != nil {
				m.errMsg = fmt.Sprintf("copy failed: %v", err)
			}
			return nil
		}

		if m.focusedField == fieldContentType {
			switch msg.String() {
			case "up", "k", "left", "h":
				if m.selectedContentType > 0 {
					m.selectedContentType--
				}
				m.syncFocusWithContentType()
				m.errMsg = ""
				return nil
			case "down", "j", "right", "l":
				if m.selectedContentType < len(m.contentTypeOptions)-1 {
					m.selectedContentType++
				}
				m.syncFocusWithContentType()
				m.errMsg = ""
				return nil
			}
		}

		if m.focusedField == fieldProperties {
			return m.handlePropertiesKey(msg)
		}

		m.errMsg = ""
		switch m.focusedField {
		case fieldMessageID:
			var cmd tea.Cmd
			m.messageIDInput, cmd = m.messageIDInput.Update(msg)
			return cmd
		case fieldSessionID:
			var cmd tea.Cmd
			m.sessionIDInput, cmd = m.sessionIDInput.Update(msg)
			return cmd
		case fieldCorrelationID:
			var cmd tea.Cmd
			m.correlationIDInput, cmd = m.correlationIDInput.Update(msg)
			return cmd
		case fieldSubject:
			var cmd tea.Cmd
			m.subjectInput, cmd = m.subjectInput.Update(msg)
			return cmd
		case fieldTimeToLive:
			var cmd tea.Cmd
			m.timeToLiveInput, cmd = m.timeToLiveInput.Update(msg)
			return cmd
		case fieldCustomContentType:
			var cmd tea.Cmd
			m.customTypeInput, cmd = m.customTypeInput.Update(msg)
			return cmd
		case fieldBody:
			var cmd tea.Cmd
			m.bodyInput, cmd = m.bodyInput.Update(msg)
			return cmd
		}
	}

	return nil
}

func (m *SendOverlayModel) startSending() tea.Cmd {
	body := strings.TrimSpace(m.bodyInput.Value())
	if body == "" {
		m.errMsg = "body required"
		return nil
	}

	contentType := m.selectedContentTypeValue()
	if contentType == "" {
		m.errMsg = "content type required"
		return nil
	}

	var ttl time.Duration
	ttlText := strings.TrimSpace(m.timeToLiveInput.Value())
	if ttlText != "" {
		parsedTTL, err := time.ParseDuration(ttlText)
		if err != nil {
			m.errMsg = "invalid TTL; use Go duration syntax like 5m or 1h30m"
			return nil
		}
		if parsedTTL <= 0 {
			m.errMsg = "TTL must be greater than 0"
			return nil
		}
		ttl = parsedTTL
	}

	m.state = sendStateSending
	m.errMsg = ""

	message := azure.MessageInfo{
		MessageID:     strings.TrimSpace(m.messageIDInput.Value()),
		SessionID:     strings.TrimSpace(m.sessionIDInput.Value()),
		CorrelationID: strings.TrimSpace(m.correlationIDInput.Value()),
		Subject:       strings.TrimSpace(m.subjectInput.Value()),
		TimeToLive:    ttl,
		ContentType:   contentType,
		Body:          m.bodyInput.Value(),
	}

	m.syncSelectedPropertyFromInputs()
	message.Properties = m.buildPropertiesMap()

	return tea.Batch(m.spinner.Tick, m.sendMessageCmd(message))
}

func (m *SendOverlayModel) sendMessageCmd(message azure.MessageInfo) tea.Cmd {
	client := m.client
	destination := m.destination

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultContextTimeout)
		defer cancel()

		generateID := strings.TrimSpace(message.MessageID) == ""
		err := client.SendSingleMessage(ctx, destination, message, generateID)
		return sendCompletedMsg{err: err}
	}
}

func (m *SendOverlayModel) selectedContentTypeValue() string {
	if m.selectedContentType < 0 || m.selectedContentType >= len(m.contentTypeOptions) {
		return ""
	}
	selected := m.contentTypeOptions[m.selectedContentType]
	if selected != "other" {
		return selected
	}
	return strings.TrimSpace(m.customTypeInput.Value())
}

func (m *SendOverlayModel) availableFields() []sendField {
	fields := []sendField{fieldMessageID, fieldSessionID, fieldSubject, fieldCorrelationID, fieldTimeToLive, fieldContentType}
	if m.contentTypeOptions[m.selectedContentType] == "other" {
		fields = append(fields, fieldCustomContentType)
	}
	fields = append(fields, fieldProperties)
	fields = append(fields, fieldBody)
	return fields
}

func (m *SendOverlayModel) moveFocus(delta int) {
	fields := m.availableFields()
	idx := 0
	for i, f := range fields {
		if f == m.focusedField {
			idx = i
			break
		}
	}

	idx = (idx + delta + len(fields)) % len(fields)
	m.setFocusedField(fields[idx])
}

func (m *SendOverlayModel) syncFocusWithContentType() {
	if m.focusedField == fieldCustomContentType && m.contentTypeOptions[m.selectedContentType] != "other" {
		m.setFocusedField(fieldContentType)
	}
}

func (m *SendOverlayModel) setFocusedField(field sendField) {
	m.focusedField = field

	m.messageIDInput.Blur()
	m.sessionIDInput.Blur()
	m.correlationIDInput.Blur()
	m.subjectInput.Blur()
	m.timeToLiveInput.Blur()
	m.customTypeInput.Blur()
	m.bodyInput.Blur()
	m.propertyKeyInput.Blur()
	m.propertyValueInput.Blur()

	switch field {
	case fieldMessageID:
		m.messageIDInput.Focus()
	case fieldSessionID:
		m.sessionIDInput.Focus()
	case fieldCorrelationID:
		m.correlationIDInput.Focus()
	case fieldSubject:
		m.subjectInput.Focus()
	case fieldTimeToLive:
		m.timeToLiveInput.Focus()
	case fieldCustomContentType:
		m.customTypeInput.Focus()
	case fieldBody:
		m.bodyInput.Focus()
	case fieldProperties:
		m.focusPropertyInput()
	}
}

func (m *SendOverlayModel) View(width, height int) string {
	maxPanelWidth := max(width-4, 30)
	panelWidth := max(width*70/100, 30)
	panelWidth = min(panelWidth, maxPanelWidth)

	maxPanelHeight := max(height-4, 10)
	panelHeight := max(height*88/100, 10)
	panelHeight = min(panelHeight, maxPanelHeight)
	composePaneHeight := max(panelHeight-10, 8)

	leftWidth, rightWidth := splitComposeWidths(panelWidth)
	m.resizeInputs(leftWidth, rightWidth, composePaneHeight)

	titleStyle := lipgloss.NewStyle().
		Foreground(styles.Primary).
		Bold(true)
	focusedLabelStyle := lipgloss.NewStyle().Foreground(styles.Secondary).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(styles.Muted)

	var content strings.Builder

	switch m.state {
	case sendStateCompose:
		content.WriteString(titleStyle.Render("SEND MESSAGE"))
		content.WriteString("\n")
		content.WriteString(styles.Subtle.Render("Destination: " + m.destination))
		content.WriteString("\n\n")

		leftContent := m.renderComposeMetadata(focusedLabelStyle, labelStyle)
		rightContent := m.renderComposeBody(focusedLabelStyle, labelStyle, composePaneHeight)
		leftPane := lipgloss.NewStyle().Width(leftWidth).Height(composePaneHeight).Render(leftContent)
		rightPane := lipgloss.NewStyle().Width(rightWidth).Height(composePaneHeight).Render(rightContent)
		content.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane))

		if m.errMsg != "" {
			content.WriteString("\n")
			content.WriteString(styles.Error.Render(m.errMsg))
		}

	case sendStateSending:
		content.WriteString(titleStyle.Render("Sending message"))
		content.WriteString("\n\n")
		content.WriteString(m.spinner.View() + " Sending to " + m.destination + "...")

	case sendStateDone:
		content.WriteString(titleStyle.Render("Message sent"))
		content.WriteString("\n\n")
		content.WriteString(lipgloss.NewStyle().Foreground(styles.Success).Bold(true).Render("✓ Message sent successfully"))
		content.WriteString("\n\n")
		content.WriteString(styles.Subtle.Render("press esc or enter to close"))

	case sendStateError:
		content.WriteString(titleStyle.Render("Send failed"))
		content.WriteString("\n\n")
		content.WriteString(lipgloss.NewStyle().Foreground(styles.ErrorColor).Bold(true).Render("✗ Could not send message"))
		if m.errMsg != "" {
			content.WriteString("\n")
			content.WriteString(styles.Error.Render(m.errMsg))
		}
		content.WriteString("\n\n")
		content.WriteString(styles.Subtle.Render("press esc or enter to close"))
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(styles.Primary).
		Padding(1, 2).
		Width(panelWidth).
		Height(panelHeight).
		Render(content.String())

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m *SendOverlayModel) resizeInputs(leftWidth, rightWidth, paneHeight int) {
	leftInputWidth := max(leftWidth-4, 20)
	rightInputWidth := max(rightWidth-4, 28)
	bodyHeight := max(paneHeight-2, 10)

	m.metaInputWidth = leftInputWidth
	m.messageIDInput.Width = leftInputWidth
	m.sessionIDInput.Width = leftInputWidth
	m.correlationIDInput.Width = leftInputWidth
	m.subjectInput.Width = leftInputWidth
	m.timeToLiveInput.Width = leftInputWidth
	m.customTypeInput.Width = leftInputWidth
	targetPropertyRowWidth := lipgloss.Width(renderTextInputField("", false, leftInputWidth))
	propertyTotalWidth := 2
	bestPropertyRowWidth := 0
	for total := 2; total <= leftInputWidth; total++ {
		keyWidth := total / 2
		valueWidth := total - keyWidth
		rowWidth := propertyRowRenderedWidth(keyWidth, valueWidth)
		if rowWidth > targetPropertyRowWidth {
			continue
		}
		if rowWidth >= bestPropertyRowWidth {
			propertyTotalWidth = total
			bestPropertyRowWidth = rowWidth
		}
	}
	propertyKeyWidth := propertyTotalWidth / 2
	propertyValueWidth := propertyTotalWidth - propertyKeyWidth
	m.propertyKeyInput.Width = max(propertyKeyWidth, 1)
	m.propertyValueInput.Width = max(propertyValueWidth, 1)

	if m.bodyInputWidth != rightInputWidth {
		m.bodyInputWidth = rightInputWidth
		m.bodyInput.SetWidth(rightInputWidth)
	}
	if m.bodyInputHeight != bodyHeight {
		m.bodyInputHeight = bodyHeight
		m.bodyInput.SetHeight(bodyHeight)
	}
}

func splitComposeWidths(panelWidth int) (int, int) {
	innerWidth := max(panelWidth-6, 40)
	contentWidth := max(innerWidth-1, 39)
	leftWidth := max(contentWidth*40/100, 28)
	rightWidth := max(contentWidth-leftWidth, 28)
	if leftWidth+rightWidth > contentWidth {
		rightWidth = contentWidth - leftWidth
	}
	return leftWidth, rightWidth
}

func (m *SendOverlayModel) renderComposeMetadata(focusedLabelStyle, labelStyle lipgloss.Style) string {
	var content strings.Builder

	if m.focusedField == fieldMessageID {
		content.WriteString(focusedLabelStyle.Render("Message ID"))
	} else {
		content.WriteString(labelStyle.Render("Message ID"))
	}
	content.WriteString("\n")
	content.WriteString(renderTextInputField(m.messageIDInput.View(), m.focusedField == fieldMessageID, m.metaInputWidth))
	content.WriteString("\n\n")

	if m.focusedField == fieldSessionID {
		content.WriteString(focusedLabelStyle.Render("Session ID"))
	} else {
		content.WriteString(labelStyle.Render("Session ID"))
	}
	content.WriteString("\n")
	content.WriteString(renderTextInputField(m.sessionIDInput.View(), m.focusedField == fieldSessionID, m.metaInputWidth))
	content.WriteString("\n\n")

	if m.focusedField == fieldSubject {
		content.WriteString(focusedLabelStyle.Render("Subject"))
	} else {
		content.WriteString(labelStyle.Render("Subject"))
	}
	content.WriteString("\n")
	content.WriteString(renderTextInputField(m.subjectInput.View(), m.focusedField == fieldSubject, m.metaInputWidth))
	content.WriteString("\n\n")

	if m.focusedField == fieldCorrelationID {
		content.WriteString(focusedLabelStyle.Render("Correlation ID"))
	} else {
		content.WriteString(labelStyle.Render("Correlation ID"))
	}
	content.WriteString("\n")
	content.WriteString(renderTextInputField(m.correlationIDInput.View(), m.focusedField == fieldCorrelationID, m.metaInputWidth))
	content.WriteString("\n\n")

	if m.focusedField == fieldTimeToLive {
		content.WriteString(focusedLabelStyle.Render("Time To Live"))
	} else {
		content.WriteString(labelStyle.Render("Time To Live"))
	}
	content.WriteString("\n")
	content.WriteString(renderTextInputField(m.timeToLiveInput.View(), m.focusedField == fieldTimeToLive, m.metaInputWidth))
	content.WriteString("\n\n")

	if m.focusedField == fieldContentType {
		content.WriteString(focusedLabelStyle.Render("Content-Type"))
	} else {
		content.WriteString(labelStyle.Render("Content-Type"))
	}
	content.WriteString("\n")
	for i, opt := range m.contentTypeOptions {
		cursor := "( )"
		if i == m.selectedContentType {
			cursor = "(x)"
		}
		line := fmt.Sprintf("%s %s", cursor, opt)
		if i == m.selectedContentType {
			content.WriteString(lipgloss.NewStyle().Foreground(styles.Primary).Render(line))
		} else {
			content.WriteString(line)
		}
		content.WriteString("\n")
	}

	if m.contentTypeOptions[m.selectedContentType] == "other" {
		content.WriteString("\n")
		if m.focusedField == fieldCustomContentType {
			content.WriteString(focusedLabelStyle.Render("Custom Content-Type"))
		} else {
			content.WriteString(labelStyle.Render("Custom Content-Type"))
		}
		content.WriteString("\n")
		content.WriteString(renderTextInputField(m.customTypeInput.View(), m.focusedField == fieldCustomContentType, m.metaInputWidth))
	}

	content.WriteString("\n\n")
	if m.focusedField == fieldProperties {
		content.WriteString(focusedLabelStyle.Render("Properties"))
	} else {
		content.WriteString(labelStyle.Render("Properties"))
	}
	content.WriteString("\n")
	keyHeaderWidth := lipgloss.Width(renderTextInputField("", false, m.propertyKeyInput.Width))
	valueHeaderWidth := lipgloss.Width(renderTextInputField("", false, m.propertyValueInput.Width))
	keyHeader := lipgloss.NewStyle().Width(keyHeaderWidth).Render(styles.Subtle.Render("Key"))
	valueHeader := lipgloss.NewStyle().Width(valueHeaderWidth).Render(styles.Subtle.Render("Value"))
	content.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, keyHeader, " ", valueHeader))
	content.WriteString("\n")
	for i, row := range m.properties {
		key := row.Key
		val := row.Value
		if i == m.selectedPropertyRow {
			if m.focusedField == fieldProperties && m.selectedPropertyCol == 0 {
				key = m.propertyKeyInput.View()
			}
			if m.focusedField == fieldProperties && m.selectedPropertyCol == 1 {
				val = m.propertyValueInput.View()
			}
		}
		keyField := renderTextInputField(
			key,
			m.focusedField == fieldProperties && i == m.selectedPropertyRow && m.selectedPropertyCol == 0,
			m.propertyKeyInput.Width,
		)
		valueField := renderTextInputField(
			val,
			m.focusedField == fieldProperties && i == m.selectedPropertyRow && m.selectedPropertyCol == 1,
			m.propertyValueInput.Width,
		)
		content.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, keyField, " ", valueField))
		content.WriteString("\n")
	}

	return content.String()
}

func (m *SendOverlayModel) handlePropertiesKey(msg tea.KeyMsg) tea.Cmd {
	m.syncSelectedPropertyFromInputs()

	if len(m.properties) == 0 {
		m.properties = append(m.properties, messageProperty{})
		m.selectedPropertyRow = 0
		m.selectedPropertyCol = 0
		m.syncInputsFromSelectedProperty()
		m.focusPropertyInput()
		return nil
	}

	switch m.selectedPropertyCol {
	case 0:
		var cmd tea.Cmd
		m.propertyKeyInput, cmd = m.propertyKeyInput.Update(msg)
		m.syncSelectedPropertyFromInputs()
		m.appendPropertyRowIfCompleted()
		return cmd
	case 1:
		var cmd tea.Cmd
		m.propertyValueInput, cmd = m.propertyValueInput.Update(msg)
		m.syncSelectedPropertyFromInputs()
		m.appendPropertyRowIfCompleted()
		return cmd
	default:
		return nil
	}
}

func (m *SendOverlayModel) appendPropertyRowIfCompleted() {
	if len(m.properties) == 0 || m.selectedPropertyRow < 0 || m.selectedPropertyRow >= len(m.properties) {
		return
	}
	if m.selectedPropertyRow != len(m.properties)-1 {
		return
	}
	row := m.properties[m.selectedPropertyRow]
	if strings.TrimSpace(row.Key) == "" || strings.TrimSpace(row.Value) == "" {
		return
	}
	m.properties = append(m.properties, messageProperty{})
}

func (m *SendOverlayModel) focusPropertyInput() {
	if m.focusedField != fieldProperties || len(m.properties) == 0 {
		return
	}
	if m.selectedPropertyCol < 0 {
		m.selectedPropertyCol = 0
	}
	if m.selectedPropertyCol > 1 {
		m.selectedPropertyCol = 1
	}
	switch m.selectedPropertyCol {
	case 0:
		m.propertyKeyInput.Focus()
	case 1:
		m.propertyValueInput.Focus()
	}
}

func (m *SendOverlayModel) syncInputsFromSelectedProperty() {
	if len(m.properties) == 0 || m.selectedPropertyRow < 0 || m.selectedPropertyRow >= len(m.properties) {
		m.propertyKeyInput.SetValue("")
		m.propertyValueInput.SetValue("")
		return
	}
	row := m.properties[m.selectedPropertyRow]
	m.propertyKeyInput.SetValue(row.Key)
	m.propertyValueInput.SetValue(row.Value)
}

func (m *SendOverlayModel) syncSelectedPropertyFromInputs() {
	if len(m.properties) == 0 || m.selectedPropertyRow < 0 || m.selectedPropertyRow >= len(m.properties) {
		return
	}
	m.properties[m.selectedPropertyRow].Key = m.propertyKeyInput.Value()
	m.properties[m.selectedPropertyRow].Value = m.propertyValueInput.Value()
}

func (m *SendOverlayModel) buildPropertiesMap() map[string]any {
	props := make(map[string]any)
	for _, row := range m.properties {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		props[key] = row.Value
	}
	if len(props) == 0 {
		return nil
	}
	return props
}

func (m *SendOverlayModel) movePropertyCell(delta int) bool {
	if len(m.properties) == 0 {
		return false
	}

	if delta > 0 {
		if m.selectedPropertyCol == 0 {
			m.selectedPropertyCol = 1
			m.focusPropertyInput()
			return true
		}
		if m.selectedPropertyRow < len(m.properties)-1 {
			m.selectedPropertyRow++
			m.selectedPropertyCol = 0
			m.syncInputsFromSelectedProperty()
			m.focusPropertyInput()
			return true
		}
		return false
	}

	if m.selectedPropertyCol == 1 {
		m.selectedPropertyCol = 0
		m.focusPropertyInput()
		return true
	}
	if m.selectedPropertyRow > 0 {
		m.selectedPropertyRow--
		m.selectedPropertyCol = 1
		m.syncInputsFromSelectedProperty()
		m.focusPropertyInput()
		return true
	}

	return false
}

func (m *SendOverlayModel) renderComposeBody(focusedLabelStyle, labelStyle lipgloss.Style, paneHeight int) string {
	var content strings.Builder
	if m.focusedField == fieldBody {
		content.WriteString(focusedLabelStyle.Render("Body"))
	} else {
		content.WriteString(labelStyle.Render("Body"))
	}
	content.WriteString("\n")
	content.WriteString(renderTextAreaField(m.renderBodyView(), m.focusedField == fieldBody, m.bodyInputWidth, m.bodyInputHeight))

	help := lipgloss.NewStyle().
		Width(max(m.bodyInputWidth+2, 10)).
		Align(lipgloss.Right).
		Render(styles.Subtle.Render("tab: next · shift+tab: prev · ctrl+y: copy body · ctrl+s: send · esc: cancel"))

	bodySection := lipgloss.NewStyle().
		Width(max(m.bodyInputWidth+2, 10)).
		Height(max(paneHeight-lipgloss.Height(help), 1)).
		Render(content.String())

	return lipgloss.JoinVertical(lipgloss.Left, bodySection, help)
}

func (m *SendOverlayModel) renderBodyView() string {
	if m.focusedField == fieldBody {
		return m.bodyInput.View()
	}

	body := strings.TrimSpace(m.bodyInput.Value())
	if body == "" || !json.Valid([]byte(body)) {
		return m.bodyInput.View()
	}

	highlighted := styles.FormatJSONBody([]byte(body))
	return clampToLines(highlighted, m.bodyInputHeight)
}

func clampToLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return s
	}

	trimmed := strings.TrimSuffix(s, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	return strings.Join(lines[:maxLines], "\n")
}

func renderTextInputField(view string, focused bool, width int) string {
	borderColor := styles.Muted
	if focused {
		borderColor = styles.Primary
	}
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width).
		Render(view)
}

func propertyRowRenderedWidth(keyWidth, valueWidth int) int {
	return lipgloss.Width(renderTextInputField("", false, keyWidth)) + 1 + lipgloss.Width(renderTextInputField("", false, valueWidth))
}

func renderTextAreaField(view string, focused bool, width, height int) string {
	borderColor := styles.Muted
	if focused {
		borderColor = styles.Primary
	}
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width).
		Height(max(height+2, 1)).
		Render(view)
}
