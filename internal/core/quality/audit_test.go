package quality_test

import (
	"strings"
	"testing"
	"time"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
)

const auditFields = `formatVersion: 1
id: audit
version: 1
defaultLocale: en
fields:
  - id: classification
    type: enum
    core: true
    required: true
    storage: metadata.dgu.classification
    values: [public, internal]
    presentation:
      labelKey: audit.classification
      section: governance
  - id: data_steward
    type: string
    core: true
    required: false
    storage: metadata.dgu.data_steward
    presentation:
      labelKey: audit.steward
      section: governance
  - id: contains_pii
    type: boolean
    core: true
    required: false
    storage: metadata.dgu.contains_pii
    presentation:
      labelKey: audit.pii
      section: governance
  - id: next_review
    type: date
    core: true
    required: false
    storage: metadata.dgu.next_review
    presentation:
      labelKey: audit.review
      section: lifecycle
  - id: retention
    type: integer
    core: true
    required: false
    storage: metadata.dgu.retention
    validation:
      minimum: 1
    presentation:
      labelKey: audit.retention
      section: lifecycle
  - id: quality_score
    type: number
    core: true
    required: true
    storage: metadata.dgu.quality_score
    presentation:
      labelKey: audit.score
      section: output
`

// The coherence rules a profile declares: the ones that used to be fixed in the server.
const auditRules = `qualityRules:
  - id: piiCoherence
    code: pii_coherence
    labelKey: audit.rule.pii
    severity: warning
    when:
      - {field: contains_pii, op: equals, value: true}
    checks:
      - {field: classification, op: set}
      - {field: data_steward, op: set}
  - id: reviewExpired
    code: review_expired
    labelKey: audit.rule.review
    severity: warning
    checks:
      - {field: next_review, op: notBefore, value: today, optional: true}
`

const auditProfile = auditFields + auditRules

var now = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func registry(t *testing.T) *metamodel.Registry {
	t.Helper()
	r, err := metamodel.Load(strings.NewReader(auditProfile))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ptr[T any](v T) *T { return &v }

func newAsset(name string, dgu map[string]any) *asset.Asset {
	return &asset.Asset{
		ID: "id-" + name, Name: ptr(name), MRN: ptr("mrn://table/test/" + name), Type: "table",
		Description: ptr("d"), UserDescription: ptr("u"), Tags: []string{"t"},
		Metadata: map[string]any{"dgu": dgu},
	}
}

func complete() map[string]any {
	return map[string]any{
		"classification": "internal", "data_steward": "u1", "contains_pii": false,
		"next_review": "2027-01-01", "retention": float64(30),
	}
}

func audit(t *testing.T, settings quality.Settings, a *asset.Asset) quality.AssetResult {
	t.Helper()
	return quality.NewAuditor(registry(t), settings, now).Audit(a)
}

func codes(r quality.AssetResult) []string {
	var out []string
	for _, i := range r.Issues {
		out = append(out, i.FieldID+":"+i.Code)
	}
	return out
}

func TestACompleteAssetScoresFullAndIgnoresTheOutputFields(t *testing.T) {
	r := audit(t, quality.DefaultSettings(), newAsset("a", complete()))
	if r.Quality != 100 || r.Status != quality.StatusCompliant || r.IssueCount != 0 {
		t.Fatalf("%+v", r)
	}
	// quality_score is required in the profile but is written by the audit, so it is no gap.
	if _, ok := r.Sections["output"]; ok {
		t.Fatalf("an output field was audited: %+v", r.Sections)
	}
}

func TestAMissingRequiredFieldIsAnErrorThatBlocksCompliance(t *testing.T) {
	values := complete()
	delete(values, "classification")
	r := audit(t, quality.DefaultSettings(), newAsset("a", values))
	if got := codes(r); len(got) != 1 || got[0] != "classification:required" {
		t.Fatalf("issues = %v", got)
	}
	if r.Issues[0].Severity != quality.SeverityError || r.Issues[0].Section != "governance" || r.Issues[0].RuleID != quality.RuleRequired {
		t.Fatalf("issue = %+v", r.Issues[0])
	}
	if r.Completeness != 88.9 || r.Conformity != 100 || r.Quality != 95.6 {
		t.Fatalf("scores = %v %v %v", r.Completeness, r.Conformity, r.Quality)
	}
	// 95.6 is over the compliant threshold, but an error-severity finding keeps it at warning.
	if r.Status != quality.StatusWarning {
		t.Fatalf("status = %s", r.Status)
	}
}

func TestAnInvalidValueUsesTheServersCodeAndCostsConformity(t *testing.T) {
	values := complete()
	values["classification"] = "secret"
	values["retention"] = float64(0)
	r := audit(t, quality.DefaultSettings(), newAsset("a", values))
	got := strings.Join(codes(r), ",")
	if got != "classification:enum,retention:range" {
		t.Fatalf("issues = %s", got)
	}
	if r.Completeness != 100 || r.Conformity != 77.8 {
		t.Fatalf("scores = %v %v", r.Completeness, r.Conformity)
	}
}

func TestCoherenceRules(t *testing.T) {
	values := complete()
	values["contains_pii"] = true
	delete(values, "data_steward")
	values["next_review"] = "2026-01-01"
	a := newAsset("a", values)
	a.ExternalLinks = []asset.ExternalLink{{Name: "docs", URL: "https://x"}, {}, {Name: " "}}

	r := audit(t, quality.DefaultSettings(), a)
	got := strings.Join(codes(r), ",")
	want := "external_links:external_link_empty,external_links:external_link_empty,data_steward:pii_coherence,next_review:review_expired"
	if got != want {
		t.Fatalf("issues = %s", got)
	}
	last := r.Issues[1]
	if last.Item == nil || *last.Item != 2 {
		t.Fatalf("the offending entry is not named: %+v", last)
	}

	values["next_review"] = "2026-10-01"
	if got := strings.Join(codes(audit(t, quality.DefaultSettings(), newAsset("b", values))), ","); strings.Contains(got, "review_expired") {
		t.Fatalf("a review due today is not overdue: %s", got)
	}
}

func TestADisabledRuleRaisesNothing(t *testing.T) {
	s := quality.DefaultSettings()
	s.Rules[quality.RuleRequired] = quality.RuleSetting{Enabled: false, Severity: quality.SeverityError}
	values := complete()
	delete(values, "classification")
	if r := audit(t, s, newAsset("a", values)); r.IssueCount != 0 {
		t.Fatalf("issues = %v", codes(r))
	}
}

func TestAStubIsJudgedOnItsNativeAttributesOnly(t *testing.T) {
	a := newAsset("stub", nil)
	a.IsStub = true
	r := audit(t, quality.DefaultSettings(), a)
	if _, governed := r.Sections["governance"]; !r.Stub || r.IssueCount != 0 || governed || r.Quality != 100 {
		t.Fatalf("a stub was held to the governed fields: %+v", r)
	}
}

func TestStatusFollowsTheThresholds(t *testing.T) {
	s := quality.DefaultSettings()
	s.Rules[quality.RuleRequired] = quality.RuleSetting{Enabled: true, Severity: quality.SeverityWarning}
	empty := newAsset("empty", nil)
	empty.Description, empty.UserDescription, empty.Tags = nil, nil, nil
	r := audit(t, s, empty)
	if r.Quality >= s.Thresholds.Warning || r.Status != quality.StatusNoncompliant {
		t.Fatalf("an empty asset: %+v", r)
	}
	values := complete()
	delete(values, "classification")
	if r := audit(t, s, newAsset("a", values)); r.Status != quality.StatusCompliant {
		t.Fatalf("95.6 with only warnings is compliant: %+v", r)
	}
}

func dimensionsOf(t *testing.T, a *asset.Asset) []string {
	t.Helper()
	return audit(t, quality.DefaultSettings(), a).Dimensions
}

func TestDimensionsListTheChecksAnAssetMeetsInOrder(t *testing.T) {
	a := newAsset("a", complete())
	a.ExternalLinks = []asset.ExternalLink{{Name: "docs", URL: "https://x"}}
	a.Metadata["dgu"].(map[string]any)["body"] = "# the note"
	if got := strings.Join(dimensionsOf(t, a), ","); got != "description,tags,ownership,classification,review,documentation,resource,completeness,conformity" {
		t.Fatalf("%s", got)
	}

	values := complete()
	delete(values, "classification")
	values["next_review"] = "2026-01-01"
	bare := newAsset("b", values)
	bare.Description, bare.UserDescription, bare.Tags = nil, nil, nil
	if got := strings.Join(dimensionsOf(t, bare), ","); got != "ownership,conformity" {
		t.Fatalf("a missing required field fails completeness; no links, tags, description or documentation; overdue review: %s", got)
	}

	invalid := complete()
	invalid["classification"] = "secret"
	if got := strings.Join(dimensionsOf(t, newAsset("c", invalid)), ","); strings.Contains(got, "conformity") || !strings.Contains(got, "completeness") {
		t.Fatalf("an invalid value fails conformity only: %s", got)
	}
	if audit(t, quality.DefaultSettings(), func() *asset.Asset { s := newAsset("s", nil); s.IsStub = true; return s }()).Dimensions != nil {
		t.Fatal("a stub has no dimensions")
	}
}

func TestScoreValueIsAFractionWithThreeDecimals(t *testing.T) {
	for in, want := range map[float64]float64{74: 0.74, 100: 1, 74.26: 0.743, 150: 1, -3: 0, 0: 0} {
		if got := quality.ScoreValue(in); got != want {
			t.Errorf("%v -> %v, want %v", in, got, want)
		}
	}
}

func TestDocumentationAvailableIsTheBodyOrThePagesAndTheLinkedResourceIsAnotherCheck(t *testing.T) {
	has := func(dims []string, id string) bool {
		for _, d := range dims {
			if d == id {
				return true
			}
		}
		return false
	}
	auditor := quality.NewAuditor(registry(t), quality.DefaultSettings(), now)

	plain := newAsset("plain", complete())
	if dims := auditor.Audit(plain).Dimensions; has(dims, "documentation") || has(dims, "resource") {
		t.Fatalf("nothing to read, nothing linked: %v", dims)
	}

	withPages := auditor.AuditWithDocs(plain, true).Dimensions
	if !has(withPages, "documentation") || has(withPages, "resource") {
		t.Fatalf("pages written in the platform are documentation, not a linked resource: %v", withPages)
	}

	body := newAsset("body", complete())
	body.Metadata["dgu"].(map[string]any)["body"] = "# a note"
	if !has(auditor.Audit(body).Dimensions, "documentation") {
		t.Fatal("an ingested markdown body is documentation")
	}
	blank := newAsset("blank", complete())
	blank.Metadata["dgu"].(map[string]any)["body"] = "  \n "
	if has(auditor.Audit(blank).Dimensions, "documentation") {
		t.Fatal("a blank body is not")
	}

	linked := newAsset("linked", complete())
	linked.ExternalLinks = []asset.ExternalLink{{Name: "wiki"}, {URL: " https://wiki "}}
	if dims := auditor.Audit(linked).Dimensions; !has(dims, "resource") || has(dims, "documentation") {
		t.Fatalf("a link out is a resource, not documentation: %v", dims)
	}
}
