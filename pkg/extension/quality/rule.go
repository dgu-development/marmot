// Package quality holds the data of the metadata quality audit: the rules, the settings, what the
// audit concludes about an asset and the summary of a run. The server evaluates; an extension
// stores, schedules and serves. Both speak these types.
package quality

import (
	"strings"
	"time"
)

// Quality dimensions group the checks of the audit by what they measure. Each has a score of its
// own and a weight in the overall one. Uniqueness and accuracy are not here: the first compares
// assets with each other and the second needs a truth outside the catalog, and a rule judges one
// asset on its metadata.
const (
	DimensionCompleteness = "completeness" // the values that should be there are
	DimensionValidity     = "validity"     // the values there are well formed and allowed
	DimensionConsistency  = "consistency"  // the values agree with each other
	DimensionTimeliness   = "timeliness"   // the values are current
)

var Dimensions = []string{DimensionCompleteness, DimensionValidity, DimensionConsistency, DimensionTimeliness}

// Condition is one test on one field.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	// Value is what the operator compares with; set and unset take none.
	Value any `json:"value,omitempty"`
	// Days moves the date a date operator compares with, from today or from the given date.
	Days int `json:"days,omitempty"`
	// Optional, in a check, lets an empty field pass instead of failing: nothing to judge.
	Optional bool `json:"optional,omitempty"`
} // @name QualityCondition

// Rule is a rule as the profile or a person declares it.
type Rule struct {
	ID string `json:"id"`
	// Code is the finding code the audit reports, which clients translate; it defaults to a
	// snake_case form of the id.
	Code string `json:"code,omitempty"`
	// Name and Description are text in the language of whoever wrote the rule. A profile rule
	// uses LabelKey and DescriptionKey, which resolve through the profile's messages.
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	LabelKey       string `json:"labelKey,omitempty"`
	DescriptionKey string `json:"descriptionKey,omitempty"`
	// Dimension is the quality dimension whose score the rule's checks feed.
	Dimension string `json:"dimension" enums:"completeness,validity,consistency,timeliness"`
	// Severity is what a finding of the rule counts as: error or warning.
	Severity string      `json:"severity"`
	When     []Condition `json:"when,omitempty"`
	Checks   []Condition `json:"checks"`
} // @name QualityRule

// RuleProblem says what is wrong with a rule and where, by path (`checks[1].value`).
type RuleProblem struct {
	Path string `json:"path"`
	Code string `json:"code"`
} // @name QualityRuleProblem

// RuleError carries every problem of a rule at once.
type RuleError struct {
	Problems []RuleProblem `json:"problems"`
} // @name QualityRuleError

func (e *RuleError) Error() string { return "quality rule validation failed" }

// Today is the date `today` stands for in a date operator.
const Today = "today"

// DateOperand is the date a date condition compares with, given today.
func (c Condition) DateOperand(today time.Time) (time.Time, bool) {
	text, _ := c.Value.(string)
	base := today
	if text != Today {
		parsed, err := time.Parse(time.DateOnly, text)
		if err != nil {
			return time.Time{}, false
		}
		base = parsed
	}
	return base.AddDate(0, 0, c.Days), true
}

// BuiltinDimension is the dimension each built-in rule counts under.
var BuiltinDimension = map[string]string{
	"required":            DimensionCompleteness,
	"validation":          DimensionValidity,
	"externalLinkInvalid": DimensionValidity,
	"externalLinkEmpty":   DimensionCompleteness,
}

// FindingCode is the code the audit reports for the rule, which clients translate: its Code, or
// a snake_case form of its ID.
func (r Rule) FindingCode() string {
	if r.Code != "" {
		return r.Code
	}
	var out strings.Builder
	for i, c := range r.ID {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(c + 'a' - 'A')
			continue
		}
		out.WriteRune(c)
	}
	return out.String()
}
