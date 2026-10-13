package asset

import (
	"context"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestSearchWithoutTextListsByRecencyInAStableOrder(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, name, mrn, type, providers, created_by, updated_at) VALUES
		('a-old', 'orders', 'mrn://table/pg/orders', 'Table', '{pg}', 'test', now() - interval '2 days'),
		('a-new', 'customers', 'mrn://table/pg/customers', 'Table', '{pg}', 'test', now()),
		('a-mid', 'payments', 'mrn://topic/kafka/payments', 'Topic', '{kafka}', 'test', now() - interval '1 day')`); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool, noopRecorder{})
	ids := func(filter SearchFilter) []string {
		t.Helper()
		assets, total, _, err := repo.Search(ctx, filter, false)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, a := range assets {
			out = append(out, a.ID)
		}
		if filter.Offset == 0 && filter.Query == "" && total != 3 {
			t.Fatalf("total = %d", total)
		}
		return out
	}

	if got := ids(SearchFilter{Limit: 10}); len(got) != 3 || got[0] != "a-new" || got[1] != "a-mid" || got[2] != "a-old" {
		t.Fatalf("listing = %v", got)
	}
	if got := ids(SearchFilter{Limit: 1, Offset: 1}); len(got) != 1 || got[0] != "a-mid" {
		t.Fatalf("second page = %v", got)
	}
	if got := ids(SearchFilter{Query: `@type: "Table"`, Limit: 10}); len(got) != 2 || got[0] != "a-new" {
		t.Fatalf("filter without text = %v", got)
	}
	if got := ids(SearchFilter{Query: "payments", Limit: 10}); len(got) != 1 || got[0] != "a-mid" {
		t.Fatalf("text still ranks = %v", got)
	}
}
