package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/popandcode/asb-tui/internal/azure"
	"github.com/popandcode/asb-tui/internal/styles"
)

const (
	NodeTypeTopic        = "topic"
	NodeTypeQueue        = "queue"
	NodeTypeSubscription = "subscription"
	NodeTypeMessages     = "messages"
)

type TreeNode struct {
	ID          string
	Name        string
	Type        string
	Children    []*TreeNode
	IsExpanded  bool
	IsLoading   bool
	HasChildren bool
	Depth       int
}

type NamespaceModel struct {
	namespace         string
	client            *azure.ServiceBusClient
	rootNodes         []*TreeNode
	subscriptionCache map[string][]*TreeNode
	cacheMutex        sync.RWMutex
	errMsg            string
	selectedIdx       int
	isLoading         bool
	spinner           spinner.Model
	viewport          viewport.Model
	flatList          []*TreeNode
}

type TopicsAndQueuesLoadedMsg struct {
	Nodes []*TreeNode
}

type SubscriptionsLoadedMsg struct {
	TopicID       string
	Subscriptions []*TreeNode
}

type SubscriptionMessagesSelectedMsg struct {
	TopicName        string
	SubscriptionName string
	IsDeadLetter     bool
}

func NewNamespaceModel(namespace string, client *azure.ServiceBusClient) *NamespaceModel {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	vp := viewport.New(0, 0)

	return &NamespaceModel{
		namespace:         namespace,
		client:            client,
		subscriptionCache: make(map[string][]*TreeNode),
		rootNodes:         []*TreeNode{},
		selectedIdx:       0,
		isLoading:         true,
		spinner:           s,
		viewport:          vp,
		flatList:          []*TreeNode{},
	}
}

func (n *NamespaceModel) Init() tea.Cmd {
	return tea.Batch(
		n.spinner.Tick,
		n.loadTopicsAndQueuesCmd(),
	)
}

func (n *NamespaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var spinnerCmd tea.Cmd
	n.spinner, spinnerCmd = n.spinner.Update(msg)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if n.selectedIdx > 0 {
				n.selectedIdx--
			}
			n.ensureSelectedVisible()
		case "down", "j":
			if n.selectedIdx < len(n.flatList)-1 {
				n.selectedIdx++
			}
			n.ensureSelectedVisible()
		case "right", "l", "enter":
			if n.selectedIdx >= 0 && n.selectedIdx < len(n.flatList) {
				node := n.flatList[n.selectedIdx]
				if node.Type == NodeTypeMessages {
					if msg := n.createMessagesSelectedMsg(node); msg != nil {
						return n, func() tea.Msg { return *msg }
					}
				} else {
					cmd := n.handleExpandNode(node)
					n.rebuildFlatList()
					if cmd != nil {
						return n, tea.Batch(n.spinner.Tick, cmd)
					}
				}
			}
		case "left", "h":
			if n.selectedIdx >= 0 && n.selectedIdx < len(n.flatList) {
				node := n.flatList[n.selectedIdx]
				n.collapseNode(node)
				n.rebuildFlatList()
			}
		}

	case tea.WindowSizeMsg:
		n.viewport.Width = msg.Width
		n.viewport.Height = max(msg.Height, 3)
		n.viewport.YOffset = 0

	case TopicsAndQueuesLoadedMsg:
		n.rootNodes = msg.Nodes
		n.selectedIdx = 0
		n.isLoading = false
		n.rebuildFlatList()
		n.viewport.YOffset = 0

	case SubscriptionsLoadedMsg:
		n.cacheMutex.Lock()
		n.subscriptionCache[msg.TopicID] = msg.Subscriptions
		n.cacheMutex.Unlock()

		if node := n.findNodeByID(msg.TopicID); node != nil {
			node.Children = msg.Subscriptions
			for _, child := range node.Children {
				child.Depth = node.Depth + 1
			}
			node.IsLoading = false
		}
		n.rebuildFlatList()

	case ErrorMsg:
		n.errMsg = string(msg)
	}

	if n.isLoading || n.anyNodeLoading() {
		return n, spinnerCmd
	}

	return n, nil
}

func (n *NamespaceModel) anyNodeLoading() bool {
	for _, node := range n.flatList {
		if node.IsLoading {
			return true
		}
	}
	return false
}

func (n *NamespaceModel) ensureSelectedVisible() {
	if n.selectedIdx >= len(n.flatList) {
		n.selectedIdx = len(n.flatList) - 1
	}
	if n.selectedIdx < 0 {
		n.selectedIdx = 0
	}

	if n.viewport.Width == 0 || n.viewport.Height == 0 {
		return
	}

	lineNum := n.selectedIdx

	viewportHeight := n.viewport.Height
	if lineNum < n.viewport.YOffset {
		n.viewport.YOffset = lineNum
	} else if lineNum >= n.viewport.YOffset+viewportHeight {
		n.viewport.YOffset = lineNum - viewportHeight + 1
	}
}

func (n *NamespaceModel) View() string {
	var s strings.Builder

	if n.isLoading {
		s.WriteString(n.spinner.View())
		s.WriteString(" ")
		s.WriteString(styles.Subtle.Render("Loading topics and queues..."))
		s.WriteString("\n")
	} else {
		s.WriteString(styles.Subtle.Render("Namespace: " + n.namespace))
		s.WriteString("\n")

		if n.errMsg != "" {
			s.WriteString(styles.Error.Render("Error: " + n.errMsg))
			s.WriteString("\n")
		}

		s.WriteString(n.ViewContent())

		s.WriteString("\n")
		s.WriteString(styles.Subtle.Render("↑↓/jk: navigate • →/l/enter: expand • ←/h: collapse • ctrl+c: quit"))
		s.WriteString("\n")
	}

	return s.String()
}

func (n *NamespaceModel) ViewContent() string {
	var s strings.Builder

	if n.isLoading {
		s.WriteString(n.spinner.View())
		s.WriteString(" ")
		s.WriteString(styles.Subtle.Render("Loading topics and queues..."))
		return s.String()
	}

	if n.errMsg != "" {
		s.WriteString(styles.Error.Render("Error: " + n.errMsg))
		return s.String()
	}

	if len(n.flatList) == 0 {
		s.WriteString(styles.Subtle.Render("No topics or queues found"))
		return s.String()
	}

	startIdx := 0
	endIdx := len(n.flatList)

	if n.viewport.Height > 0 && len(n.flatList) > n.viewport.Height {
		startIdx = max(n.viewport.YOffset, 0)
		endIdx = min(startIdx+n.viewport.Height, len(n.flatList))
	}

	for i := startIdx; i < endIdx; i++ {
		node := n.flatList[i]
		isSelected := i == n.selectedIdx
		n.drawNodeLine(&s, node, isSelected)
	}

	return strings.TrimSuffix(s.String(), "\n")
}

func (n *NamespaceModel) drawNodeLine(s *strings.Builder, node *TreeNode, isSelected bool) {
	indent := strings.Repeat("  ", node.Depth)

	var icon string
	switch node.Type {
	case NodeTypeTopic:
		if node.IsExpanded {
			icon = "⌄"
		} else {
			icon = "›"
		}
	case NodeTypeQueue:
		icon = "□"
	case NodeTypeSubscription:
		if node.IsExpanded {
			icon = "⌄"
		} else {
			icon = "›"
		}
	case NodeTypeMessages:
		icon = "◉"
	default:
		icon = "○"
	}

	var display string
	if node.Type == NodeTypeSubscription || node.Type == NodeTypeMessages {
		display = fmt.Sprintf("%s  %s %s", indent, icon, node.Name)
	} else {
		display = fmt.Sprintf("%s%s %s", indent, icon, node.Name)
	}

	if node.IsLoading {
		display += " " + n.spinner.View()
	}

	var line string
	if isSelected {
		line = "  " + styles.Selected.Render(display)
	} else {
		line = "  " + display
	}

	s.WriteString(line)
	s.WriteString("\n")
}

func (n *NamespaceModel) rebuildFlatList() {
	n.flatList = n.buildFlatList()
}

func (n *NamespaceModel) buildFlatList() []*TreeNode {
	var flatList []*TreeNode

	var traverse func(*TreeNode)
	traverse = func(node *TreeNode) {
		flatList = append(flatList, node)
		if node.IsExpanded && len(node.Children) > 0 {
			for _, child := range node.Children {
				traverse(child)
			}
		}
	}

	for _, node := range n.rootNodes {
		traverse(node)
	}

	return flatList
}

func (n *NamespaceModel) findNodeByID(id string) *TreeNode {
	var search func(*TreeNode) *TreeNode
	search = func(node *TreeNode) *TreeNode {
		if node.ID == id {
			return node
		}
		for _, child := range node.Children {
			if result := search(child); result != nil {
				return result
			}
		}
		return nil
	}

	for _, node := range n.rootNodes {
		if result := search(node); result != nil {
			return result
		}
	}
	return nil
}

func (n *NamespaceModel) handleExpandNode(node *TreeNode) tea.Cmd {
	if node == nil || !node.HasChildren || node.IsExpanded {
		return nil
	}

	node.IsExpanded = true

	if node.Type == NodeTypeTopic && len(node.Children) == 0 {
		n.cacheMutex.RLock()
		if cached, ok := n.subscriptionCache[node.ID]; ok {
			n.cacheMutex.RUnlock()
			node.Children = cached
			for _, child := range node.Children {
				child.Depth = node.Depth + 1
			}
			return nil
		}
		n.cacheMutex.RUnlock()

		node.IsLoading = true
		return n.loadSubscriptionsCmd(node.ID)
	}

	return nil
}

func (n *NamespaceModel) collapseNode(node *TreeNode) {
	if node == nil || !node.IsExpanded {
		return
	}

	node.IsExpanded = false
}

// createMessagesSelectedMsg parses a messages node ID and creates the appropriate message.
// Node IDs have format: "sub-{topicName}-{subscriptionName}-active" or "sub-{topicName}-{subscriptionName}-dlq"
func (n *NamespaceModel) createMessagesSelectedMsg(node *TreeNode) *SubscriptionMessagesSelectedMsg {
	if node == nil || node.Type != NodeTypeMessages {
		return nil
	}

	id := node.ID
	if !strings.HasPrefix(id, "sub-") {
		return nil
	}

	id = strings.TrimPrefix(id, "sub-")

	var isDeadLetter bool
	var topicAndSub string

	if strings.HasSuffix(id, "-active") {
		isDeadLetter = false
		topicAndSub = strings.TrimSuffix(id, "-active")
	} else if strings.HasSuffix(id, "-dlq") {
		isDeadLetter = true
		topicAndSub = strings.TrimSuffix(id, "-dlq")
	} else {
		return nil
	}

	lastHyphen := strings.LastIndex(topicAndSub, "-")
	if lastHyphen == -1 {
		return nil
	}

	topicName := topicAndSub[:lastHyphen]
	subscriptionName := topicAndSub[lastHyphen+1:]

	return &SubscriptionMessagesSelectedMsg{
		TopicName:        topicName,
		SubscriptionName: subscriptionName,
		IsDeadLetter:     isDeadLetter,
	}
}

func (n *NamespaceModel) loadTopicsAndQueuesCmd() tea.Cmd {
	client := n.client

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultContextTimeout)
		defer cancel()

		topics, err := client.ListTopics(ctx)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to load topics: %v", err))
		}

		queues, err := client.ListQueues(ctx)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to load queues: %v", err))
		}

		var nodes []*TreeNode

		for _, topic := range topics {
			nodes = append(nodes, &TreeNode{
				ID:          fmt.Sprintf("topic-%s", topic),
				Name:        topic,
				Type:        NodeTypeTopic,
				HasChildren: true,
				Children:    []*TreeNode{},
				Depth:       0,
			})
		}

		for _, queue := range queues {
			nodes = append(nodes, &TreeNode{
				ID:          fmt.Sprintf("queue-%s", queue),
				Name:        queue,
				Type:        NodeTypeQueue,
				HasChildren: false,
				Children:    []*TreeNode{},
				Depth:       0,
			})
		}

		return TopicsAndQueuesLoadedMsg{Nodes: nodes}
	}
}

func (n *NamespaceModel) loadSubscriptionsCmd(topicID string) tea.Cmd {
	client := n.client

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultContextTimeout)
		defer cancel()

		topicName := strings.TrimPrefix(topicID, "topic-")

		subscriptions, err := client.ListSubscriptions(ctx, topicName)
		if err != nil {
			return ErrorMsg(fmt.Sprintf("failed to load subscriptions for %s: %v", topicName, err))
		}

		var nodes []*TreeNode
		for _, sub := range subscriptions {
			subNode := &TreeNode{
				ID:          fmt.Sprintf("sub-%s-%s", topicName, sub),
				Name:        sub,
				Type:        NodeTypeSubscription,
				HasChildren: true,
				Children: []*TreeNode{
					{
						ID:          fmt.Sprintf("sub-%s-%s-active", topicName, sub),
						Name:        "Active Messages",
						Type:        NodeTypeMessages,
						HasChildren: false,
						Children:    []*TreeNode{},
						Depth:       2,
					},
					{
						ID:          fmt.Sprintf("sub-%s-%s-dlq", topicName, sub),
						Name:        "DLQ Messages",
						Type:        NodeTypeMessages,
						HasChildren: false,
						Children:    []*TreeNode{},
						Depth:       2,
					},
				},
				Depth: 1,
			}
			nodes = append(nodes, subNode)
		}

		return SubscriptionsLoadedMsg{
			TopicID:       topicID,
			Subscriptions: nodes,
		}
	}
}

const defaultContextTimeout = 30 * time.Second
