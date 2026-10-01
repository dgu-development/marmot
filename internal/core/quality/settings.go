// Package quality is the fork-only metadata quality audit: versioned settings,
// manual runs with their results, and the history kept in tables of their own.
// See the ADR-007 of the distribution and web/docs/docs/Configure/quality.md.
package quality

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/robfig/cron/v3"
)

type RuleID string

const (
	RuleRequired            RuleID = "required"
	RuleValidation          RuleID = "validation"
	RuleExternalLinkInvalid RuleID = "externalLinkInvalid"
	RuleExternalLinkEmpty   RuleID = "externalLinkEmpty"
)

// Rules lists the built-in rules, in display order. The profile declares others, which the settings
// hold too; see ProfileRules.
var Rules = []RuleID{RuleRequired, RuleValidation, RuleExternalLinkInvalid, RuleExternalLinkEmpty}

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type RuleSetting struct {
	Enabled  bool     `json:"enabled"`
	Severity Severity `json:"severity" enums:"error,warning"`
} // @name QualityRuleSetting

// Weights are the percentages each quality dimension has in the overall score and add up to 100.
// An asset to which a dimension does not apply (no rule of it is evaluated) is scored over the
// others, their weights taken in proportion.
type Weights struct {
	Completeness float64 `json:"completeness"`
	Validity     float64 `json:"validity"`
	Consistency  float64 `json:"consistency"`
	Timeliness   float64 `json:"timeliness"`
} // @name QualityWeights

func (w Weights) of(dimension string) float64 {
	switch dimension {
	case metamodel.DimensionCompleteness:
		return w.Completeness
	case metamodel.DimensionValidity:
		return w.Validity
	case metamodel.DimensionConsistency:
		return w.Consistency
	case metamodel.DimensionTimeliness:
		return w.Timeliness
	}
	return 0
}

// mix is the weighted mean of the scores of the dimensions that apply.
func (w Weights) mix(scores map[string]float64) float64 {
	sum, total := 0.0, 0.0
	for dimension, score := range scores {
		sum += score * w.of(dimension)
		total += w.of(dimension)
	}
	if total > 0 {
		return sum / total
	}
	if len(scores) == 0 {
		return 0
	}
	for _, score := range scores {
		sum += score
	}
	return sum / float64(len(scores))
}

// Thresholds are the scores, out of 100, from which an asset is compliant or
// only warned about.
type Thresholds struct {
	Compliant float64 `json:"compliant"`
	Warning   float64 `json:"warning"`
} // @name QualityThresholds

// Retention is how long each kind of history is kept, in days; 0 keeps nothing beyond the latest run.
type Retention struct {
	RunDays    int `json:"run_days"`
	ResultDays int `json:"result_days"`
} // @name QualityRetention

// Settings is the criterion every score in the platform is computed with.
type Settings struct {
	Weights    Weights                `json:"weights"`
	Thresholds Thresholds             `json:"thresholds"`
	Rules      map[RuleID]RuleSetting `json:"rules"`
	// Schedule is a five-field cron expression; empty means runs are manual only.
	Schedule      string    `json:"schedule"`
	Retention     Retention `json:"retention"`
	BatchSize     int       `json:"batch_size"`
	MaxRunSeconds int       `json:"max_run_seconds"`
} // @name QualitySettings

// Stored is Settings as saved. Version 0 means nobody has saved yet and the
// values are the defaults.
type Stored struct {
	Settings
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by,omitempty"`
} // @name QualityStoredSettings

const (
	MinBatchSize     = 50
	MaxBatchSize     = 5000
	MinRunSeconds    = 60
	MaxRunSeconds    = 86400
	MaxRetentionDays = 3650
)

func DefaultSettings() Settings {
	rules := make(map[RuleID]RuleSetting, len(Rules))
	for _, id := range Rules {
		rules[id] = RuleSetting{Enabled: true, Severity: SeverityWarning}
	}
	rules[RuleRequired] = RuleSetting{Enabled: true, Severity: SeverityError}
	rules[RuleValidation] = RuleSetting{Enabled: true, Severity: SeverityError}
	return Settings{
		Weights:       Weights{Completeness: 30, Validity: 30, Consistency: 20, Timeliness: 20},
		Thresholds:    Thresholds{Compliant: 90, Warning: 70},
		Rules:         rules,
		Retention:     Retention{RunDays: 730, ResultDays: 90},
		BatchSize:     500,
		MaxRunSeconds: 3600,
	}
}

// FieldError names a setting and the reason it was rejected.
type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
} // @name QualityFieldError

// ValidationError carries every problem at once, in the shape the metamodel
// API uses for governed fields.
type ValidationError struct {
	Fields []FieldError `json:"fields"`
} // @name QualityValidationError

func (e *ValidationError) Error() string { return "quality settings validation failed" }

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ParseSchedule validates a cron expression. Empty is valid and means manual only.
func ParseSchedule(expression string) (cron.Schedule, error) {
	return parseSchedule(expression)
}

func inPercent(value float64) bool {
	return !math.IsNaN(value) && value >= 0 && value <= 100
}

// Validate reports every invalid setting for a profile that declares no rules of its own.
func (s Settings) Validate() error { return s.ValidateWith(nil) }

// ValidateWith reports every invalid setting, knowing the rules a profile declares besides the
// built-in ones. Codes: range, total, order, unknown_rule, missing_rule, severity, cron,
// retention, batch, timeout.
func (s Settings) ValidateWith(profileRules []RuleID) error {
	known := append(slices.Clone(Rules), profileRules...)
	var problems []FieldError
	add := func(field, code string) { problems = append(problems, FieldError{field, code}) }

	total, inRange := 0.0, true
	for _, dimension := range metamodel.QualityDimensions {
		inRange = inRange && inPercent(s.Weights.of(dimension))
		total += s.Weights.of(dimension)
	}
	if !inRange {
		add("weights", "range")
	} else if math.Abs(total-100) > 0.01 {
		add("weights", "total")
	}
	if !inPercent(s.Thresholds.Compliant) || !inPercent(s.Thresholds.Warning) {
		add("thresholds", "range")
	} else if s.Thresholds.Compliant <= s.Thresholds.Warning {
		add("thresholds", "order")
	}
	for id, rule := range s.Rules {
		if !slices.Contains(known, id) {
			add("rules."+string(id), "unknown_rule")
		} else if rule.Severity != SeverityError && rule.Severity != SeverityWarning {
			add("rules."+string(id), "severity")
		}
	}
	for _, id := range known {
		if _, ok := s.Rules[id]; !ok {
			add("rules."+string(id), "missing_rule")
		}
	}
	if _, err := ParseSchedule(s.Schedule); err != nil {
		add("schedule", "cron")
	}
	if s.Retention.RunDays < 0 || s.Retention.RunDays > MaxRetentionDays ||
		s.Retention.ResultDays < 0 || s.Retention.ResultDays > MaxRetentionDays {
		add("retention", "range")
	} else if s.Retention.ResultDays > s.Retention.RunDays {
		add("retention", "order")
	}
	if s.BatchSize < MinBatchSize || s.BatchSize > MaxBatchSize {
		add("batch_size", "range")
	}
	if s.MaxRunSeconds < MinRunSeconds || s.MaxRunSeconds > MaxRunSeconds {
		add("max_run_seconds", "range")
	}

	if len(problems) == 0 {
		return nil
	}
	slices.SortFunc(problems, func(a, b FieldError) int {
		return cmp.Or(strings.Compare(a.Field, b.Field), strings.Compare(a.Code, b.Code))
	})
	return &ValidationError{Fields: problems}
}
