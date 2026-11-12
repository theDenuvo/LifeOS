package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/theDenuvo/LifeOS/internal/application/settings"
)

// listSettings handles GET /api/v1/settings.
func (h *Handler) listSettings(w http.ResponseWriter, r *http.Request) {
	values, err := h.services.Settings.List(r.Context())
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": values})
}

// getSetting handles GET /api/v1/settings/{key}.
func (h *Handler) getSetting(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	value, err := h.services.Settings.Get(r.Context(), key)
	if err != nil {
		h.writeSettingsError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": json.RawMessage(value)})
}

// putSetting handles PUT /api/v1/settings/{key}.
func (h *Handler) putSetting(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))

	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	if len(body.Value) == 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "value is required")
		return
	}

	if err := h.services.Settings.Put(r.Context(), key, body.Value); err != nil {
		h.writeSettingsError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": json.RawMessage(body.Value)})
}

// writeSettingsError maps settings errors onto problem responses.
func (h *Handler) writeSettingsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, settings.ErrNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Setting not found")
	case errors.Is(err, settings.ErrInvalidKey), errors.Is(err, settings.ErrInvalidValue):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
	}
}
