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

const scoreProfile = auditProfile + `  - id: quality_dimensions
    type: list
    itemType: enum
    values: [description, tags, ownership, classification, review, documentation, resource, completeness, conformity]
    core: true
    nullable: true
    system: true
    storage: metadata.dgu.quality_dimensions
    presentation:
      labelKey: audit.dimensions
  - id: quality_evaluated_at
    type: date
    core: true
    nullable: true
    system: true
    storage: metadata.dgu.quality_evaluated_at
    presentation:
      labelKey: audit.evaluated
`

// The audit profile asks for quality_score as required, which a system field cannot be; this one
// has it the way the distribution does.
func scoreRegistry(t *testing.T) *metamodel.Registry {
	t.Helper()
	profile := strings.Replace(scoreProfile, "    required: true\n    storage: metadata.dgu.quality_score", "    nullable: true\n    system: true\n    storage: metadata.dgu.quality_score", 1)
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
	if changes["quality_score"] != 1.0 || changes["quality_evaluated_at"] != "2026-10-01" {
		t.Fatalf("changes = %v", changes)
	}
	if dims, _ := changes["quality_dimensions"].([]string); len(dims) != 7 {
		t.Fatalf("dimensions = %v", changes["quality_dimensions"])
	}

	a.Metadata["dgu"].(map[string]any)["quality_score"] = 1.0
	a.Metadata["dgu"].(map[string]any)["quality_dimensions"] = []any{"description", "tags", "ownership", "classification", "review", "completeness", "conformity"}
	a.Metadata["dgu"].(map[string]any)["quality_evaluated_at"] = "2026-09-01"
	if changes := auditor.ScoreChanges(a, result); changes != nil {
		t.Fatalf("what it says already: the date alone never forces a write: %v", changes)
	}

	a.Metadata["dgu"].(map[string]any)["quality_score"] = 0.9
	if changes := auditor.ScoreChanges(a, result); changes["quality_score"] != 1.0 || changes["quality_evaluated_at"] != "2026-10-01" || changes["quality_dimensions"] != nil {
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
		// The audit profile declares quality_score, nothing else: only that one is written.
		t.Fatalf("%v", changes)
	}
}

type recordedWrites struct {
	inputs   []asset.UpdateInput
	ids      []string
	conflict map[string]bool
	fail     map[string]bool
}

func (r *recordedWrites) Update(_ context.Context, id string, input asset.UpdateInput) (*asset.Asset, error) {
	r.ids = append(r.ids, id)
	r.inputs = append(r.inputs, input)
	switch {
	case r.conflict[id]:
		return nil, asset.ErrVersionConflict
	case r.fail[id]:
		return nil, errors.New("boom")
	}
	return &asset.Asset{ID: id}, nil
}

func TestARunWritesOnlyWhatChangedAsThePlatformAndCountsHowItWent(t *testing.T) {
	settings := quality.DefaultSettings()
	settings.BatchSize = 2
	source := &memorySource{assets: catalog(5)}
	source.assets[1].IsStub = true
	for i, a := range source.assets {
		a.Version = int64(10 + i)
	}
	said := source.assets[2].Metadata["dgu"].(map[string]any)
	said["quality_score"] = 1.0
	said["quality_dimensions"] = []any{"description", "tags", "ownership", "classification", "review", "completeness", "conformity"}
	said["quality_evaluated_at"] = "2026-09-01"
	repo := newMemoryRuns()
	writes := &recordedWrites{conflict: map[string]bool{"id-a03": true}, fail: map[string]bool{"id-a04": true}}
	svc := quality.NewRunService(fixedSettings{settings}, repo, source, scoreRegistry(t), quality.WithScoreWriter(writes))
	t.Cleanup(svc.Shutdown)

	if _, err := svc.StartRun(context.Background(), quality.TriggerManual, ""); err != nil {
		t.Fatal(err)
	}
	wait(t, repo)
	if repo.failure != "" {
		t.Fatalf("failed: %s", repo.failure)
	}

	// a00, a03, a04 need a score; the stub (a01) is skipped and a02 already has it.
	if got := strings.Join(writes.ids, ","); got != "id-a00,id-a03,id-a04" {
		t.Fatalf("written: %s", got)
	}
	first := writes.inputs[0]
	if !first.SystemWrite || !first.SkipNotification || first.ExpectedVersion == nil || *first.ExpectedVersion != 10 {
		t.Fatalf("it writes as the platform, quietly, against the version it read: %+v", first)
	}
	if first.Metadata != nil || first.GovernedFields["quality_score"] != 1.0 {
		t.Fatalf("it patches fields, not the whole asset: %+v", first)
	}
	if repo.counts != (quality.ScoreCounts{Written: 1, Conflicts: 1, Failed: 1}) {
		t.Fatalf("counts = %+v", repo.counts)
	}
}

func TestARunWithoutAWriterOnlyRecords(t *testing.T) {
	source := &memorySource{assets: catalog(2)}
	repo := newMemoryRuns()
	svc := service(t, quality.DefaultSettings(), source, repo, scoreRegistry(t))
	if _, err := svc.StartRun(context.Background(), quality.TriggerManual, ""); err != nil {
		t.Fatal(err)
	}
	wait(t, repo)
	if repo.counts != (quality.ScoreCounts{}) {
		t.Fatalf("counts = %+v", repo.counts)
	}
}

func TestAProfileThatDoesNotListACheckYetNeverReceivesIt(t *testing.T) {
	older := strings.Replace(scoreProfile, "documentation, resource, completeness", "documentation, completeness", 1)
	older = strings.Replace(older, "    required: true\n    storage: metadata.dgu.quality_score", "    nullable: true\n    system: true\n    storage: metadata.dgu.quality_score", 1)
	registry, err := metamodel.Load(strings.NewReader(older))
	if err != nil {
		t.Fatal(err)
	}
	auditor := quality.NewAuditor(registry, quality.DefaultSettings(), now)
	a := newAsset("a", complete())
	a.ExternalLinks = []asset.ExternalLink{{Name: "wiki", URL: "https://wiki"}}
	changes := auditor.ScoreChanges(a, auditor.AuditWithDocs(a, true))
	dims, _ := changes["quality_dimensions"].([]string)
	for _, d := range dims {
		if d == "resource" {
			t.Fatalf("a value the profile does not have would fail the write: %v", dims)
		}
	}
	if len(dims) == 0 {
		t.Fatal("the others are still written")
	}
}
