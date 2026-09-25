package search

import (
	"context"
	"testing"
	"time"

	"github.com/marmotdata/marmot/internal/metrics"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

type noopRecorder struct{ metrics.Recorder }

func (noopRecorder) RecordDBQuery(context.Context, string, time.Duration, bool) {}

func TestGlossaryResultsCarryTheirMetadata(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO glossary_terms (name, definition, metadata)
		VALUES ('CLV', 'Acronym', '{"dgu": {"term_type": "acronym"}}')`); err != nil {
		t.Fatal(err)
	}
	results, _, _, err := NewPostgresRepository(pool, noopRecorder{}).Search(ctx, Filter{Query: "CLV", Limit: 10})
	if err != nil || len(results) != 1 {
		t.Fatalf("results = %v, %v", results, err)
	}
	metadata, _ := results[0].Metadata["metadata"].(map[string]interface{})
	dgu, _ := metadata["dgu"].(map[string]interface{})
	if dgu["term_type"] != "acronym" {
		t.Fatalf("result metadata = %+v", results[0].Metadata)
	}
}

func TestSearchCountsItsMatches(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	for _, name := range []string{"Invoice line", "Invoice header", "Invoice total"} {
		if _, err := pool.Exec(ctx, `INSERT INTO glossary_terms (name, definition) VALUES ($1, 'Billing')`, name); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewPostgresRepository(pool, noopRecorder{})
	for _, q := range []string{"invoice", "invoice header", "Invoic"} {
		results, total, facets, err := repo.Search(ctx, Filter{Query: q, Limit: 1})
		if err != nil || len(results) != 1 {
			t.Fatalf("%q: results = %v, %v", q, results, err)
		}
		if total < 1 || facets.Types[ResultTypeGlossary] != total {
			t.Fatalf("%q: total = %d, types = %v; want the matches counted beyond the page", q, total, facets.Types)
		}
	}
	_, total, _, err := repo.Search(ctx, Filter{Query: "invoice", Limit: 1})
	if err != nil || total != 3 {
		t.Fatalf("invoice: total = %d, %v", total, err)
	}
}

func TestHardDeletingASoftDeletedTermKeepsTheCount(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	count := func() (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT count FROM summary_counts WHERE dimension = 'entity_type' AND key = 'glossary'), 0)`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, name := range []string{"Kept", "Gone"} {
		if _, err := pool.Exec(ctx, `INSERT INTO glossary_terms (name, definition) VALUES ($1, 'x')`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE glossary_terms SET deleted_at = now() WHERE name = 'Gone'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM glossary_terms WHERE name = 'Gone'`); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("glossary count = %d, want 1", n)
	}
}
