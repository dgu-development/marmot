package extensions_test

import (
	"context"
	"io/fs"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/marmotdata/marmot/internal/extensions"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
	"github.com/marmotdata/marmot/pkg/extension"
)

type sample struct{ routes []extension.Route }

func (sample) ID() string { return "sample" }

func (sample) Migrations() fs.FS {
	return fstest.MapFS{"001_notes.sql": {Data: []byte("CREATE TABLE ext_sample_notes (id serial PRIMARY KEY, domain_id uuid REFERENCES domains(id));\n---- create above / drop below ----\nDROP TABLE ext_sample_notes;\n")}}
}

func (s sample) Routes(extension.Host) []extension.Route { return s.routes }

var registeredSample = &sample{}

func init() { extension.Register(registeredSample) }

func TestExtensionGetsItsOwnTrackAndPrefix(t *testing.T) {
	// TempDB runs the server's set-up, which now ends with the extensions' tracks.
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var version int
	if err := pool.QueryRow(ctx, "SELECT version FROM "+extensions.VersionTable("sample")).Scan(&version); err != nil || version != 1 {
		t.Fatalf("extension track: version %d, %v", version, err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO ext_sample_notes DEFAULT VALUES"); err != nil {
		t.Fatalf("extension table: %v", err)
	}
}

func TestRoutesAreServedUnderTheExtensionPrefix(t *testing.T) {
	ok := func(http.ResponseWriter, *http.Request) {}
	guarded := 0
	guard := func(resource, action string) []func(http.HandlerFunc) http.HandlerFunc {
		if resource == "glossary" && action == "manage" {
			guarded++
		}
		return nil
	}

	registeredSample.routes = []extension.Route{
		{Method: http.MethodGet, Path: "/notes/{id}", Handler: ok},
		{Method: http.MethodPost, Path: "/notes", Handler: ok, Resource: "glossary", Action: "manage"},
	}
	routes, err := extensions.Routes(nil, guard)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Path != "/api/v1/ext/sample/notes/{id}" || guarded != 1 {
		t.Fatalf("routes: %+v, guarded %d", routes, guarded)
	}

	for name, bad := range map[string]extension.Route{
		"path outside the prefix": {Method: http.MethodGet, Path: "notes", Handler: ok},
		"no handler":              {Method: http.MethodGet, Path: "/notes"},
		"half a permission":       {Method: http.MethodGet, Path: "/notes", Handler: ok, Resource: "glossary"},
	} {
		registeredSample.routes = []extension.Route{bad}
		if _, err := extensions.Routes(nil, guard); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestRegisterRefusesBadAndRepeatedIDs(t *testing.T) {
	for name, e := range map[string]extension.Extension{"repeated": sample{}, "invalid": badID{}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s id: registered", name)
				}
			}()
			extension.Register(e)
		}()
	}
}

type badID struct{ sample }

func (badID) ID() string { return "Not-Valid" }
