package quality

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
	"github.com/marmotdata/marmot/internal/core/user"
)

const maxRuleBodyBytes = 64 << 10

// RulesResponse lists every rule that applies to the audit.
type RulesResponse struct {
	Rules []quality.RuleInfo `json:"rules"`
} // @name QualityRulesResponse

// RuleRequest is a custom rule as a person writes it.
type RuleRequest struct {
	metamodel.QualityRule
	Enabled bool `json:"enabled"`
} // @name QualityRuleRequest

func decodeRule(w http.ResponseWriter, r *http.Request) (RuleRequest, bool) {
	var request RuleRequest
	request.Enabled = true
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRuleBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		common.RespondError(w, http.StatusBadRequest, "Invalid request body")
		return request, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		common.RespondError(w, http.StatusBadRequest, "Expected one JSON object")
		return request, false
	}
	return request, true
}

func caller(r *http.Request) string {
	if usr, ok := r.Context().Value(common.UserContextKey).(*user.User); ok {
		return usr.ID
	}
	return ""
}

// ruleError answers what a rule operation can fail with.
func ruleError(w http.ResponseWriter, err error, what string) {
	var invalid *metamodel.QualityRuleError
	switch {
	case errors.As(err, &invalid):
		common.RespondJSON(w, http.StatusBadRequest, invalid)
	case errors.Is(err, quality.ErrRuleNotFound):
		common.RespondError(w, http.StatusNotFound, "Quality rule not found")
	case errors.Is(err, quality.ErrRuleExists):
		common.RespondError(w, http.StatusConflict, "A quality rule with this id already exists")
	case errors.Is(err, quality.ErrRuleVersion):
		common.RespondError(w, http.StatusPreconditionFailed, "The quality rule was changed by someone else")
	case errors.Is(err, quality.ErrRuleVersionRequired):
		common.RespondError(w, http.StatusPreconditionRequired, "If-Match required")
	case errors.Is(err, quality.ErrRuleLimit):
		common.RespondError(w, http.StatusUnprocessableEntity, "Too many custom quality rules")
	case errors.Is(err, quality.ErrMetamodelOff):
		common.RespondError(w, http.StatusUnprocessableEntity, "The metamodel profile is not loaded")
	default:
		internalError(w, err, what)
	}
}

// @Summary List the quality rules
// @Description Every rule the audit applies: the built-in ones, the ones the metamodel profile declares (read-only here) and the custom ones people wrote. The severity and on/off of the first two are in the settings; a custom rule carries its own.
// @Tags quality
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} QualityRulesResponse
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @ID listQualityRules
// @Router /api/v1/quality/rules [get]
func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.rules.Rules(r.Context())
	if err != nil {
		internalError(w, err, "Listing quality rules failed")
		return
	}
	common.RespondJSON(w, http.StatusOK, RulesResponse{Rules: rules})
}

// @Summary Create a custom quality rule
// @Description A declarative rule over the fields of the profile: a closed set of operators, never code. 400 lists every problem with its path. The id is generated when omitted.
// @Tags quality
// @Accept json
// @Produce json
// @Param rule body QualityRuleRequest true "Rule"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 201 {object} quality.CustomRule
// @Failure 400 {object} metamodel.QualityRuleError
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @Failure 409 {object} common.ErrorResponse
// @Failure 422 {object} common.ErrorResponse
// @ID createQualityRule
// @Router /api/v1/quality/rules [post]
func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeRule(w, r)
	if !ok {
		return
	}
	created, err := h.rules.Create(r.Context(), request.QualityRule, request.Enabled, caller(r))
	if err != nil {
		ruleError(w, err, "Creating a quality rule failed")
		return
	}
	w.Header().Set("ETag", etag(created.Version))
	common.RespondJSON(w, http.StatusCreated, created)
}

// @Summary Change a custom quality rule
// @Description Replaces a custom rule. Requires If-Match with the version read; 412 when someone changed it first. The rules of the profile cannot be changed here.
// @Tags quality
// @Accept json
// @Produce json
// @Param id path string true "Rule ID"
// @Param If-Match header string true "Version read, quoted"
// @Param rule body QualityRuleRequest true "Rule"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} quality.CustomRule
// @Failure 400 {object} metamodel.QualityRuleError
// @Failure 404 {object} common.ErrorResponse
// @Failure 412 {object} common.ErrorResponse
// @Failure 428 {object} common.ErrorResponse
// @ID updateQualityRule
// @Router /api/v1/quality/rules/{id} [put]
func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request) {
	header := r.Header.Get("If-Match")
	if header == "" {
		common.RespondError(w, http.StatusPreconditionRequired, "If-Match required")
		return
	}
	version, ok := parseIfMatch(header)
	if !ok || version < 1 {
		common.RespondError(w, http.StatusBadRequest, "If-Match must contain one quoted rule version")
		return
	}
	request, ok := decodeRule(w, r)
	if !ok {
		return
	}
	updated, err := h.rules.Update(r.Context(), r.PathValue("id"), request.QualityRule, request.Enabled, version, caller(r))
	if err != nil {
		ruleError(w, err, "Changing a quality rule failed")
		return
	}
	w.Header().Set("ETag", etag(updated.Version))
	common.RespondJSON(w, http.StatusOK, updated)
}

// @Summary Delete a custom quality rule
// @Tags quality
// @Param id path string true "Rule ID"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 204
// @Failure 404 {object} common.ErrorResponse
// @ID deleteQualityRule
// @Router /api/v1/quality/rules/{id} [delete]
func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := h.rules.Delete(r.Context(), r.PathValue("id")); err != nil {
		ruleError(w, err, "Deleting a quality rule failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// EvaluateRequest names the assets to judge.
type EvaluateRequest struct {
	AssetIDs []string `json:"asset_ids"`
} // @name QualityEvaluateRequest

// EvaluateResponse is how each asset fares now.
type EvaluateResponse struct {
	Results []quality.AssetResult `json:"results"`
} // @name QualityEvaluateResponse

// @Summary Evaluate assets now
// @Description Judges up to 200 assets as they are, with the current settings, profile rules and custom rules, and records nothing. It is what a card on an asset page shows: findings and the checks the asset meets. An id that is not an asset is left out.
// @Tags quality
// @Accept json
// @Produce json
// @Param request body QualityEvaluateRequest true "Assets"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} QualityEvaluateResponse
// @Failure 400 {object} common.ErrorResponse
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @Failure 422 {object} common.ErrorResponse
// @ID evaluateQualityAssets
// @Router /api/v1/quality/evaluate [post]
func (h *Handler) evaluate(w http.ResponseWriter, r *http.Request) {
	var request EvaluateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRuleBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || len(request.AssetIDs) == 0 {
		common.RespondError(w, http.StatusBadRequest, "asset_ids must list at least one asset")
		return
	}
	if len(request.AssetIDs) > quality.MaxEvaluated {
		common.RespondError(w, http.StatusBadRequest, "At most 200 assets can be evaluated at once")
		return
	}
	results, err := h.runs.Evaluate(r.Context(), request.AssetIDs)
	switch {
	case errors.Is(err, quality.ErrMetamodelOff):
		common.RespondError(w, http.StatusUnprocessableEntity, "The metamodel profile is not loaded")
	case err != nil:
		internalError(w, err, "Evaluating assets failed")
	default:
		if results == nil {
			results = []quality.AssetResult{}
		}
		common.RespondJSON(w, http.StatusOK, EvaluateResponse{Results: results})
	}
}
