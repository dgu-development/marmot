package metamodel

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Quality rules are checks on the metadata of an asset that the audit applies and reports as
// findings. They are declarative: a closed set of operators over the fields of the profile, never
// code, SQL or anything received from the catalog. A rule applies to an asset when every `when`
// condition holds (always, if there are none) and then raises one finding for each `check` that
// does not.

// BuiltinQualityRules are the rules the audit has no profile for: they judge what the metamodel
// itself defines (required fields, valid values) and the structure of the external links.
var BuiltinQualityRules = []string{"required", "validation", "externalLinkInvalid", "externalLinkEmpty"}

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

var QualityDimensions = []string{DimensionCompleteness, DimensionValidity, DimensionConsistency, DimensionTimeliness}

// BuiltinRuleDimension is the dimension each built-in rule counts under.
var BuiltinRuleDimension = map[string]string{
	"required":            DimensionCompleteness,
	"validation":          DimensionValidity,
	"externalLinkInvalid": DimensionValidity,
	"externalLinkEmpty":   DimensionCompleteness,
}

// Operators of a condition, by the value they take.
const (
	OpSet         = "set"         // the field has a value
	OpUnset       = "unset"       // the field has none
	OpEquals      = "equals"      // value: a string, number or boolean
	OpNotEquals   = "notEquals"   // like equals; also true when unset
	OpOneOf       = "oneOf"       // value: a list of strings or numbers
	OpNoneOf      = "noneOf"      // like oneOf; also true when unset
	OpContains    = "contains"    // a list field has this item
	OpNotContains = "notContains" // a list field lacks it; also true when unset
	OpMatches     = "matches"     // value: a regular expression (RE2) the whole text satisfies
	OpMinItems    = "minItems"    // value: a number, for list fields
	OpMaxItems    = "maxItems"    // value: a number, for list fields
	OpAtLeast     = "atLeast"     // value: a number, for number and integer fields
	OpAtMost      = "atMost"      // value: a number, for number and integer fields
	OpNotBefore   = "notBefore"   // date fields; value: "today" or YYYY-MM-DD, moved by days
	OpNotAfter    = "notAfter"    // date fields; same
)

// Limits keep a rule readable and cheap to evaluate on every asset of a large catalog.
const (
	MaxRuleConditions = 20
	MaxRuleText       = 200
	MaxRegexLength    = 200
	maxOneOfValues    = 50
)

var ruleIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,59}$`)
var ruleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,59}$`)

// QualityCondition is one test on one field.
type QualityCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	// Value is what the operator compares with; set and unset take none.
	Value any `json:"value,omitempty"`
	// Days moves the date a date operator compares with, from today or from the given date.
	Days int `json:"days,omitempty"`
	// Optional, in a check, lets an empty field pass instead of failing: nothing to judge.
	Optional bool `json:"optional,omitempty"`
} // @name QualityCondition

// QualityRule is a rule as the profile or a person declares it.
type QualityRule struct {
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
	Severity string             `json:"severity"`
	When     []QualityCondition `json:"when,omitempty"`
	Checks   []QualityCondition `json:"checks"`
} // @name QualityRule

// QualityRuleProblem says what is wrong with a rule and where, by path (`checks[1].value`).
type QualityRuleProblem struct {
	Path string `json:"path"`
	Code string `json:"code"`
} // @name QualityRuleProblem

// QualityRuleError carries every problem of a rule at once.
type QualityRuleError struct {
	Problems []QualityRuleProblem `json:"problems"`
} // @name QualityRuleError

func (e *QualityRuleError) Error() string { return "quality rule validation failed" }

// Today is the date `today` stands for in a date operator.
const Today = "today"

// DateOperand is the date a date condition compares with, given today.
func (c QualityCondition) DateOperand(today time.Time) (time.Time, bool) {
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

func isNumberType(t string) bool { return t == "number" || t == "integer" }

func isScalar(value any) bool {
	switch value.(type) {
	case string, bool, float64, float32, int, int64:
		return true
	}
	return false
}

func asNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// validateCondition checks a condition against the fields it names. Native fields (name,
// description, tags…) are fields like any other; the external links are not, which is why their
// structure is a built-in rule.
func validateCondition(c QualityCondition, path string, fields map[string]Field, inWhen bool) []QualityRuleProblem {
	problem := func(sub, code string) []QualityRuleProblem {
		return []QualityRuleProblem{{Path: path + sub, Code: code}}
	}
	field, ok := fields[c.Field]
	if !ok || !slices.Contains(field.AppliesTo.EffectiveKinds(), "asset") {
		return problem(".field", "unknown_field")
	}
	if c.Optional && inWhen {
		return problem(".optional", "optional_in_when")
	}
	if c.Days != 0 && c.Op != OpNotBefore && c.Op != OpNotAfter {
		return problem(".days", "days_only_for_dates")
	}
	valueType := field.Type
	if field.Type == "list" {
		valueType = field.ItemType
	}
	switch c.Op {
	case OpSet, OpUnset:
		if c.Value != nil {
			return problem(".value", "unexpected_value")
		}
	case OpEquals, OpNotEquals:
		if !isScalar(c.Value) || field.Type == "list" {
			return problem(".value", "bad_value")
		}
		if !valueFits(field, valueType, c.Value) {
			return problem(".value", "type_mismatch")
		}
	case OpContains, OpNotContains:
		if field.Type != "list" {
			return problem(".op", "type_mismatch")
		}
		if !isScalar(c.Value) || !valueFits(field, valueType, c.Value) {
			return problem(".value", "type_mismatch")
		}
	case OpOneOf, OpNoneOf:
		list, ok := c.Value.([]any)
		if !ok || len(list) == 0 || len(list) > maxOneOfValues || field.Type == "list" {
			return problem(".value", "bad_value")
		}
		for _, item := range list {
			if !isScalar(item) || !valueFits(field, valueType, item) {
				return problem(".value", "type_mismatch")
			}
		}
	case OpMatches:
		pattern, ok := c.Value.(string)
		if !ok || pattern == "" || len(pattern) > MaxRegexLength {
			return problem(".value", "bad_value")
		}
		if valueType != "string" || field.Type == "list" {
			return problem(".op", "type_mismatch")
		}
		if _, err := regexp.Compile("^(?:" + pattern + ")$"); err != nil {
			return problem(".value", "bad_regex")
		}
	case OpMinItems, OpMaxItems:
		n, ok := asNumber(c.Value)
		if !ok || n < 0 || n != float64(int(n)) || n > 10000 {
			return problem(".value", "bad_value")
		}
		if field.Type != "list" {
			return problem(".op", "type_mismatch")
		}
	case OpAtLeast, OpAtMost:
		if _, ok := asNumber(c.Value); !ok {
			return problem(".value", "bad_value")
		}
		if !isNumberType(field.Type) {
			return problem(".op", "type_mismatch")
		}
	case OpNotBefore, OpNotAfter:
		if field.Type != "date" {
			return problem(".op", "type_mismatch")
		}
		if _, ok := c.DateOperand(time.Now()); !ok {
			return problem(".value", "bad_value")
		}
		if c.Days < -36500 || c.Days > 36500 {
			return problem(".days", "bad_value")
		}
	default:
		return problem(".op", "unknown_operator")
	}
	return nil
}

// valueFits says whether a comparison value is of the type the field holds, and a member of it for
// an enum.
func valueFits(field Field, valueType string, value any) bool {
	switch valueType {
	case "string", "date":
		_, ok := value.(string)
		return ok
	case "enum":
		text, ok := value.(string)
		return ok && slices.Contains(field.Values, text)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer", "number":
		_, ok := asNumber(value)
		return ok
	}
	return false
}

// ValidateQualityRule reports every problem of a rule. fields are the asset fields by id. custom
// rules, written by people, carry their own name; profile rules carry message keys.
func ValidateQualityRule(rule QualityRule, fields map[string]Field, custom bool) error {
	var problems []QualityRuleProblem
	add := func(path, code string) { problems = append(problems, QualityRuleProblem{Path: path, Code: code}) }

	if !ruleIDPattern.MatchString(rule.ID) {
		add("id", "invalid_id")
	} else if slices.Contains(BuiltinQualityRules, rule.ID) {
		add("id", "reserved_id")
	}
	if rule.Code != "" && !ruleCodePattern.MatchString(rule.Code) {
		add("code", "invalid_code")
	}
	if !slices.Contains(QualityDimensions, rule.Dimension) {
		add("dimension", "invalid_dimension")
	}
	if rule.Severity != "error" && rule.Severity != "warning" {
		add("severity", "invalid_severity")
	}
	switch {
	case custom && strings.TrimSpace(rule.Name) == "":
		add("name", "name_required")
	case !custom && rule.LabelKey == "":
		add("labelKey", "label_required")
	}
	for path, text := range map[string]string{"name": rule.Name, "description": rule.Description} {
		if len([]rune(text)) > MaxRuleText {
			add(path, "too_long")
		}
	}
	for path, key := range map[string]string{"labelKey": rule.LabelKey, "descriptionKey": rule.DescriptionKey} {
		if key != "" && !messageKey.MatchString(key) {
			add(path, "invalid_message_key")
		}
	}
	if len(rule.Checks) == 0 {
		add("checks", "no_checks")
	}
	if len(rule.Checks) > MaxRuleConditions {
		add("checks", "too_many")
	}
	if len(rule.When) > MaxRuleConditions {
		add("when", "too_many")
	}
	for i, c := range rule.When {
		if i < MaxRuleConditions {
			problems = append(problems, validateCondition(c, fmt.Sprintf("when[%d]", i), fields, true)...)
		}
	}
	for i, c := range rule.Checks {
		if i < MaxRuleConditions {
			problems = append(problems, validateCondition(c, fmt.Sprintf("checks[%d]", i), fields, false)...)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	slices.SortFunc(problems, func(a, b QualityRuleProblem) int {
		return strings.Compare(a.Path+":"+a.Code, b.Path+":"+b.Code)
	})
	return &QualityRuleError{Problems: problems}
}

// validateProfileRules checks the rules a profile declares: valid, with distinct ids, and none
// reusing the id of a built-in rule.
func validateProfileRules(rules []QualityRule, fields []Field) error {
	if len(rules) > 100 {
		return fmt.Errorf("a profile declares at most 100 quality rules")
	}
	byID := make(map[string]Field, len(fields))
	for _, f := range fields {
		byID[f.ID] = f
	}
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if seen[rule.ID] {
			return fmt.Errorf("duplicate quality rule %q", rule.ID)
		}
		seen[rule.ID] = true
		if err := ValidateQualityRule(rule, byID, false); err != nil {
			return fmt.Errorf("quality rule %q: %s", rule.ID, describe(err))
		}
	}
	return nil
}

func describe(err error) string {
	invalid, ok := err.(*QualityRuleError)
	if !ok {
		return err.Error()
	}
	parts := make([]string, len(invalid.Problems))
	for i, p := range invalid.Problems {
		parts[i] = p.Path + ": " + p.Code
	}
	return strings.Join(parts, "; ")
}

// QualityRules are the rules the profile declares, in its order.
func (r *Registry) QualityRules() []QualityRule { return r.schema.QualityRules }

// QualityRuleFields are the asset fields a rule may name, by id.
func (r *Registry) QualityRuleFields() map[string]Field {
	out := make(map[string]Field)
	for _, f := range r.Fields("asset") {
		out[f.ID] = f
	}
	return out
}
