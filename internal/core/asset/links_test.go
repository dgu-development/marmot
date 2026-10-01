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
