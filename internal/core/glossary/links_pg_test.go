package glossary

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

const linkProfile = `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: stands_for
    type: list
    itemType: string
    core: true
    storage: metadata.dgu.stands_for
    appliesTo:
      kinds: [glossary_term]
    presentation:
      labelKey: example.stands_for.label
      control: glossary_term
      inverseLabelKey: example.stands_for.inverse
`

func TestTermLinks(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, name) VALUES ('ana', 'Ana') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	registry, err := metamodel.Load(strings.NewReader(linkProfile))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewPostgresRepository(pool, noopRecorder{}), WithMetamodel(registry))
	owners := []OwnerInput{{ID: userID, Type: "user"}}
	links := func(ids ...any) map[string]interface{} {
		return map[string]interface{}{"dgu": map[string]interface{}{"stands_for": ids}}
	}
	violation := func(err error) string {
		var v *metamodel.ValidationError
		if !errors.As(err, &v) || len(v.Fields) != 1 {
			t.Fatalf("expected one field violation, got %v", err)
		}
		return v.Fields[0].Code
	}

	postcode, err := svc.Create(ctx, CreateTermInput{Name: "Postcode", Definition: "Postal code", Owners: owners})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Create(ctx, CreateTermInput{Name: "Pending account", Definition: "Unsettled", Owners: owners})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(ctx, CreateTermInput{Name: "CP", Definition: "Acronym", Owners: owners, Metadata: links(postcode.ID, "00000000-0000-4000-8000-00000000abcd")})
	if code := violation(err); code != "term_not_found" {
		t.Fatalf("unknown term: code %q", code)
	}
	_, err = svc.Create(ctx, CreateTermInput{Name: "CP", Definition: "Acronym", Owners: owners, Metadata: links("not-a-uuid")})
	if code := violation(err); code != "term_not_found" {
		t.Fatalf("malformed ID: code %q", code)
	}
	cp, err := svc.Create(ctx, CreateTermInput{Name: "CP", Definition: "Acronym", Owners: owners, Metadata: links(postcode.ID, pending.ID)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Update(ctx, cp.ID, UpdateTermInput{Metadata: links(postcode.ID, cp.ID)})
	if code := violation(err); code != "self_reference" {
		t.Fatalf("self link: code %q", code)
	}

	refs, err := svc.References(ctx, postcode.ID)
	if err != nil || len(refs) != 1 || refs[0].Field != "stands_for" || len(refs[0].Terms) != 1 || refs[0].Terms[0].Name != "CP" {
		t.Fatalf("references to Postcode = %+v, %v", refs, err)
	}
	resolved, err := svc.RefsByID(ctx, []string{pending.ID, "garbage", postcode.ID})
	if err != nil || len(resolved) != 2 {
		t.Fatalf("RefsByID = %+v, %v", resolved, err)
	}

	if err := svc.Delete(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if refs, _ := svc.References(ctx, postcode.ID); len(refs) != 1 {
		t.Fatalf("a deleted link target leaves the other references alone: %+v", refs)
	}
	// A link to a term deleted since never blocks an unrelated edit; adding a dead one does.
	if _, err := svc.Update(ctx, cp.ID, UpdateTermInput{Definition: ptr("Ambiguous acronym")}); err != nil {
		t.Fatalf("a stale link blocked an unrelated edit: %v", err)
	}
	_, err = svc.Update(ctx, postcode.ID, UpdateTermInput{Metadata: links(pending.ID)})
	if code := violation(err); code != "term_not_found" {
		t.Fatalf("adding a deleted term: code %q", code)
	}
}
