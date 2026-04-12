package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus"
	"github.com/google/uuid"
)

const (
	interactiveBrowserRedirectURL = "http://localhost:8080"
	defaultContextTimeout         = 30 * time.Second
	azureAPIVersion               = "2020-01-01"
	azureManagementURL            = "https://management.azure.com"
	azureManagementScope          = "https://management.azure.com/.default"
	emulatorAdminPort             = 5300
)

// httpOnlyTransport forces HTTP (no TLS) for the emulator admin API.
// The emulator exposes its management endpoint over plain HTTP on port 5300,
// but the SDK defaults to HTTPS. This is the official workaround from
// https://github.com/Azure/azure-service-bus-emulator-installer/blob/main/Sample-Code-Snippets/Go
type httpOnlyTransport struct {
	inner *http.Client
}

func (t *httpOnlyTransport) Do(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *clone.URL
	u.Scheme = "http"
	clone.URL = &u
	return t.inner.Do(clone)
}

type ServiceBusClient struct {
	client      *azservicebus.Client
	adminClient *admin.Client
	namespace   string
	isEmulator  bool
}

func (sbc *ServiceBusClient) GetNamespace() string {
	return sbc.namespace
}

type NamespaceInfo struct {
	Name           string
	FullyQualified string
	Subscription   string
	ResourceGroup  string
	Location       string
}

// MessageInfo I choose "MessageInfo" instead of "Message" to avoid confusion/conflict with azservicebus package
type MessageInfo struct {
	MessageID      string
	SessionID      string
	CorrelationID  string
	SequenceNumber int64
	Subject        string
	Body           string
	EnqueuedTime   time.Time
	ContentType    string
	Properties     map[string]any
}

type PeekSessionKind int

const (
	PeekSessionNone PeekSessionKind = iota
	PeekSessionByID
)

type PeekSessionOptions struct {
	Kind      PeekSessionKind
	SessionID string
}

func (o PeekSessionOptions) Validate() error {
	switch o.Kind {
	case PeekSessionNone:
		if o.SessionID != "" {
			return fmt.Errorf("session id must be empty for mode %d", o.Kind)
		}
		return nil
	case PeekSessionByID:
		if strings.TrimSpace(o.SessionID) == "" {
			return fmt.Errorf("session id is required")
		}
		return nil
	default:
		return fmt.Errorf("unknown session mode: %d", o.Kind)
	}
}

func GetAzureCliAuthenticatedUser() (string, bool) {
	_, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return "", false
	}

	username := getAzureCLIUsername()
	return username, true
}

func getAzureCLIUsername() string {
	cmd := exec.Command("az", "account", "show", "--query", "user.name", "--output", "tsv")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func NewServiceBusClientFromAzureCLI(namespace string) (*ServiceBusClient, error) {
	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure CLI credential: %w", err)
	}

	return newServiceBusClientWithCredential(cred, namespace)
}

func NewServiceBusClientFromInteractiveBrowser(namespace string) (*ServiceBusClient, error) {
	cred, err := newInteractiveBrowserCredential()
	if err != nil {
		return nil, fmt.Errorf("failed to create browser credential: %w", err)
	}
	return newServiceBusClientWithCredential(cred, namespace)
}

func NewServiceBusClientFromConnectionString(connectionString string) (*ServiceBusClient, error) {
	client, err := azservicebus.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create service bus client from connection string: %w", err)
	}

	emulator := isEmulatorConnectionString(connectionString)

	var adminOpts *admin.ClientOptions
	adminConnStr := connectionString

	if emulator {
		adminConnStr = injectAdminPort(connectionString, emulatorAdminPort)
		adminOpts = &admin.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: &httpOnlyTransport{inner: http.DefaultClient},
			},
		}
	}

	adminClient, err := admin.NewClientFromConnectionString(adminConnStr, adminOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create admin client from connection string: %w", err)
	}

	namespace := parseNamespaceFromConnectionString(connectionString)

	return &ServiceBusClient{
		client:      client,
		adminClient: adminClient,
		namespace:   namespace,
		isEmulator:  emulator,
	}, nil
}

func parseNamespaceFromConnectionString(connectionString string) string {
	for part := range strings.SplitSeq(connectionString, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "endpoint=") {
			endpoint := part[len("endpoint="):]
			endpoint = strings.TrimPrefix(endpoint, "sb://")
			endpoint = strings.TrimSuffix(endpoint, "/")
			// Strip port if present (e.g. "localhost:5672" -> "localhost")
			if idx := strings.Index(endpoint, ":"); idx > 0 {
				endpoint = endpoint[:idx]
			}
			if idx := strings.Index(endpoint, "."); idx > 0 {
				return endpoint[:idx]
			}
			return endpoint
		}
	}
	return ""
}

func isEmulatorConnectionString(connStr string) bool {
	for part := range strings.SplitSeq(connStr, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "UseDevelopmentEmulator") {
			return strings.EqualFold(kv[1], "true")
		}
	}
	return false
}

// injectAdminPort rewrites the Endpoint in a connection string to use the
// given port. The emulator admin API listens on a different port (5300) than
// the AMQP broker (5672).
func injectAdminPort(connStr string, port int) string {
	var parts []string
	for part := range strings.SplitSeq(connStr, ";") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "endpoint=") {
			endpoint := trimmed[len("endpoint="):]
			endpoint = strings.TrimSuffix(endpoint, "/")
			// Strip existing port if any
			scheme := ""
			if strings.HasPrefix(endpoint, "sb://") {
				scheme = "sb://"
				endpoint = endpoint[len("sb://"):]
			}
			if idx := strings.Index(endpoint, ":"); idx > 0 {
				endpoint = endpoint[:idx]
			}
			parts = append(parts, fmt.Sprintf("Endpoint=%s%s:%d", scheme, endpoint, port))
		} else {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, ";")
}

func newInteractiveBrowserCredential() (azcore.TokenCredential, error) {
	opts := &azidentity.InteractiveBrowserCredentialOptions{
		RedirectURL: interactiveBrowserRedirectURL,
	}
	cred, err := azidentity.NewInteractiveBrowserCredential(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create interactive browser credential: %w", err)
	}
	return cred, nil
}

func newServiceBusClientWithCredential(cred azcore.TokenCredential, namespace string) (*ServiceBusClient, error) {
	fqdn := fmt.Sprintf("%s.servicebus.windows.net", namespace)
	log.Printf("[newServiceBusClientWithCredential] namespace=%q fqdn=%q", namespace, fqdn)

	client, err := azservicebus.NewClient(fqdn, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create service bus client: %w", err)
	}

	adminClient, err := admin.NewClient(fqdn, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create admin client: %w", err)
	}

	return &ServiceBusClient{
		client:      client,
		adminClient: adminClient,
		namespace:   namespace,
	}, nil
}

func GetNamespacesForAzureCLI(ctx context.Context) ([]NamespaceInfo, error) {
	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure CLI credential: %w", err)
	}
	return getNamespaces(ctx, cred)
}

func GetNamespacesForInteractiveBrowser(ctx context.Context) ([]NamespaceInfo, error) {
	cred, err := newInteractiveBrowserCredential()
	if err != nil {
		return nil, err
	}
	return getNamespaces(ctx, cred)
}

func GetNamespacesForServicePrincipal(ctx context.Context, tenantID, clientID, clientSecret string) ([]NamespaceInfo, error) {
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create service principal credential: %w", err)
	}
	return getNamespaces(ctx, cred)
}

func NewServiceBusClientFromServicePrincipal(namespace, tenantID, clientID, clientSecret string) (*ServiceBusClient, error) {
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create service principal credential: %w", err)
	}
	return newServiceBusClientWithCredential(cred, namespace)
}

func getNamespaces(ctx context.Context, cred azcore.TokenCredential) ([]NamespaceInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultContextTimeout)
	defer cancel()

	subscriptions, err := listSubscriptions(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to list subscriptions: %w", err)
	}

	var namespaces []NamespaceInfo

	for _, subID := range subscriptions {
		nsClient, err := armservicebus.NewNamespacesClient(subID, cred, nil)
		if err != nil {
			continue
		}

		nsPager := nsClient.NewListPager(nil)
		for nsPager.More() {
			nsPage, err := nsPager.NextPage(ctx)
			if err != nil {
				break
			}

			for _, ns := range nsPage.Value {
				if ns.Name == nil {
					continue
				}

				location := ""
				if ns.Location != nil {
					location = *ns.Location
				}

				resourceGroup := "Unknown"
				if ns.ID != nil {
					resourceGroup = extractResourceGroup(*ns.ID)
				}

				fqdn := fmt.Sprintf("%s.servicebus.windows.net", *ns.Name)

				namespaces = append(namespaces, NamespaceInfo{
					Name:           *ns.Name,
					FullyQualified: fqdn,
					Subscription:   subID,
					ResourceGroup:  resourceGroup,
					Location:       location,
				})
			}
		}
	}

	if len(namespaces) == 0 {
		return nil, fmt.Errorf("no service bus namespaces found in any accessible subscriptions")
	}

	return namespaces, nil
}

func listSubscriptions(ctx context.Context, cred azcore.TokenCredential) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultContextTimeout)
	defer cancel()

	bearerPolicy := runtime.NewBearerTokenPolicy(
		cred,
		[]string{azureManagementScope},
		nil,
	)

	pl := runtime.NewPipeline(
		"asb-tui",
		"",
		runtime.PipelineOptions{
			PerRetry: []policy.Policy{bearerPolicy},
		},
		nil,
	)

	req, err := runtime.NewRequest(
		ctx,
		http.MethodGet,
		azureManagementURL+"/subscriptions?api-version="+azureAPIVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := pl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call subscriptions API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("subscriptions API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Value []struct {
			SubscriptionID string `json:"subscriptionId"`
		} `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode subscriptions: %w", err)
	}

	var subIDs []string
	for _, sub := range result.Value {
		if sub.SubscriptionID != "" {
			subIDs = append(subIDs, sub.SubscriptionID)
		}
	}

	if len(subIDs) == 0 {
		return nil, fmt.Errorf("no subscriptions found")
	}

	return subIDs, nil
}

// extractResourceGroup extracts the resource group name from an Azure resource ID.
// ID format: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/...
func extractResourceGroup(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "resourceGroups" {
			return parts[i+1]
		}
	}
	return "Unknown"
}

func (sbc *ServiceBusClient) ListTopics(ctx context.Context) ([]string, error) {
	pager := sbc.adminClient.NewListTopicsPager(nil)
	var topics []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			log.Printf("[ListTopics] error fetching page: %v", err)
			return nil, fmt.Errorf("failed to list topics: %w", err)
		}

		log.Printf("[ListTopics] got page with %d topics", len(page.Topics))
		for _, topic := range page.Topics {
			topics = append(topics, topic.TopicName)
		}
	}

	log.Printf("[ListTopics] total topics found: %d, pager.More() returned false", len(topics))
	return topics, nil
}

func (sbc *ServiceBusClient) ListQueues(ctx context.Context) ([]string, error) {
	pager := sbc.adminClient.NewListQueuesPager(nil)
	var queues []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			log.Printf("[ListQueues] error fetching page: %v", err)
			return nil, fmt.Errorf("failed to list queues: %w", err)
		}

		log.Printf("[ListQueues] got page with %d queues", len(page.Queues))
		for _, queue := range page.Queues {
			queues = append(queues, queue.QueueName)
		}
	}

	log.Printf("[ListQueues] total queues found: %d, pager.More() returned false", len(queues))
	return queues, nil
}

func (sbc *ServiceBusClient) ListSubscriptions(ctx context.Context, topicName string) ([]string, error) {
	pager := sbc.adminClient.NewListSubscriptionsPager(topicName, nil)
	var subscriptions []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subscriptions for topic %s: %w", topicName, err)
		}

		for _, sub := range page.Subscriptions {
			subscriptions = append(subscriptions, sub.SubscriptionName)
		}
	}

	return subscriptions, nil
}

func (sbc *ServiceBusClient) EntityRequiresSession(ctx context.Context, entityName string) (bool, error) {
	if parts := strings.SplitN(entityName, "/", 2); len(parts) == 2 {
		resp, err := sbc.adminClient.GetSubscription(ctx, parts[0], parts[1], nil)
		if err != nil {
			return false, fmt.Errorf("failed to get subscription properties: %w", err)
		}
		if resp == nil || resp.RequiresSession == nil {
			return false, nil
		}
		return *resp.RequiresSession, nil
	}

	resp, err := sbc.adminClient.GetQueue(ctx, entityName, nil)
	if err != nil {
		return false, fmt.Errorf("failed to get queue properties: %w", err)
	}
	if resp == nil || resp.RequiresSession == nil {
		return false, nil
	}
	return *resp.RequiresSession, nil
}

func (sbc *ServiceBusClient) PeekMessages(ctx context.Context, entityName string, isDeadLetter bool, maxMessages int, fromSequenceNumber *int64, sessionOpts PeekSessionOptions) ([]MessageInfo, error) {
	if err := sessionOpts.Validate(); err != nil {
		return nil, err
	}

	var receiver interface {
		Close(context.Context) error
		PeekMessages(context.Context, int, *azservicebus.PeekMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	}
	var err error

	var opts *azservicebus.ReceiverOptions
	if isDeadLetter {
		opts = &azservicebus.ReceiverOptions{
			SubQueue: azservicebus.SubQueueDeadLetter,
		}
	}

	acceptCtx, acceptCancel := context.WithTimeout(ctx, 10*time.Second)
	defer acceptCancel()

	// entityName format: "topic/subscription" (contains /) or "queue" (no /)
	if parts := strings.SplitN(entityName, "/", 2); len(parts) == 2 {
		subscriptionName := parts[1]
		if isDeadLetter && sessionOpts.Kind == PeekSessionByID {
			subscriptionName += "/$DeadLetterQueue"
		}

		switch sessionOpts.Kind {
		case PeekSessionNone:
			receiver, err = sbc.client.NewReceiverForSubscription(parts[0], parts[1], opts)
		case PeekSessionByID:
			receiver, err = sbc.client.AcceptSessionForSubscription(acceptCtx, parts[0], subscriptionName, sessionOpts.SessionID, nil)
		}
	} else {
		queueName := entityName
		if isDeadLetter && sessionOpts.Kind == PeekSessionByID {
			queueName += "/$DeadLetterQueue"
		}

		switch sessionOpts.Kind {
		case PeekSessionNone:
			receiver, err = sbc.client.NewReceiverForQueue(entityName, opts)
		case PeekSessionByID:
			receiver, err = sbc.client.AcceptSessionForQueue(acceptCtx, queueName, sessionOpts.SessionID, nil)
		}
	}

	if err != nil {
		if sessionOpts.Kind == PeekSessionByID {
			var sbErr *azservicebus.Error
			if (errors.As(err, &sbErr) && sbErr.Code == azservicebus.CodeTimeout) ||
				errors.Is(err, context.DeadlineExceeded) {
				return []MessageInfo{}, nil
			}
		}
		return nil, fmt.Errorf("failed to create receiver: %w", err)
	}
	defer receiver.Close(ctx)

	var peekOpts *azservicebus.PeekMessagesOptions
	if fromSequenceNumber != nil {
		peekOpts = &azservicebus.PeekMessagesOptions{
			FromSequenceNumber: fromSequenceNumber,
		}
	}

	peekedMessages, err := receiver.PeekMessages(ctx, maxMessages, peekOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to peek messages: %w", err)
	}

	var result []MessageInfo
	for _, msg := range peekedMessages {
		pm := MessageInfo{
			SequenceNumber: *msg.SequenceNumber,
			MessageID:      msg.MessageID,
			Properties:     msg.ApplicationProperties,
		}

		if msg.Subject != nil {
			pm.Subject = *msg.Subject
		}

		if msg.ContentType != nil {
			pm.ContentType = *msg.ContentType
		}

		if msg.CorrelationID != nil {
			pm.CorrelationID = *msg.CorrelationID
		}

		if msg.SessionID != nil {
			pm.SessionID = *msg.SessionID
		}

		if msg.EnqueuedTime != nil {
			pm.EnqueuedTime = *msg.EnqueuedTime
		}

		if msg.Body != nil {
			pm.Body = string(msg.Body)
		}

		result = append(result, pm)
	}

	return result, nil
}

func (sbc *ServiceBusClient) GetMessageCount(ctx context.Context, entityName string, isDeadLetter bool) (count int64, err error) {
	// The emulator returns incomplete runtime properties (nil MessageCount etc.)
	// which causes the SDK to panic with a nil pointer dereference. Recover
	// gracefully — message count is non-critical (used for page indicators only).
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[GetMessageCount] recovered from panic: %v", r)
			count = -1
			err = nil
		}
	}()

	// entityName format: "topic/subscription" (contains /) or "queue" (no /)
	if parts := strings.SplitN(entityName, "/", 2); len(parts) == 2 {
		resp, err := sbc.adminClient.GetSubscriptionRuntimeProperties(ctx, parts[0], parts[1], nil)
		if err != nil {
			return -1, err
		}
		if isDeadLetter {
			return int64(resp.DeadLetterMessageCount), nil
		}
		return int64(resp.ActiveMessageCount), nil
	}

	resp, err := sbc.adminClient.GetQueueRuntimeProperties(ctx, entityName, nil)
	if err != nil {
		return -1, err
	}
	if isDeadLetter {
		return int64(resp.DeadLetterMessageCount), nil
	}
	return int64(resp.ActiveMessageCount), nil
}

type SendProgress struct {
	Sent  int
	Total int
	Err   error
	Done  bool
}

func (sbc *ServiceBusClient) SendMessages(ctx context.Context, destination string, messages []MessageInfo, preserveIDs bool) <-chan SendProgress {
	ch := make(chan SendProgress, 1)
	total := len(messages)

	go func() {
		defer close(ch)

		sender, err := sbc.client.NewSender(destination, nil)
		if err != nil {
			ch <- SendProgress{Total: total, Err: fmt.Errorf("failed to create sender: %w", err), Done: true}
			return
		}
		defer sender.Close(ctx)

		for i, msg := range messages {
			sbMsg := &azservicebus.Message{
				Body:                  []byte(msg.Body),
				ApplicationProperties: msg.Properties,
			}

			if msg.Subject != "" {
				s := msg.Subject
				sbMsg.Subject = &s
			}
			if msg.ContentType != "" {
				ct := msg.ContentType
				sbMsg.ContentType = &ct
			}
			if msg.CorrelationID != "" {
				cid := msg.CorrelationID
				sbMsg.CorrelationID = &cid
			}
			if msg.SessionID != "" {
				sid := msg.SessionID
				sbMsg.SessionID = &sid
			}

			if preserveIDs {
				id := msg.MessageID
				sbMsg.MessageID = &id
			} else {
				id := uuid.NewString()
				sbMsg.MessageID = &id
			}

			if err := sender.SendMessage(ctx, sbMsg, nil); err != nil {
				ch <- SendProgress{Sent: i, Total: total, Err: fmt.Errorf("failed to send message %d/%d: %w", i+1, total, err), Done: true}
				return
			}

			ch <- SendProgress{Sent: i + 1, Total: total}
		}

		ch <- SendProgress{Sent: total, Total: total, Done: true}
	}()

	return ch
}

func (sbc *ServiceBusClient) SendSingleMessage(ctx context.Context, destination string, message MessageInfo, generateID bool) error {
	sender, err := sbc.client.NewSender(destination, nil)
	if err != nil {
		return fmt.Errorf("failed to create sender: %w", err)
	}
	defer sender.Close(ctx)

	sbMsg := &azservicebus.Message{
		Body:                  []byte(message.Body),
		ApplicationProperties: message.Properties,
	}

	if message.Subject != "" {
		s := message.Subject
		sbMsg.Subject = &s
	}
	if message.ContentType != "" {
		ct := message.ContentType
		sbMsg.ContentType = &ct
	}
	if message.CorrelationID != "" {
		cid := message.CorrelationID
		sbMsg.CorrelationID = &cid
	}
	if message.SessionID != "" {
		sid := message.SessionID
		sbMsg.SessionID = &sid
	}

	if generateID || strings.TrimSpace(message.MessageID) == "" {
		id := uuid.NewString()
		sbMsg.MessageID = &id
	} else {
		id := message.MessageID
		sbMsg.MessageID = &id
	}

	if err := sender.SendMessage(ctx, sbMsg, nil); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}
