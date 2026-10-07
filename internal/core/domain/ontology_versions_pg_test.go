package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestOntologyVersionRestoresTermsAndDomain(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var domainID, kept, removed string
	if err := pool.QueryRow(ctx, `INSERT INTO domains (path, depth, name, description, metadata) VALUES ('redes', 1, 'Redes', 'antes', '{"ontology":{"sector":"energia"},"other":1}') RETURNING id`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	term := func(name string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO glossary_terms (name, definition, metadata, tags) VALUES ($1, 'def', '{"dgu":{"term_type":"concept"}}', ARRAY['a']) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO glossary_term_domains (glossary_term_id, domain_id) VALUES ($1, $2)`, id, domainID)
		return id
	}
	kept, removed = term("Router"), term("Enlace")

	versions := NewOntologyVersions(pool)
	first, err := versions.Publish(ctx, domainID, "inicial", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.Terms != 2 {
		t.Fatalf("first version: %+v", first)
	}

	exec(`UPDATE glossary_terms SET name = 'Encaminador', metadata = '{}' WHERE id = $1`, kept)
	exec(`UPDATE glossary_terms SET deleted_at = now() WHERE id = $1`, removed)
	added := term("Sede")
	exec(`UPDATE domains SET description = 'después', metadata = '{"ontology":{"sector":"salud"},"other":2}' WHERE id = $1`, domainID)

	saved, err := versions.Restore(ctx, domainID, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 2 || saved.BeforeRestoreOf == nil || *saved.BeforeRestoreOf != 1 || saved.Terms != 2 {
		t.Fatalf("version saved before restoring: %+v", saved)
	}

	var name, termType string
	var deleted bool
	if err := pool.QueryRow(ctx, `SELECT name, metadata #>> '{dgu,term_type}', deleted_at IS NOT NULL FROM glossary_terms WHERE id = $1`, kept).Scan(&name, &termType, &deleted); err != nil {
		t.Fatal(err)
	}
	if name != "Router" || termType != "concept" || deleted {
		t.Fatalf("changed term not restored: %s %s %v", name, termType, deleted)
	}
	for id, want := range map[string]bool{removed: false, added: true} {
		if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM glossary_terms WHERE id = $1`, id).Scan(&deleted); err != nil {
			t.Fatal(err)
		}
		if deleted != want {
			t.Fatalf("term %s deleted = %v, want %v", id, deleted, want)
		}
	}
	var description, sector, other string
	if err := pool.QueryRow(ctx, `SELECT description, metadata #>> '{ontology,sector}', metadata ->> 'other' FROM domains WHERE id = $1`, domainID).Scan(&description, &sector, &other); err != nil {
		t.Fatal(err)
	}
	// What the domain keeps outside its ontology is not the version's to restore.
	if description != "antes" || sector != "energia" || other != "2" {
		t.Fatalf("domain not restored: %s %s %s", description, sector, other)
	}

	if _, err := versions.Restore(ctx, domainID, 99, nil); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("missing version: %v", err)
	}
	if list, err := versions.List(ctx, domainID); err != nil || len(list) != 2 || list[0].Version != 2 {
		t.Fatalf("a failed restore must not leave a version behind: %v %+v", err, list)
	}
	if _, err := versions.Publish(ctx, "00000000-0000-4000-8000-00000000dead", "", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing domain: %v", err)
	}
}
