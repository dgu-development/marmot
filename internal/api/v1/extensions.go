package v1

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/marmotdata/marmot/internal/extensions"
	"github.com/marmotdata/marmot/pkg/config"
	"github.com/marmotdata/marmot/pkg/extension"
)

// extensionHost is the server as an extension sees it.
type extensionHost struct {
	db      *pgxpool.Pool
	domains domain.Service
}

func (h extensionHost) DB() *pgxpool.Pool { return h.db }

func (h extensionHost) Principal(r *http.Request) (extension.Principal, bool) {
	return common.PrincipalFromContext(r.Context())
}

func (h extensionHost) Domains() extension.Domains {
	if h.domains == nil {
		return nil
	}
	return extensionDomains{h.domains}
}

type extensionDomains struct{ service domain.Service }

func (d extensionDomains) Exists(ctx context.Context, id string) (bool, error) {
	_, err := d.service.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (d extensionDomains) can(r *http.Request, id string, action domain.Action) (bool, error) {
	principal, ok := common.PrincipalFromContext(r.Context())
	if !ok {
		return false, nil
	}
	if principal.HasPermission("domains", "manage") {
		return true, nil
	}
	target, err := d.service.Get(r.Context(), id)
	if err != nil {
		return false, err
	}
	scope, err := d.service.Scope(r.Context(), principal)
	if err != nil {
		return false, err
	}
	return scope.Can(action, target.Path), nil
}

func (d extensionDomains) MayAdminister(r *http.Request, id string) (bool, error) {
	return d.can(r, id, domain.ActionAdmin)
}

func (d extensionDomains) MayWrite(r *http.Request, id string) (bool, error) {
	state, err := d.service.Enforcement(r.Context())
	if err != nil {
		return false, err
	}
	if !state.Write {
		return true, nil
	}
	return d.can(r, id, domain.ActionWrite)
}

func extensionRoutes(db *pgxpool.Pool, domains domain.Service, users user.Service, authService auth.Service, cfg *config.Config) (extensions.Handler, error) {
	return extensions.Routes(extensionHost{db: db, domains: domains}, func(resource, action string) []func(http.HandlerFunc) http.HandlerFunc {
		guard := []func(http.HandlerFunc) http.HandlerFunc{common.WithAuth(users, authService, cfg)}
		if resource != "" {
			guard = append(guard, common.RequirePermission(users, resource, action))
		}
		return guard
	})
}
