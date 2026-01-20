package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

type AppState int

const (
	StateAuth AppState = iota
	StateExplorer
)

type RootModel struct {
	state         AppState
	authModel     *AuthModel
	explorerModel *ExplorerModel
	windowWidth   int
	windowHeight  int
}

func NewRootModel() *RootModel {
	return &RootModel{
		state:     StateAuth,
		authModel: NewAuthModel(),
	}
}

func (m *RootModel) Init() tea.Cmd {
	return m.authModel.Init()
}

func (m *RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.windowWidth = wsMsg.Width
		m.windowHeight = wsMsg.Height
	}

	switch msg := msg.(type) {
	case NamespaceConnectedMsg:
		m.explorerModel = NewExplorerModel(msg.Namespace, msg.Client)
		m.state = StateExplorer
		initCmd := m.explorerModel.Init()
		if m.windowWidth > 0 && m.windowHeight > 0 {
			wsMsg := tea.WindowSizeMsg{Width: m.windowWidth, Height: m.windowHeight}
			_, sizeCmd := m.explorerModel.Update(wsMsg)
			return m, tea.Batch(initCmd, sizeCmd)
		}
		return m, initCmd

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	switch m.state {
	case StateAuth:
		var cmd tea.Cmd
		var authModel tea.Model
		authModel, cmd = m.authModel.Update(msg)
		m.authModel = authModel.(*AuthModel)
		return m, cmd

	case StateExplorer:
		var cmd tea.Cmd
		var explorerModel tea.Model
		explorerModel, cmd = m.explorerModel.Update(msg)
		m.explorerModel = explorerModel.(*ExplorerModel)
		return m, cmd
	}

	return m, nil
}

func (m *RootModel) View() string {
	switch m.state {
	case StateAuth:
		return m.authModel.View()
	case StateExplorer:
		return m.explorerModel.View()
	}
	return ""
}
