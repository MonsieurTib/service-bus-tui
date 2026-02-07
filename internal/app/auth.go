package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/popandcode/asb-tui/internal/azure"
	"github.com/popandcode/asb-tui/internal/styles"
)

const (
	authContextTimeout = 30 * time.Second
)

type CredentialType int

const (
	AzureCLI CredentialType = iota
	InteractiveBrowser
	ConnectionString
)

type AuthModel struct {
	selectedAuth           CredentialType
	authOptions            []string
	credentialTypes        []CredentialType
	inNamespaceMode        bool
	inConnectionStringMode bool
	connectionStringInput  textinput.Model
	errMsg                 string
	isAuthenticating       bool
	namespaces             []azure.NamespaceInfo
	selectedNamespaceIdx   int
	authenticatedUser      string
	spinner                spinner.Model
	height                 int
	scrollOffset           int
}

func NewAuthModel() *AuthModel {
	s := spinner.New()
	s.Spinner = spinner.Dot

	ti := textinput.New()
	ti.Placeholder = "Endpoint=sb://...;SharedAccessKeyName=...;SharedAccessKey=..."
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	ti.Width = 80

	m := &AuthModel{
		authOptions: []string{
			"Interactive Browser",
			"Connection String",
		},
		credentialTypes: []CredentialType{
			InteractiveBrowser,
			ConnectionString,
		},
		selectedAuth:          0,
		connectionStringInput: ti,
		spinner:               s,
		height:                50,
		scrollOffset:          0,
	}

	if user, ok := azure.GetAzureCliAuthenticatedUser(); ok {
		m.authenticatedUser = user
		m.authOptions = append(
			[]string{fmt.Sprintf("Azure CLI, authenticated as %s", user)},
			m.authOptions...,
		)
		m.credentialTypes = append(
			[]CredentialType{AzureCLI},
			m.credentialTypes...,
		)
		m.selectedAuth = 0
	}

	return m
}

func (m *AuthModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, textinput.Blink)
}

func (m *AuthModel) selectedCredentialType() CredentialType {
	if int(m.selectedAuth) < len(m.credentialTypes) {
		return m.credentialTypes[m.selectedAuth]
	}
	return m.credentialTypes[0]
}

func (m *AuthModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var spinnerCmd tea.Cmd
	m.spinner, spinnerCmd = m.spinner.Update(msg)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.inConnectionStringMode {
				m.inConnectionStringMode = false
				m.connectionStringInput.SetValue("")
				m.errMsg = ""
				return m, spinnerCmd
			}
			if m.inNamespaceMode {
				m.inNamespaceMode = false
				m.namespaces = nil
				m.errMsg = ""
				m.scrollOffset = 0
			}
			return m, spinnerCmd
		}

		if m.inConnectionStringMode {
			return m.updateConnectionStringInput(msg)
		} else if m.inNamespaceMode {
			return m.updateNamespaceSelection(msg)
		} else {
			return m.updateAuthSelection(msg)
		}

	case tea.WindowSizeMsg:
		m.height = max(msg.Height-5, 5)

	case NamespacesLoadedMsg:
		m.inNamespaceMode = true
		m.namespaces = msg.Namespaces
		m.selectedNamespaceIdx = 0
		m.scrollOffset = 0
		m.isAuthenticating = false
		return m, spinnerCmd

	case ErrorMsg:
		m.errMsg = string(msg)
		m.isAuthenticating = false
		return m, spinnerCmd
	}

	return m, spinnerCmd
}

func (m *AuthModel) updateNamespaceSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.selectedNamespaceIdx > 0 {
			m.selectedNamespaceIdx--
		}
	case "down", "j":
		if m.selectedNamespaceIdx < len(m.namespaces)-1 {
			m.selectedNamespaceIdx++
		}
	case "enter":
		m.isAuthenticating = true
		return m, m.connectWithNamespaceCmd(m.namespaces[m.selectedNamespaceIdx].Name)
	}
	return m, m.spinner.Tick
}

func (m *AuthModel) updateAuthSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.selectedAuth > 0 {
			m.selectedAuth--
		}
	case "down", "j":
		if m.selectedAuth < CredentialType(len(m.authOptions)-1) {
			m.selectedAuth++
		}
	case "enter":
		m.errMsg = ""
		if m.selectedCredentialType() == ConnectionString {
			m.inConnectionStringMode = true
			m.connectionStringInput.Focus()
			return m, textinput.Blink
		}
		m.isAuthenticating = true
		return m, tea.Batch(m.spinner.Tick, m.authenticateAndListNamespacesCmd())
	}
	return m, m.spinner.Tick
}

func (m *AuthModel) updateConnectionStringInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		connStr := strings.TrimSpace(m.connectionStringInput.Value())
		if connStr == "" {
			m.errMsg = "connection string cannot be empty"
			return m, nil
		}
		m.errMsg = ""
		m.isAuthenticating = true
		m.inConnectionStringMode = false
		return m, tea.Batch(m.spinner.Tick, m.connectWithConnectionStringCmd(connStr))
	default:
		var cmd tea.Cmd
		m.connectionStringInput, cmd = m.connectionStringInput.Update(msg)
		return m, cmd
	}
}

func (m *AuthModel) View() string {
	var s strings.Builder

	if m.isAuthenticating {
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(styles.Subtle.Render("Connecting..."))
		s.WriteString("\n")
	} else {
		if m.inConnectionStringMode {
			m.viewConnectionStringInput(&s)
		} else if m.inNamespaceMode {
			m.viewNamespaceSelection(&s)
		} else {
			m.viewAuthSelection(&s)
		}

		if m.errMsg != "" {
			s.WriteString("\n")
			s.WriteString(styles.Error.Render("Error: " + m.errMsg))
			s.WriteString("\n")
		}

		s.WriteString("\n")
		s.WriteString(styles.Subtle.Render("↑↓/jk: navigate | enter: select | esc: back | ctrl+c: quit"))
		s.WriteString("\n")
	}

	return s.String()
}

func (m *AuthModel) viewNamespaceSelection(s *strings.Builder) {
	s.WriteString(styles.Subtle.Render("Select Namespace"))
	s.WriteString("\n\n")

	maxLines := m.height

	if m.selectedNamespaceIdx >= len(m.namespaces) {
		m.selectedNamespaceIdx = len(m.namespaces) - 1
	}

	if m.selectedNamespaceIdx < m.scrollOffset {
		m.scrollOffset = m.selectedNamespaceIdx
	} else if m.selectedNamespaceIdx >= m.scrollOffset+maxLines {
		m.scrollOffset = m.selectedNamespaceIdx - maxLines + 1
	}

	endIdx := min(m.scrollOffset+maxLines, len(m.namespaces))

	for i := m.scrollOffset; i < endIdx; i++ {
		ns := m.namespaces[i]
		subID := ns.Subscription
		if len(subID) > 8 {
			subID = subID[:8]
		}
		display := fmt.Sprintf("%s (%s / %s)", ns.Name, subID, ns.ResourceGroup)

		var line string
		if i == m.selectedNamespaceIdx {
			line = styles.Selected.Render("▶ " + display)
		} else {
			line = "  " + display
		}
		s.WriteString(line)
		s.WriteString("\n")
	}
}

func (m *AuthModel) viewAuthSelection(s *strings.Builder) {
	s.WriteString(styles.Subtle.Render("Select Authentication Method"))
	s.WriteString("\n\n")

	for i, opt := range m.authOptions {
		var line string
		if CredentialType(i) == m.selectedAuth {
			line = styles.Selected.Render("▶ " + opt)
		} else {
			line = "  " + opt
		}
		s.WriteString(line)
		s.WriteString("\n")
	}

	if m.isAuthenticating {
		s.WriteString("\n")
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(styles.Subtle.Render("Connecting..."))
		s.WriteString("\n")
	}
}

func (m *AuthModel) viewConnectionStringInput(s *strings.Builder) {
	s.WriteString(styles.Subtle.Render("Enter Connection String"))
	s.WriteString("\n\n")
	s.WriteString(m.connectionStringInput.View())
	s.WriteString("\n")
}

type NamespacesLoadedMsg struct {
	Namespaces []azure.NamespaceInfo
}

type ErrorMsg string

func (m *AuthModel) authenticateAndListNamespacesCmd() tea.Cmd {
	credType := m.selectedCredentialType()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), authContextTimeout)
		defer cancel()

		var namespaces []azure.NamespaceInfo
		var err error

		if credType == AzureCLI {
			namespaces, err = azure.GetNamespacesForAzureCLI(ctx)
		} else {
			namespaces, err = azure.GetNamespacesForInteractiveBrowser(ctx)
		}

		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to authenticate or list namespaces: %v", err))
		}

		return NamespacesLoadedMsg{Namespaces: namespaces}
	}
}

func (m *AuthModel) connectWithNamespaceCmd(namespace string) tea.Cmd {
	credType := m.selectedCredentialType()
	return func() tea.Msg {
		var client *azure.ServiceBusClient
		var err error

		if credType == AzureCLI {
			client, err = azure.NewServiceBusClientFromAzureCLI(namespace)
		} else {
			client, err = azure.NewServiceBusClientFromInteractiveBrowser(namespace)
		}

		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to connect: %v", err))
		}

		log.Printf("authenticated and connected to namespace: %s", namespace)
		return NamespaceConnectedMsg{
			Namespace: namespace,
			Client:    client,
		}
	}
}

func (m *AuthModel) connectWithConnectionStringCmd(connectionString string) tea.Cmd {
	return func() tea.Msg {
		client, err := azure.NewServiceBusClientFromConnectionString(connectionString)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to connect with connection string: %v", err))
		}

		namespace := client.GetNamespace()

		log.Printf("connected to namespace via connection string: %s", namespace)
		return NamespaceConnectedMsg{
			Namespace: namespace,
			Client:    client,
		}
	}
}

type NamespaceConnectedMsg struct {
	Namespace string
	Client    *azure.ServiceBusClient
}
