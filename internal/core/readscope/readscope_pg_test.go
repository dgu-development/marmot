package readscope_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/dataproduct"
	"github.com/marmotdata/marmot/internal/core/glossary"
	"github.com/marmotdata/marmot/internal/core/lineage"
	"github.com/marmotdata/marmot/internal/core/readscope"
	"github.com/marmotdata/marmot/internal/core/search"
	"github.com/marmotdata/marmot/internal/metrics"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

type noopRecorder struct{ metrics.Recorder }

func (noopRecorder) RecordDBQuery(context.Context, string, time.Duration, bool) {}

type outsiderKey struct{}

// catalog is one open domain and one restricted, each with an asset, a term
// and a product; the two assets are joined by lineage and share a tag.
type catalog struct {
	pool                       *pgxpool.Pool
	restricted                 string
	openTerm, secretTerm       string
	openProduct, secretProduct string
	outsider, insider          context.Context
}

func seed(t *testing.T) catalog {
	t.Helper()
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	c := catalog{pool: pool, insider: ctx}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var open string
	must(pool.QueryRow(ctx, `WITH g AS (SELECT gen_random_uuid() AS id)
		INSERT INTO domains (id, path, depth, name) SELECT id, '/' || id || '/', 1, 'Open' FROM g RETURNING id::text`).Scan(&open))
	must(pool.QueryRow(ctx, `WITH g AS (SELECT gen_random_uuid() AS id)
		INSERT INTO domains (id, path, depth, name, restricted) SELECT id, '/' || id || '/', 1, 'Secret', true FROM g RETURNING id::text`).Scan(&c.restricted))
	_, err := pool.Exec(ctx, `INSERT INTO assets (id, name, mrn, type, providers, tags, created_by) VALUES
		('a-open', 'orders', 'mrn://table/pg/orders', 'Table', '{pg}', '{shared}', 'test'),
		('a-secret', 'mergers', 'mrn://table/pg/mergers', 'Table', '{pg}', '{shared,classified}', 'test'),
		('a-free', 'loose', 'mrn://topic/kafka/loose', 'Topic', '{kafka}', '{}', 'test')`)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO asset_domains (asset_id, domain_id) VALUES ('a-open', $1), ('a-secret', $2)`, open, c.restricted)
	must(err)
	must(pool.QueryRow(ctx, `INSERT INTO glossary_terms (name, definition) VALUES ('Customer', 'Buys') RETURNING id::text`).Scan(&c.openTerm))
	must(pool.QueryRow(ctx, `INSERT INTO glossary_terms (name, definition) VALUES ('Target', 'To be bought') RETURNING id::text`).Scan(&c.secretTerm))
	_, err = pool.Exec(ctx, `INSERT INTO glossary_term_domains (glossary_term_id, domain_id) VALUES ($1, $2)`, c.secretTerm, c.restricted)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO asset_terms (asset_id, glossary_term_id) VALUES ('a-open', $1), ('a-open', $2), ('a-secret', $1)`, c.openTerm, c.secretTerm)
	must(err)
	must(pool.QueryRow(ctx, `INSERT INTO data_products (name) VALUES ('Sales') RETURNING id::text`).Scan(&c.openProduct))
	must(pool.QueryRow(ctx, `INSERT INTO data_products (name) VALUES ('Deal room') RETURNING id::text`).Scan(&c.secretProduct))
	_, err = pool.Exec(ctx, `INSERT INTO data_product_domains (data_product_id, domain_id) VALUES ($1, $2)`, c.secretProduct, c.restricted)
	must(err)
	_, err = lineage.NewPostgresRepository(pool).CreateDirectLineage(ctx, "mrn://table/pg/mergers", "mrn://table/pg/orders", "DIRECT", "")
	must(err)

	readscope.SetResolver(func(ctx context.Context) ([]string, error) {
		if ctx.Value(outsiderKey{}) != nil {
			return []string{c.restricted}, nil
		}
		return nil, nil
	})
	t.Cleanup(func() { readscope.SetResolver(nil) })
	c.outsider = context.WithValue(ctx, outsiderKey{}, true)
	return c
}

func TestEveryTableOfADomainHasACondition(t *testing.T) {
	c := seed(t)
	db := readscope.Scoped(c.pool)
	for _, table := range readscope.TableNames {
		var all, seen int
		if err := db.QueryRow(c.insider, "SELECT count(*) FROM "+table).Scan(&all); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if err := db.QueryRow(c.outsider, "SELECT count(*) FROM "+table).Scan(&seen); err != nil {
			t.Fatalf("%s as an outsider: %v", table, err)
		}
		if seen > all {
			t.Errorf("%s: an outsider sees %d of %d rows", table, seen, all)
		}
		want := map[string][2]int{
			"assets": {3, 2}, "glossary_terms": {2, 1}, "data_products": {2, 1},
			"asset_terms": {3, 1}, "lineage_edges": {1, 0}, "search_index": {7, 4},
		}
		if w, ok := want[table]; ok && (all != w[0] || seen != w[1]) {
			t.Errorf("%s: %d rows, %d for an outsider; want %d and %d", table, all, seen, w[0], w[1])
		}
	}
}

func TestAssetsOfARestrictedDomainAreMissingForAnOutsider(t *testing.T) {
	c := seed(t)
	repo := asset.NewPostgresRepository(c.pool, noopRecorder{})

	if _, err := repo.Get(c.insider, "a-secret"); err != nil {
		t.Fatalf("an insider reads it: %v", err)
	}
	if _, err := repo.Get(c.outsider, "a-secret"); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("by id: %v", err)
	}
	if _, err := repo.GetByMRN(c.outsider, "mrn://table/pg/mergers"); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("by mrn: %v", err)
	}
	if _, err := repo.Get(c.outsider, "a-free"); err != nil {
		t.Fatalf("an asset without a domain stays readable: %v", err)
	}

	assets, total, counts, err := repo.Search(c.outsider, asset.SearchFilter{Limit: 10}, true)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(assets) != 2 || counts.Types["Table"] != 1 || counts.Tags["classified"] != 0 || counts.Tags["shared"] != 1 {
		t.Fatalf("search: total %d, %d assets, counts %+v", total, len(assets), counts)
	}
	if found, _, _, err := repo.Search(c.outsider, asset.SearchFilter{Query: "mergers", Limit: 10}, false); err != nil || len(found) != 0 {
		t.Fatalf("search by name: %v %v", found, err)
	}

	summary, err := repo.Summary(c.outsider)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Types["Table"].Count != 1 || summary.Tags["classified"] != 0 {
		t.Fatalf("summary counts what is hidden: %+v", summary)
	}
	if tags, err := repo.GetTagSuggestions(c.outsider, "", 10); err != nil || len(tags) != 1 || tags[0] != "shared" {
		t.Fatalf("tag suggestions: %v %v", tags, err)
	}

	terms, err := repo.GetTerms(c.outsider, "a-open")
	if err != nil || len(terms) != 1 || terms[0].TermID != c.openTerm {
		t.Fatalf("terms of a visible asset: %+v %v", terms, err)
	}
	if terms, err := repo.GetTerms(c.outsider, "a-secret"); err != nil || len(terms) != 0 {
		t.Fatalf("terms of a hidden asset: %+v %v", terms, err)
	}
	if assets, total, err := repo.GetAssetsByTerm(c.outsider, c.openTerm, 10, 0); err != nil || total != 1 || len(assets) != 1 {
		t.Fatalf("assets of a term: %d %v", total, err)
	}
	if refs, err := repo.RefsByID(c.outsider, []string{"a-open", "a-secret"}); err != nil || len(refs) != 1 {
		t.Fatalf("refs: %+v %v", refs, err)
	}
}

func TestSearchLeavesOutTheRestrictedDomainInResultsAndFacets(t *testing.T) {
	c := seed(t)
	repo := search.NewPostgresRepository(c.pool, noopRecorder{})

	for name, filter := range map[string]search.Filter{
		"listing": {Limit: 50},
		"by type": {Limit: 50, Types: []search.ResultType{search.ResultTypeAsset, search.ResultTypeGlossary, search.ResultTypeDataProduct}},
		"text":    {Limit: 50, Query: "mergers"},
		"term":    {Limit: 50, Query: "Target"},
		"product": {Limit: 50, Query: "Deal"},
	} {
		results, total, facets, err := repo.Search(c.outsider, filter)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, r := range results {
			if r.ID == "a-secret" || r.ID == c.secretTerm || r.ID == c.secretProduct {
				t.Errorf("%s: %s %s is in the results", name, r.Type, r.Name)
			}
		}
		if filter.Query != "" && total != 0 {
			t.Errorf("%s: total %d gives the match away", name, total)
		}
		if filter.Query == "" && (total != 4 || facets.Types[search.ResultTypeAsset] != 2) {
			t.Errorf("%s: total %d, facets %+v", name, total, facets.Types)
		}
	}
	if _, total, _, err := repo.Search(c.insider, search.Filter{Limit: 50}); err != nil || total != 7 {
		t.Fatalf("an insider sees everything: %d %v", total, err)
	}
}

func TestLineageStopsAtARestrictedNeighbour(t *testing.T) {
	c := seed(t)
	repo := lineage.NewPostgresRepository(c.pool)

	inside, err := repo.GetAssetLineage(c.insider, "a-open", 5, "both")
	if err != nil || len(inside.Edges) != 1 {
		t.Fatalf("an insider sees the edge: %+v %v", inside, err)
	}
	outside, err := repo.GetAssetLineage(c.outsider, "a-open", 5, "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(outside.Edges) != 0 {
		t.Fatalf("edges to a hidden asset: %+v", outside.Edges)
	}
	for _, n := range outside.Nodes {
		if n.ID == "a-secret" || (n.Asset != nil && n.Asset.ID == "a-secret") {
			t.Fatalf("the hidden neighbour is a node: %+v", n)
		}
	}
	if neighbours, err := repo.GetImmediateNeighbors(c.outsider, "mrn://table/pg/orders", "upstream"); err != nil || len(neighbours) != 0 {
		t.Fatalf("neighbours: %v %v", neighbours, err)
	}
}

func TestTermsAndProductsOfARestrictedDomainAreMissing(t *testing.T) {
	c := seed(t)
	terms := glossary.NewPostgresRepository(c.pool, noopRecorder{})
	if _, err := terms.Get(c.outsider, c.secretTerm); err == nil {
		t.Fatal("the hidden term is readable")
	}
	if list, err := terms.List(c.outsider, 0, 10); err != nil || list.Total != 1 || len(list.Terms) != 1 {
		t.Fatalf("terms: %+v %v", list, err)
	}
	if list, err := terms.List(c.insider, 0, 10); err != nil || list.Total != 2 {
		t.Fatalf("an insider lists both: %+v %v", list, err)
	}

	products := dataproduct.NewPostgresRepository(c.pool, noopRecorder{})
	if _, err := products.Get(c.outsider, c.secretProduct); err == nil {
		t.Fatal("the hidden product is readable")
	}
	if list, err := products.List(c.outsider, 0, 10); err != nil || list.Total != 1 {
		t.Fatalf("products: %+v %v", list, err)
	}
}
