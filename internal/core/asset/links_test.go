package asset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/metamodel"
)

const linkProfile = `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: applies_to
    type: list
    itemType: string
    core: true
    nullable: true
    storage: metadata.example.applies_to
    presentation:
      labelKey: example.applies_to.label
      control: asset
      inverseLabelKey: example.applies_to.inverse
`

func newLinkService(t *testing.T) Service {
	t.Helper()
	registry, err := metamodel.Load(strings.NewReader(linkProfile))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(newMemoryRepo(), WithMetamodel(registry))
}

func withLinks(name string, ids ...any) CreateInput {
	input := validCreate(name)
	input.Metadata["example"] = map[string]any{"applies_to": ids}
	return input
}

func hasViolation(err error, field, code string) bool {
	var validation *metamodel.ValidationError
	if !errors.As(err, &validation) {
		return false
	}
	for _, v := range validation.Fields {
		if v.Field == field && v.Code == code {
			return true
		}
	}
	return false
}

func TestAssetLinksMustPointAtExistingAssets(t *testing.T) {
	svc := newLinkService(t)
	target, err := svc.Create(context.Background(), validCreate("target"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), withLinks("ok", target.ID)); err != nil {
		t.Fatalf("a link to an existing asset must be accepted: %v", err)
	}
	_, err = svc.Create(context.Background(), withLinks("bad", "7f0c1f1e-0000-4000-8000-000000000000"))
	if !hasViolation(err, "applies_to", "asset_not_found") {
		t.Fatalf("a link to a missing asset must be rejected: %v", err)
	}
}

func TestAssetLinksRejectSelfAndKeepExistingOnes(t *testing.T) {
	svc := newLinkService(t)
	target, _ := svc.Create(context.Background(), validCreate("target"))
	source, err := svc.Create(context.Background(), withLinks("source", target.ID))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Update(context.Background(), source.ID, UpdateInput{
		ExpectedVersion: &source.Version,
		Metadata:        map[string]any{"example": map[string]any{"applies_to": []any{source.ID}}},
	})
	if !hasViolation(err, "applies_to", "self_reference") {
		t.Fatalf("an asset must not link to itself: %v", err)
	}
	if err := svc.Delete(context.Background(), target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(context.Background(), source.ID, UpdateInput{
		ExpectedVersion: &source.Version,
		Metadata:        map[string]any{"example": map[string]any{"applies_to": []any{target.ID}}, "plugin": map[string]any{"extra": "no"}},
	}); err != nil {
		t.Fatalf("a link the asset already had must survive its target being deleted: %v", err)
	}
}

func TestAssetLinksRespectTargetAssetTypes(t *testing.T) {
	registry, err := metamodel.Load(strings.NewReader(`formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: asset_type
    type: enum
    core: true
    nullable: true
    storage: metadata.example.asset_type
    values: [table, rule]
    presentation:
      labelKey: example.asset_type.label
  - id: applies_to
    type: list
    itemType: string
    core: true
    nullable: true
    storage: metadata.example.applies_to
    presentation:
      labelKey: example.applies_to.label
      control: asset
      targetAssetTypes: [table]
`))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(newMemoryRepo(), WithMetamodel(registry))
	typed := func(name, assetType string) *Asset {
		input := validCreate(name)
		if assetType != "" {
			input.Metadata["example"] = map[string]any{"asset_type": assetType}
		}
		asset, err := svc.Create(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		return asset
	}
	table, rule, untyped := typed("table", "table"), typed("rule", "rule"), typed("untyped", "")
	if _, err := svc.Create(context.Background(), withLinks("ok", table.ID)); err != nil {
		t.Fatalf("a link to an accepted type must be accepted: %v", err)
	}
	for _, target := range []*Asset{rule, untyped} {
		_, err := svc.Create(context.Background(), withLinks("bad-"+*target.Name, table.ID, target.ID))
		if !hasViolation(err, "applies_to", "target_type") {
			t.Fatalf("a link to %s must be rejected: %v", *target.Name, err)
		}
	}
}

func TestAnMRNLinkResolvesOnceItsTargetExists(t *testing.T) {
	svc := newLinkService(t)
	ctx := context.Background()
	first := validCreate("source")
	mrn := "mrn://table/test/source"
	first.MRN = &mrn
	first.Metadata["example"] = map[string]any{"applies_to": []any{"mrn://table/test/later"}}
	first.DeferLinks = true
	source, err := svc.Create(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if value, present := metamodel.ValueAt(source.Metadata, "metadata.example.applies_to"); present {
		t.Fatalf("a deferred field with an MRN that names nothing yet is not stored: %v", value)
	}
	later := validCreate("later")
	laterMRN := "mrn://table/test/later"
	later.MRN = &laterMRN
	target, err := svc.Create(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.Update(ctx, source.ID, UpdateInput{
		Metadata: map[string]any{"example": map[string]any{"applies_to": []any{"mrn://table/test/later"}}},
		FromSync: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := metamodel.ValueAt(updated.Metadata, "metadata.example.applies_to")
	if got := LinkIDs(value); len(got) != 1 || got[0] != target.ID {
		t.Fatalf("the link must hold the target's ID once it exists: %v", got)
	}
	if !HasLinkMRNs(map[string]any{"a": []any{"x", "mrn://t/p/n"}}) || HasLinkMRNs(map[string]any{"a": "x"}) {
		t.Fatal("HasLinkMRNs finds an MRN anywhere in the metadata")
	}
}
