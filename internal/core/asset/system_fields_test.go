package asset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/metamodel"
)

const systemProfile = `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: retention
    type: integer
    core: true
    required: false
    nullable: true
    storage: metadata.example.retention
    validation:
      minimum: 1
    presentation:
      labelKey: example.retention.label
  - id: score
    type: number
    core: true
    required: false
    nullable: true
    system: true
    storage: metadata.example.score
    validation:
      minimum: 0
      maximum: 1
    presentation:
      labelKey: example.score.label
`

func systemService(t *testing.T) (Service, *Asset) {
	t.Helper()
	registry, err := metamodel.Load(strings.NewReader(systemProfile))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(newMemoryRepo(), WithMetamodel(registry))
	input := validCreate("system")
	input.Metadata["example"] = map[string]any{"retention": 30.0}
	created, err := svc.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return svc, created
}

func scoreOf(a *Asset) any {
	value, _ := metamodel.ValueAt(a.Metadata, "metadata.example.score")
	return value
}

func code(t *testing.T, err error) string {
	t.Helper()
	var invalid *metamodel.ValidationError
	if !errors.As(err, &invalid) || len(invalid.Fields) != 1 {
		t.Fatalf("not a single violation: %v", err)
	}
	return invalid.Fields[0].Field + ":" + invalid.Fields[0].Code
}

func TestNobodyPatchesASystemField(t *testing.T) {
	svc, created := systemService(t)
	_, err := svc.PatchFields(context.Background(), created.ID, created.Version, map[string]any{"score": 0.5})
	if got := code(t, err); got != "score:system" {
		t.Fatalf("%s", got)
	}
}

func TestAWholeAssetUpdateCannotChangeASystemField(t *testing.T) {
	svc, created := systemService(t)
	ctx := context.Background()
	written, err := svc.Update(ctx, created.ID, UpdateInput{
		ExpectedVersion: &created.Version, SystemWrite: true, SkipNotification: true,
		GovernedFields: map[string]any{"score": 0.74},
	})
	if err != nil || scoreOf(written) != 0.74 {
		t.Fatalf("the platform writes it: %v %v", scoreOf(written), err)
	}

	for name, metadata := range map[string]map[string]any{
		"another value": {"example": map[string]any{"retention": 30.0, "score": 0.1}},
		"left out":      {"example": map[string]any{"retention": 30.0}},
		"removed":       {"example": map[string]any{"retention": 30.0, "score": nil}},
	} {
		current, _ := svc.Get(ctx, created.ID)
		updated, err := svc.Update(ctx, created.ID, UpdateInput{ExpectedVersion: &current.Version, Metadata: metadata})
		if err != nil || scoreOf(updated) != 0.74 {
			t.Errorf("%s: score is %v (%v)", name, scoreOf(updated), err)
		}
	}
}

func TestADiscoveryRunAndACreationCannotSetASystemField(t *testing.T) {
	svc, created := systemService(t)
	ctx := context.Background()
	synced, err := svc.Update(ctx, created.ID, UpdateInput{FromSync: true, Metadata: map[string]any{"example": map[string]any{"retention": 30.0, "score": 0.9}}})
	if err != nil || scoreOf(synced) != nil {
		t.Fatalf("ingestion set it: %v %v", scoreOf(synced), err)
	}

	input := validCreate("born")
	input.Metadata["example"] = map[string]any{"score": 1.0}
	born, err := svc.Create(ctx, input)
	if err != nil || scoreOf(born) != nil {
		t.Fatalf("creation kept it: %v %v", scoreOf(born), err)
	}
}

func TestASystemWriteIsNotBlockedByAnInvalidValueElsewhereButChecksItsOwn(t *testing.T) {
	svc, created := systemService(t)
	ctx := context.Background()
	repo := svc.(*service).repo.(*memoryRepo)
	repo.byID[created.ID].Metadata["example"].(map[string]any)["retention"] = -5.0

	if _, err := svc.PatchFields(ctx, created.ID, created.Version, map[string]any{"retention": 10.0}); err != nil {
		t.Fatalf("a human can still fix it: %v", err)
	}
	repo.byID[created.ID].Metadata["example"].(map[string]any)["retention"] = -5.0
	current, _ := svc.Get(ctx, created.ID)

	written, err := svc.Update(ctx, created.ID, UpdateInput{ExpectedVersion: &current.Version, SystemWrite: true, SkipNotification: true, GovernedFields: map[string]any{"score": 0.2}})
	if err != nil || scoreOf(written) != 0.2 {
		t.Fatalf("an asset with an invalid value is the one that has to be scored: %v %v", scoreOf(written), err)
	}

	current, _ = svc.Get(ctx, created.ID)
	_, err = svc.Update(ctx, created.ID, UpdateInput{ExpectedVersion: &current.Version, SystemWrite: true, GovernedFields: map[string]any{"score": 7.0}})
	if got := code(t, err); got != "score:range" {
		t.Fatalf("the value it writes is checked: %s", got)
	}
}

func TestASystemWriteWithAStaleVersionConflicts(t *testing.T) {
	svc, created := systemService(t)
	stale := created.Version - 1
	_, err := svc.Update(context.Background(), created.ID, UpdateInput{ExpectedVersion: &stale, SystemWrite: true, GovernedFields: map[string]any{"score": 0.3}})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v", err)
	}
}
