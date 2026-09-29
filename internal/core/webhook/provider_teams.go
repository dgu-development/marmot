package webhook

import (
	"encoding/json"
	"fmt"
	"time"
)

// TeamsProvider formats messages for Microsoft Teams webhooks created with the
// Workflows app ("Post to a channel when a webhook request is received"), which
// expect an Adaptive Card attachment. Office 365 connectors are retired.
type TeamsProvider struct{}

type teamsPayload struct {
	Type        string            `json:"type"`
	Attachments []teamsAttachment `json:"attachments"`
}

type teamsAttachment struct {
	ContentType string    `json:"contentType"`
	Content     teamsCard `json:"content"`
}

type teamsCard struct {
	Schema  string         `json:"$schema"`
	Type    string         `json:"type"`
	Version string         `json:"version"`
	Body    []teamsElement `json:"body"`
	Actions []teamsAction  `json:"actions,omitempty"`
}

type teamsElement struct {
	Type     string      `json:"type"`
	Text     string      `json:"text,omitempty"`
	Weight   string      `json:"weight,omitempty"`
	Size     string      `json:"size,omitempty"`
	IsSubtle bool        `json:"isSubtle,omitempty"`
	Wrap     bool        `json:"wrap,omitempty"`
	Facts    []teamsFact `json:"facts,omitempty"`
}

type teamsFact struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

type teamsAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (p *TeamsProvider) FormatMessage(notification WebhookNotification) ([]byte, error) {
	body := []teamsElement{
		{Type: "TextBlock", Text: truncate(notification.Title, 256), Weight: "Bolder", Size: "Medium", Wrap: true},
		{Type: "TextBlock", Text: notification.Message, Wrap: true},
	}

	facts, link := notificationFacts(notification.Data)
	if len(facts) > 0 {
		teamsFacts := make([]teamsFact, 0, len(facts))
		for _, f := range facts {
			teamsFacts = append(teamsFacts, teamsFact{Title: f.label, Value: f.value})
		}
		body = append(body, teamsElement{Type: "FactSet", Facts: teamsFacts})
	}

	body = append(body, teamsElement{
		Type:     "TextBlock",
		Text:     fmt.Sprintf("%s | %s", formatNotificationType(notification.Type), time.Now().UTC().Format(time.RFC3339)),
		Size:     "Small",
		IsSubtle: true,
		Wrap:     true,
	})

	card := teamsCard{
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Type:    "AdaptiveCard",
		Version: "1.4",
		Body:    body,
	}
	if link != "" {
		card.Actions = []teamsAction{{Type: "Action.OpenUrl", Title: "View", URL: link}}
	}

	return json.Marshal(teamsPayload{
		Type:        "message",
		Attachments: []teamsAttachment{{ContentType: "application/vnd.microsoft.card.adaptive", Content: card}},
	})
}

func (p *TeamsProvider) ContentType() string {
	return "application/json"
}
