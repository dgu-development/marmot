package quality

import (
	"regexp"
	"slices"
	"time"

	"github.com/marmotdata/marmot/internal/core/metamodel"
)

// compiledCondition is a condition ready to evaluate: its regular expression compiled once.
type compiledCondition struct {
	metamodel.QualityCondition
	re *regexp.Regexp
}

type compiledRule struct {
	id        RuleID
	code      string
	dimension string
	severity  Severity
	when      []compiledCondition
	checks    []compiledCondition
}

func compileConditions(conditions []metamodel.QualityCondition) []compiledCondition {
	out := make([]compiledCondition, len(conditions))
	for i, c := range conditions {
		out[i].QualityCondition = c
		if c.Op == metamodel.OpMatches {
			pattern, _ := c.Value.(string)
			// Anchored, so a pattern describes the whole value; RE2 keeps the cost linear.
			out[i].re = regexp.MustCompile("^(?:" + pattern + ")$")
		}
	}
	return out
}

func compileRule(rule metamodel.QualityRule, severity Severity) *compiledRule {
	code := rule.FindingCode()
	dimension := rule.Dimension
	if dimension == "" {
		dimension = metamodel.DimensionValidity
	}
	return &compiledRule{
		id: RuleID(rule.ID), code: code, dimension: dimension, severity: severity,
		when: compileConditions(rule.When), checks: compileConditions(rule.Checks),
	}
}

// activeRules are the rules an audit applies: the profile's that the settings leave enabled, and the
// custom ones that are enabled and still valid against the profile (a field they name may have been
// removed from it since they were written).
func activeRules(registry *metamodel.Registry, settings Settings, custom []CustomRule) []*compiledRule {
	var out []*compiledRule
	for _, rule := range registry.QualityRules() {
		setting, ok := settings.Rules[RuleID(rule.ID)]
		if !ok {
			setting = RuleSetting{Enabled: true, Severity: Severity(rule.Severity)}
		}
		if setting.Enabled {
			out = append(out, compileRule(rule, setting.Severity))
		}
	}
	fields := registry.QualityRuleFields()
	for _, c := range custom {
		if c.Enabled && metamodel.ValidateQualityRule(c.Rule, fields, true) == nil {
			out = append(out, compileRule(c.Rule, Severity(c.Severity)))
		}
	}
	return out
}

func listOf(value any) ([]any, bool) {
	switch list := value.(type) {
	case []any:
		return list, true
	case []string:
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = item
		}
		return out, true
	}
	return nil, false
}

func numberOf(value any) (float64, bool) {
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

func sameScalar(value, want any) bool {
	if a, ok := numberOf(value); ok {
		b, ok := numberOf(want)
		return ok && a == b
	}
	return value == want
}

func isOneOf(value any, wanted any) bool {
	list, _ := wanted.([]any)
	return slices.ContainsFunc(list, func(want any) bool { return sameScalar(value, want) })
}

// holds says whether a condition is true of the values. Apart from the operators that are about
// absence (unset, notEquals, noneOf, notContains), a condition on a field with no value is false.
func (c compiledCondition) holds(values map[string]any, today time.Time) bool {
	value := values[c.Field]
	empty := unset(value)
	switch c.Op {
	case metamodel.OpSet:
		return !empty
	case metamodel.OpUnset:
		return empty
	case metamodel.OpEquals:
		return !empty && sameScalar(value, c.Value)
	case metamodel.OpNotEquals:
		return empty || !sameScalar(value, c.Value)
	case metamodel.OpOneOf:
		return !empty && isOneOf(value, c.Value)
	case metamodel.OpNoneOf:
		return empty || !isOneOf(value, c.Value)
	case metamodel.OpContains, metamodel.OpNotContains:
		list, _ := listOf(value)
		has := slices.ContainsFunc(list, func(item any) bool { return sameScalar(item, c.Value) })
		if c.Op == metamodel.OpContains {
			return has
		}
		return !has
	case metamodel.OpMatches:
		text, ok := value.(string)
		return ok && c.re.MatchString(text)
	case metamodel.OpMinItems, metamodel.OpMaxItems:
		list, _ := listOf(value)
		limit, _ := numberOf(c.Value)
		if c.Op == metamodel.OpMinItems {
			return float64(len(list)) >= limit
		}
		return float64(len(list)) <= limit
	case metamodel.OpAtLeast, metamodel.OpAtMost:
		number, ok := numberOf(value)
		limit, _ := numberOf(c.Value)
		if !ok {
			return false
		}
		if c.Op == metamodel.OpAtLeast {
			return number >= limit
		}
		return number <= limit
	case metamodel.OpNotBefore, metamodel.OpNotAfter:
		text, ok := value.(string)
		if !ok {
			return false
		}
		date, err := time.Parse(time.DateOnly, text)
		operand, valid := c.DateOperand(today)
		if err != nil || !valid {
			return false
		}
		if c.Op == metamodel.OpNotBefore {
			return !date.Before(operand)
		}
		return !date.After(operand)
	}
	return false
}

// apply returns the findings of a rule on an asset: none when it does not apply to it, otherwise
// one for each check that fails, at most one per field. It also says how many checks it judged
// and how many held, which feed the score of its dimension; an optional check on an empty field
// judges nothing.
func (r *compiledRule) apply(values map[string]any, today time.Time) (out []finding, judged, held int) {
	for _, c := range r.when {
		if !c.holds(values, today) {
			return nil, 0, 0
		}
	}
	for _, c := range r.checks {
		if c.Optional && unset(values[c.Field]) {
			continue
		}
		judged++
		if c.holds(values, today) {
			held++
			continue
		}
		if slices.ContainsFunc(out, func(f finding) bool { return f.fieldID == c.Field }) {
			continue
		}
		out = append(out, finding{fieldID: c.Field, code: r.code, rule: r.id, severity: r.severity, dsl: true})
	}
	return out, judged, held
}
