package quality

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/quality"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/rs/zerolog/log"
)

const (
	defaultRunLimit = 20
	maxRunLimit     = 100
)

// RunsResponse is a page of runs, newest first.
type RunsResponse struct {
	Runs   []quality.Run `json:"runs"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
} // @name QualityRunsResponse

// ResultsResponse is a page of the results of one run.
type ResultsResponse struct {
	Results []quality.AssetResult `json:"results"`
	Total   int                   `json:"total"`
	Limit   int                   `json:"limit"`
	Offset  int                   `json:"offset"`
} // @name QualityResultsResponse

// RunConflict answers a start while another run is in progress, and says which one.
type RunConflict struct {
	Error string       `json:"error"`
	Run   *quality.Run `json:"run,omitempty"`
} // @name QualityRunConflict

func intParam(r *http.Request, name string, fallback, upper int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || value < 0 {
		return fallback
	}
	return min(value, upper)
}

func internalError(w http.ResponseWriter, err error, what string) {
	log.Error().Err(err).Msg(what)
	common.RespondError(w, http.StatusInternalServerError, "Internal server error")
}

// @Summary Start a quality audit run
// @Description Audits every asset of the catalog in the background, with the saved settings and the effective metamodel. One run at a time: a second request gets 409 with the run in progress.
// @Tags quality
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 202 {object} quality.Run
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @Failure 409 {object} QualityRunConflict
// @Failure 422 {object} common.ErrorResponse
// @ID startQualityRun
// @Router /api/v1/quality/runs [post]
func (h *Handler) startRun(w http.ResponseWriter, r *http.Request) {
	by := ""
	if usr, ok := r.Context().Value(common.UserContextKey).(*user.User); ok {
		by = usr.ID
	}
	run, err := h.runs.StartRun(r.Context(), quality.TriggerManual, by)
	switch {
	case errors.Is(err, quality.ErrRunInProgress):
		common.RespondJSON(w, http.StatusConflict, RunConflict{Error: "A quality run is already in progress", Run: run})
	case errors.Is(err, quality.ErrMetamodelOff):
		common.RespondError(w, http.StatusUnprocessableEntity, "The metamodel profile is not loaded")
	case err != nil:
		internalError(w, err, "Starting a quality run failed")
	default:
		w.Header().Set("Location", "/api/v1/quality/runs/"+run.ID)
		common.RespondJSON(w, http.StatusAccepted, run)
	}
}

// @Summary List quality audit runs
// @Description Newest first, with the summary of the finished ones.
// @Tags quality
// @Produce json
// @Param limit query int false "Page size, up to 100" default(20)
// @Param offset query int false "Offset" default(0)
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} QualityRunsResponse
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @ID listQualityRuns
// @Router /api/v1/quality/runs [get]
func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", defaultRunLimit, maxRunLimit)
	if limit == 0 {
		limit = defaultRunLimit
	}
	offset := intParam(r, "offset", 0, 1<<31-1)
	runs, total, err := h.runs.Runs(r.Context(), limit, offset)
	if err != nil {
		internalError(w, err, "Listing quality runs failed")
		return
	}
	common.RespondJSON(w, http.StatusOK, RunsResponse{Runs: runs, Total: total, Limit: limit, Offset: offset})
}

// @Summary Get a quality audit run
// @Description The run with its progress, the settings it used and, once finished, its summary.
// @Tags quality
// @Produce json
// @Param id path string true "Run ID"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} quality.Run
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @Failure 404 {object} common.ErrorResponse
// @ID getQualityRun
// @Router /api/v1/quality/runs/{id} [get]
func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.runs.Run(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, quality.ErrRunNotFound):
		common.RespondError(w, http.StatusNotFound, "Quality run not found")
	case err != nil:
		internalError(w, err, "Reading a quality run failed")
	default:
		common.RespondJSON(w, http.StatusOK, run)
	}
}

// @Summary List the results of a quality audit run
// @Description One entry per audited asset, weakest first. The findings of each asset are only kept for the last successful run.
// @Tags quality
// @Produce json
// @Param id path string true "Run ID"
// @Param status query string false "compliant, warning or noncompliant"
// @Param domain query string false "A domain ID, or unassigned"
// @Param type query string false "Asset type"
// @Param q query string false "Text in the asset name or MRN"
// @Param sort query string false "quality (default) or name"
// @Param limit query int false "Page size, up to 500" default(50)
// @Param offset query int false "Offset" default(0)
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} QualityResultsResponse
// @Failure 400 {object} common.ErrorResponse
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @Failure 404 {object} common.ErrorResponse
// @ID getQualityRunResults
// @Router /api/v1/quality/runs/{id}/results [get]
func (h *Handler) getResults(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := quality.ResultFilter{
		Status: quality.Status(query.Get("status")), Domain: query.Get("domain"), Type: query.Get("type"),
		Query: query.Get("q"), Sort: query.Get("sort"),
		Limit:  intParam(r, "limit", quality.DefaultResultLimit, quality.MaxResultLimit),
		Offset: intParam(r, "offset", 0, 1<<31-1),
	}
	switch filter.Status {
	case "", quality.StatusCompliant, quality.StatusWarning, quality.StatusNoncompliant:
	default:
		common.RespondError(w, http.StatusBadRequest, "status must be compliant, warning or noncompliant")
		return
	}
	if filter.Sort != "" && filter.Sort != "quality" && filter.Sort != "name" {
		common.RespondError(w, http.StatusBadRequest, "sort must be quality or name")
		return
	}
	results, total, err := h.runs.Results(r.Context(), r.PathValue("id"), filter)
	switch {
	case errors.Is(err, quality.ErrRunNotFound):
		common.RespondError(w, http.StatusNotFound, "Quality run not found")
	case err != nil:
		internalError(w, err, "Reading quality results failed")
	default:
		limit := filter.Limit
		if limit == 0 {
			limit = quality.DefaultResultLimit
		}
		common.RespondJSON(w, http.StatusOK, ResultsResponse{Results: results, Total: total, Limit: limit, Offset: filter.Offset})
	}
}
