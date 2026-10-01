package assets

import (
	"errors"
	"net/http"
	"strings"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/rs/zerolog/log"
)

const maxRefIDs = 100

// @Summary Assets pointing at an asset
// @Description For each profile field with the asset control, the assets whose value names this asset (for example, the rules that apply to a table).
// @Tags assets
// @Produce json
// @Param id path string true "Asset ID"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {array} asset.AssetReferences
// @Failure 404 {object} common.ErrorResponse
// @Failure 500 {object} common.ErrorResponse
// @ID getAssetReferences
// @Router /api/v1/assets/references/{id} [get]
func (h *Handler) getAssetReferences(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/assets/references/"), "/")
	refs, err := h.assetService.References(r.Context(), id)
	if err != nil {
		if errors.Is(err, asset.ErrNotFound) {
			common.RespondError(w, http.StatusNotFound, "Asset not found")
			return
		}
		log.Error().Err(err).Str("id", id).Msg("Failed to get asset references")
		common.RespondError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	common.RespondJSON(w, http.StatusOK, refs)
}

// @Summary Resolve asset IDs
// @Description The assets with the given IDs, for showing asset-control values by name. Unknown IDs are left out.
// @Tags assets
// @Produce json
// @Param ids query string true "Comma-separated asset IDs (at most 100)"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {array} asset.AssetRef
// @Failure 400 {object} common.ErrorResponse
// @Failure 500 {object} common.ErrorResponse
// @ID getAssetRefs
// @Router /api/v1/assets/refs [get]
func (h *Handler) getAssetRefs(w http.ResponseWriter, r *http.Request) {
	var ids []string
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > maxRefIDs {
		common.RespondError(w, http.StatusBadRequest, "Too many IDs")
		return
	}
	refs, err := h.assetService.RefsByID(r.Context(), ids)
	if err != nil {
		log.Error().Err(err).Msg("Failed to resolve asset IDs")
		common.RespondError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	common.RespondJSON(w, http.StatusOK, refs)
}
