package quality_test

import (
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
)

const rulesFields = `formatVersion: 1
id: rules
version: 1
defaultLocale: en
fields:
  - {id: classification, type: enum, core: true, nullable: true, values: [public, internal, secret], storage: metadata.d.classification, presentation: {labelKey: r.c}}
  - {id: steward, type: string, core: true, nullable: true, storage: metadata.d.steward, presentation: {labelKey: r.s}}
  - {id: pii, type: boolean, core: true, nullable: true, storage: metadata.d.pii, presentation: {labelKey: r.p}}
  - {id: code, type: string, core: true, nullable: true, storage: metadata.d.code, presentation: {labelKey: r.code}}
  - {id: review, type: date, core: true, nullable: true, storage: metadata.d.review, presentation: {labelKey: r.r}}
  - {id: days, type: integer, core: true, nullable: true, storage: metadata.d.days, presentation: {labelKey: r.days}}
  - {id: areas, type: list, itemType: string, core: true, nullable: true, storage: metadata.d.areas, presentation: {labelKey: r.areas}}
`

func rulesRegistry(t *testing.T, rules string) *metamodel.Registry {
	t.Helper()
	text := rulesFields
	if rules != "" {
		text += "qualityRules:\n" + rules
	}
	r, err := metamodel.Load(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ruleAsset(values map[string]any) *asset.Asset {
	a := newAsset("r", nil)
	a.Metadata = map[string]any{"d": values}
	return a
}

func issuesOf(r quality.AssetResult) string {
	var out []string
	for _, i := range r.Issues {
		out = append(out, string(i.RuleID)+":"+i.FieldID+":"+string(i.Severity))
	}
	return strings.Join(out, ",")
}

func judge(t *testing.T, reg *metamodel.Registry, settings quality.Settings, a *asset.Asset, custom ...quality.CustomRule) quality.AssetResult {
	t.Helper()
	var options []quality.AuditorOption
	if len(custom) > 0 {
		options = append(options, quality.WithCustomRules(custom))
	}
	return quality.NewAuditor(reg, settings, now, options...).Audit(a)
}

func TestEveryOperatorJudgesTheFieldItNames(t *testing.T) {
	cases := []struct {
		name  string
		cond  string
		value map[string]any
		fails bool
	}{
		{"set, with value", "{field: steward, op: set}", map[string]any{"steward": "ana"}, false},
		{"set, blank", "{field: steward, op: set}", map[string]any{"steward": "  "}, true},
		{"unset, empty", "{field: steward, op: unset}", map[string]any{}, false},
		{"unset, with value", "{field: steward, op: unset}", map[string]any{"steward": "ana"}, true},
		{"equals", "{field: classification, op: equals, value: public}", map[string]any{"classification": "public"}, false},
		{"equals, other", "{field: classification, op: equals, value: public}", map[string]any{"classification": "internal"}, true},
		{"equals, empty", "{field: classification, op: equals, value: public}", map[string]any{}, true},
		{"notEquals, empty", "{field: classification, op: notEquals, value: secret}", map[string]any{}, false},
		{"notEquals, equal", "{field: classification, op: notEquals, value: secret}", map[string]any{"classification": "secret"}, true},
		{"oneOf", "{field: classification, op: oneOf, value: [public, internal]}", map[string]any{"classification": "internal"}, false},
		{"oneOf, other", "{field: classification, op: oneOf, value: [public, internal]}", map[string]any{"classification": "secret"}, true},
		{"noneOf", "{field: classification, op: noneOf, value: [secret]}", map[string]any{"classification": "secret"}, true},
		{"boolean", "{field: pii, op: equals, value: true}", map[string]any{"pii": true}, false},
		{"matches", "{field: code, op: matches, value: '[A-Z]{3}-[0-9]+'}", map[string]any{"code": "ABC-12"}, false},
		{"matches, whole value", "{field: code, op: matches, value: '[A-Z]{3}-[0-9]+'}", map[string]any{"code": "xABC-12"}, true},
		{"minItems", "{field: areas, op: minItems, value: 2}", map[string]any{"areas": []any{"a", "b"}}, false},
		{"minItems, short", "{field: areas, op: minItems, value: 2}", map[string]any{"areas": []any{"a"}}, true},
		{"minItems, empty", "{field: areas, op: minItems, value: 1}", map[string]any{}, true},
		{"maxItems", "{field: areas, op: maxItems, value: 1}", map[string]any{"areas": []any{"a", "b"}}, true},
		{"contains", "{field: areas, op: contains, value: a}", map[string]any{"areas": []any{"a", "b"}}, false},
		{"notContains", "{field: areas, op: notContains, value: c}", map[string]any{"areas": []any{"a"}}, false},
		{"atLeast", "{field: days, op: atLeast, value: 30}", map[string]any{"days": 40.0}, false},
		{"atLeast, less", "{field: days, op: atLeast, value: 30}", map[string]any{"days": 10.0}, true},
		{"atMost", "{field: days, op: atMost, value: 30}", map[string]any{"days": 10.0}, false},
		{"notBefore today, due today", "{field: review, op: notBefore, value: today}", map[string]any{"review": "2026-10-01"}, false},
		{"notBefore today, overdue", "{field: review, op: notBefore, value: today}", map[string]any{"review": "2026-09-30"}, true},
		{"notBefore today, empty", "{field: review, op: notBefore, value: today}", map[string]any{}, true},
		{"notAfter, soon enough", "{field: review, op: notAfter, value: today, days: 30}", map[string]any{"review": "2026-10-20"}, false},
		{"notAfter, too far", "{field: review, op: notAfter, value: today, days: 30}", map[string]any{"review": "2026-12-01"}, true},
		{"notBefore a date", "{field: review, op: notBefore, value: '2026-01-01'}", map[string]any{"review": "2025-12-31"}, true},
	}
	for _, c := range cases {
		reg := rulesRegistry(t, "  - {id: r1, labelKey: k, dimension: validity, severity: warning, checks: ["+c.cond+"]}\n")
		got := judge(t, reg, quality.DefaultSettings(), ruleAsset(c.value))
		if failed := strings.Contains(issuesOf(got), "r1:"); failed != c.fails {
			t.Errorf("%s: failed=%v, want %v (%s)", c.name, failed, c.fails, issuesOf(got))
		}
	}
}

func TestAnOptionalCheckPassesWhenThereIsNothingToJudgeAndWhenSkipsTheRule(t *testing.T) {
	reg := rulesRegistry(t, `
  - id: reviewed
    labelKey: k
    dimension: validity
    severity: warning
    checks:
      - {field: review, op: notBefore, value: today, optional: true}
  - id: piiNeedsSteward
    labelKey: k
    dimension: validity
    severity: error
    when:
      - {field: pii, op: equals, value: true}
      - {field: classification, op: equals, value: secret}
    checks:
      - {field: steward, op: set}
      - {field: code, op: set}
`)
	if got := issuesOf(judge(t, reg, quality.DefaultSettings(), ruleAsset(map[string]any{}))); got != "" {
		t.Fatalf("nothing to judge: %s", got)
	}
	if got := issuesOf(judge(t, reg, quality.DefaultSettings(), ruleAsset(map[string]any{"review": "2026-01-01"}))); got != "reviewed:review:warning" {
		t.Fatalf("overdue: %s", got)
	}
	if got := issuesOf(judge(t, reg, quality.DefaultSettings(), ruleAsset(map[string]any{"pii": true}))); got != "" {
		t.Fatalf("one condition of when is not enough: %s", got)
	}
	got := judge(t, reg, quality.DefaultSettings(), ruleAsset(map[string]any{"pii": true, "classification": "secret"}))
	if issuesOf(got) != "piiNeedsSteward:steward:error,piiNeedsSteward:code:error" || got.Status == quality.StatusCompliant {
		t.Fatalf("every failing check is a finding on its field, and an error blocks compliance: %s (%s)", issuesOf(got), got.Status)
	}
}

func TestTheSettingsDecideWhetherAProfileRuleAppliesAndHowSevere(t *testing.T) {
	reg := rulesRegistry(t, "  - {id: stewardSet, code: steward_missing, labelKey: k, dimension: validity, severity: warning, checks: [{field: steward, op: set}]}\n")
	settings := quality.DefaultSettings()

	// A rule the settings have never seen applies at the severity its declaration gives.
	if got := issuesOf(judge(t, reg, settings, ruleAsset(nil))); got != "stewardSet:steward:warning" {
		t.Fatalf("default: %s", got)
	}
	settings.Rules["stewardSet"] = quality.RuleSetting{Enabled: true, Severity: quality.SeverityError}
	if got := issuesOf(judge(t, reg, settings, ruleAsset(nil))); got != "stewardSet:steward:error" {
		t.Fatalf("severity: %s", got)
	}
	settings.Rules["stewardSet"] = quality.RuleSetting{Enabled: false, Severity: quality.SeverityError}
	if got := issuesOf(judge(t, reg, settings, ruleAsset(nil))); got != "" {
		t.Fatalf("disabled: %s", got)
	}
	issue := judge(t, rulesRegistry(t, "  - {id: stewardSet, code: steward_missing, labelKey: k, dimension: validity, severity: warning, checks: [{field: steward, op: set}]}\n"), quality.DefaultSettings(), ruleAsset(nil)).Issues[0]
	if issue.Code != "steward_missing" {
		t.Fatalf("the finding carries the rule's code: %+v", issue)
	}
}

func custom(id string, enabled bool, severity string, checks ...metamodel.QualityCondition) quality.CustomRule {
	return quality.CustomRule{
		Rule:    metamodel.QualityRule{ID: id, Name: "Mine", Dimension: metamodel.DimensionValidity, Severity: severity, Checks: checks},
		Enabled: enabled, Version: 1,
	}
}

func TestCustomRulesApplyLikeTheProfilesAndAnInvalidOrDisabledOneIsSkipped(t *testing.T) {
	reg := rulesRegistry(t, "")
	set := metamodel.QualityCondition{Field: "steward", Op: metamodel.OpSet}
	got := judge(t, reg, quality.DefaultSettings(), ruleAsset(nil), custom("rule_a", true, "error", set))
	if issuesOf(got) != "rule_a:steward:error" || got.Issues[0].Code != "rule_a" {
		t.Fatalf("%s %+v", issuesOf(got), got.Issues)
	}
	if got := issuesOf(judge(t, reg, quality.DefaultSettings(), ruleAsset(nil), custom("rule_a", false, "error", set))); got != "" {
		t.Fatalf("disabled: %s", got)
	}
	gone := metamodel.QualityCondition{Field: "removed_since", Op: metamodel.OpSet}
	if got := issuesOf(judge(t, reg, quality.DefaultSettings(), ruleAsset(nil), custom("rule_b", true, "error", gone))); got != "" {
		t.Fatalf("a rule naming a field the profile lost cannot be judged and is skipped: %s", got)
	}
}

func TestTheChecksOfARuleFeedTheScoreOfItsDimensionAndNotTheCardChecks(t *testing.T) {
	reg := rulesRegistry(t, "  - {id: r1, labelKey: k, dimension: validity, severity: error, checks: [{field: steward, op: set}]}\n")
	with := judge(t, reg, quality.DefaultSettings(), ruleAsset(nil))
	without := judge(t, rulesRegistry(t, ""), quality.DefaultSettings(), ruleAsset(nil))
	if strings.Join(with.Dimensions, ",") != strings.Join(without.Dimensions, ",") {
		t.Fatalf("the card checks are the same: %v vs %v", with.Dimensions, without.Dimensions)
	}
	if with.Scores["validity"] >= without.Scores["validity"] || with.Scores["completeness"] != without.Scores["completeness"] || with.Quality >= without.Quality {
		t.Fatalf("a failed check lowers its own dimension and the total only: %v vs %v", with.Scores, without.Scores)
	}
}

func TestADimensionNoRuleAppliedToHasNoScoreAndTheWeightsGoToTheOthers(t *testing.T) {
	reg := rulesRegistry(t, `  - {id: fresh, labelKey: k, dimension: timeliness, severity: warning, when: [{field: steward, op: set}], checks: [{field: steward, op: set}]}
  - {id: agree, labelKey: k, dimension: consistency, severity: warning, checks: [{field: steward, op: unset}]}
`)
	got := judge(t, reg, quality.DefaultSettings(), ruleAsset(nil))
	if _, ok := got.Scores["timeliness"]; ok {
		t.Fatalf("a rule whose condition does not hold judges nothing: %v", got.Scores)
	}
	if got.Scores["consistency"] != 100 {
		t.Fatalf("consistency = %v", got.Scores)
	}
	want := (got.Scores["completeness"]*30 + got.Scores["validity"]*30 + 100*20) / 80
	if diff := got.Quality - want; diff > 0.1 || diff < -0.1 {
		t.Fatalf("quality %v, want %v over the dimensions that apply: %v", got.Quality, want, got.Scores)
	}
}
