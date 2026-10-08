package metamodel

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const rulesProfile = `formatVersion: 1
id: rules
version: 1
defaultLocale: en
fields:
  - {id: pii, type: boolean, core: true, nullable: true, storage: metadata.r.pii, presentation: {labelKey: r.pii}}
  - {id: classification, type: enum, core: true, nullable: true, values: [public, internal], storage: metadata.r.classification, presentation: {labelKey: r.classification}}
  - {id: steward, type: string, core: true, nullable: true, storage: metadata.r.steward, presentation: {labelKey: r.steward}}
  - {id: review, type: date, core: true, nullable: true, storage: metadata.r.review, presentation: {labelKey: r.review}}
  - {id: code, type: string, core: true, nullable: true, storage: metadata.r.code, presentation: {labelKey: r.code}}
  - {id: days, type: integer, core: true, nullable: true, storage: metadata.r.days, presentation: {labelKey: r.days}}
  - {id: areas, type: list, itemType: enum, values: [a, b], core: true, nullable: true, storage: metadata.r.areas, presentation: {labelKey: r.areas}}
  - {id: homepage, type: url, core: true, nullable: true, storage: metadata.r.homepage, presentation: {labelKey: r.homepage}}
  - {id: laws, type: list, itemType: url, core: true, nullable: true, storage: metadata.r.laws, presentation: {labelKey: r.laws}}
  - {id: glossary_only, type: string, core: true, nullable: true, appliesTo: {kinds: [glossary_term]}, storage: metadata.r.glossary, presentation: {labelKey: r.glossary}}
`

func profileWithRules(t *testing.T, rules string) (*Registry, error) {
	t.Helper()
	return Load(strings.NewReader(rulesProfile + "qualityRules:\n" + rules))
}

func fieldsOf(t *testing.T) map[string]Field {
	t.Helper()
	r, err := Load(strings.NewReader(rulesProfile))
	if err != nil {
		t.Fatal(err)
	}
	return r.QualityRuleFields()
}

func TestAProfileDeclaresQualityRules(t *testing.T) {
	r, err := profileWithRules(t, `
  - id: piiCoherence
    code: pii_coherence
    labelKey: r.rule.pii
    dimension: validity
    severity: warning
    when:
      - {field: pii, op: equals, value: true}
    checks:
      - {field: classification, op: set}
      - {field: steward, op: set}
  - id: reviewExpired
    code: review_expired
    labelKey: r.rule.review
    dimension: validity
    severity: warning
    checks:
      - {field: review, op: notBefore, value: today, optional: true}
`)
	if err != nil {
		t.Fatal(err)
	}
	rules := r.QualityRules()
	if len(rules) != 2 || rules[0].ID != "piiCoherence" || rules[0].When[0].Value != true || !rules[1].Checks[0].Optional {
		t.Fatalf("rules = %+v", rules)
	}
	before := r.Schema().Hash
	other, _ := profileWithRules(t, `
  - id: reviewExpired
    labelKey: r.rule.review
    dimension: validity
    severity: error
    checks:
      - {field: review, op: notBefore, value: today}
`)
	if other.Schema().Hash == before {
		t.Fatal("the rules are part of the profile's identity")
	}
}

func TestAProfileWithoutRulesLoadsAsBefore(t *testing.T) {
	r, err := Load(strings.NewReader(rulesProfile))
	if err != nil || len(r.QualityRules()) != 0 {
		t.Fatalf("%v %v", r, err)
	}
}

func TestRejectInvalidProfileRules(t *testing.T) {
	cases := map[string]string{
		"unknown field":    `  - {id: a, labelKey: k, severity: warning, checks: [{field: nope, op: set}]}`,
		"another kind":     `  - {id: a, labelKey: k, severity: warning, checks: [{field: glossary_only, op: set}]}`,
		"no checks":        `  - {id: a, labelKey: k, severity: warning, checks: []}`,
		"reserved id":      `  - {id: required, labelKey: k, severity: warning, checks: [{field: steward, op: set}]}`,
		"no dimension":     `  - {id: a, labelKey: k, severity: warning, checks: [{field: steward, op: set}]}`,
		"bad dimension":    `  - {id: a, labelKey: k, dimension: accuracy, severity: warning, checks: [{field: steward, op: set}]}`,
		"bad severity":     `  - {id: a, labelKey: k, severity: info, checks: [{field: steward, op: set}]}`,
		"no label":         `  - {id: a, severity: warning, checks: [{field: steward, op: set}]}`,
		"unknown operator": `  - {id: a, labelKey: k, severity: warning, checks: [{field: steward, op: starts}]}`,
		"duplicate":        "  - {id: a, labelKey: k, severity: warning, checks: [{field: steward, op: set}]}\n  - {id: a, labelKey: k, severity: warning, checks: [{field: steward, op: set}]}",
		"unknown key":      `  - {id: a, labelKey: k, severity: warning, script: "x", checks: [{field: steward, op: set}]}`,
		"unknown cond key": `  - {id: a, labelKey: k, severity: warning, checks: [{field: steward, op: set, sql: "x"}]}`,
	}
	for name, rule := range cases {
		if _, err := profileWithRules(t, rule+"\n"); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestConditionsAreCheckedAgainstTheFieldsTheyName(t *testing.T) {
	fields := fieldsOf(t)
	rule := func(c QualityCondition) QualityRule {
		return QualityRule{ID: "custom1", Name: "A rule", Dimension: "validity", Severity: "warning", Checks: []QualityCondition{c}}
	}
	problem := func(c QualityCondition) string {
		err := ValidateQualityRule(rule(c), fields, true)
		var invalid *QualityRuleError
		if !errors.As(err, &invalid) {
			return ""
		}
		return invalid.Problems[0].Path + ":" + invalid.Problems[0].Code
	}

	good := []QualityCondition{
		{Field: "steward", Op: OpSet},
		{Field: "steward", Op: OpUnset},
		{Field: "classification", Op: OpEquals, Value: "public"},
		{Field: "classification", Op: OpNoneOf, Value: []any{"public"}},
		{Field: "pii", Op: OpEquals, Value: true},
		{Field: "code", Op: OpMatches, Value: `[A-Z]{3}-\d+`},
		{Field: "days", Op: OpAtLeast, Value: 1.0},
		{Field: "areas", Op: OpContains, Value: "a"},
		{Field: "homepage", Op: OpEquals, Value: "https://example.org/a"},
		{Field: "laws", Op: OpContains, Value: "https://example.org/eli/1"},
		{Field: "areas", Op: OpMinItems, Value: 1.0},
		{Field: "review", Op: OpNotBefore, Value: "today"},
		{Field: "review", Op: OpNotAfter, Value: "today", Days: 30},
		{Field: "review", Op: OpNotBefore, Value: "2026-01-01"},
		{Field: "name", Op: OpMatches, Value: `[a-z_]+`},
		{Field: "tags", Op: OpMinItems, Value: 1.0},
	}
	for _, c := range good {
		if got := problem(c); got != "" {
			t.Errorf("%+v: %s", c, got)
		}
	}

	bad := map[string]QualityCondition{
		"checks[0].field:unknown_field":      {Field: "external_links", Op: OpSet},
		"checks[0].value:unexpected_value":   {Field: "steward", Op: OpSet, Value: "x"},
		"checks[0].value:type_mismatch":      {Field: "classification", Op: OpEquals, Value: "secret"},
		"checks[0].op:type_mismatch":         {Field: "steward", Op: OpMinItems, Value: 1.0},
		"checks[0].value:bad_regex":          {Field: "code", Op: OpMatches, Value: `(`},
		"checks[0].value:bad_value":          {Field: "review", Op: OpNotBefore, Value: "tomorrow"},
		"checks[0].days:days_only_for_dates": {Field: "steward", Op: OpSet, Days: 3},
		"checks[0].op:unknown_operator":      {Field: "steward", Op: "startsWith", Value: "x"},
		"checks[0].value:bad_value ":         {Field: "classification", Op: OpOneOf, Value: []any{}},
	}
	for want, c := range bad {
		if got := problem(c); got != strings.TrimSpace(want) {
			t.Errorf("%+v: got %q, want %q", c, got, want)
		}
	}
}

func TestACustomRuleNeedsANameAndWhenCannotBeOptional(t *testing.T) {
	fields := fieldsOf(t)
	rule := QualityRule{ID: "custom1", Dimension: "validity", Severity: "warning", When: []QualityCondition{{Field: "pii", Op: OpEquals, Value: true, Optional: true}}, Checks: []QualityCondition{{Field: "steward", Op: OpSet}}}
	var invalid *QualityRuleError
	if err := ValidateQualityRule(rule, fields, true); !errors.As(err, &invalid) || len(invalid.Problems) != 2 {
		t.Fatalf("%v", err)
	}
	paths := invalid.Problems[0].Path + "," + invalid.Problems[1].Path
	if paths != "name,when[0].optional" {
		t.Fatalf("%s", paths)
	}
}

func TestADateOperandIsTodayOrADateMovedByDays(t *testing.T) {
	today := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	for c, want := range map[QualityCondition]string{
		{Value: "today"}:                "2026-10-01",
		{Value: "today", Days: 30}:      "2026-10-31",
		{Value: "2026-01-15", Days: -5}: "2026-01-10",
	} {
		got, ok := c.DateOperand(today)
		if !ok || got.Format(time.DateOnly) != want {
			t.Errorf("%+v -> %v %v, want %s", c, got, ok, want)
		}
	}
	if _, ok := (QualityCondition{Value: "soon"}).DateOperand(today); ok {
		t.Fatal("not a date")
	}
}
