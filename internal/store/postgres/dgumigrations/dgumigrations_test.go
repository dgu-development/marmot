package dgumigrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"
	"github.com/marmotdata/marmot/internal/store/postgres/dgumigrations"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

const unassigned = "00000000-0000-4000-8000-000000000001"

func versions(t *testing.T, pool *pgxpool.Pool) (core, fork int32) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, "SELECT version FROM public.schema_version").Scan(&core); err != nil {
		t.Fatalf("reading core version: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT version FROM "+dgumigrations.VersionTable).Scan(&fork); err != nil {
		t.Fatalf("reading fork version: %v", err)
	}
	return core, fork
}

func forkMigrationCount(t *testing.T) int32 {
	t.Helper()
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var n int32
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			n++
		}
	}
	return n
}

func TestForkTrackRunsAfterCoreAndIsIdempotent(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()

	core, fork := versions(t, pool)
	if want := forkMigrationCount(t); fork != want {
		t.Fatalf("fork version = %d, want %d", fork, want)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if err := dgumigrations.Migrate(ctx, conn.Conn()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if c, f := versions(t, pool); c != core || f != fork {
		t.Fatalf("versions changed on rerun: core %d→%d, fork %d→%d", core, c, fork, f)
	}

	var name string
	var depth int
	if err := pool.QueryRow(ctx, "SELECT name, depth FROM domains WHERE id = $1", unassigned).Scan(&name, &depth); err != nil {
		t.Fatalf("unassigned domain: %v", err)
	}
	if depth != 1 {
		t.Fatalf("unassigned depth = %d, want 1", depth)
	}
}

func TestDomainConstraints(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()

	var root string
	if err := pool.QueryRow(ctx, `
		INSERT INTO domains (path, depth, name) VALUES ('/pending/', 1, 'Finance') RETURNING id`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE domains SET path = '/' || id || '/' WHERE id = $1", root); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"depth over 8", "INSERT INTO domains (parent_id, path, depth, name) VALUES ($1, '/x/', 9, 'Deep')", []any{root}},
		{"root with depth 2", "INSERT INTO domains (path, depth, name) VALUES ('/y/', 2, 'Orphan')", nil},
		{"child with depth 1", "INSERT INTO domains (parent_id, path, depth, name) VALUES ($1, '/z/', 1, 'Flat')", []any{root}},
		{"duplicate root name", "INSERT INTO domains (path, depth, name) VALUES ('/w/', 1, 'finance')", nil},
		{"blank name", "INSERT INTO domains (path, depth, name) VALUES ('/v/', 1, '  ')", nil},
		{"membership for a missing asset", "INSERT INTO asset_domains (asset_id, domain_id) VALUES ('missing', $1)", []any{root}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.sql, tc.args...); err == nil {
				t.Fatal("insert succeeded, want a constraint violation")
			}
		})
	}

	t.Run("sibling names are unique per parent, case-insensitively", func(t *testing.T) {
		insert := "INSERT INTO domains (parent_id, path, depth, name) VALUES ($1, $2, 2, $3)"
		if _, err := pool.Exec(ctx, insert, root, "/"+root+"/a/", "Payments"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, insert, root, "/"+root+"/b/", "PAYMENTS"); err == nil {
			t.Fatal("duplicate sibling name accepted")
		}
	})

	t.Run("deleting an asset drops its membership", func(t *testing.T) {
		asset := pgtest.SeedAsset(t, pool)
		if _, err := pool.Exec(ctx, "INSERT INTO asset_domains (asset_id, domain_id) VALUES ($1, $2)", asset, root); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM assets WHERE id = $1", asset); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM asset_domains WHERE asset_id = $1", asset).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("membership survived the asset: %d rows", n)
		}
	})

	t.Run("a childless domain with members cannot be deleted", func(t *testing.T) {
		var legal string
		if err := pool.QueryRow(ctx, "INSERT INTO domains (path, depth, name) VALUES ('/legal/', 1, 'Legal') RETURNING id").Scan(&legal); err != nil {
			t.Fatal(err)
		}
		asset := pgtest.SeedAsset(t, pool)
		if _, err := pool.Exec(ctx, "INSERT INTO asset_domains (asset_id, domain_id) VALUES ($1, $2)", asset, legal); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM domains WHERE id = $1", legal); err == nil {
			t.Fatal("deleted a domain that still has members")
		}
	})
}

func TestStartingWorkflowsIsGrantedToAdminOnly(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var admin, user int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE r.name = 'admin'), count(*) FILTER (WHERE r.name = 'user')
		  FROM permissions p
		  JOIN role_permissions rp ON rp.permission_id = p.id
		  JOIN roles r ON r.id = rp.role_id
		 WHERE p.resource_type = 'workflows' AND p.action = 'start'`).Scan(&admin, &user)
	if err != nil {
		t.Fatal(err)
	}
	if admin != 1 || user != 0 {
		t.Fatalf("admin has %d and user has %d grants of workflows:start", admin, user)
	}
}

func TestSearchUnaccentMigrationKeepsExistingDataSearchableAndReverses(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	m, err := migrate.NewMigrator(ctx, conn.Conn(), dgumigrations.VersionTable)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.LoadMigrations(os.DirFS("migrations")); err != nil {
		t.Fatal(err)
	}

	// Back to the english vectors, with data indexed the old way.
	if err := m.MigrateTo(ctx, 10); err != nil {
		t.Fatalf("rolling back: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO glossary_terms (name, definition) VALUES ('Clasificación', 'Nivel de sensibilidad')`); err != nil {
		t.Fatal(err)
	}
	var found bool
	if err := pool.QueryRow(ctx, `SELECT search_text @@ websearch_to_tsquery('english', 'clasificacion') FROM search_index WHERE name = 'Clasificación'`).Scan(&found); err != nil || found {
		t.Fatalf("after rollback an unaccented word matched (%v, %v): the vectors are not english again", found, err)
	}

	// Forward again: the existing row, and its search_index copy, become accent-insensitive.
	if err := m.MigrateTo(ctx, 11); err != nil {
		t.Fatalf("migrating up over existing data: %v", err)
	}
	for _, table := range []string{"glossary_terms", "search_index"} {
		var n int
		q := `SELECT count(*) FROM ` + table + ` WHERE name = 'Clasificación' AND search_text @@ websearch_to_tsquery('public.dgu_search', 'clasificacion sensibilidad')`
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: existing row not searchable without accents after the migration (%d, %v)", table, n, err)
		}
	}
}

func TestQualityPermissionsAndAuditorRole(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT r.name, p.action
		  FROM permissions p
		  JOIN role_permissions rp ON rp.permission_id = p.id
		  JOIN roles r ON r.id = rp.role_id
		 WHERE p.resource_type = 'quality' AND r.deleted_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	granted := map[string]map[string]bool{}
	for rows.Next() {
		var role, action string
		if err := rows.Scan(&role, &action); err != nil {
			t.Fatal(err)
		}
		if granted[role] == nil {
			granted[role] = map[string]bool{}
		}
		granted[role][action] = true
	}
	for _, action := range []string{"view", "run", "manage"} {
		if !granted["admin"][action] || !granted["quality_auditor"][action] {
			t.Errorf("admin or quality_auditor lacks quality:%s: %v", action, granted)
		}
	}
	if len(granted["user"]) != 1 || !granted["user"]["view"] {
		t.Errorf("user should only view: %v", granted["user"])
	}
	var system bool
	if err := pool.QueryRow(ctx, `SELECT is_system FROM roles WHERE name = 'quality_auditor' AND deleted_at IS NULL`).Scan(&system); err != nil || system {
		t.Errorf("quality_auditor must exist and be editable: system=%v err=%v", system, err)
	}
}
