package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/MonsieurTib/service-bus-tui/internal/azure"
	"github.com/MonsieurTib/service-bus-tui/internal/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	authContextTimeout       = 30 * time.Second
	emulatorConnectionString = "Endpoint=sb://localhost;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=SAS_KEY_VALUE;UseDevelopmentEmulator=true"
)

type CredentialType int

const (
	AzureCLI CredentialType = iota
	InteractiveBrowser
	ServicePrincipal
	ConnectionString
	SavedConnections
	Emulator
)

type savePromptFocus int

const (
	savePromptFocusName savePromptFocus = iota
	savePromptFocusSave
	savePromptFocusSkip
)

type AuthModel struct {
	selectedAuth            CredentialType
	authOptions             []string
	credentialTypes         []CredentialType
	inNamespaceMode         bool
	inConnectionStringMode  bool
	inSavedConnectionsMode  bool
	inSaveConnectionPrompt  bool
	connectionStringInput   textinput.Model
	saveNameInput           textinput.Model
	savePromptFocus         savePromptFocus
	inServicePrincipalMode  bool
	servicePrincipalInputs  [4]textinput.Model
	focusedSPInput          int
	store                   *ConnectionStore
	savedConnections        []SavedConnection
	selectedSavedIdx        int
	pendingConnection       *NamespaceConnectedMsg
	pendingConnectionString string
	spTenantID              string
	spClientID              string
	spClientSecret          string
	errMsg                  string
	isAuthenticating        bool
	namespaces              []azure.NamespaceInfo
	namespaceFilterInput    textinput.Model
	selectedNamespaceIdx    int
	authenticatedUser       string
	spinner                 spinner.Model
	width                   int
	height                  int
	scrollOffset            int
}

func NewAuthModel() *AuthModel {
	s := spinner.New()
	s.Spinner = spinner.Dot

	ti := textinput.New()
	ti.Placeholder = "Endpoint=sb://...;SharedAccessKeyName=...;SharedAccessKey=..."
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	ti.Width = 80

	saveNameInput := textinput.New()
	saveNameInput.Placeholder = "my-namespace"
	saveNameInput.Width = 40

	namespaceFilterInput := textinput.New()
	namespaceFilterInput.Placeholder = "Filter namespaces..."
	namespaceFilterInput.Width = 80

	// Service Principal text inputs: Tenant ID, Client ID, Client Secret, Namespace (optional)
	var spInputs [4]textinput.Model
	for i := range spInputs {
		spInputs[i] = textinput.New()
		spInputs[i].Width = 60
	}
	spInputs[0].Placeholder = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	spInputs[1].Placeholder = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	spInputs[2].Placeholder = "your-client-secret"
	spInputs[2].EchoMode = textinput.EchoPassword
	spInputs[2].EchoCharacter = '*'
	spInputs[3].Placeholder = "my-namespace"

	m := &AuthModel{
		store:                  NewConnectionStore(),
		selectedAuth:           0,
		connectionStringInput:  ti,
		saveNameInput:          saveNameInput,
		namespaceFilterInput:   namespaceFilterInput,
		servicePrincipalInputs: spInputs,
		spinner:                s,
		width:                  100,
		height:                 50,
		scrollOffset:           0,
	}

	if saved, err := m.store.List(); err == nil {
		m.savedConnections = saved
	}
	m.refreshAuthOptions()

	if user, ok := azure.GetAzureCliAuthenticatedUser(); ok {
		m.authenticatedUser = user
		m.refreshAuthOptions()
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

func (m *AuthModel) refreshAuthOptions() {
	authOptions := []string{
		"Interactive Browser",
		"Service Principal",
		"Connection String",
	}
	credentialTypes := []CredentialType{
		InteractiveBrowser,
		ServicePrincipal,
		ConnectionString,
	}

	if len(m.savedConnections) > 0 {
		authOptions = append(authOptions, "Saved Connection Strings")
		credentialTypes = append(credentialTypes, SavedConnections)
	}

	authOptions = append(authOptions, "Emulator (localhost)")
	credentialTypes = append(credentialTypes, Emulator)

	if m.authenticatedUser != "" {
		authOptions = append(
			[]string{fmt.Sprintf("Azure CLI, authenticated as %s", m.authenticatedUser)},
			authOptions...,
		)
		credentialTypes = append([]CredentialType{AzureCLI}, credentialTypes...)
	}

	m.authOptions = authOptions
	m.credentialTypes = credentialTypes
	if len(m.authOptions) == 0 {
		m.selectedAuth = 0
		return
	}
	if int(m.selectedAuth) >= len(m.authOptions) {
		m.selectedAuth = CredentialType(len(m.authOptions) - 1)
	}
}

func (m *AuthModel) refreshSavedConnections() error {
	saved, err := m.store.List()
	if err != nil {
		return err
	}
	m.savedConnections = saved
	m.refreshAuthOptions()
	if m.selectedSavedIdx >= len(m.savedConnections) {
		m.selectedSavedIdx = max(0, len(m.savedConnections)-1)
	}
	return nil
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
			if m.inSaveConnectionPrompt {
				return m.skipPendingConnection()
			}
			if m.inSavedConnectionsMode {
				m.inSavedConnectionsMode = false
				m.errMsg = ""
				return m, spinnerCmd
			}
			if m.inConnectionStringMode {
				m.inConnectionStringMode = false
				m.connectionStringInput.SetValue("")
				m.errMsg = ""
				return m, spinnerCmd
			}
			if m.inServicePrincipalMode {
				m.inServicePrincipalMode = false
				for i := range m.servicePrincipalInputs {
					m.servicePrincipalInputs[i].SetValue("")
				}
				m.focusedSPInput = 0
				m.errMsg = ""
				return m, spinnerCmd
			}
			if m.inNamespaceMode {
				if m.namespaceFilterInput.Value() != "" {
					m.namespaceFilterInput.SetValue("")
					m.selectedNamespaceIdx = 0
					m.scrollOffset = 0
					return m, spinnerCmd
				}
				m.inNamespaceMode = false
				m.namespaces = nil
				m.namespaceFilterInput.Blur()
				m.errMsg = ""
				m.scrollOffset = 0
			}
			return m, spinnerCmd
		}

		if m.inSaveConnectionPrompt {
			return m.updateSaveConnectionPrompt(msg)
		} else if m.inSavedConnectionsMode {
			return m.updateSavedConnections(msg)
		} else if m.inConnectionStringMode {
			return m.updateConnectionStringInput(msg)
		} else if m.inServicePrincipalMode {
			return m.updateServicePrincipalInput(msg)
		} else if m.inNamespaceMode {
			return m.updateNamespaceSelection(msg)
		} else {
			return m.updateAuthSelection(msg)
		}

	case tea.WindowSizeMsg:
		m.width = max(msg.Width, 20)
		m.height = max(msg.Height-5, 5)

	case NamespacesLoadedMsg:
		m.inNamespaceMode = true
		m.namespaces = msg.Namespaces
		m.selectedNamespaceIdx = 0
		m.scrollOffset = 0
		m.isAuthenticating = false
		m.namespaceFilterInput.SetValue("")
		m.namespaceFilterInput.Focus()
		return m, tea.Batch(spinnerCmd, textinput.Blink)

	case ErrorMsg:
		m.errMsg = string(msg)
		m.isAuthenticating = false
		return m, spinnerCmd

	case ConnectionStringConnectedMsg:
		m.isAuthenticating = false
		m.inSaveConnectionPrompt = true
		m.savePromptFocus = savePromptFocusName
		m.pendingConnectionString = msg.ConnectionString
		m.pendingConnection = &NamespaceConnectedMsg{Namespace: msg.Namespace, Client: msg.Client}
		defaultName := strings.TrimSpace(msg.Namespace)
		m.saveNameInput.SetValue(defaultName)
		m.saveNameInput.Focus()
		m.errMsg = ""
		return m, textinput.Blink
	}

	return m, spinnerCmd
}

func (m *AuthModel) updateNamespaceSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredNamespaces()

	switch msg.String() {
	case "up":
		if m.selectedNamespaceIdx > 0 {
			m.selectedNamespaceIdx--
		}
		return m, m.spinner.Tick
	case "down":
		if m.selectedNamespaceIdx < len(filtered)-1 {
			m.selectedNamespaceIdx++
		}
		return m, m.spinner.Tick
	case "enter":
		if len(filtered) == 0 {
			return m, m.spinner.Tick
		}
		m.isAuthenticating = true
		return m, m.connectWithNamespaceCmd(filtered[m.selectedNamespaceIdx].Name)
	}

	var cmd tea.Cmd
	m.namespaceFilterInput, cmd = m.namespaceFilterInput.Update(msg)

	filtered = m.filteredNamespaces()
	if m.selectedNamespaceIdx >= len(filtered) {
		m.selectedNamespaceIdx = max(len(filtered)-1, 0)
	}
	m.scrollOffset = 0

	return m, tea.Batch(cmd, m.spinner.Tick)
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
		if m.selectedCredentialType() == Emulator {
			m.isAuthenticating = true
			return m, tea.Batch(m.spinner.Tick, m.connectWithConnectionStringCmd(emulatorConnectionString))
		}
		if m.selectedCredentialType() == SavedConnections {
			if err := m.refreshSavedConnections(); err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			if len(m.savedConnections) == 0 {
				m.errMsg = "no saved connection strings"
				return m, nil
			}
			m.inSavedConnectionsMode = true
			m.selectedSavedIdx = 0
			return m, nil
		}
		if m.selectedCredentialType() == ConnectionString {
			m.inConnectionStringMode = true
			m.connectionStringInput.Focus()
			return m, textinput.Blink
		}
		if m.selectedCredentialType() == ServicePrincipal {
			m.inServicePrincipalMode = true
			m.focusedSPInput = 0
			m.servicePrincipalInputs[0].Focus()
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
		return m, tea.Batch(m.spinner.Tick, m.connectWithConnectionStringAndPromptCmd(connStr))
	default:
		var cmd tea.Cmd
		m.connectionStringInput, cmd = m.connectionStringInput.Update(msg)
		return m, cmd
	}
}

func (m *AuthModel) updateSavedConnections(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.savedConnections) == 0 {
		m.inSavedConnectionsMode = false
		m.refreshAuthOptions()
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if m.selectedSavedIdx > 0 {
			m.selectedSavedIdx--
		}
	case "down", "j":
		if m.selectedSavedIdx < len(m.savedConnections)-1 {
			m.selectedSavedIdx++
		}
	case "enter":
		entry := m.savedConnections[m.selectedSavedIdx]
		m.inSavedConnectionsMode = false
		m.isAuthenticating = true
		m.errMsg = ""
		return m, tea.Batch(m.spinner.Tick, m.connectWithConnectionStringCmd(entry.ConnectionString))
	case "x", "delete":
		entry := m.savedConnections[m.selectedSavedIdx]
		if err := m.store.Delete(entry.ID); err != nil {
			m.errMsg = fmt.Sprintf("failed to delete saved connection: %v", err)
			return m, nil
		}
		if err := m.refreshSavedConnections(); err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		if len(m.savedConnections) == 0 {
			m.inSavedConnectionsMode = false
			m.errMsg = ""
		}
	}

	return m, nil
}

func (m *AuthModel) updateSaveConnectionPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab":
		m.moveSavePromptFocus(1)
		return m, textinput.Blink
	case "shift+tab":
		m.moveSavePromptFocus(-1)
		return m, textinput.Blink
	case "left", "h":
		if m.savePromptFocus == savePromptFocusSkip {
			m.setSavePromptFocus(savePromptFocusSave)
		}
		return m, textinput.Blink
	case "right", "l":
		if m.savePromptFocus == savePromptFocusSave {
			m.setSavePromptFocus(savePromptFocusSkip)
		}
		return m, textinput.Blink
	case "enter":
		switch m.savePromptFocus {
		case savePromptFocusName:
			m.setSavePromptFocus(savePromptFocusSave)
			return m, textinput.Blink
		case savePromptFocusSave:
			if strings.TrimSpace(m.saveNameInput.Value()) == "" {
				return m, nil
			}
			if _, err := m.store.Save(m.saveNameInput.Value(), m.pendingConnectionString); err != nil {
				m.errMsg = fmt.Sprintf("failed to save connection string: %v", err)
				return m, nil
			}
			if err := m.refreshSavedConnections(); err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			m.errMsg = ""
			return m.finishPendingConnection()
		case savePromptFocusSkip:
			return m.skipPendingConnection()
		}
	default:
		if m.savePromptFocus == savePromptFocusName {
			var cmd tea.Cmd
			m.saveNameInput, cmd = m.saveNameInput.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m *AuthModel) moveSavePromptFocus(step int) {
	next := int(m.savePromptFocus) + step
	if next < 0 {
		next = 2
	}
	if next > 2 {
		next = 0
	}
	m.setSavePromptFocus(savePromptFocus(next))
}

func (m *AuthModel) setSavePromptFocus(focus savePromptFocus) {
	m.savePromptFocus = focus
	if focus == savePromptFocusName {
		m.saveNameInput.Focus()
	} else {
		m.saveNameInput.Blur()
	}
}

func (m *AuthModel) finishPendingConnection() (tea.Model, tea.Cmd) {
	if m.pendingConnection == nil {
		m.inSaveConnectionPrompt = false
		return m, nil
	}
	connected := *m.pendingConnection
	m.inSaveConnectionPrompt = false
	m.pendingConnection = nil
	m.pendingConnectionString = ""
	m.saveNameInput.SetValue("")
	return m, func() tea.Msg { return connected }
}

func (m *AuthModel) skipPendingConnection() (tea.Model, tea.Cmd) {
	return m.finishPendingConnection()
}

func (m *AuthModel) updateServicePrincipalInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "down":
		m.servicePrincipalInputs[m.focusedSPInput].Blur()
		m.focusedSPInput = (m.focusedSPInput + 1) % len(m.servicePrincipalInputs)
		m.servicePrincipalInputs[m.focusedSPInput].Focus()
		return m, textinput.Blink
	case "shift+tab", "up":
		m.servicePrincipalInputs[m.focusedSPInput].Blur()
		m.focusedSPInput = (m.focusedSPInput - 1 + len(m.servicePrincipalInputs)) % len(m.servicePrincipalInputs)
		m.servicePrincipalInputs[m.focusedSPInput].Focus()
		return m, textinput.Blink
	case "enter":
		tenantID := strings.TrimSpace(m.servicePrincipalInputs[0].Value())
		clientID := strings.TrimSpace(m.servicePrincipalInputs[1].Value())
		clientSecret := strings.TrimSpace(m.servicePrincipalInputs[2].Value())
		namespace := strings.TrimSpace(m.servicePrincipalInputs[3].Value())

		if tenantID == "" || clientID == "" || clientSecret == "" {
			m.errMsg = "tenant ID, client ID, and client secret are required"
			return m, nil
		}

		m.errMsg = ""
		m.spTenantID = tenantID
		m.spClientID = clientID
		m.spClientSecret = clientSecret
		m.isAuthenticating = true
		m.inServicePrincipalMode = false

		if namespace != "" {
			// Namespace provided — skip discovery, connect directly
			return m, tea.Batch(m.spinner.Tick, m.connectWithNamespaceCmd(namespace))
		}
		// No namespace — attempt discovery
		return m, tea.Batch(m.spinner.Tick, m.authenticateAndListNamespacesForSPCmd())
	default:
		var cmd tea.Cmd
		m.servicePrincipalInputs[m.focusedSPInput], cmd = m.servicePrincipalInputs[m.focusedSPInput].Update(msg)
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
		if m.inSavedConnectionsMode {
			m.viewSavedConnections(&s)
		} else if m.inConnectionStringMode {
			m.viewConnectionStringInput(&s)
		} else if m.inServicePrincipalMode {
			m.viewServicePrincipalInput(&s)
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
		if m.inSavedConnectionsMode {
			s.WriteString(styles.Subtle.Render("↑↓/jk: navigate | enter: connect | x/del: delete | esc: back | ctrl+c: quit"))
		} else if m.inNamespaceMode {
			s.WriteString(styles.Subtle.Render("type to filter | ↑↓: navigate | enter: connect | esc: clear/back | ctrl+c: quit"))
		} else {
			s.WriteString(styles.Subtle.Render("↑↓/jk: navigate | enter: select | esc: back | ctrl+c: quit"))
		}
		s.WriteString("\n")
	}

	view := s.String()
	if m.inSaveConnectionPrompt {
		return m.renderSaveConnectionPrompt(view)
	}

	return view
}

func namespaceDisplayLine(ns azure.NamespaceInfo) string {
	subID := ns.Subscription
	if len(subID) > 8 {
		subID = subID[:8]
	}
	return fmt.Sprintf("%s (%s / %s)", ns.Name, subID, ns.ResourceGroup)
}

func (m *AuthModel) filteredNamespaces() []azure.NamespaceInfo {
	q := strings.ToLower(strings.TrimSpace(m.namespaceFilterInput.Value()))
	if q == "" {
		return m.namespaces
	}
	var out []azure.NamespaceInfo
	for _, ns := range m.namespaces {
		if strings.Contains(strings.ToLower(namespaceDisplayLine(ns)), q) {
			out = append(out, ns)
		}
	}
	return out
}

func (m *AuthModel) viewNamespaceSelection(s *strings.Builder) {
	s.WriteString(styles.Subtle.Render("Select Namespace"))
	s.WriteString("\n\n")

	inputWidth := max(m.width-4, 10)
	m.namespaceFilterInput.Width = inputWidth
	s.WriteString(renderTextInputField(m.namespaceFilterInput.View(), true, inputWidth))
	s.WriteString("\n\n")

	filtered := m.filteredNamespaces()

	// The filter box above consumes 4 extra lines (label + blank, box + blank).
	maxLines := max(m.height-4, 1)

	if len(filtered) == 0 {
		s.WriteString(styles.Subtle.Render("No namespaces match"))
		s.WriteString("\n")
		return
	}

	if m.selectedNamespaceIdx >= len(filtered) {
		m.selectedNamespaceIdx = len(filtered) - 1
	}
	if m.selectedNamespaceIdx < 0 {
		m.selectedNamespaceIdx = 0
	}

	if m.selectedNamespaceIdx < m.scrollOffset {
		m.scrollOffset = m.selectedNamespaceIdx
	} else if m.selectedNamespaceIdx >= m.scrollOffset+maxLines {
		m.scrollOffset = m.selectedNamespaceIdx - maxLines + 1
	}

	endIdx := min(m.scrollOffset+maxLines, len(filtered))

	for i := m.scrollOffset; i < endIdx; i++ {
		display := namespaceDisplayLine(filtered[i])

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

func (m *AuthModel) viewSavedConnections(s *strings.Builder) {
	s.WriteString(styles.Subtle.Render("Saved Connection Strings"))
	s.WriteString("\n\n")

	for i, entry := range m.savedConnections {
		entryID := entry.ID
		if len(entryID) > 8 {
			entryID = entryID[:8]
		}
		line := fmt.Sprintf("%s  %s", entry.Name, styles.Subtle.Render("("+entryID+")"))
		if i == m.selectedSavedIdx {
			s.WriteString(styles.Selected.Render("▶ " + line))
		} else {
			s.WriteString("  " + line)
		}
		s.WriteString("\n")
	}

	if len(m.savedConnections) == 0 {
		s.WriteString(styles.Subtle.Render("No saved connection strings"))
		s.WriteString("\n")
	}
}

func (m *AuthModel) viewConnectionStringInput(s *strings.Builder) {
	s.WriteString(styles.Subtle.Render("Enter Connection String"))
	s.WriteString("\n\n")
	s.WriteString(m.connectionStringInput.View())
	s.WriteString("\n")
}

func (m *AuthModel) viewServicePrincipalInput(s *strings.Builder) {
	s.WriteString(styles.Subtle.Render("Enter Service Principal Credentials"))
	s.WriteString("\n\n")

	labels := [4]string{"Tenant ID", "Client ID", "Client Secret", "Namespace"}
	for i, label := range labels {
		if i == m.focusedSPInput {
			s.WriteString(styles.Selected.Render(label))
		} else {
			s.WriteString(styles.Subtle.Render(label))
		}
		if i == 3 {
			s.WriteString(styles.Subtle.Render(" (optional — required if SP lacks Reader role on subscription)"))
		}
		s.WriteString("\n")
		s.WriteString(m.servicePrincipalInputs[i].View())
		s.WriteString("\n\n")
	}
}

func (m *AuthModel) renderSaveConnectionPrompt(_ string) string {
	panelWidth := min(60, max(m.width-4, 40))
	panelHeight := min(14, max(m.height-4, 10))
	// innerWidth: usable content area inside panel border+padding (same formula as send.go)
	innerWidth := panelWidth - 6
	// inputWidth: container width for renderTextInputField (its border+padding = 4 chars overhead)
	inputWidth := max(innerWidth, 20)
	m.saveNameInput.Width = inputWidth

	nameEmpty := strings.TrimSpace(m.saveNameInput.Value()) == ""

	var labelStr string
	if m.savePromptFocus == savePromptFocusName {
		labelStr = styles.Label.Render("Connection name")
	} else {
		labelStr = styles.Subtle.Render("Connection name")
	}

	renderButton := func(label string, focused bool, disabled bool) string {
		borderColor := styles.Muted
		textColor := styles.Muted
		if focused && !disabled {
			borderColor = styles.Primary
			textColor = styles.Primary
		}
		return lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(borderColor).
			Foreground(textColor).
			Bold(focused && !disabled).
			Padding(0, 1).
			Render(label)
	}

	saveBtn := renderButton("save", m.savePromptFocus == savePromptFocusSave, nameEmpty)
	skipBtn := renderButton("skip", m.savePromptFocus == savePromptFocusSkip, false)
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, saveBtn, "  ", skipBtn)

	inputField := renderTextInputField(m.saveNameInput.View(), m.savePromptFocus == savePromptFocusName, inputWidth)
	// align buttons right edge with input right edge by measuring actual rendered width
	buttonsRow := lipgloss.NewStyle().Width(lipgloss.Width(inputField)).Align(lipgloss.Right).Render(buttons)

	content := lipgloss.JoinVertical(lipgloss.Left,
		styles.Title.Render("Save connection string?"),
		labelStr,
		inputField,
		"",
		buttonsRow,
		"",
		styles.Subtle.Render("tab: focus next • enter: select • esc: skip"),
	)

	panelStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(styles.Primary).
		Padding(1, 2).
		Width(panelWidth).
		Height(panelHeight)

	overlay := panelStyle.Render(content)
	return lipgloss.Place(max(m.width, 60), max(m.height+5, 20), lipgloss.Center, lipgloss.Center, overlay)
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

func (m *AuthModel) authenticateAndListNamespacesForSPCmd() tea.Cmd {
	tenantID := m.spTenantID
	clientID := m.spClientID
	clientSecret := m.spClientSecret
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), authContextTimeout)
		defer cancel()

		namespaces, err := azure.GetNamespacesForServicePrincipal(ctx, tenantID, clientID, clientSecret)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to authenticate or list namespaces: %v", err))
		}

		return NamespacesLoadedMsg{Namespaces: namespaces}
	}
}

func (m *AuthModel) connectWithNamespaceCmd(namespace string) tea.Cmd {
	credType := m.selectedCredentialType()
	tenantID := m.spTenantID
	clientID := m.spClientID
	clientSecret := m.spClientSecret
	return func() tea.Msg {
		var client *azure.ServiceBusClient
		var err error

		switch credType {
		case AzureCLI:
			client, err = azure.NewServiceBusClientFromAzureCLI(namespace)
		case ServicePrincipal:
			client, err = azure.NewServiceBusClientFromServicePrincipal(namespace, tenantID, clientID, clientSecret)
		default:
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

func (m *AuthModel) connectWithConnectionStringAndPromptCmd(connectionString string) tea.Cmd {
	return func() tea.Msg {
		client, err := azure.NewServiceBusClientFromConnectionString(connectionString)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to connect with connection string: %v", err))
		}

		namespace := client.GetNamespace()

		log.Printf("connected to namespace via connection string: %s", namespace)
		return ConnectionStringConnectedMsg{
			Namespace:        namespace,
			Client:           client,
			ConnectionString: strings.TrimSpace(connectionString),
		}
	}
}

type NamespaceConnectedMsg struct {
	Namespace string
	Client    *azure.ServiceBusClient
}

type ConnectionStringConnectedMsg struct {
	Namespace        string
	Client           *azure.ServiceBusClient
	ConnectionString string
}
