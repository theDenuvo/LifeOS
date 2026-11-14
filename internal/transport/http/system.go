package httptransport

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/theDenuvo/LifeOS/internal/application/profile"
)

// getProfile handles GET /api/v1/profile.
func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	value, err := h.services.Profile.Get(r.Context())
	if err != nil {
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

// putProfile handles PUT /api/v1/profile.
func (h *Handler) putProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	value, err := h.services.Profile.Update(r.Context(), body.Name, body.Role)
	if err != nil {
		if errors.Is(err, profile.ErrInvalidName) || errors.Is(err, profile.ErrInvalidRole) {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
			return
		}
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

// status handles GET /api/v1/status.
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	databaseOK := true
	if err := h.db.PingContext(ctx); err != nil {
		databaseOK = false
	}

	pendingActions := 0
	if databaseOK {
		_ = h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pending_actions WHERE status = 'pending'`).Scan(&pendingActions)
	}

	revision := uint64(0)
	if h.services.Broker != nil {
		revision = h.services.Broker.LastID()
	}
	version := h.services.Version
	if version == "" {
		version = "dev"
	}
	assistantConfigured := h.cfg.LLMAPIKey != "" || h.cfg.LLMProvider == "ollama"

	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":      databaseOK,
		"version": version,
		"time":    formatTime(h.now()),
		"database": map[string]any{
			"ok": databaseOK,
		},
		"assistant": map[string]any{
			"configured": assistantConfigured,
		},
		"events": map[string]any{
			"revision": revision,
		},
		"pending_actions": pendingActions,
		"synced_at":       formatTime(h.now()),
	})
}
