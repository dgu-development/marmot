package webhook

import (
	"encoding/json"
	"strings"
	"testing"
)

var sampleNotification = WebhookNotification{
	Type:    "schema_change",
	Title:   "Schema changed on users",
	Message: "Column email changed",
	Data: map[string]interface{}{
		"asset_name": "users",
		"asset_mrn":  "postgres://prod/public/users",
		"status":     "",
		"link":       "https://marmot.example.com/discover/table/postgres/users",
	},
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return out
}

func TestDefaultRegistryHasEveryValidProvider(t *testing.T) {
	registry := DefaultRegistry()
	for name := range ValidProviders {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("provider %q is valid but not registered", name)
		}
	}
}

func TestTeamsProviderSendsAnAdaptiveCard(t *testing.T) {
	raw, err := (&TeamsProvider{}).FormatMessage(sampleNotification)
	if err != nil {
		t.Fatal(err)
	}
	payload := decode(t, raw)
	if payload["type"] != "message" {
		t.Fatalf("type = %v", payload["type"])
	}
	attachment := payload["attachments"].([]any)[0].(map[string]any)
	if attachment["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Fatalf("contentType = %v", attachment["contentType"])
	}
	card := attachment["content"].(map[string]any)
	if card["type"] != "AdaptiveCard" || card["version"] != "1.4" {
		t.Fatalf("card = %v", card)
	}
	body := card["body"].([]any)
	if body[0].(map[string]any)["text"] != sampleNotification.Title {
		t.Fatalf("first block = %v", body[0])
	}
	facts := body[2].(map[string]any)["facts"].([]any)
	if len(facts) != 2 {
		t.Fatalf("facts = %v, want asset and MRN only (empty status and link are not facts)", facts)
	}
	action := card["actions"].([]any)[0].(map[string]any)
	if action["type"] != "Action.OpenUrl" || action["url"] != sampleNotification.Data["link"] {
		t.Fatalf("action = %v", action)
	}
}

func TestTeamsProviderOmitsActionsWithoutLink(t *testing.T) {
	raw, err := (&TeamsProvider{}).FormatMessage(WebhookNotification{Type: "system", Title: "t", Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "actions") {
		t.Fatalf("unexpected actions in %s", raw)
	}
}

func TestGoogleChatProviderSendsTextAndCard(t *testing.T) {
	raw, err := (&GoogleChatProvider{}).FormatMessage(sampleNotification)
	if err != nil {
		t.Fatal(err)
	}
	payload := decode(t, raw)
	if payload["text"] != "Schema changed on users: Column email changed" {
		t.Fatalf("text = %v", payload["text"])
	}
	card := payload["cardsV2"].([]any)[0].(map[string]any)["card"].(map[string]any)
	if card["header"].(map[string]any)["subtitle"] != "Schema Change" {
		t.Fatalf("header = %v", card["header"])
	}
	widgets := card["sections"].([]any)[0].(map[string]any)["widgets"].([]any)
	if len(widgets) != 4 {
		t.Fatalf("widgets = %v, want message, two facts and the button", widgets)
	}
	button := widgets[3].(map[string]any)["buttonList"].(map[string]any)["buttons"].([]any)[0].(map[string]any)
	if button["onClick"].(map[string]any)["openLink"].(map[string]any)["url"] != sampleNotification.Data["link"] {
		t.Fatalf("button = %v", button)
	}
}

func TestValidateCreateAcceptsNewProviders(t *testing.T) {
	s := &Service{}
	for _, provider := range []string{ProviderTeams, ProviderGoogleChat} {
		err := s.validateCreate(CreateWebhookInput{
			TeamID:            "t",
			Name:              "n",
			Provider:          provider,
			WebhookURL:        "https://203.0.113.10/hook",
			NotificationTypes: []string{"system"},
		})
		if err != nil {
			t.Errorf("%s: %v", provider, err)
		}
	}
}
