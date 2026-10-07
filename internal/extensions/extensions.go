// Package extensions runs what pkg/extension registers: each extension's
// migration track and its routes.
package extensions

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/tern/v2/migrate"
	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/pkg/extension"
	"github.com/rs/zerolog/log"
)

// VersionTable is where an extension's applied version is recorded.
func VersionTable(id string) string { return "public.ext_" + id + "_schema_version" }

// Migrate applies the pending migrations of every registered extension, each
// in its own track. It runs after the core and fork tracks, whose tables an
// extension may reference.
func Migrate(ctx context.Context, conn *pgx.Conn) error {
	for _, e := range extension.Registered() {
		files := e.Migrations()
		if files == nil {
			continue
		}
		migrator, err := migrate.NewMigrator(ctx, conn, VersionTable(e.ID()))
		if err != nil {
			return fmt.Errorf("creating migrator of extension %s: %w", e.ID(), err)
		}
		id := e.ID()
		migrator.OnStart = func(sequence int32, name, direction, _ string) {
			log.Info().Str("extension", id).Int32("sequence", sequence).Str("name", name).Str("direction", direction).Msg("Running extension migration")
		}
		if err := migrator.LoadMigrations(files); err != nil {
			return fmt.Errorf("loading migrations of extension %s: %w", id, err)
		}
		if err := migrator.Migrate(ctx); err != nil {
			return fmt.Errorf("running migrations of extension %s: %w", id, err)
		}
	}
	return nil
}

// Middleware builds what guards a route: authentication always, and the
// permission the route names.
type Middleware func(resource, action string) []func(http.HandlerFunc) http.HandlerFunc

// Routes returns every extension's routes under /api/v1/ext/<id>.
func Routes(host extension.Host, guard Middleware) ([]common.Route, error) {
	var out []common.Route
	for _, e := range extension.Registered() {
		for _, route := range e.Routes(host) {
			if !strings.HasPrefix(route.Path, "/") || route.Handler == nil || route.Method == "" {
				return nil, fmt.Errorf("extension %s: invalid route %q %q", e.ID(), route.Method, route.Path)
			}
			if (route.Resource == "") != (route.Action == "") {
				return nil, fmt.Errorf("extension %s: route %s names half a permission", e.ID(), route.Path)
			}
			out = append(out, common.Route{
				Path:       "/api/v1/ext/" + e.ID() + route.Path,
				Method:     route.Method,
				Handler:    route.Handler,
				Middleware: guard(route.Resource, route.Action),
			})
		}
	}
	return out, nil
}

// Handler adapts the routes to the server's handler list.
type Handler []common.Route

func (h Handler) Routes() []common.Route { return h }
