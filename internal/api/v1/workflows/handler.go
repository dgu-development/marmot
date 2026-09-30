// Package workflows exposes the fork-only workflow engine over HTTP. It is
// only registered when workflows.enabled is set.
package workflows

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/marmotdata/marmot/internal/core/workflow"
	"github.com/marmotdata/marmot/pkg/config"
	"github.com/rs/zerolog/log"
)

const maxBodyBytes = workflow.MaxDiagramBytes + 4096

type Handler struct {
	service     *workflow.Service
	userService user.Service
	authService auth.Service
	config      *config.Config
}

func NewHandler(service *workflow.Service, userService user.Service, authService auth.Service, config *config.Config) *Handler {
	return &Handler{service: service, userService: userService, authService: authService, config: config}
}

// Every route needs workflows/view. Managing definitions and seeing every
// run needs workflows/manage, which the service checks, so the error names
// the operation rather than a missing middleware.
func (h *Handler) Routes() []common.Route {
	view := []func(http.HandlerFunc) http.HandlerFunc{
		common.WithAuth(h.userService, h.authService, h.config),
		common.RequirePermission(h.userService, "workflows", "view"),
	}
	return []common.Route{
		{Path: "/api/v1/workflows/capabilities", Method: http.MethodGet, Handler: h.capabilities, Middleware: view},
		{Path: "/api/v1/workflows/definitions", Method: http.MethodGet, Handler: h.listDefinitions, Middleware: view},
		{Path: "/api/v1/workflows/definitions", Method: http.MethodPost, Handler: h.createDefinition, Middleware: view},
		{Path: "/api/v1/workflows/definitions/validate", Method: http.MethodPost, Handler: h.validate, Middleware: view},
		{Path: "/api/v1/workflows/definitions/{id}", Method: http.MethodGet, Handler: h.getDefinition, Middleware: view},
		{Path: "/api/v1/workflows/definitions/{id}", Method: http.MethodPut, Handler: h.updateDefinition, Middleware: view},
		{Path: "/api/v1/workflows/definitions/{id}", Method: http.MethodDelete, Handler: h.deleteDefinition, Middleware: view},
		{Path: "/api/v1/workflows/definitions/{id}/bpmn", Method: http.MethodGet, Handler: h.exportDefinition, Middleware: view},
		{Path: "/api/v1/workflows/definitions/{id}/publish", Method: http.MethodPost, Handler: h.publish, Middleware: view},
		{Path: "/api/v1/workflows/definitions/{id}/retire", Method: http.MethodPost, Handler: h.retire, Middleware: view},
		{Path: "/api/v1/workflows/instances", Method: http.MethodGet, Handler: h.listInstances, Middleware: view},
		{Path: "/api/v1/workflows/instances", Method: http.MethodPost, Handler: h.start, Middleware: view},
		{Path: "/api/v1/workflows/instances/{id}", Method: http.MethodGet, Handler: h.getInstance, Middleware: view},
		{Path: "/api/v1/workflows/instances/{id}/cancel", Method: http.MethodPost, Handler: h.cancel, Middleware: view},
		{Path: "/api/v1/workflows/tasks", Method: http.MethodGet, Handler: h.myTasks, Middleware: view},
		{Path: "/api/v1/workflows/tasks/{id}/complete", Method: http.MethodPost, Handler: h.complete, Middleware: view},
	}
}

// ErrorResponse carries a stable code next to the English message, and the
// diagram issues when the error is about the diagram.
type ErrorResponse struct {
	Error  string           `json:"error"`
	Code   string           `json:"code"`
	Issues []workflow.Issue `json:"issues,omitempty"`
	// Fields lists the governed fields whose values the metamodel refused.
	Fields []metamodel.Violation `json:"fields,omitempty"`
}

func respondCode(w http.ResponseWriter, status int, code, message string) {
	common.RespondJSON(w, status, ErrorResponse{Error: message, Code: code})
}

func respondErr(w http.ResponseWriter, err error) {
	var invalid *workflow.ValidationError
	var fields *metamodel.ValidationError
	switch {
	case errors.As(err, &invalid):
		common.RespondJSON(w, http.StatusBadRequest, ErrorResponse{Error: "The diagram cannot run", Code: "invalid_diagram", Issues: invalid.Issues})
	case errors.As(err, &fields):
		common.RespondJSON(w, http.StatusBadRequest, ErrorResponse{Error: "The asset refused the field values", Code: "invalid_fields", Fields: fields.Fields})
	case errors.Is(err, asset.ErrVersionConflict):
		respondCode(w, http.StatusConflict, "conflict", "The asset changed meanwhile; reload and try again")
	case errors.Is(err, domain.ErrForbidden):
		respondCode(w, http.StatusForbidden, "forbidden", "Not allowed in this domain")
	case errors.Is(err, workflow.ErrInvalidInput):
		respondCode(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, workflow.ErrForbidden):
		respondCode(w, http.StatusForbidden, "forbidden", "Not allowed")
	case errors.Is(err, workflow.ErrNotFound):
		respondCode(w, http.StatusNotFound, "not_found", "Not found")
	case errors.Is(err, workflow.ErrNotRunnable):
		respondCode(w, http.StatusConflict, "not_published", "The definition is not published")
	case errors.Is(err, workflow.ErrNotRunning):
		respondCode(w, http.StatusConflict, "not_running", "The run has already ended")
	case errors.Is(err, workflow.ErrConflict):
		respondCode(w, http.StatusConflict, "conflict", "Changed by someone else; reload and try again")
	default:
		log.Error().Err(err).Msg("Workflow request failed")
		respondCode(w, http.StatusInternalServerError, "internal", "Internal server error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(v); err != nil {
		respondCode(w, http.StatusBadRequest, "invalid_input", "Invalid request body")
		return false
	}
	return true
}

func principal(r *http.Request) auth.Principal {
	p, _ := common.PrincipalFromContext(r.Context())
	return p
}

// DefinitionRequest carries a BPMN 2.0 document.
type DefinitionRequest struct {
	BPMN string `json:"bpmn"`
}

// @Summary Workflow engine capabilities
// @Description The action catalogue and the optional features this server runs. A 404 means the engine is off.
// @Tags workflows
// @Produce json
// @Success 200 {object} workflow.Capabilities
// @Router /api/v1/workflows/capabilities [get]
func (h *Handler) capabilities(w http.ResponseWriter, _ *http.Request) {
	common.RespondJSON(w, http.StatusOK, h.service.Capabilities())
}

// @Summary List workflow definitions
// @Description Managers see every version; everybody else only published ones.
// @Tags workflows
// @Produce json
// @Success 200 {array} workflow.Definition
// @Router /api/v1/workflows/definitions [get]
func (h *Handler) listDefinitions(w http.ResponseWriter, r *http.Request) {
	defs, err := h.service.ListDefinitions(r.Context(), principal(r))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, defs)
}

// @Summary Create a workflow draft
// @Description Stores the BPMN as the next version of its process id. A draft may have issues; they come back and block publishing.
// @Tags workflows
// @Accept json
// @Produce json
// @Param definition body DefinitionRequest true "BPMN document"
// @Success 201 {object} workflow.Definition
// @Failure 400 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Router /api/v1/workflows/definitions [post]
func (h *Handler) createDefinition(w http.ResponseWriter, r *http.Request) {
	var req DefinitionRequest
	if !decode(w, r, &req) {
		return
	}
	d, err := h.service.CreateDefinition(r.Context(), principal(r), []byte(req.BPMN))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusCreated, d)
}

// ValidateResponse lists what keeps a diagram from running.
type ValidateResponse struct {
	Valid  bool             `json:"valid"`
	Issues []workflow.Issue `json:"issues"`
}

// @Summary Validate a BPMN document
// @Tags workflows
// @Accept json
// @Produce json
// @Param definition body DefinitionRequest true "BPMN document"
// @Success 200 {object} ValidateResponse
// @Router /api/v1/workflows/definitions/validate [post]
func (h *Handler) validate(w http.ResponseWriter, r *http.Request) {
	var req DefinitionRequest
	if !decode(w, r, &req) {
		return
	}
	_, err := workflow.Parse([]byte(req.BPMN))
	var invalid *workflow.ValidationError
	switch {
	case err == nil:
		common.RespondJSON(w, http.StatusOK, ValidateResponse{Valid: true, Issues: []workflow.Issue{}})
	case errors.As(err, &invalid):
		common.RespondJSON(w, http.StatusOK, ValidateResponse{Issues: invalid.Issues})
	default:
		respondErr(w, err)
	}
}

// @Summary Get a workflow definition
// @Tags workflows
// @Produce json
// @Param id path string true "Definition ID"
// @Success 200 {object} workflow.Definition
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/workflows/definitions/{id} [get]
func (h *Handler) getDefinition(w http.ResponseWriter, r *http.Request) {
	d, err := h.service.GetDefinition(r.Context(), r.PathValue("id"))
	if err == nil && d.Status != workflow.StatusPublished && !canManage(principal(r)) {
		err = workflow.ErrNotFound
	}
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, d)
}

func canManage(p auth.Principal) bool {
	return p != nil && (p.IsAdmin() || p.HasPermission("workflows", "manage"))
}

// @Summary Export a workflow definition as BPMN
// @Tags workflows
// @Produce xml
// @Param id path string true "Definition ID"
// @Success 200 {string} string "BPMN 2.0 XML"
// @Router /api/v1/workflows/definitions/{id}/bpmn [get]
func (h *Handler) exportDefinition(w http.ResponseWriter, r *http.Request) {
	d, err := h.service.GetDefinition(r.Context(), r.PathValue("id"))
	if err == nil && d.Status != workflow.StatusPublished && !canManage(principal(r)) {
		err = workflow.ErrNotFound
	}
	if err != nil {
		respondErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.ProcessKey+`-v`+strconv.Itoa(d.Version)+`.bpmn"`)
	_, _ = w.Write([]byte(d.BPMN))
}

// @Summary Replace a workflow draft
// @Tags workflows
// @Accept json
// @Produce json
// @Param id path string true "Definition ID"
// @Param definition body DefinitionRequest true "BPMN document"
// @Success 200 {object} workflow.Definition
// @Failure 409 {object} ErrorResponse "Not a draft"
// @Router /api/v1/workflows/definitions/{id} [put]
func (h *Handler) updateDefinition(w http.ResponseWriter, r *http.Request) {
	var req DefinitionRequest
	if !decode(w, r, &req) {
		return
	}
	d, err := h.service.UpdateDefinition(r.Context(), principal(r), r.PathValue("id"), []byte(req.BPMN))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, d)
}

// @Summary Delete a workflow draft
// @Tags workflows
// @Param id path string true "Definition ID"
// @Success 204
// @Failure 409 {object} ErrorResponse "Not a draft"
// @Router /api/v1/workflows/definitions/{id} [delete]
func (h *Handler) deleteDefinition(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteDraft(r.Context(), principal(r), r.PathValue("id")); err != nil {
		respondErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// @Summary Publish a workflow draft
// @Tags workflows
// @Produce json
// @Param id path string true "Definition ID"
// @Success 200 {object} workflow.Definition
// @Failure 400 {object} ErrorResponse "The diagram cannot run"
// @Router /api/v1/workflows/definitions/{id}/publish [post]
func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	d, err := h.service.Publish(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, d)
}

// @Summary Retire a published workflow version
// @Tags workflows
// @Produce json
// @Param id path string true "Definition ID"
// @Success 200 {object} workflow.Definition
// @Router /api/v1/workflows/definitions/{id}/retire [post]
func (h *Handler) retire(w http.ResponseWriter, r *http.Request) {
	d, err := h.service.Retire(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, d)
}

// StartRequest starts a run of a published definition.
// Supply either target (one asset) or query_expression (batch, same language as asset rules).
type StartRequest struct {
	DefinitionID    string           `json:"definition_id"`
	Target          *workflow.Target `json:"target,omitempty"`
	QueryExpression string           `json:"query_expression,omitempty"`
	Limit           int              `json:"limit,omitempty"`
}

// @Summary Start a workflow run
// @Tags workflows
// @Accept json
// @Produce json
// @Param run body StartRequest true "Definition and optional target asset or query_expression batch"
// @Success 201 {object} workflow.Instance
// @Success 201 {object} workflow.StartBatchResult "When query_expression is set"
// @Failure 409 {object} ErrorResponse "Not published"
// @Router /api/v1/workflows/instances [post]
func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	var req StartRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.QueryExpression) != "" {
		if req.Target != nil && req.Target.ID != "" {
			respondCode(w, http.StatusBadRequest, "invalid_input", "Use either target or query_expression, not both")
			return
		}
		batch, err := h.service.StartQuery(r.Context(), principal(r), req.DefinitionID, req.QueryExpression, req.Limit)
		if err != nil {
			respondErr(w, err)
			return
		}
		common.RespondJSON(w, http.StatusCreated, batch)
		return
	}
	in, err := h.service.Start(r.Context(), principal(r), req.DefinitionID, req.Target)
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusCreated, in)
}

// @Summary List workflow runs
// @Description Runs the caller started or has a task in; all=true lists every run and needs workflows/manage.
// @Tags workflows
// @Produce json
// @Param all query bool false "Every run"
// @Param status query string false "running, completed, failed or cancelled"
// @Param target_kind query string false "Target kind"
// @Param target_id query string false "Target ID"
// @Param limit query int false "At most 200"
// @Success 200 {array} workflow.Instance
// @Router /api/v1/workflows/instances [get]
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := workflow.InstanceFilter{Status: q.Get("status"), TargetKind: q.Get("target_kind"), TargetID: q.Get("target_id"), Limit: limit}
	list, err := h.service.ListInstances(r.Context(), principal(r), f, q.Get("all") == "true")
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, list)
}

// @Summary Get a workflow run
// @Description With its tasks, audit events and diagram. Only for managers, its initiator and its participants.
// @Tags workflows
// @Produce json
// @Param id path string true "Run ID"
// @Success 200 {object} workflow.InstanceDetail
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/workflows/instances/{id} [get]
func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request) {
	in, err := h.service.GetInstance(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, in)
}

// @Summary Cancel a workflow run
// @Tags workflows
// @Produce json
// @Param id path string true "Run ID"
// @Success 200 {object} workflow.Instance
// @Router /api/v1/workflows/instances/{id}/cancel [post]
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	in, err := h.service.Cancel(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, in)
}

// @Summary List my open workflow tasks
// @Tags workflows
// @Produce json
// @Success 200 {array} workflow.Task
// @Router /api/v1/workflows/tasks [get]
func (h *Handler) myTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.service.MyTasks(r.Context(), principal(r))
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, tasks)
}

// CompleteRequest decides a task. Decision becomes the `decision` variable
// the gateways read. Fields supplies governed values when the user task
// declares dgu:formFields.
type CompleteRequest struct {
	Decision string         `json:"decision"`
	Comment  string         `json:"comment"`
	Fields   map[string]any `json:"fields,omitempty"`
}

// @Summary Complete a workflow task
// @Description The caller must be a candidate for the task now, not only when it was created.
// @Tags workflows
// @Accept json
// @Produce json
// @Param id path string true "Task ID"
// @Param decision body CompleteRequest true "Decision, comment and optional form fields"
// @Success 200 {object} workflow.Instance
// @Failure 403 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse "Already decided or the run ended"
// @Router /api/v1/workflows/tasks/{id}/complete [post]
func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	var req CompleteRequest
	if !decode(w, r, &req) {
		return
	}
	in, err := h.service.CompleteTask(r.Context(), principal(r), r.PathValue("id"), req.Decision, req.Comment, req.Fields)
	if err != nil {
		respondErr(w, err)
		return
	}
	common.RespondJSON(w, http.StatusOK, in)
}
