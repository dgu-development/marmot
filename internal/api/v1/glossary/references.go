package glossary

import (
	"errors"
	"net/http"
	"strings"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/glossary"
	"github.com/rs/zerolog/log"
)

const maxRefIDs = 100

// @Summary Terms pointing at a term
// @Description For each profile field with the glossary_term control, the live terms whose value names this term (for example, the acronyms that stand for it).
// @Tags glossary
// @Produce json
// @Param id path string true "Term ID"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {array} glossary.TermReferences
// @Failure 404 {object} common.ErrorResponse
// @Failure 500 {object} common.ErrorResponse
// @ID getGlossaryTermReferences
// @Router /api/v1/glossary/references/{id} [get]
func (h *Handler) getReferences(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/glossary/references/"), "/")
	refs, err := h.glossaryService.References(r.Context(), id)
	if err != nil {
		if errors.Is(err, glossary.ErrTermNotFound) || errors.Is(err, glossary.ErrNotFound) {
			common.RespondError(w, http.StatusNotFound, "Glossary term not found")
			return
		}
		log.Error().Err(err).Str("id", id).Msg("Failed to get term references")
		common.RespondError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	common.RespondJSON(w, http.StatusOK, refs)
}

// @Summary Resolve term IDs
// @Description The live terms with the given IDs, for showing glossary_term values by name. Unknown or deleted IDs are left out.
// @Tags glossary
// @Produce json
// @Param ids query string true "Comma-separated term IDs (at most 100)"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {array} glossary.TermRef
// @Failure 400 {object} common.ErrorResponse
// @Failure 500 {object} common.ErrorResponse
// @ID getGlossaryTermRefs
// @Router /api/v1/glossary/refs [get]
func (h *Handler) getRefs(w http.ResponseWriter, r *http.Request) {
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
	refs, err := h.glossaryService.RefsByID(r.Context(), ids)
	if err != nil {
		log.Error().Err(err).Msg("Failed to resolve term IDs")
		common.RespondError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	common.RespondJSON(w, http.StatusOK, refs)
}
