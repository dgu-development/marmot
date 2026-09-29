package webhook

import (
	"encoding/json"
	"fmt"
)

// GoogleChatProvider formats messages for Google Chat incoming webhooks using a card,
// with plain text as the fallback shown in notifications.
type GoogleChatProvider struct{}

type googleChatPayload struct {
	Text    string           `json:"text"`
	CardsV2 []googleChatCard `json:"cardsV2"`
}

type googleChatCard struct {
	CardID string             `json:"cardId"`
	Card   googleChatCardBody `json:"card"`
}

type googleChatCardBody struct {
	Header   googleChatHeader    `json:"header"`
	Sections []googleChatSection `json:"sections"`
}

type googleChatHeader struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

type googleChatSection struct {
	Widgets []googleChatWidget `json:"widgets"`
}

type googleChatWidget struct {
	TextParagraph *googleChatText       `json:"textParagraph,omitempty"`
	DecoratedText *googleChatDecorated  `json:"decoratedText,omitempty"`
	ButtonList    *googleChatButtonList `json:"buttonList,omitempty"`
}

type googleChatText struct {
	Text string `json:"text"`
}

type googleChatDecorated struct {
	TopLabel string `json:"topLabel"`
	Text     string `json:"text"`
}

type googleChatButtonList struct {
	Buttons []googleChatButton `json:"buttons"`
}

type googleChatButton struct {
	Text    string            `json:"text"`
	OnClick googleChatOnClick `json:"onClick"`
}

type googleChatOnClick struct {
	OpenLink googleChatLink `json:"openLink"`
}

type googleChatLink struct {
	URL string `json:"url"`
}

func (p *GoogleChatProvider) FormatMessage(notification WebhookNotification) ([]byte, error) {
	widgets := []googleChatWidget{{TextParagraph: &googleChatText{Text: notification.Message}}}

	facts, link := notificationFacts(notification.Data)
	for _, f := range facts {
		widgets = append(widgets, googleChatWidget{DecoratedText: &googleChatDecorated{TopLabel: f.label, Text: f.value}})
	}
	if link != "" {
		widgets = append(widgets, googleChatWidget{ButtonList: &googleChatButtonList{
			Buttons: []googleChatButton{{Text: "View", OnClick: googleChatOnClick{OpenLink: googleChatLink{URL: link}}}},
		}})
	}

	return json.Marshal(googleChatPayload{
		Text: fmt.Sprintf("%s: %s", notification.Title, notification.Message),
		CardsV2: []googleChatCard{{
			CardID: "marmot-notification",
			Card: googleChatCardBody{
				Header:   googleChatHeader{Title: truncate(notification.Title, 200), Subtitle: formatNotificationType(notification.Type)},
				Sections: []googleChatSection{{Widgets: widgets}},
			},
		}},
	})
}

func (p *GoogleChatProvider) ContentType() string {
	return "application/json; charset=UTF-8"
}
