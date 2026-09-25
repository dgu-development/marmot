package importer

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marmotdata/marmot/internal/core/glossary"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/metrics"
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
`

type noopRecorder struct{ metrics.Recorder }

func (noopRecorder) RecordDBQuery(context.Context, string, time.Duration, bool) {}

type ownerByID string

func (o ownerByID) ResolveOwner(context.Context, string) (glossary.OwnerInput, error) {
	return glossary.OwnerInput{ID: string(o), Type: "user"}, nil
}

func TestImportAndExportTermLinksByName(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, name) VALUES ('ana', 'Ana') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	reg, err := metamodel.Load(strings.NewReader(linkProfile))
	if err != nil {
		t.Fatal(err)
	}
	svc := glossary.NewService(glossary.NewPostgresRepository(pool, noopRecorder{}), glossary.WithMetamodel(reg))
	postcode, err := svc.Create(ctx, glossary.CreateTermInput{Name: "Postcode", Definition: "Postal code", Owners: []glossary.OwnerInput{{ID: userID, Type: "user"}}})
	if err != nil {
		t.Fatal(err)
	}
	im := New(reg, svc, ownerByID(userID))

	if c := im.Columns()[len(im.Columns())-1]; c.Format != "terms" || !strings.Contains(Describe(c), "Names of other terms") {
		t.Fatalf("link column = %+v", c)
	}

	bad, err := im.Validate(ctx, csvSheet(t, "name,definition,owners,stands_for\nCP,Acronym,ana,Nope\nXX,Acronym,ana,xx\n"), OnExistingSkip)
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{bad.Rows[0].Errors[0].Code, bad.Rows[1].Errors[0].Code}
	if !slices.Equal(codes, []string{"term_not_found", "self_reference"}) {
		t.Fatalf("codes = %v", codes)
	}

	result, err := im.Validate(ctx, csvSheet(t, "name,definition,owners,stands_for\nCP,Acronym,ana,postcode|Pending account\nPending account,Unsettled,ana,\n"), OnExistingSkip)
	if err != nil || !result.Valid() {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if err := im.Apply(ctx, svc, result); err != nil {
		t.Fatal(err)
	}
	cp, err := svc.GetByName(ctx, "CP")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := svc.GetByName(ctx, "Pending account")
	if err != nil {
		t.Fatal(err)
	}
	linked, _ := metamodel.ValueAt(cp.Metadata, "metadata.dgu.stands_for")
	if ids := glossary.LinkIDs(linked); !slices.Equal(ids, []string{postcode.ID, pending.ID}) {
		t.Fatalf("stands_for = %v, want the IDs of Postcode and a term created by the same file", ids)
	}

	rows, err := im.Export(ctx, svc)
	if err != nil {
		t.Fatal(err)
	}
	col := len(im.Columns()) - 1
	for _, row := range rows {
		if row[0] == "CP" && row[col] != "Postcode|Pending account" {
			t.Fatalf("exported link cell = %q, want names", row[col])
		}
	}
}
