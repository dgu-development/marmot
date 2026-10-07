package domains

import (
	"net/http"
	"strconv"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/domain"
)

// WithOntologyVersions adds the routes that publish and restore versions of a domain's ontology.
func (h *Handler) WithOntologyVersions(versions *domain.OntologyVersions) *Handler {
	h.versions = versions
	return h
}

func (h *Handler) ontologyRoutes(view []func(http.HandlerFunc) http.HandlerFunc) []common.Route {
	if h.versions == nil {
		return nil
	}
	return []common.Route{
		{Path: "/api/v1/ontologies/{domainId}/versions", Method: http.MethodGet, Handler: h.listOntologyVersions, Middleware: view},
		{Path: "/api/v1/ontologies/{domainId}/versions", Method: http.MethodPost, Handler: h.publishOntologyVersion, Middleware: view},
		{Path: "/api/v1/ontologies/{domainId}/versions/{version}", Method: http.MethodGet, Handler: h.getOntologyVersion, Middleware: view},
		{Path: "/api/v1/ontologies/{domainId}/versions/{version}/restore", Method: http.MethodPost, Handler: h.restoreOntologyVersion, Middleware: view},
	}
}

type PublishOntologyRequest struct {
	Note string `json:"note"`
} // @name PublishOntologyRequest

const maxOntologyNote = 500

func actorID(r *http.Request) *string {
	if p, ok := common.PrincipalFromContext(r.Context()); ok {
		id := p.ID()
		return &id
	}
	return nil
}

func versionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		respondCode(w, http.StatusBadRequest, "invalid_input", "Invalid version")
		return 0, false
	}
	return version, true
}

// @Summary List the versions of a domain's ontology
// @Description Newest first, without their snapshots.
// @Tags ontologies
// @Produce json
// @Param domainId path string true "Domain ID"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {array} domain.OntologyVersion
// @ID listOntologyVersions
// @Router /api/v1/ontologies/{domainId}/versions [get]
func (h *Handler) listOntologyVersions(w http.ResponseWriter, r *http.Request) {
	if _, err := h.service.Get(r.Context(), r.PathValue("domainId")); err != nil {
		respondError(w, err, "list ontology versions")
		return
	}
	versions, err := h.versions.List(r.Context(), r.PathValue("domainId"))
	if err != nil {
		respondError(w, err, "list ontology versions")
		return
	}
	common.RespondJSON(w, http.StatusOK, versions)
}

// @Summary Publish a version of a domain's ontology
// @Description Stores the domain's glossary terms and its ontology metadata as they are now. Needs to administer the domain.
// @Tags ontologies
// @Accept json
// @Produce json
// @Param domainId path string true "Domain ID"
// @Param request body PublishOntologyRequest false "Note"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 201 {object} domain.OntologyVersion
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @ID publishOntologyVersion
// @Router /api/v1/ontologies/{domainId}/versions [post]
func (h *Handler) publishOntologyVersion(w http.ResponseWriter, r *http.Request) {
	var req PublishOntologyRequest
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	if len(req.Note) > maxOntologyNote {
		respondCode(w, http.StatusBadRequest, "invalid_input", "Note too long")
		return
	}
	if !h.requireAdmin(w, r, r.PathValue("domainId")) {
		return
	}
	version, err := h.versions.Publish(r.Context(), r.PathValue("domainId"), req.Note, actorID(r))
	if err != nil {
		respondError(w, err, "publish ontology version")
		return
	}
	common.RespondJSON(w, http.StatusCreated, version)
}

// @Summary Get a version of a domain's ontology
// @Description With its snapshot: the terms and the domain's ontology metadata as they were.
// @Tags ontologies
// @Produce json
// @Param domainId path string true "Domain ID"
// @Param version path int true "Version number"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} domain.OntologyVersion
// @Failure 404 {object} ErrorResponse
// @ID getOntologyVersion
// @Router /api/v1/ontologies/{domainId}/versions/{version} [get]
func (h *Handler) getOntologyVersion(w http.ResponseWriter, r *http.Request) {
	number, ok := versionParam(w, r)
	if !ok {
		return
	}
	if _, err := h.service.Get(r.Context(), r.PathValue("domainId")); err != nil {
		respondError(w, err, "get ontology version")
		return
	}
	version, err := h.versions.Get(r.Context(), r.PathValue("domainId"), number)
	if err != nil {
		respondError(w, err, "get ontology version")
		return
	}
	common.RespondJSON(w, http.StatusOK, version)
}

// @Summary Restore a version of a domain's ontology
// @Description Puts the terms back as they were, soft-deletes those created since and publishes the replaced state first, which is the version returned. Needs to administer the domain.
// @Tags ontologies
// @Produce json
// @Param domainId path string true "Domain ID"
// @Param version path int true "Version number"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} domain.OntologyVersion
// @Failure 403 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @ID restoreOntologyVersion
// @Router /api/v1/ontologies/{domainId}/versions/{version}/restore [post]
func (h *Handler) restoreOntologyVersion(w http.ResponseWriter, r *http.Request) {
	number, ok := versionParam(w, r)
	if !ok {
		return
	}
	if !h.requireAdmin(w, r, r.PathValue("domainId")) {
		return
	}
	saved, err := h.versions.Restore(r.Context(), r.PathValue("domainId"), number, actorID(r))
	if err != nil {
		respondError(w, err, "restore ontology version")
		return
	}
	common.RespondJSON(w, http.StatusOK, saved)
}
