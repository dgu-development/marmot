package quality_test

import (
	"context"
	"errors"
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
		reg := rulesRegistry(t, "  - {id: r1, labelKey: k, severity: warning, checks: ["+c.cond+"]}\n")
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
    severity: warning
    checks:
      - {field: review, op: notBefore, value: today, optional: true}
  - id: piiNeedsSteward
    labelKey: k
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
	reg := rulesRegistry(t, "  - {id: stewardSet, code: steward_missing, labelKey: k, severity: warning, checks: [{field: steward, op: set}]}\n")
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
	issue := judge(t, rulesRegistry(t, "  - {id: stewardSet, code: steward_missing, labelKey: k, severity: warning, checks: [{field: steward, op: set}]}\n"), quality.DefaultSettings(), ruleAsset(nil)).Issues[0]
	if issue.Code != "steward_missing" {
		t.Fatalf("the finding carries the rule's code: %+v", issue)
	}
}

func custom(id string, enabled bool, severity string, checks ...metamodel.QualityCondition) quality.CustomRule {
	return quality.CustomRule{
		QualityRule: metamodel.QualityRule{ID: id, Name: "Mine", Severity: severity, Checks: checks},
		Enabled:     enabled, Version: 1,
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

func TestADeclaredRuleDoesNotChangeTheDimensionsOrTheScore(t *testing.T) {
	reg := rulesRegistry(t, "  - {id: r1, labelKey: k, severity: error, checks: [{field: steward, op: set}]}\n")
	with := judge(t, reg, quality.DefaultSettings(), ruleAsset(nil))
	without := judge(t, rulesRegistry(t, ""), quality.DefaultSettings(), ruleAsset(nil))
	if with.Quality != without.Quality || strings.Join(with.Dimensions, ",") != strings.Join(without.Dimensions, ",") {
		t.Fatalf("a finding of a rule counts for the status, not for the score or the checks: %v vs %v", with, without)
	}
}

type memoryRules struct {
	rules map[string]quality.CustomRule
}

func (m *memoryRules) List(context.Context) ([]quality.CustomRule, error) {
	var out []quality.CustomRule
	for _, r := range m.rules {
		out = append(out, r)
	}
	return out, nil
}

func (m *memoryRules) Get(_ context.Context, id string) (*quality.CustomRule, error) {
	r, ok := m.rules[id]
	if !ok {
		return nil, quality.ErrRuleNotFound
	}
	return &r, nil
}

func (m *memoryRules) Create(_ context.Context, rule quality.CustomRule) (*quality.CustomRule, error) {
	if _, ok := m.rules[rule.ID]; ok {
		return nil, quality.ErrRuleExists
	}
	rule.Version = 1
	m.rules[rule.ID] = rule
	return &rule, nil
}

func (m *memoryRules) Update(_ context.Context, rule quality.CustomRule, expected int64) (*quality.CustomRule, error) {
	current, ok := m.rules[rule.ID]
	if !ok {
		return nil, quality.ErrRuleNotFound
	}
	if current.Version != expected {
		return nil, quality.ErrRuleVersion
	}
	rule.Version = current.Version + 1
	m.rules[rule.ID] = rule
	return &rule, nil
}

func (m *memoryRules) Delete(_ context.Context, id string) error {
	if _, ok := m.rules[id]; !ok {
		return quality.ErrRuleNotFound
	}
	delete(m.rules, id)
	return nil
}

func (m *memoryRules) Count(context.Context) (int, error) { return len(m.rules), nil }

func ruleServiceFor(t *testing.T, profileRules string) (quality.RuleService, *memoryRules, quality.Service) {
	t.Helper()
	reg := rulesRegistry(t, profileRules)
	settings := quality.NewService(&memorySettings{}, quality.WithRegistry(reg))
	store := &memoryRules{rules: map[string]quality.CustomRule{}}
	return quality.NewRuleService(store, settings, reg), store, settings
}

type memorySettings struct{ stored *quality.Stored }

func (m *memorySettings) Get(context.Context) (*quality.Stored, error) { return m.stored, nil }
func (m *memorySettings) Save(_ context.Context, s quality.Settings, expected int64, _ string) (*quality.Stored, error) {
	version := int64(0)
	if m.stored != nil {
		version = m.stored.Version
	}
	if version != expected {
		return nil, quality.ErrVersionConflict
	}
	m.stored = &quality.Stored{Settings: s, Version: version + 1}
	return m.stored, nil
}

func TestPeopleCreateChangeAndDeleteCustomRulesWithTheSameValidation(t *testing.T) {
	svc, store, _ := ruleServiceFor(t, "")
	ctx := context.Background()
	rule := metamodel.QualityRule{Name: "Steward needed", Severity: "warning", Checks: []metamodel.QualityCondition{{Field: "steward", Op: metamodel.OpSet}}}

	created, err := svc.Create(ctx, rule, true, "u1")
	if err != nil || !strings.HasPrefix(created.ID, "rule_") || created.Version != 1 || created.CreatedBy != "u1" {
		t.Fatalf("%+v %v", created, err)
	}

	bad := rule
	bad.Checks = []metamodel.QualityCondition{{Field: "nope", Op: metamodel.OpSet}, {Field: "steward", Op: "weird"}}
	var invalid *metamodel.QualityRuleError
	if _, err := svc.Create(ctx, bad, true, "u1"); !errors.As(err, &invalid) || len(invalid.Problems) != 2 {
		t.Fatalf("every problem at once: %v", err)
	}
	reserved := rule
	reserved.ID = "required"
	if _, err := svc.Create(ctx, reserved, true, ""); !errors.As(err, &invalid) {
		t.Fatalf("a built-in id is reserved: %v", err)
	}

	renamed := rule
	renamed.Name = "Steward is mandatory"
	updated, err := svc.Update(ctx, created.ID, renamed, false, created.Version, "u2")
	if err != nil || updated.Version != 2 || updated.Enabled || updated.Name != "Steward is mandatory" || updated.ID != created.ID {
		t.Fatalf("%+v %v", updated, err)
	}
	if _, err := svc.Update(ctx, created.ID, renamed, true, 1, "u2"); !errors.Is(err, quality.ErrRuleVersion) {
		t.Fatalf("a stale version: %v", err)
	}
	if _, err := svc.Update(ctx, created.ID, renamed, true, 0, "u2"); !errors.Is(err, quality.ErrRuleVersionRequired) {
		t.Fatalf("no version: %v", err)
	}
	if _, err := svc.Update(ctx, "rule_missing", renamed, true, 1, ""); !errors.Is(err, quality.ErrRuleNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	if err := svc.Delete(ctx, created.ID); err != nil || len(store.rules) != 0 {
		t.Fatalf("%v", err)
	}
	if err := svc.Delete(ctx, created.ID); !errors.Is(err, quality.ErrRuleNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestACustomRuleCannotTakeTheIdOfAProfileRuleOrExceedTheLimit(t *testing.T) {
	svc, store, _ := ruleServiceFor(t, "  - {id: stewardSet, labelKey: k, severity: warning, checks: [{field: steward, op: set}]}\n")
	ctx := context.Background()
	rule := metamodel.QualityRule{ID: "stewardSet", Name: "Mine", Severity: "warning", Checks: []metamodel.QualityCondition{{Field: "steward", Op: metamodel.OpSet}}}
	if _, err := svc.Create(ctx, rule, true, ""); !errors.Is(err, quality.ErrRuleExists) {
		t.Fatalf("%v", err)
	}
	rule.ID = "mine"
	if _, err := svc.Create(ctx, rule, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, rule, true, ""); !errors.Is(err, quality.ErrRuleExists) {
		t.Fatalf("%v", err)
	}
	for i := 0; len(store.rules) < quality.MaxCustomRules; i++ {
		store.rules["x"+string(rune('a'+i%26))+string(rune('a'+i/26))] = quality.CustomRule{}
	}
	rule.ID = "one_more"
	if _, err := svc.Create(ctx, rule, true, ""); !errors.Is(err, quality.ErrRuleLimit) {
		t.Fatalf("%v", err)
	}
}

func TestRulesListsEveryRuleWithItsSourceAndAProblemForOneThatNoLongerFits(t *testing.T) {
	svc, store, _ := ruleServiceFor(t, "  - {id: stewardSet, labelKey: r.steward, descriptionKey: r.steward.help, severity: error, checks: [{field: steward, op: set}]}\n")
	store.rules["rule_old"] = quality.CustomRule{QualityRule: metamodel.QualityRule{ID: "rule_old", Name: "Old", Severity: "warning", Checks: []metamodel.QualityCondition{{Field: "gone", Op: metamodel.OpSet}}}, Enabled: true, Version: 3}
	rules, err := svc.Rules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var summary []string
	for _, r := range rules {
		summary = append(summary, string(r.Source)+":"+string(r.ID))
	}
	want := "builtin:required,builtin:validation,builtin:externalLinkInvalid,builtin:externalLinkEmpty,profile:stewardSet,custom:rule_old"
	if strings.Join(summary, ",") != want {
		t.Fatalf("%s", strings.Join(summary, ","))
	}
	profile, old := rules[4], rules[5]
	if profile.LabelKey != "r.steward" || profile.Severity != quality.SeverityError || !profile.Enabled || profile.Definition == nil {
		t.Fatalf("%+v", profile)
	}
	if old.Problem != "invalid" || old.Version != 3 || old.Name != "Old" {
		t.Fatalf("%+v", old)
	}
}

func TestSettingsGainTheProfilesRulesAndLoseTheOnesThatNoLongerExist(t *testing.T) {
	repo := &memorySettings{}
	reg := rulesRegistry(t, "  - {id: stewardSet, labelKey: k, severity: error, checks: [{field: steward, op: set}]}\n")
	svc := quality.NewService(repo, quality.WithRegistry(reg))
	ctx := context.Background()

	fresh, err := svc.Settings(ctx)
	if err != nil || fresh.Rules["stewardSet"] != (quality.RuleSetting{Enabled: true, Severity: quality.SeverityError}) || len(fresh.Rules) != 5 {
		t.Fatalf("%+v %v", fresh.Rules, err)
	}

	// Saved before the profile had the rule, and with a rule the profile has since dropped.
	repo.stored = &quality.Stored{Version: 4, Settings: quality.DefaultSettings()}
	repo.stored.Rules["piiCoherence"] = quality.RuleSetting{Enabled: false, Severity: quality.SeverityWarning}
	read, _ := svc.Settings(ctx)
	if _, ok := read.Rules["piiCoherence"]; ok || read.Rules["stewardSet"].Severity != quality.SeverityError {
		t.Fatalf("%+v", read.Rules)
	}

	// What it reads can be sent back; an unknown rule cannot.
	if _, err := svc.UpdateSettings(ctx, read.Settings, 4, ""); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	typo := read.Settings
	typo.Rules = map[quality.RuleID]quality.RuleSetting{"stewardSett": {Enabled: true, Severity: quality.SeverityWarning}}
	var invalid *quality.ValidationError
	if _, err := svc.UpdateSettings(ctx, typo, 5, ""); !errors.As(err, &invalid) {
		t.Fatalf("a typo must not pass: %v", err)
	}
	if got := invalid.Fields[len(invalid.Fields)-1]; got.Code != "unknown_rule" && invalid.Fields[0].Code != "unknown_rule" {
		t.Fatalf("%+v", invalid.Fields)
	}
}

func TestEvaluateJudgesAssetsNowWithTheirDocumentationAndInTheOrderAsked(t *testing.T) {
	source := &memorySource{assets: catalog(3)}
	repo := newMemoryRuns()
	reg := scoreRegistry(t)
	rules := &memoryRules{rules: map[string]quality.CustomRule{
		"rule_x": custom("rule_x", true, "error", metamodel.QualityCondition{Field: "data_steward", Op: metamodel.OpEquals, Value: "nobody"}),
	}}
	svc := quality.NewRunService(fixedSettings{quality.DefaultSettings()}, repo, source, reg, quality.WithRuleStore(rules))
	t.Cleanup(svc.Shutdown)

	results, err := svc.Evaluate(context.Background(), []string{"id-a02", "missing", "id-a00"})
	if err != nil || len(results) != 2 || results[0].AssetID != "id-a02" || results[1].AssetID != "id-a00" {
		t.Fatalf("%+v %v", results, err)
	}
	if len(results[0].Dimensions) == 0 || issuesOf(results[0]) != "rule_x:data_steward:error" {
		t.Fatalf("a card needs the findings and the checks: %s %v", issuesOf(results[0]), results[0].Dimensions)
	}

	if _, err := quality.NewRunService(fixedSettings{quality.DefaultSettings()}, repo, source, metamodel.Native()).Evaluate(context.Background(), []string{"id-a00"}); !errors.Is(err, quality.ErrMetamodelOff) {
		t.Fatalf("%v", err)
	}
}
