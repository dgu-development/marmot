package asset

import (
	"context"
	"testing"
	"time"

	"github.com/marmotdata/marmot/internal/metrics"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

type noopRecorder struct{ metrics.Recorder }

func (noopRecorder) RecordDBQuery(context.Context, string, time.Duration, bool) {}

func TestAssetsByTermListsTheTermAssets(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var termID string
	assetID := "a-orders"
	if err := pool.QueryRow(ctx, `INSERT INTO glossary_terms (name, definition)
		VALUES ('Customer', 'Buys things') RETURNING id`).Scan(&termID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, name, mrn, type, providers, created_by) VALUES
		($1, 'orders', 'mrn://table/pg/orders', 'Table', '{pg}', 'test'),
		('a-other', 'other', 'mrn://table/pg/other', 'Table', '{pg}', 'test')`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO asset_terms (asset_id, glossary_term_id) VALUES ($1, $2)`, assetID, termID); err != nil {
		t.Fatal(err)
	}

	assets, total, err := NewPostgresRepository(pool, noopRecorder{}).GetAssetsByTerm(ctx, termID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(assets) != 1 || assets[0].ID != assetID {
		t.Fatalf("assets = %+v, total = %d", assets, total)
	}
}
