package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/background"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/assetrule"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/enrichment"
	"github.com/marmotdata/marmot/internal/core/glossary"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/notification"
	"github.com/marmotdata/marmot/internal/core/quality"
	"github.com/marmotdata/marmot/internal/core/team"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/marmotdata/marmot/internal/extensions"
	"github.com/marmotdata/marmot/pkg/config"
	"github.com/marmotdata/marmot/pkg/extension"
	extquality "github.com/marmotdata/marmot/pkg/extension/quality"
)

// extensionServices is what the server lends the extensions.
type extensionServices struct {
	db            *pgxpool.Pool
	domains       domain.Service
	users         user.Service
	teams         *team.Service
	assets        asset.Service
	registry      *metamodel.Registry
	glossary      glossary.Service
	rules         assetrule.Service
	notifications *notification.Service
	quality       *quality.Engine
}

// extensionHost is the server as an extension sees it.
type extensionHost struct {
	extensionServices
	tasks *[]*background.SingletonTask
}

func (h extensionHost) DB() *pgxpool.Pool { return h.db }

// extensionPrincipal adds to a principal what the contract asks in its own words.
type extensionPrincipal struct{ auth.Principal }

func (p extensionPrincipal) IsUser() bool { return p.Type() == auth.PrincipalTypeUser }

func (h extensionHost) Principal(r *http.Request) (extension.Principal, bool) {
	p, ok := common.PrincipalFromContext(r.Context())
	if !ok {
		return nil, false
	}
	return extensionPrincipal{p}, true
}

func (h extensionHost) As(ctx context.Context, userID string) (extension.Principal, context.Context, bool) {
	u, err := h.users.Get(ctx, userID)
	if err != nil || u == nil || !u.Active {
		return nil, ctx, false
	}
	p := auth.NewUserPrincipal(u)
	return extensionPrincipal{p}, context.WithValue(ctx, common.PrincipalContextKey, p), true
}

func (h extensionHost) Schedule(task extension.Task) {
	t := background.NewSingletonTask(background.SingletonConfig{Name: task.Name, DB: h.db, Interval: task.Interval, TaskFn: task.Run})
	t.Start(context.Background())
	*h.tasks = append(*h.tasks, t)
}

func (h extensionHost) Users() extension.Users            { return extensionUsers{h.users} }
func (h extensionHost) Teams() extension.Teams            { return extensionTeams{h.teams} }
func (h extensionHost) Assets() extension.Assets          { return extensionAssets{h.assets, h.registry} }
func (h extensionHost) Glossary() extension.Glossary      { return extensionGlossary{h.glossary} }
func (h extensionHost) Queries() extension.Queries        { return extensionQueries{h.rules} }
func (h extensionHost) Quality() extquality.Engine        { return h.quality }
func (h extensionHost) Notifications() extension.Notifier { return extensionNotifier{h.notifications} }

// extensionErr turns the errors of the core services into the contract's, keeping the text.
func extensionErr(err error) error {
	var invalid *metamodel.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &invalid):
		fields := make([]extension.FieldViolation, 0, len(invalid.Fields))
		for _, v := range invalid.Fields {
			fields = append(fields, extension.FieldViolation{Field: v.Field, Code: v.Code})
		}
		return &extension.FieldsError{Fields: fields}
	case errors.Is(err, asset.ErrVersionConflict):
		return fmt.Errorf("%w: %v", extension.ErrVersionConflict, err)
	case errors.Is(err, domain.ErrForbidden):
		return fmt.Errorf("%w: %v", extension.ErrForbidden, err)
	case errors.Is(err, asset.ErrAssetNotFound), errors.Is(err, asset.ErrNotFound), errors.Is(err, user.ErrUserNotFound),
		errors.Is(err, team.ErrTeamNotFound), errors.Is(err, domain.ErrNotFound), errors.Is(err, glossary.ErrTermNotFound), errors.Is(err, glossary.ErrNotFound):
		return fmt.Errorf("%w: %v", extension.ErrNotFound, err)
	}
	return err
}

type extensionUsers struct{ service user.Service }

func extensionUser(u *user.User, err error) (*extension.User, error) {
	if err != nil || u == nil {
		return nil, extensionErr(err)
	}
	return &extension.User{ID: u.ID, Username: u.Username, Name: u.Name, Active: u.Active}, nil
}

func (s extensionUsers) Get(ctx context.Context, id string) (*extension.User, error) {
	return extensionUser(s.service.Get(ctx, id))
}

func (s extensionUsers) GetUserByUsername(ctx context.Context, username string) (*extension.User, error) {
	return extensionUser(s.service.GetUserByUsername(ctx, username))
}

type extensionTeams struct{ service *team.Service }

func extensionTeam(t *team.Team, err error) (*extension.Team, error) {
	if err != nil || t == nil {
		return nil, extensionErr(err)
	}
	return &extension.Team{ID: t.ID, Name: t.Name}, nil
}

func (s extensionTeams) GetTeam(ctx context.Context, id string) (*extension.Team, error) {
	return extensionTeam(s.service.GetTeam(ctx, id))
}

func (s extensionTeams) GetTeamByName(ctx context.Context, name string) (*extension.Team, error) {
	return extensionTeam(s.service.GetTeamByName(ctx, name))
}

func (s extensionTeams) ListMembers(ctx context.Context, teamID string) ([]*extension.TeamMember, error) {
	members, err := s.service.ListMembers(ctx, teamID)
	if err != nil {
		return nil, extensionErr(err)
	}
	out := make([]*extension.TeamMember, 0, len(members))
	for _, m := range members {
		out = append(out, &extension.TeamMember{UserID: m.UserID})
	}
	return out, nil
}

type extensionAssets struct {
	service  asset.Service
	registry *metamodel.Registry
}

func extensionAsset(a *asset.Asset, err error) (*extension.Asset, error) {
	if err != nil || a == nil {
		return nil, extensionErr(err)
	}
	out := &extension.Asset{ID: a.ID, Type: a.Type, Version: a.Version, Tags: a.Tags}
	if a.MRN != nil {
		out.MRN = *a.MRN
	}
	if a.Name != nil {
		out.Name = *a.Name
	}
	return out, nil
}

func (s extensionAssets) Get(ctx context.Context, id string) (*extension.Asset, error) {
	return extensionAsset(s.service.Get(ctx, id))
}

func (s extensionAssets) PatchFields(ctx context.Context, id string, version int64, fields map[string]any) (*extension.Asset, error) {
	return extensionAsset(s.service.PatchFields(ctx, id, version, fields))
}

func (s extensionAssets) AddTag(ctx context.Context, id, tag string) (*extension.Asset, error) {
	return extensionAsset(s.service.AddTag(ctx, id, tag))
}

func (s extensionAssets) RemoveTag(ctx context.Context, id, tag string) (*extension.Asset, error) {
	return extensionAsset(s.service.RemoveTag(ctx, id, tag))
}

func (s extensionAssets) AddTerms(ctx context.Context, assetID string, termIDs []string, source, createdBy string) error {
	return extensionErr(s.service.AddTerms(ctx, assetID, termIDs, source, createdBy))
}

func (s extensionAssets) RemoveTerm(ctx context.Context, assetID, termID string) error {
	return extensionErr(s.service.RemoveTerm(ctx, assetID, termID))
}

func (s extensionAssets) Coerce(fieldID string, value any) (any, bool, error) {
	field, ok := s.registry.Field(fieldID)
	if !ok || !slices.Contains(field.AppliesTo.EffectiveKinds(), "asset") {
		return value, false, nil
	}
	coerced, ok := metamodel.Coerce(field, value)
	if !ok {
		return nil, true, &extension.FieldsError{Fields: []extension.FieldViolation{{Field: fieldID, Code: "type"}}}
	}
	return coerced, true, nil
}

type extensionGlossary struct{ service glossary.Service }

func (s extensionGlossary) GetByName(ctx context.Context, name string) (*extension.Term, error) {
	term, err := s.service.GetByName(ctx, name)
	if err != nil || term == nil {
		return nil, extensionErr(err)
	}
	return &extension.Term{ID: term.ID, Name: term.Name}, nil
}

type extensionQueries struct{ rules assetrule.Service }

func (s extensionQueries) Match(ctx context.Context, query string, limit int) ([]string, int, error) {
	q := strings.TrimSpace(query)
	preview, err := s.rules.PreviewRule(ctx, assetrule.RulePreviewInput{RuleType: enrichment.RuleTypeQuery, QueryExpression: &q}, limit)
	if err != nil {
		return nil, 0, err
	}
	if len(preview.Errors) > 0 {
		return nil, 0, errors.New(strings.Join(preview.Errors, "; "))
	}
	return preview.AssetIDs, preview.AssetCount, nil
}

type extensionNotifier struct{ service *notification.Service }

func (s extensionNotifier) Create(ctx context.Context, n extension.Notification) error {
	recipients := make([]notification.Recipient, 0, len(n.Recipients))
	for _, r := range n.Recipients {
		recipients = append(recipients, notification.Recipient{Type: r.Type, ID: r.ID})
	}
	return s.service.Create(ctx, notification.CreateNotificationInput{Recipients: recipients, Type: n.Type, Title: n.Title, Message: n.Message, Data: n.Data})
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

func (d extensionDomains) DomainOf(ctx context.Context, kind, entityID string) (string, error) {
	id, err := d.service.DomainOf(ctx, domain.Kind(kind), entityID)
	return id, extensionErr(err)
}

func (d extensionDomains) Roles(ctx context.Context, domainID string) ([]extension.RoleAssignment, error) {
	roles, err := d.service.Roles(ctx, domainID)
	if err != nil {
		return nil, extensionErr(err)
	}
	out := make([]extension.RoleAssignment, 0, len(roles))
	for _, r := range roles {
		out = append(out, extension.RoleAssignment{Role: string(r.Role), SubjectType: string(r.SubjectType), SubjectID: r.SubjectID, SubjectMissing: r.SubjectMissing})
	}
	return out, nil
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

// extensionRoutes mounts the extensions and returns the tasks they scheduled, for the server to stop.
func extensionRoutes(services extensionServices, authService auth.Service, cfg *config.Config) (extensions.Handler, []*background.SingletonTask, error) {
	var tasks []*background.SingletonTask
	handler, err := extensions.Routes(extensionHost{extensionServices: services, tasks: &tasks}, func(resource, action string) []func(http.HandlerFunc) http.HandlerFunc {
		guard := []func(http.HandlerFunc) http.HandlerFunc{common.WithAuth(services.users, authService, cfg)}
		if resource != "" {
			guard = append(guard, common.RequirePermission(services.users, resource, action))
		}
		return guard
	})
	return handler, tasks, err
}
