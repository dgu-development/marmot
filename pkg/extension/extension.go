// Package extension is the contract between the server and the capabilities
// compiled into it from outside this repository. It is the only part of the
// server an extension may import: everything under internal/ stays private.
//
// An extension registers itself from an init function; the image build links
// the extensions its lock names by generating blank imports of them.
package extension

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Extension is a capability with its own routes and, optionally, its own tables.
type Extension interface {
	// ID names the extension in its routes (/api/v1/ext/<id>/...) and in its
	// migration track (public.ext_<id>_schema_version). Lowercase letters,
	// digits and underscores, starting with a letter.
	ID() string
	// Migrations holds the extension's *.sql files in tern format, or nil.
	// Its tables are named ext_<id>_*; core tables are not altered from here.
	Migrations() fs.FS
	// Routes is called once at start-up.
	Routes(host Host) []Route
}

// Route is served under /api/v1/ext/<id>. Every route requires an
// authenticated caller; Resource and Action add a permission check when set.
type Route struct {
	Method string
	// Path is relative to the extension's prefix and starts with a slash.
	Path     string
	Handler  http.HandlerFunc
	Resource string
	Action   string
}

// Principal is who is calling.
type Principal interface {
	ID() string
	DisplayName() string
	HasPermission(resource, action string) bool
}

// Domains answers what the caller may do in a domain. It is nil on a server
// without domains.
type Domains interface {
	// Exists reports whether id names a domain.
	Exists(ctx context.Context, id string) (bool, error)
	// MayWrite: edit the domain's entities. While writes are not scoped by
	// domain it is true and the route's own permission decides.
	MayWrite(r *http.Request, id string) (bool, error)
	// MayAdminister: change the domain itself.
	MayAdminister(r *http.Request, id string) (bool, error)
}

// Host is what the server hands an extension.
type Host interface {
	DB() *pgxpool.Pool
	Principal(r *http.Request) (Principal, bool)
	Domains() Domains
}

var (
	mu         sync.Mutex
	registered = map[string]Extension{}
	validID    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
)

// Register adds an extension. It panics on an invalid or repeated ID: both
// are build mistakes, and a server that started without the extension its
// lock names would hide them.
func Register(e Extension) {
	mu.Lock()
	defer mu.Unlock()
	id := e.ID()
	if !validID.MatchString(id) {
		panic(fmt.Sprintf("extension: invalid id %q", id))
	}
	if _, taken := registered[id]; taken {
		panic(fmt.Sprintf("extension: %q registered twice", id))
	}
	registered[id] = e
}

// Registered returns the extensions in ID order.
func Registered() []Extension {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Extension, 0, len(registered))
	for _, e := range registered {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
