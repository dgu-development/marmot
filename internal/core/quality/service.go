package quality

import (
	"context"
	"errors"
	"fmt"

	"github.com/marmotdata/marmot/internal/core/metamodel"
)

type Service interface {
	// Settings returns the saved settings, or the defaults with version 0.
	Settings(ctx context.Context) (*Stored, error)
	// UpdateSettings validates and saves. expected is the version the caller
	// read; a stale one returns ErrVersionConflict.
	UpdateSettings(ctx context.Context, settings Settings, expected int64, by string) (*Stored, error)
}

type service struct {
	repo     Repository
	registry *metamodel.Registry
}

// ServiceOption adjusts the settings service.
type ServiceOption func(*service)

// WithRegistry tells the service which rules the metamodel profile declares, so the settings hold
// a severity and an on/off for each of them as well as for the built-in ones.
func WithRegistry(registry *metamodel.Registry) ServiceOption {
	return func(s *service) { s.registry = registry }
}

func NewService(repo Repository, options ...ServiceOption) Service {
	s := &service{repo: repo}
	for _, option := range options {
		option(s)
	}
	return s
}

// ProfileRules are the ids of the rules the profile declares.
func ProfileRules(registry *metamodel.Registry) []RuleID {
	if registry == nil {
		return nil
	}
	var out []RuleID
	for _, rule := range registry.QualityRules() {
		out = append(out, RuleID(rule.ID))
	}
	return out
}

// complete gives every rule the settings should hold an entry: the default one when nobody has set
// it (a rule the profile gained after the settings were saved), and drops the entries of rules that
// no longer exist, so what a client reads is always what it may send back.
func (s *service) complete(settings Settings) Settings {
	defaults := s.defaultRules()
	rules := make(map[RuleID]RuleSetting, len(defaults))
	for id, fallback := range defaults {
		if setting, ok := settings.Rules[id]; ok {
			rules[id] = setting
		} else {
			rules[id] = fallback
		}
	}
	settings.Rules = rules
	return settings
}

// defaultRules is the setting of every rule that exists: a built-in rule as DefaultSettings has
// it, a profile rule at the severity its declaration gives.
func (s *service) defaultRules() map[RuleID]RuleSetting {
	defaults := DefaultSettings().Rules
	if s.registry != nil {
		for _, rule := range s.registry.QualityRules() {
			defaults[RuleID(rule.ID)] = RuleSetting{Enabled: true, Severity: Severity(rule.Severity)}
		}
	}
	return defaults
}

func (s *service) Settings(ctx context.Context) (*Stored, error) {
	stored, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading quality settings: %w", err)
	}
	if stored == nil {
		return &Stored{Settings: s.complete(DefaultSettings())}, nil
	}
	stored.Settings = s.complete(stored.Settings)
	return stored, nil
}

func (s *service) UpdateSettings(ctx context.Context, settings Settings, expected int64, by string) (*Stored, error) {
	if expected < 0 {
		return nil, ErrVersionRequired
	}
	// A missing rule is filled in, an unknown one is an error: a typo must not pass silently.
	if settings.Rules == nil {
		settings.Rules = map[RuleID]RuleSetting{}
	}
	for id, fallback := range s.defaultRules() {
		if _, ok := settings.Rules[id]; !ok {
			settings.Rules[id] = fallback
		}
	}
	if err := settings.ValidateWith(ProfileRules(s.registry)); err != nil {
		return nil, err
	}
	stored, err := s.repo.Save(ctx, settings, expected, by)
	if err != nil {
		if errors.Is(err, ErrVersionConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("saving quality settings: %w", err)
	}
	return stored, nil
}
