package search

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestSearchIgnoresAccentsInBothDirections(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	for _, a := range []struct{ name, description string }{
		{"órdenes_históricas", "Pedidos de años anteriores"},
		{"ordenes_actuales", "Pedidos en curso"},
		{"clientes", "Clasificación de datos personales"},
		{"café_naïve", "Ñandú y cañón"},
		{"facturas", "Sin relación"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO assets (id, name, mrn, type, providers, description, created_by)
			SELECT gen_random_uuid()::text, $1::text, 'mrn://tabla/obsidian/' || $1::text, 'Tabla', ARRAY['Obsidian'], $2::text, id
			  FROM users WHERE username = 'admin'`, a.name, a.description); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewPostgresRepository(pool, noopRecorder{})

	find := func(q string) []string {
		t.Helper()
		results, _, _, err := repo.Search(ctx, Filter{Query: q, Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		var names []string
		for _, r := range results {
			names = append(names, r.Name)
		}
		sort.Strings(names)
		return names
	}
	has := func(names []string, want ...string) bool {
		for _, w := range want {
			found := false
			for _, n := range names {
				found = found || n == w
			}
			if !found {
				return false
			}
		}
		return true
	}

	tests := []struct {
		query string
		want  []string
		why   string
	}{
		{"ordenes", []string{"órdenes_históricas", "ordenes_actuales"}, "one word, unaccented, finds the accented name (trigram)"},
		{"órdenes", []string{"órdenes_históricas", "ordenes_actuales"}, "one word, accented, finds the unaccented name (trigram)"},
		{"historicas", []string{"órdenes_históricas"}, "a word inside the name"},
		{"or", []string{"órdenes_históricas", "ordenes_actuales"}, "a two-letter prefix"},
		{"clasificacion datos", []string{"clientes"}, "several words, unaccented, find an accented description (full text)"},
		{"clasificación DATOS", []string{"clientes"}, "mixed accents and case"},
		{"anos pedidos", []string{"órdenes_históricas"}, "ñ folds to n as unaccent defines it"},
		{"cafe naive", []string{"café_naïve"}, "diaeresis and acute"},
		{`@name contains "historicas"`, []string{"órdenes_históricas"}, "contains ignores accents"},
		{`@name contains "históricas"`, []string{"órdenes_históricas"}, "contains with the accent"},
		{`@name contains "ORDENES"`, []string{"órdenes_históricas", "ordenes_actuales"}, "contains ignores case"},
		{`@provider contains "obsid"`, []string{"órdenes_históricas", "ordenes_actuales", "clientes", "café_naïve", "facturas"}, "provider contains"},
	}
	for _, tc := range tests {
		got := find(tc.query)
		if !has(got, tc.want...) {
			t.Errorf("%q (%s): got %v, want at least %v", tc.query, tc.why, got, tc.want)
		}
	}

	// What must not widen: equality of identifiers stays exact apart from case.
	if got := find(`@name: "ordenes_historicas"`); has(got, "órdenes_históricas") {
		t.Errorf("equality matched across accents: %v", got)
	}
	if got := find(`@name: "ÓRDENES_HISTÓRICAS"`); !has(got, "órdenes_históricas") {
		t.Errorf("equality lost its case-insensitivity: %v", got)
	}
	// And unrelated words still miss.
	if got := find("zzzqqq"); len(got) != 0 {
		t.Errorf("nonsense matched %v", got)
	}
}

func TestNoSearchColumnStillUsesTheEnglishConfiguration(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT table_name, generation_expression
		  FROM information_schema.columns
		 WHERE column_name = 'search_text' AND generation_expression IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var checked int
	for rows.Next() {
		var table, expr string
		if err := rows.Scan(&table, &expr); err != nil {
			t.Fatal(err)
		}
		checked++
		if strings.Contains(expr, "english") || !strings.Contains(expr, "dgu_search") {
			t.Errorf("%s.search_text is generated with %s", table, expr)
		}
	}
	if checked < 7 {
		t.Errorf("only %d generated search_text columns found", checked)
	}
	var stale int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = 'public' AND p.proname LIKE 'search_index_%_trigger'
		   AND pg_get_functiondef(p.oid) LIKE '%''english''%'`).Scan(&stale); err != nil || stale != 0 {
		t.Errorf("sync triggers still using english: %d, %v", stale, err)
	}
}

func TestNoQueryInTheCodeBaseUsesTheEnglishConfiguration(t *testing.T) {
	// Queries and stored vectors must agree on the configuration or nothing matches; a new
	// query written against 'english' would silently miss every accented word.
	out, err := grepGo(t, "'english'")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("'english' text search configuration used in: %v", out)
	}
}

// grepGo lists the non-test Go files under internal/ that contain needle.
func grepGo(t *testing.T, needle string) ([]string, error) {
	t.Helper()
	var found []string
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), needle) {
			found = append(found, path)
		}
		return nil
	})
	return found, err
}
