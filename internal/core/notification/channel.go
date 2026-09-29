package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// DefaultChannelTimeout bounds a single delivery so a slow channel cannot hold a worker.
const DefaultChannelTimeout = 30 * time.Second

// Channel delivers notifications to individual users outside the in-app inbox,
// for example by email. Team webhooks are not channels: they go through ExternalNotifier.
type Channel interface {
	// Name identifies the channel in preference keys, such as "email".
	Name() string
	// Accepts reports whether the channel handles a notification type.
	Accepts(notificationType string) bool
	// Deliver sends one notification to the given users. It may be retried by the
	// implementation, so it must be idempotent per notification and user.
	Deliver(ctx context.Context, delivery Delivery) error
}

// Delivery is what a channel receives: the notification and the users it goes to,
// already expanded from teams and filtered by their preferences.
type Delivery struct {
	Type    string                 `json:"type"`
	Title   string                 `json:"title"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
	UserIDs []string               `json:"user_ids"`
}

// ChannelPreferenceKey is the preference that turns a notification type on or off
// for one channel. When a user has not set it, their in-app preference for the type applies.
func ChannelPreferenceKey(channel, notificationType string) string {
	return channel + ":" + notificationType
}

// WithChannels registers delivery channels.
func WithChannels(channels ...Channel) ServiceOption {
	return func(s *Service) {
		s.channels = append(s.channels, channels...)
	}
}

// RegisterChannel adds a delivery channel after service creation.
func (s *Service) RegisterChannel(channel Channel) {
	s.channels = append(s.channels, channel)
}

func channelRecipients(userIDs []string, prefs map[string]map[string]bool, channel, notificationType string) []string {
	key := ChannelPreferenceKey(channel, notificationType)
	out := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		userPrefs := prefs[userID]
		if enabled, ok := userPrefs[key]; ok {
			if enabled {
				out = append(out, userID)
			}
			continue
		}
		if enabled, ok := userPrefs[notificationType]; !ok || enabled {
			out = append(out, userID)
		}
	}
	return out
}

func (s *Service) dispatchToChannels(ctx context.Context, input CreateNotificationInput, userIDs []string) {
	if len(s.channels) == 0 || len(userIDs) == 0 {
		return
	}

	var prefs map[string]map[string]bool
	if s.userPrefsProvider != nil {
		loaded, err := s.userPrefsProvider.GetNotificationPreferencesBatch(ctx, userIDs)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to load notification preferences for channels, defaulting to enabled")
		} else {
			prefs = loaded
		}
	}

	for _, channel := range s.channels {
		if !channel.Accepts(input.Type) {
			continue
		}
		recipients := channelRecipients(userIDs, prefs, channel.Name(), input.Type)
		if len(recipients) == 0 {
			continue
		}
		job := &channelJob{
			svc:     s,
			channel: channel,
			delivery: Delivery{
				Type:    input.Type,
				Title:   input.Title,
				Message: input.Message,
				Data:    input.Data,
				UserIDs: recipients,
			},
		}
		if s.workerPool == nil || !s.workerPool.Submit(job) {
			if err := job.Execute(ctx); err != nil {
				log.Error().Err(err).Str("channel", channel.Name()).Msg("Notification channel delivery failed")
			}
		}
	}
}

type channelJob struct {
	svc      *Service
	channel  Channel
	delivery Delivery
}

func (j *channelJob) ID() string {
	return fmt.Sprintf("notification-channel:%s:%s:%s", j.channel.Name(), j.delivery.Type, j.delivery.Title)
}

// Execute detaches from the caller's context: the notification is already stored, and
// the request that triggered it may end before the channel finishes.
func (j *channelJob) Execute(ctx context.Context) error {
	base := j.svc.ctx
	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	deliverCtx, cancel := context.WithTimeout(base, DefaultChannelTimeout)
	defer cancel()
	if err := j.channel.Deliver(deliverCtx, j.delivery); err != nil {
		return fmt.Errorf("channel %s: %w", j.channel.Name(), err)
	}
	return nil
}
