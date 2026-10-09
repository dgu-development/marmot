package quality_test

import (
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
)

const scoreProfile = auditFields + `  - id: metadata_quality_dimensions
    type: list
    itemType: enum
    values: [description, tags, ownership, classification, review, documentation, resource, completeness, conformity]
    core: true
    nullable: true
    system: true
    storage: metadata.dgu.metadata_quality_dimensions
    presentation:
      labelKey: audit.dimensions
  - id: metadata_quality_evaluated_at
    type: date
    core: true
    nullable: true
    system: true
    storage: metadata.dgu.metadata_quality_evaluated_at
    presentation:
      labelKey: audit.evaluated
` + auditRules

// The audit profile asks for metadata_quality_score as required, which a system field cannot be; this one
// has it the way the distribution does.
func scoreRegistry(t *testing.T) *metamodel.Registry {
	t.Helper()
	profile := strings.Replace(scoreProfile, "    required: true\n    storage: metadata.dgu.metadata_quality_score", "    nullable: true\n    system: true\n    storage: metadata.dgu.metadata_quality_score", 1)
	r, err := metamodel.Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func scoreAuditor(t *testing.T) *quality.Auditor {
	t.Helper()
	return quality.NewAuditor(scoreRegistry(t), quality.DefaultSettings(), now)
}

func TestAnAssetWithoutAScoreIsWrittenOneWithTheScoreItDeservesIsNot(t *testing.T) {
	auditor := scoreAuditor(t)
	a := newAsset("a", complete())
	result := auditor.Audit(a)

	changes := auditor.ScoreChanges(a, result)
	if changes["metadata_quality_score"] != 1.0 || changes["metadata_quality_evaluated_at"] != "2026-10-01" {
		t.Fatalf("changes = %v", changes)
	}
	if dims, _ := changes["metadata_quality_dimensions"].([]string); len(dims) != 7 {
		t.Fatalf("dimensions = %v", changes["metadata_quality_dimensions"])
	}

	a.Metadata["dgu"].(map[string]any)["metadata_quality_score"] = 1.0
	a.Metadata["dgu"].(map[string]any)["metadata_quality_dimensions"] = []any{"description", "tags", "ownership", "classification", "review", "completeness", "conformity"}
	a.Metadata["dgu"].(map[string]any)["metadata_quality_evaluated_at"] = "2026-09-01"
	if changes := auditor.ScoreChanges(a, result); changes != nil {
		t.Fatalf("what it says already: the date alone never forces a write: %v", changes)
	}

	a.Metadata["dgu"].(map[string]any)["metadata_quality_score"] = 0.9
	if changes := auditor.ScoreChanges(a, result); changes["metadata_quality_score"] != 1.0 || changes["metadata_quality_evaluated_at"] != "2026-10-01" || changes["metadata_quality_dimensions"] != nil {
		t.Fatalf("only what moved, and the day it was judged: %v", changes)
	}
}

func TestAStubIsNeverWritten(t *testing.T) {
	auditor := scoreAuditor(t)
	stub := newAsset("s", nil)
	stub.IsStub = true
	if changes := auditor.ScoreChanges(stub, auditor.Audit(stub)); changes != nil {
		t.Fatalf("%v", changes)
	}
}

func TestAProfileWithoutTheFieldsIsAuditedAndWritesNothing(t *testing.T) {
	auditor := quality.NewAuditor(registry(t), quality.DefaultSettings(), now)
	a := newAsset("a", complete())
	if changes := auditor.ScoreChanges(a, auditor.Audit(a)); len(changes) != 1 {
		// The audit profile declares metadata_quality_score, nothing else: only that one is written.
		t.Fatalf("%v", changes)
	}
}

func TestAProfileThatDoesNotListACheckYetNeverReceivesIt(t *testing.T) {
	older := strings.Replace(scoreProfile, "documentation, resource, completeness", "documentation, completeness", 1)
	older = strings.Replace(older, "    required: true\n    storage: metadata.dgu.metadata_quality_score", "    nullable: true\n    system: true\n    storage: metadata.dgu.metadata_quality_score", 1)
	registry, err := metamodel.Load(strings.NewReader(older))
	if err != nil {
		t.Fatal(err)
	}
	auditor := quality.NewAuditor(registry, quality.DefaultSettings(), now)
	a := newAsset("a", complete())
	a.ExternalLinks = []asset.ExternalLink{{Name: "wiki", URL: "https://wiki"}}
	changes := auditor.ScoreChanges(a, auditor.AuditWithDocs(a, true))
	dims, _ := changes["metadata_quality_dimensions"].([]string)
	for _, d := range dims {
		if d == "resource" {
			t.Fatalf("a value the profile does not have would fail the write: %v", dims)
		}
	}
	if len(dims) == 0 {
		t.Fatal("the others are still written")
	}
}
