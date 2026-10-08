package quality

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
)

type fakeRuleService struct {
	rules   []quality.RuleInfo
	created *quality.CustomRule
	err     error
	seen    metamodel.QualityRule
	enabled bool
	version int64
	id      string
}

func (f *fakeRuleService) Rules(context.Context) ([]quality.RuleInfo, error) { return f.rules, f.err }

func (f *fakeRuleService) Create(_ context.Context, rule metamodel.QualityRule, enabled bool, _ string) (*quality.CustomRule, error) {
	f.seen, f.enabled = rule, enabled
	if f.err != nil {
		return nil, f.err
	}
	return &quality.CustomRule{Rule: rule, Enabled: enabled, Version: 1}, nil
}

func (f *fakeRuleService) Update(_ context.Context, id string, rule metamodel.QualityRule, enabled bool, expected int64, _ string) (*quality.CustomRule, error) {
	f.id, f.seen, f.enabled, f.version = id, rule, enabled, expected
	if f.err != nil {
		return nil, f.err
	}
	return &quality.CustomRule{Rule: rule, Enabled: enabled, Version: expected + 1}, nil
}

func (f *fakeRuleService) Delete(_ context.Context, id string) error { f.id = id; return f.err }

func send(handler http.HandlerFunc, method, body, ifMatch string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	r.SetPathValue("id", "rule_a")
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

const goodRule = `{"name":"Steward","severity":"warning","checks":[{"field":"steward","op":"set"}]}`

func TestRulesAreListedWithTheirSource(t *testing.T) {
	h := &Handler{rules: &fakeRuleService{rules: []quality.RuleInfo{{ID: "required", Source: quality.SourceBuiltin}, {ID: "rule_a", Source: quality.SourceCustom, Version: 2}}}}
	w := send(h.listRules, http.MethodGet, "", "")
	var body RulesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK || len(body.Rules) != 2 || body.Rules[1].Version != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestACreatedRuleAnswers201WithItsVersionAndIsEnabledByDefault(t *testing.T) {
	rules := &fakeRuleService{}
	h := &Handler{rules: rules}
	w := send(h.createRule, http.MethodPost, goodRule, "")
	if w.Code != http.StatusCreated || w.Header().Get("ETag") != `"1"` || !rules.enabled || rules.seen.Name != "Steward" {
		t.Fatalf("%d %q enabled=%v %+v", w.Code, w.Header().Get("ETag"), rules.enabled, rules.seen)
	}
	if send(h.createRule, http.MethodPost, `{"name":"x","enabled":false,"severity":"error","checks":[{"field":"a","op":"set"}]}`, ""); rules.enabled {
		t.Fatal("enabled: false is respected")
	}
}

func TestARuleWithUnknownKeysOrTwoObjectsIsRefused(t *testing.T) {
	h := &Handler{rules: &fakeRuleService{}}
	for name, body := range map[string]string{
		"unknown rule key":      `{"name":"x","severity":"warning","script":"rm -rf","checks":[{"field":"a","op":"set"}]}`,
		"unknown condition key": `{"name":"x","severity":"warning","checks":[{"field":"a","op":"set","sql":"select 1"}]}`,
		"not json":              `nope`,
		"two objects":           goodRule + goodRule,
	} {
		if w := send(h.createRule, http.MethodPost, body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s -> %d", name, w.Code)
		}
	}
}

func TestRuleErrorsMapToTheirStatus(t *testing.T) {
	for want, err := range map[int]error{
		http.StatusBadRequest:           &metamodel.QualityRuleError{Problems: []metamodel.QualityRuleProblem{{Path: "checks[0].field", Code: "unknown_field"}}},
		http.StatusNotFound:             quality.ErrRuleNotFound,
		http.StatusConflict:             quality.ErrRuleExists,
		http.StatusPreconditionFailed:   quality.ErrRuleVersion,
		http.StatusPreconditionRequired: quality.ErrRuleVersionRequired,
		http.StatusUnprocessableEntity:  quality.ErrRuleLimit,
		http.StatusInternalServerError:  errors.New("pq: password=secret"),
	} {
		h := &Handler{rules: &fakeRuleService{err: err}}
		w := send(h.createRule, http.MethodPost, goodRule, "")
		if w.Code != want {
			t.Errorf("%v -> %d, want %d", err, w.Code, want)
		}
		if want == http.StatusInternalServerError && strings.Contains(w.Body.String(), "secret") {
			t.Error("an internal error leaks its detail")
		}
		if want == http.StatusBadRequest {
			var body metamodel.QualityRuleError
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Problems) != 1 || body.Problems[0].Path != "checks[0].field" {
				t.Errorf("the problems are reported with their path: %s", w.Body)
			}
		}
	}
}

func TestChangingARuleNeedsTheVersionItRead(t *testing.T) {
	rules := &fakeRuleService{}
	h := &Handler{rules: rules}
	if w := send(h.updateRule, http.MethodPut, goodRule, ""); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("no If-Match -> %d", w.Code)
	}
	for _, bad := range []string{"bogus", `"0"`, `"x"`, "3"} {
		if w := send(h.updateRule, http.MethodPut, goodRule, bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s -> %d", bad, w.Code)
		}
	}
	w := send(h.updateRule, http.MethodPut, goodRule, `"4"`)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"5"` || rules.id != "rule_a" || rules.version != 4 {
		t.Fatalf("%d %q %+v", w.Code, w.Header().Get("ETag"), rules)
	}
}

func TestDeletingARule(t *testing.T) {
	rules := &fakeRuleService{}
	h := &Handler{rules: rules}
	if w := send(h.deleteRule, http.MethodDelete, "", ""); w.Code != http.StatusNoContent || rules.id != "rule_a" {
		t.Fatalf("%d", w.Code)
	}
	rules.err = quality.ErrRuleNotFound
	if w := send(h.deleteRule, http.MethodDelete, "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("%d", w.Code)
	}
}

func TestEvaluatingNeedsAssetsAndNoMoreThanTheLimit(t *testing.T) {
	runs := &fakeRuns{}
	h := &Handler{runs: runs}
	for name, body := range map[string]string{"empty": `{"asset_ids":[]}`, "missing": `{}`, "unknown key": `{"asset_ids":["a"],"all":true}`, "not json": `x`} {
		if w := send(h.evaluate, http.MethodPost, body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s -> %d", name, w.Code)
		}
	}
	many := `{"asset_ids":["` + strings.Repeat(`a","`, quality.MaxEvaluated) + `a"]}`
	if w := send(h.evaluate, http.MethodPost, many, ""); w.Code != http.StatusBadRequest {
		t.Errorf("too many -> %d", w.Code)
	}
	w := send(h.evaluate, http.MethodPost, `{"asset_ids":["a","b"]}`, "")
	var body EvaluateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK || body.Results == nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	runs.err = quality.ErrMetamodelOff
	if w := send(h.evaluate, http.MethodPost, `{"asset_ids":["a"]}`, ""); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d", w.Code)
	}
}

func TestOnlyWhoManagesTheAuditChangesRulesAndAnyoneWhoViewsItReadsThem(t *testing.T) {
	for _, route := range (&Handler{}).Routes() {
		key := route.Method + " " + route.Path
		switch key {
		case "GET /api/v1/quality/rules", "POST /api/v1/quality/evaluate", "POST /api/v1/quality/rules", "PUT /api/v1/quality/rules/{id}", "DELETE /api/v1/quality/rules/{id}":
			if len(route.Middleware) != 2 {
				t.Errorf("%s has %d middleware, want authentication and a permission", key, len(route.Middleware))
			}
		}
	}
}
