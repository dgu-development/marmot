package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/knowledge"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/marmotdata/marmot/pkg/config"
	"github.com/rs/zerolog/log"
)

var ErrForbidden = errors.New("knowledge permission denied")

type Access struct {
	Users          user.Service
	Guard          *domain.Guard
	DomainsEnabled bool
}

func (a Access) Check(ctx context.Context, write bool) error {
	resources := []string{"assets", "glossary"}
	if a.DomainsEnabled {
		resources = append(resources, "domains")
	}
	for _, resource := range resources {
		ok, err := common.HasPermission(ctx, a.Users, resource, "view")
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
	}
	if write {
		ok, err := common.HasPermission(ctx, a.Users, "knowledge", "write")
		if err != nil {
			return err
		}
		if !ok {
			return ErrForbidden
		}
		if a.Guard != nil {
			return a.Guard.AuthorizeGlobal(ctx)
		}
	}
	return nil
}

// PublicPage removes candidate data even from a published page with a newer draft.
func PublicPage(p *knowledge.Page) {
	p.DraftContent = ""
	p.DraftHash = ""
	p.DraftSourceHash = ""
	p.DraftSources = nil
	p.DraftMode = ""
	p.Sources = p.PublishedSources
}

type Handler struct {
	service *knowledge.Service
	access  Access
	auth    auth.Service
	config  *config.Config
}

func NewHandler(s *knowledge.Service, a Access, authService auth.Service, cfg *config.Config) *Handler {
	return &Handler{service: s, access: a, auth: authService, config: cfg}
}

func (h *Handler) Routes() []common.Route {
	routes := []common.Route{
		{Path: "/api/v1/knowledge", Method: http.MethodGet, Handler: h.list},
		{Path: "/api/v1/knowledge/settings", Method: http.MethodGet, Handler: h.settings},
		{Path: "/api/v1/knowledge/context", Method: http.MethodGet, Handler: h.context},
		{Path: "/api/v1/knowledge/compile", Method: http.MethodPost, Handler: h.compileBatch},
		{Path: "/api/v1/knowledge/pages/{entityType}/{entityId}", Method: http.MethodGet, Handler: h.get},
		{Path: "/api/v1/knowledge/pages/{entityType}/{entityId}/compile", Method: http.MethodPost, Handler: h.compile},
		{Path: "/api/v1/knowledge/pages/{entityType}/{entityId}/publish", Method: http.MethodPost, Handler: h.publish},
	}
	for i := range routes {
		routes[i].Middleware = []func(http.HandlerFunc) http.HandlerFunc{common.WithAuth(h.access.Users, h.auth, h.config), h.require(routes[i].Method == http.MethodPost)}
	}
	return routes
}

func (h *Handler) require(write bool) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := h.access.Check(r.Context(), write); err != nil {
				respondError(w, err)
				return
			}
			next(w, r)
		}
	}
}

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden), errors.Is(err, domain.ErrForbidden):
		common.RespondError(w, http.StatusForbidden, "Knowledge permission denied")
	case errors.Is(err, knowledge.ErrNotFound):
		common.RespondError(w, http.StatusNotFound, "Knowledge entity not found")
	case errors.Is(err, knowledge.ErrInvalid):
		common.RespondError(w, http.StatusBadRequest, "Invalid knowledge input")
	case errors.Is(err, knowledge.ErrTooLarge):
		common.RespondError(w, http.StatusUnprocessableEntity, "Knowledge sources exceed compilation limit")
	case errors.Is(err, knowledge.ErrConflict):
		common.RespondError(w, http.StatusConflict, "The draft or its sources changed. Compile and review again.")
	default:
		log.Error().Err(err).Msg("Knowledge operation failed")
		common.RespondError(w, http.StatusInternalServerError, "Knowledge operation failed")
	}
}

func entity(r *http.Request) knowledge.Entity {
	return knowledge.Entity{Kind: r.PathValue("entityType"), ID: r.PathValue("entityId")}
}

// @Summary Browse WikiLLM entities and publication status
// @Tags knowledge
// @Produce json
// @Param q query string false "Search"
// @Param limit query int false "Limit" default(20)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} knowledge.ListResult
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/knowledge [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	result, err := h.service.List(r.Context(), q.Get("q"), common.ParseLimit(q.Get("limit"), 20, 50), common.ParseOffset(q.Get("offset")))
	if err != nil {
		respondError(w, err)
		return
	}
	if err := h.access.Check(r.Context(), true); err != nil {
		if !errors.Is(err, ErrForbidden) && !errors.Is(err, domain.ErrForbidden) {
			respondError(w, err)
			return
		}
		for i := range result.Pages {
			PublicPage(&result.Pages[i])
		}
	}
	common.RespondJSON(w, http.StatusOK, result)
}

// @Summary Read a WikiLLM entity page
// @Tags knowledge
// @Produce json
// @Param entityType path string true "Entity type" Enums(asset,data_product,glossary_term,domain)
// @Param entityId path string true "Entity ID"
// @Success 200 {object} knowledge.Page
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/knowledge/pages/{entityType}/{entityId} [get]
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.service.Get(r.Context(), entity(r))
	if err != nil {
		respondError(w, err)
		return
	}
	if err := h.access.Check(r.Context(), true); err != nil {
		if !errors.Is(err, ErrForbidden) && !errors.Is(err, domain.ErrForbidden) {
			respondError(w, err)
			return
		}
		PublicPage(p)
	}
	common.RespondJSON(w, http.StatusOK, p)
}

// @Summary Retrieve fresh published knowledge with citations
// @Tags knowledge
// @Produce json
// @Param q query string false "Task or question"
// @Param limit query int false "Limit" default(5)
// @Success 200 {array} knowledge.Page
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/knowledge/context [get]
func (h *Handler) context(w http.ResponseWriter, r *http.Request) {
	pages, err := h.service.Context(r.Context(), r.URL.Query().Get("q"), common.ParseLimit(r.URL.Query().Get("limit"), 5, 10))
	if err != nil {
		respondError(w, err)
		return
	}
	for i := range pages {
		PublicPage(&pages[i])
	}
	common.RespondJSON(w, http.StatusOK, pages)
}

// @Summary Compile an entity into a review candidate
// @Tags knowledge
// @Produce json
// @Param entityType path string true "Entity type" Enums(asset,data_product,glossary_term,domain)
// @Param entityId path string true "Entity ID"
// @Success 200 {object} knowledge.Page
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/knowledge/pages/{entityType}/{entityId}/compile [post]
func (h *Handler) compile(w http.ResponseWriter, r *http.Request) {
	p, err := h.service.Compile(r.Context(), entity(r))
	if err != nil {
		respondError(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, p)
}

// @Summary Compile a bounded batch of catalog entities
// @Tags knowledge
// @Produce json
// @Param limit query int false "Batch size" default(1)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} knowledge.BatchResult
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/knowledge/compile [post]
func (h *Handler) compileBatch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	result, err := h.service.CompileBatch(r.Context(), common.ParseLimit(q.Get("limit"), 1, 1), common.ParseOffset(q.Get("offset")))
	if err != nil {
		respondError(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, result)
}

type PublishRequest struct {
	DraftHash string `json:"draft_hash"`
}

// @Summary Publish the exact reviewed draft if its sources are still current
// @Tags knowledge
// @Accept json
// @Produce json
// @Param entityType path string true "Entity type" Enums(asset,data_product,glossary_term,domain)
// @Param entityId path string true "Entity ID"
// @Param request body PublishRequest true "Reviewed draft hash"
// @Success 200 {object} knowledge.Page
// @Failure 409 {object} common.ErrorResponse
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/knowledge/pages/{entityType}/{entityId}/publish [post]
func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	p, ok := common.PrincipalFromContext(r.Context())
	if !ok {
		common.RespondError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	var in PublishRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		common.RespondError(w, http.StatusBadRequest, "Invalid publish request")
		return
	}
	result, err := h.service.Publish(r.Context(), entity(r), in.DraftHash, string(p.Type())+":"+p.ID())
	if err != nil {
		respondError(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, result)
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	canWrite := false
	if err := h.access.Check(r.Context(), true); err == nil {
		canWrite = true
	} else if !errors.Is(err, ErrForbidden) && !errors.Is(err, domain.ErrForbidden) {
		respondError(w, err)
		return
	}
	mode := "extractive"
	if h.config.Knowledge.Endpoint != "" {
		mode = "llm"
	}
	common.RespondJSON(w, http.StatusOK, map[string]any{"can_write": canWrite, "mode": mode, "interval_seconds": h.config.Knowledge.IntervalSeconds})
}
