package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MonsieurTib/service-bus-tui/internal/azure"
	"github.com/MonsieurTib/service-bus-tui/internal/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/reflow/truncate"
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
	EntityName  string // e.g. "topic", "topic/subscription", or "queue"
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
	manualMode        bool
	inManualEntry     bool
	entityInput       textinput.Model
}

type TopicsAndQueuesLoadedMsg struct {
	Nodes []*TreeNode
}

// ManualModeMsg means the connection has no MANAGE claim, so entities have
// to be added by name instead of enumerated.
type ManualModeMsg struct {
	Entities []string
}

type SubscriptionsLoadedMsg struct {
	TopicID       string
	Subscriptions []*TreeNode
}

type MessagesSelectedMsg struct {
	EntityName   string // "topic/subscription" or "queue"
	IsDeadLetter bool
}

func NewNamespaceModel(namespace string, client *azure.ServiceBusClient) *NamespaceModel {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	vp := viewport.New(0, 0)

	entityInput := textinput.New()
	entityInput.Placeholder = "e.g. orders"
	entityInput.Width = 40

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
		entityInput:       entityInput,
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
		if n.inManualEntry {
			return n.updateManualEntry(msg)
		}

		switch msg.String() {
		case "a":
			if n.manualMode {
				n.inManualEntry = true
				n.entityInput.SetValue("")
				n.entityInput.Focus()
				return n, tea.Batch(spinnerCmd, textinput.Blink)
			}
		case "S":
			if n.selectedIdx >= 0 && n.selectedIdx < len(n.flatList) {
				node := n.flatList[n.selectedIdx]
				if sendMsg := n.createSendRequestedMsg(node); sendMsg != nil {
					return n, func() tea.Msg { return *sendMsg }
				}
			}
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
		case "ctrl+r":
			n.isLoading = true
			n.subscriptionCache = make(map[string][]*TreeNode)
			return n, tea.Batch(n.spinner.Tick, n.loadTopicsAndQueuesCmd())
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

	case ManualModeMsg:
		n.manualMode = true
		n.isLoading = false
		n.errMsg = ""

		var seededEntity string
		seededCount := 0
		for _, entity := range msg.Entities {
			if entityName, added := n.addManualEntity(entity); added {
				seededEntity = entityName
				seededCount++
			}
		}

		n.rebuildFlatList()
		n.viewport.YOffset = 0

		// Auto-load the seeded entity so the user doesn't have to dig for it.
		if seededCount == 1 {
			n.selectedIdx = max(len(n.flatList)-2, 0)
			n.ensureSelectedVisible()
			return n, func() tea.Msg {
				return MessagesSelectedMsg{EntityName: seededEntity, IsDeadLetter: false}
			}
		}
		n.selectedIdx = 0

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
		n.isLoading = false
		n.errMsg = string(msg)
		for _, node := range n.flatList {
			node.IsLoading = false
		}
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

func (n *NamespaceModel) HandleMouse(msg tea.MouseMsg) {
	if n.isLoading || len(n.flatList) == 0 {
		return
	}

	switch msg.Type {
	case tea.MouseWheelUp:
		if n.selectedIdx > 0 {
			n.selectedIdx--
			n.ensureSelectedVisible()
		}
	case tea.MouseWheelDown:
		if n.selectedIdx < len(n.flatList)-1 {
			n.selectedIdx++
			n.ensureSelectedVisible()
		}
	}
}

func (n *NamespaceModel) SelectedPath(namespace string) string {
	if namespace == "" {
		return ""
	}

	if n.selectedIdx < 0 || n.selectedIdx >= len(n.flatList) {
		return ""
	}

	node := n.flatList[n.selectedIdx]
	if node == nil {
		return ""
	}

	base := ""
	if node.EntityName != "" {
		base = namespace + "/" + node.EntityName
	} else if node.Name != "" {
		base = namespace + "/" + node.Name
	}

	if base == "" || node.Type != NodeTypeMessages {
		return base
	}

	if strings.HasSuffix(node.ID, "-active") {
		return base + "/active-messages"
	}
	if strings.HasSuffix(node.ID, "-dlq") {
		return base + "/dlq"
	}

	return base
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
		s.WriteString(styles.Subtle.Render("↑↓/jk: navigate • →/l/enter: expand • ←/h: collapse • S: send • ctrl+c: quit"))
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

	if n.inManualEntry {
		inputWidth := max(n.viewport.Width-4, 10)
		n.entityInput.Width = inputWidth

		s.WriteString(styles.Label.Render("Add entity"))
		s.WriteString("\n")
		s.WriteString(renderTextInputField(n.entityInput.View(), true, inputWidth))
		s.WriteString("\n")
		s.WriteString(styles.Subtle.Render("queue or topic/sub"))
		s.WriteString("\n")
		s.WriteString(styles.Subtle.Render("enter add, esc cancel"))
		s.WriteString("\n\n")
	}

	if len(n.flatList) == 0 {
		if n.inManualEntry {
			return s.String()
		}
		if n.manualMode {
			s.WriteString(styles.Subtle.Render("No MANAGE permission."))
			s.WriteString("\n")
			s.WriteString(styles.Subtle.Render("Press 'a' to add an entity."))
		} else {
			s.WriteString(styles.Subtle.Render("No topics or queues found"))
		}
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
		if node.IsExpanded {
			icon = "⌄"
		} else {
			icon = "›"
		}

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
	if node.Type == NodeTypeSubscription || node.Type == NodeTypeMessages || (node.Type == NodeTypeQueue && node.Depth > 0) {
		display = fmt.Sprintf("%s  %s %s", indent, icon, node.Name)
	} else {
		display = fmt.Sprintf("%s%s %s", indent, icon, node.Name)
	}

	if node.IsLoading {
		display += " " + n.spinner.View()
	}

	linePrefix := "  "

	if maxWidth := n.viewport.Width - len(linePrefix); maxWidth > 0 {
		display = truncate.StringWithTail(display, uint(maxWidth), "…")
	}

	var line string
	if isSelected {
		line = linePrefix + styles.Selected.Render(display)
	} else {
		line = linePrefix + display
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

func (n *NamespaceModel) updateManualEntry(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(n.entityInput.Value())
		n.inManualEntry = false
		n.entityInput.Blur()
		n.entityInput.SetValue("")
		if name == "" {
			return n, nil
		}
		entityName, added := n.addManualEntity(name)
		if !added {
			return n, nil
		}
		n.rebuildFlatList()
		n.selectedIdx = max(len(n.flatList)-2, 0)
		n.ensureSelectedVisible()
		return n, func() tea.Msg {
			return MessagesSelectedMsg{EntityName: entityName, IsDeadLetter: false}
		}
	case "esc":
		n.inManualEntry = false
		n.entityInput.Blur()
		n.entityInput.SetValue("")
		return n, nil
	default:
		var cmd tea.Cmd
		n.entityInput, cmd = n.entityInput.Update(msg)
		return n, cmd
	}
}

// addManualEntity adds name as a root node. Returns its EntityName and true,
// or ("", false) if blank or already added.
func (n *NamespaceModel) addManualEntity(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}

	node := newManualEntityNode(name)

	for _, existing := range n.rootNodes {
		if existing.EntityName == node.EntityName {
			return "", false
		}
	}

	n.rootNodes = append(n.rootNodes, node)
	return node.EntityName, true
}

// RemoveManualEntityByName removes a manually-added entity, e.g. once its
// peek fails and we know it doesn't exist.
func (n *NamespaceModel) RemoveManualEntityByName(entityName string) {
	if !n.manualMode {
		return
	}

	for i, root := range n.rootNodes {
		if root.EntityName == entityName {
			n.rootNodes = append(n.rootNodes[:i], n.rootNodes[i+1:]...)
			n.rebuildFlatList()
			if n.selectedIdx >= len(n.flatList) {
				n.selectedIdx = max(len(n.flatList)-1, 0)
			}
			n.ensureSelectedVisible()
			return
		}
	}
}

// createMessagesSelectedMsg reads entity metadata directly from the node.
func (n *NamespaceModel) createMessagesSelectedMsg(node *TreeNode) *MessagesSelectedMsg {
	if node == nil || node.Type != NodeTypeMessages {
		return nil
	}

	if node.EntityName == "" {
		return nil
	}

	isDeadLetter := strings.HasSuffix(node.ID, "-dlq")

	return &MessagesSelectedMsg{
		EntityName:   node.EntityName,
		IsDeadLetter: isDeadLetter,
	}
}

func (n *NamespaceModel) createSendRequestedMsg(node *TreeNode) *SendRequestedMsg {
	if node == nil {
		return nil
	}

	if node.Type != NodeTypeTopic && node.Type != NodeTypeQueue {
		return nil
	}

	destination := node.EntityName
	if destination == "" {
		destination = node.Name
	}
	if destination == "" {
		return nil
	}

	return &SendRequestedMsg{Destination: destination}
}

func (n *NamespaceModel) loadTopicsAndQueuesCmd() tea.Cmd {
	client := n.client

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultContextTimeout)
		defer cancel()

		topics, err := client.ListTopics(ctx)
		if err != nil {
			if azure.IsAuthorizationError(err) {
				return ManualModeMsg{Entities: manualSeedEntities(client.GetEntityPath())}
			}
			return ErrorMsg(fmt.Sprintf("failed to load topics: %v", err))
		}

		queues, err := client.ListQueues(ctx)
		if err != nil {
			if azure.IsAuthorizationError(err) {
				return ManualModeMsg{Entities: manualSeedEntities(client.GetEntityPath())}
			}
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
			nodes = append(nodes, newQueueNode(queue))
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
			nodes = append(nodes, newSubscriptionNode(topicName, sub))
		}

		return SubscriptionsLoadedMsg{
			TopicID:       topicID,
			Subscriptions: nodes,
		}
	}
}

// manualSeedEntities seeds manual mode from the connection string's EntityPath.
func manualSeedEntities(entityPath string) []string {
	if entityPath == "" {
		return nil
	}
	return []string{entityPath}
}

// newMessageNodes builds the Active/DLQ Messages leaf pair.
func newMessageNodes(idPrefix, entityName string, depth int) []*TreeNode {
	return []*TreeNode{
		{
			ID:          idPrefix + "-active",
			Name:        "Active Messages",
			Type:        NodeTypeMessages,
			EntityName:  entityName,
			HasChildren: false,
			Children:    []*TreeNode{},
			Depth:       depth,
		},
		{
			ID:          idPrefix + "-dlq",
			Name:        "DLQ Messages",
			Type:        NodeTypeMessages,
			EntityName:  entityName,
			HasChildren: false,
			Children:    []*TreeNode{},
			Depth:       depth,
		},
	}
}

func newQueueNode(name string) *TreeNode {
	id := fmt.Sprintf("queue-%s", name)
	return &TreeNode{
		ID:          id,
		Name:        name,
		Type:        NodeTypeQueue,
		HasChildren: true,
		EntityName:  name,
		Children:    newMessageNodes(id, name, 1),
		Depth:       0,
	}
}

func newSubscriptionNode(topicName, subName string) *TreeNode {
	entityName := fmt.Sprintf("%s/%s", topicName, subName)
	id := fmt.Sprintf("sub-%s-%s", topicName, subName)
	return &TreeNode{
		ID:          id,
		Name:        subName,
		Type:        NodeTypeSubscription,
		EntityName:  entityName,
		HasChildren: true,
		Children:    newMessageNodes(id, entityName, 2),
		Depth:       1,
	}
}

// newManualEntityNode builds a queue node, or a subscription node if name is
// "topic/subscription". Bare topics aren't supported since we can't
// enumerate their subscriptions without MANAGE.
func newManualEntityNode(name string) *TreeNode {
	var node *TreeNode
	if topic, sub, ok := splitTopicSubscription(name); ok {
		node = newSubscriptionNode(topic, sub)
		node.Depth = 0
		for _, child := range node.Children {
			child.Depth = 1
		}
	} else {
		node = newQueueNode(name)
	}
	// Expand so Active/DLQ show up right away.
	node.IsExpanded = true
	return node
}

func splitTopicSubscription(name string) (topic, sub string, ok bool) {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	topic = strings.TrimSpace(parts[0])
	sub = strings.TrimSpace(parts[1])
	if topic == "" || sub == "" {
		return "", "", false
	}
	return topic, sub, true
}

const defaultContextTimeout = 30 * time.Second
