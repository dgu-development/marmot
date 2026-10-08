package quality

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/marmotdata/marmot/internal/core/metamodel"
)

// MaxCustomRules bounds how many rules people can write: every one is evaluated on every asset.
const MaxCustomRules = 100

var (
	ErrRuleNotFound = errors.New("quality rule not found")
	ErrRuleExists   = errors.New("a quality rule with this id already exists")
	ErrRuleVersion  = errors.New("quality rule version conflict")
	ErrRuleLimit    = errors.New("too many custom quality rules")
	// ErrRuleVersionRequired answers a write that did not say which version it read.
	ErrRuleVersionRequired = errors.New("quality rule version required")
)

// RuleRepository stores the custom rules.
type RuleRepository interface {
	List(ctx context.Context) ([]CustomRule, error)
	Get(ctx context.Context, id string) (*CustomRule, error)
	// Create fails with ErrRuleExists on a taken id.
	Create(ctx context.Context, rule CustomRule) (*CustomRule, error)
	// Update is compare-and-set on the version read; a stale one is ErrRuleVersion.
	Update(ctx context.Context, rule CustomRule, expected int64) (*CustomRule, error)
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
}

// RuleService reads the rules and manages the custom ones.
type RuleService interface {
	// Rules lists every rule: built-in, from the profile and custom, in that order.
	Rules(ctx context.Context) ([]RuleInfo, error)
	Create(ctx context.Context, rule metamodel.QualityRule, enabled bool, by string) (*CustomRule, error)
	Update(ctx context.Context, id string, rule metamodel.QualityRule, enabled bool, expected int64, by string) (*CustomRule, error)
	Delete(ctx context.Context, id string) error
}

type ruleService struct {
	repo     RuleRepository
	settings Service
	registry *metamodel.Registry
}

func NewRuleService(repo RuleRepository, settings Service, registry *metamodel.Registry) RuleService {
	return &ruleService{repo: repo, settings: settings, registry: registry}
}

func newRuleID() (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "rule_" + hex.EncodeToString(raw[:]), nil
}

func (s *ruleService) Rules(ctx context.Context) ([]RuleInfo, error) {
	stored, err := s.settings.Settings(ctx)
	if err != nil {
		return nil, err
	}
	var out []RuleInfo
	for _, id := range Rules {
		setting := stored.Rules[id]
		out = append(out, RuleInfo{ID: id, Source: SourceBuiltin, Dimension: metamodel.BuiltinRuleDimension[string(id)], Severity: setting.Severity, Enabled: setting.Enabled})
	}
	for _, rule := range s.registry.QualityRules() {
		setting := stored.Rules[RuleID(rule.ID)]
		definition := rule
		out = append(out, RuleInfo{
			ID: RuleID(rule.ID), Source: SourceProfile, Dimension: rule.Dimension, LabelKey: rule.LabelKey, DescriptionKey: rule.DescriptionKey,
			Code: rule.FindingCode(), Severity: setting.Severity, Enabled: setting.Enabled, Definition: &definition,
		})
	}
	custom, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading custom quality rules: %w", err)
	}
	fields := s.registry.QualityRuleFields()
	for _, c := range custom {
		definition := c.Rule
		info := RuleInfo{
			ID: RuleID(c.ID), Source: SourceCustom, Dimension: c.Dimension, Name: c.Name, Description: c.Description, Code: c.FindingCode(),
			Severity: Severity(c.Severity), Enabled: c.Enabled, Definition: &definition, Version: c.Version,
		}
		if err := metamodel.ValidateQualityRule(c.Rule, fields, true); err != nil {
			info.Problem = "invalid"
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *ruleService) taken(id string) bool {
	return slices.Contains(Rules, RuleID(id)) || slices.Contains(ProfileRules(s.registry), RuleID(id))
}

func (s *ruleService) validate(rule metamodel.QualityRule) error {
	if !s.registry.Enabled() {
		return ErrMetamodelOff
	}
	return metamodel.ValidateQualityRule(rule, s.registry.QualityRuleFields(), true)
}

func (s *ruleService) Create(ctx context.Context, rule metamodel.QualityRule, enabled bool, by string) (*CustomRule, error) {
	if rule.ID == "" {
		id, err := newRuleID()
		if err != nil {
			return nil, err
		}
		rule.ID = id
	}
	if err := s.validate(rule); err != nil {
		return nil, err
	}
	if s.taken(rule.ID) {
		return nil, ErrRuleExists
	}
	count, err := s.repo.Count(ctx)
	if err != nil {
		return nil, err
	}
	if count >= MaxCustomRules {
		return nil, ErrRuleLimit
	}
	return s.repo.Create(ctx, CustomRule{Rule: rule, Enabled: enabled, CreatedBy: by, UpdatedBy: by})
}

func (s *ruleService) Update(ctx context.Context, id string, rule metamodel.QualityRule, enabled bool, expected int64, by string) (*CustomRule, error) {
	if expected < 1 {
		return nil, ErrRuleVersionRequired
	}
	rule.ID = id
	if err := s.validate(rule); err != nil {
		return nil, err
	}
	return s.repo.Update(ctx, CustomRule{Rule: rule, Enabled: enabled, UpdatedBy: by}, expected)
}

func (s *ruleService) Delete(ctx context.Context, id string) error { return s.repo.Delete(ctx, id) }
