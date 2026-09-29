package notification

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type recordingChannel struct {
	name       string
	accepts    string
	deliveries []Delivery
	err        error
	ctxErr     error
}

func (c *recordingChannel) Name() string          { return c.name }
func (c *recordingChannel) Accepts(t string) bool { return c.accepts == "" || c.accepts == t }
func (c *recordingChannel) Deliver(ctx context.Context, d Delivery) error {
	c.ctxErr = ctx.Err()
	slices.Sort(d.UserIDs)
	c.deliveries = append(c.deliveries, d)
	return c.err
}

type staticPrefs map[string]map[string]bool

func (p staticPrefs) GetNotificationPreferences(_ context.Context, userID string) (map[string]bool, error) {
	return p[userID], nil
}

func (p staticPrefs) GetNotificationPreferencesBatch(_ context.Context, userIDs []string) (map[string]map[string]bool, error) {
	out := make(map[string]map[string]bool, len(userIDs))
	for _, id := range userIDs {
		if prefs, ok := p[id]; ok {
			out[id] = prefs
		}
	}
	return out, nil
}

type failingPrefs struct{}

func (failingPrefs) GetNotificationPreferences(context.Context, string) (map[string]bool, error) {
	return nil, errors.New("down")
}

func (failingPrefs) GetNotificationPreferencesBatch(context.Context, []string) (map[string]map[string]bool, error) {
	return nil, errors.New("down")
}

func newChannelService(prefs UserPreferencesProvider, channels ...Channel) *Service {
	s := NewService(nil, nil, WithUserPreferencesProvider(prefs), WithChannels(channels...))
	s.workerPool = nil
	return s
}

func TestChannelRecipientsFollowChannelThenInAppPreference(t *testing.T) {
	prefs := map[string]map[string]bool{
		"muted-type":    {TypeMention: false},
		"channel-off":   {ChannelPreferenceKey("email", TypeMention): false},
		"channel-on":    {TypeMention: false, ChannelPreferenceKey("email", TypeMention): true},
		"other-channel": {ChannelPreferenceKey("chat", TypeMention): false},
	}
	users := []string{"muted-type", "channel-off", "channel-on", "other-channel", "no-prefs"}

	got := channelRecipients(users, prefs, "email", TypeMention)

	want := []string{"channel-on", "other-channel", "no-prefs"}
	if !slices.Equal(got, want) {
		t.Fatalf("recipients = %v, want %v", got, want)
	}
}

func TestDispatchToChannelsSkipsChannelsThatDoNotAcceptTheType(t *testing.T) {
	email := &recordingChannel{name: "email", accepts: TypeMention}
	chat := &recordingChannel{name: "chat", accepts: TypeJobComplete}
	s := newChannelService(staticPrefs{}, email, chat)

	s.dispatchToChannels(context.Background(), CreateNotificationInput{Type: TypeMention, Title: "t", Message: "m"}, []string{"b", "a"})

	if len(email.deliveries) != 1 || !slices.Equal(email.deliveries[0].UserIDs, []string{"a", "b"}) {
		t.Fatalf("email deliveries = %+v", email.deliveries)
	}
	if len(chat.deliveries) != 0 {
		t.Fatalf("chat should not receive a mention: %+v", chat.deliveries)
	}
}

func TestDispatchToChannelsSkipsWhenNobodyWantsIt(t *testing.T) {
	email := &recordingChannel{name: "email"}
	s := newChannelService(staticPrefs{"a": {ChannelPreferenceKey("email", TypeMention): false}}, email)

	s.dispatchToChannels(context.Background(), CreateNotificationInput{Type: TypeMention}, []string{"a"})

	if len(email.deliveries) != 0 {
		t.Fatalf("no delivery expected, got %+v", email.deliveries)
	}
}

func TestDispatchToChannelsDeliversToEveryoneWhenPreferencesFail(t *testing.T) {
	email := &recordingChannel{name: "email"}
	s := newChannelService(failingPrefs{}, email)

	s.dispatchToChannels(context.Background(), CreateNotificationInput{Type: TypeMention}, []string{"a", "b"})

	if len(email.deliveries) != 1 || len(email.deliveries[0].UserIDs) != 2 {
		t.Fatalf("deliveries = %+v", email.deliveries)
	}
}

func TestChannelDeliveryOutlivesTheCallerContext(t *testing.T) {
	email := &recordingChannel{name: "email"}
	s := newChannelService(staticPrefs{}, email)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.dispatchToChannels(ctx, CreateNotificationInput{Type: TypeMention}, []string{"a"})

	if len(email.deliveries) != 1 {
		t.Fatalf("deliveries = %+v", email.deliveries)
	}
	if email.ctxErr != nil {
		t.Fatalf("delivery context already done: %v", email.ctxErr)
	}
}

func TestChannelJobWrapsDeliveryErrors(t *testing.T) {
	boom := errors.New("smtp down")
	email := &recordingChannel{name: "email", err: boom}
	job := &channelJob{svc: newChannelService(staticPrefs{}), channel: email}

	err := job.Execute(context.Background())

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped %v", err, boom)
	}
}
