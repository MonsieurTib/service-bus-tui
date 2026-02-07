package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
)

const (
	interactiveBrowserRedirectURL = "http://localhost:8080"
	defaultContextTimeout         = 30 * time.Second
	azureAPIVersion               = "2020-01-01"
	azureManagementURL            = "https://management.azure.com"
	azureManagementScope          = "https://management.azure.com/.default"
)

type ServiceBusClient struct {
	client      *azservicebus.Client
	adminClient *admin.Client
	namespace   string
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
	SequenceNumber int64
	Subject        string
	Body           string
	EnqueuedTime   time.Time
	ContentType    string
	Properties     map[string]any
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

	adminClient, err := admin.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create admin client from connection string: %w", err)
	}

	namespace := parseNamespaceFromConnectionString(connectionString)

	return &ServiceBusClient{
		client:      client,
		adminClient: adminClient,
		namespace:   namespace,
	}, nil
}

func parseNamespaceFromConnectionString(connectionString string) string {
	for part := range strings.SplitSeq(connectionString, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "endpoint=") {
			endpoint := part[len("endpoint="):]
			endpoint = strings.TrimPrefix(endpoint, "sb://")
			endpoint = strings.TrimSuffix(endpoint, "/")
			if idx := strings.Index(endpoint, "."); idx > 0 {
				return endpoint[:idx]
			}
			return endpoint
		}
	}
	return ""
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
			return nil, fmt.Errorf("failed to list topics: %w", err)
		}

		for _, topic := range page.Topics {
			topics = append(topics, topic.TopicName)
		}
	}

	return topics, nil
}

func (sbc *ServiceBusClient) ListQueues(ctx context.Context) ([]string, error) {
	pager := sbc.adminClient.NewListQueuesPager(nil)
	var queues []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list queues: %w", err)
		}

		for _, queue := range page.Queues {
			queues = append(queues, queue.QueueName)
		}
	}

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

func (sbc *ServiceBusClient) PeekMessages(ctx context.Context, entityName string, isDeadLetter bool, maxMessages int) ([]MessageInfo, error) {
	// entityName format: "topic/subscription" or "queue"
	parts := strings.SplitN(entityName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid entity name format: %s (expected 'topic/subscription')", entityName)
	}
	topicName := parts[0]
	subscriptionName := parts[1]

	var receiver *azservicebus.Receiver
	var err error

	if isDeadLetter {
		receiver, err = sbc.client.NewReceiverForSubscription(
			topicName,
			subscriptionName,
			&azservicebus.ReceiverOptions{
				SubQueue: azservicebus.SubQueueDeadLetter,
			},
		)
	} else {
		receiver, err = sbc.client.NewReceiverForSubscription(
			topicName,
			subscriptionName,
			nil,
		)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create receiver: %w", err)
	}
	defer receiver.Close(ctx)

	peekedMessages, err := receiver.PeekMessages(ctx, maxMessages, nil)
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
