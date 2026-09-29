package webhook

import (
	"fmt"
	"sync"
)

// Provider formats notification messages for a specific webhook destination.
type Provider interface {
	// FormatMessage converts a notification into the provider-specific payload.
	FormatMessage(notification WebhookNotification) ([]byte, error)
	// ContentType returns the HTTP Content-Type header for requests.
	ContentType() string
}

// WebhookNotification contains the data needed to format a webhook message.
type WebhookNotification struct {
	Type    string                 `json:"type"`
	Title   string                 `json:"title"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// ProviderRegistry holds all registered webhook providers.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewProviderRegistry creates a new empty registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry.
func (r *ProviderRegistry) Register(name string, provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = provider
}

// Get retrieves a provider by name.
func (r *ProviderRegistry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// DefaultRegistry creates a registry with the built-in providers.
func DefaultRegistry() *ProviderRegistry {
	registry := NewProviderRegistry()
	registry.Register(ProviderSlack, &SlackProvider{})
	registry.Register(ProviderDiscord, &DiscordProvider{})
	registry.Register(ProviderGeneric, &GenericProvider{})
	registry.Register(ProviderTeams, &TeamsProvider{})
	registry.Register(ProviderGoogleChat, &GoogleChatProvider{})
	return registry
}

type notificationFact struct {
	label string
	value string
}

// notificationFacts returns the data fields every provider shows, in a stable order,
// and the link back to Marmot, if any.
func notificationFacts(data map[string]interface{}) ([]notificationFact, string) {
	var facts []notificationFact
	link := ""
	for _, key := range []string{"asset_name", "asset_mrn", "pipeline_name", "status", "link"} {
		val, ok := data[key]
		if !ok {
			continue
		}
		strVal := fmt.Sprintf("%v", val)
		if strVal == "" {
			continue
		}
		if key == "link" {
			link = strVal
			continue
		}
		facts = append(facts, notificationFact{label: formatFieldLabel(key), value: strVal})
	}
	return facts, link
}
