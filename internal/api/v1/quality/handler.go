// Package quality exposes the fork-only quality audit over HTTP. It is only
// registered when quality.enabled is set; see docs/docs/Configure/quality.md.
package quality

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/quality"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/marmotdata/marmot/pkg/config"
	"github.com/rs/zerolog/log"
)

const maxBodyBytes = 64 << 10

type Handler struct {
	service     quality.Service
	userService user.Service
	authService auth.Service
	config      *config.Config
}

func NewHandler(service quality.Service, userService user.Service, authService auth.Service, config *config.Config) *Handler {
	return &Handler{service: service, userService: userService, authService: authService, config: config}
}

func (h *Handler) Routes() []common.Route {
	guarded := func(action string) []func(http.HandlerFunc) http.HandlerFunc {
		return []func(http.HandlerFunc) http.HandlerFunc{
			common.WithAuth(h.userService, h.authService, h.config),
			common.RequirePermission(h.userService, "metadata_quality", action),
		}
	}
	return []common.Route{
		{Path: "/api/v1/quality/settings", Method: http.MethodGet, Handler: h.getSettings, Middleware: guarded("view")},
		{Path: "/api/v1/quality/settings", Method: http.MethodPut, Handler: h.putSettings, Middleware: guarded("manage")},
	}
}

func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// parseIfMatch reads one quoted version. Unlike assets, 0 is valid: it means
// the caller saw the defaults and expects nobody to have saved since.
func parseIfMatch(header string) (int64, bool) {
	header = strings.TrimSpace(header)
	if len(header) < 3 || header[0] != '"' || header[len(header)-1] != '"' {
		return 0, false
	}
	version, err := strconv.ParseInt(header[1:len(header)-1], 10, 64)
	return version, err == nil && version >= 0 && header == etag(version)
}

// @Summary Get the quality audit settings
// @Description The criterion the platform's quality scores are computed with. Version 0 means the defaults, not yet saved.
// @Tags quality
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} quality.Stored
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @ID getQualitySettings
// @Router /api/v1/quality/settings [get]
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	stored, err := h.service.Settings(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Reading quality settings failed")
		common.RespondError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	w.Header().Set("ETag", etag(stored.Version))
	common.RespondJSON(w, http.StatusOK, stored)
}

// @Summary Change the quality audit settings
// @Description Replaces the settings. Requires If-Match with the version read; 412 when someone saved first.
// @Tags quality
// @Accept json
// @Produce json
// @Param If-Match header string true "Version read, quoted; \"0\" when the defaults were read"
// @Param settings body quality.Settings true "Settings"
// @Security ApiKeyAuth
// @Security BearerAuth
// @Success 200 {object} quality.Stored
// @Failure 400 {object} quality.ValidationError
// @Failure 401 {object} common.ErrorResponse
// @Failure 403 {object} common.ErrorResponse
// @Failure 412 {object} common.ErrorResponse
// @Failure 428 {object} common.ErrorResponse
// @ID putQualitySettings
// @Router /api/v1/quality/settings [put]
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	header := r.Header.Get("If-Match")
	if header == "" {
		common.RespondError(w, http.StatusPreconditionRequired, "If-Match required")
		return
	}
	version, ok := parseIfMatch(header)
	if !ok {
		common.RespondError(w, http.StatusBadRequest, "If-Match must contain one quoted settings version")
		return
	}

	var settings quality.Settings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		common.RespondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		common.RespondError(w, http.StatusBadRequest, "Expected one JSON object")
		return
	}

	by := ""
	if usr, ok := r.Context().Value(common.UserContextKey).(*user.User); ok {
		by = usr.ID
	}
	stored, err := h.service.UpdateSettings(r.Context(), settings, version, by)
	if err != nil {
		var invalid *quality.ValidationError
		switch {
		case errors.As(err, &invalid):
			common.RespondJSON(w, http.StatusBadRequest, invalid)
		case errors.Is(err, quality.ErrVersionConflict):
			common.RespondError(w, http.StatusPreconditionFailed, "Quality settings were changed by someone else")
		case errors.Is(err, quality.ErrVersionRequired):
			common.RespondError(w, http.StatusPreconditionRequired, "If-Match required")
		default:
			log.Error().Err(err).Msg("Saving quality settings failed")
			common.RespondError(w, http.StatusInternalServerError, "Internal server error")
		}
		return
	}
	w.Header().Set("ETag", etag(stored.Version))
	common.RespondJSON(w, http.StatusOK, stored)
}
