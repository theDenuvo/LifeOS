package httptransport

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	appmemory "github.com/theDenuvo/LifeOS/internal/application/memory"
	domainmemory "github.com/theDenuvo/LifeOS/internal/domain/memory"
)

type memorySourceBody struct {
	Kind       string `json:"kind"`
	SourceID   int64  `json:"source_id,omitempty"`
	Label      string `json:"label"`
	Excerpt    string `json:"excerpt,omitempty"`
	ObservedAt string `json:"observed_at"`
}

type memoryBody struct {
	ID           int64              `json:"id"`
	Area         string             `json:"area"`
	Category     string             `json:"category"`
	Title        string             `json:"title"`
	Content      string             `json:"content"`
	Certainty    string             `json:"certainty"`
	Confidence   float64            `json:"confidence"`
	Status       string             `json:"status"`
	SupersededBy *int64             `json:"superseded_by"`
	Entities     []string           `json:"entities"`
	Sources      []memorySourceBody `json:"sources"`
	CreatedAt    string             `json:"created_at"`
	UpdatedAt    string             `json:"updated_at"`
	Version      int                `json:"version"`
	Links        []memoryLinkBody   `json:"links,omitempty"`
}

type memoryLinkBody struct {
	ID         int64  `json:"id"`
	EntryID    int64  `json:"entry_id"`
	TargetType string `json:"target_type"`
	TargetID   int64  `json:"target_id"`
	Relation   string `json:"relation"`
	CreatedAt  string `json:"created_at"`
}

type memoryConflictBody struct {
	ID         int64       `json:"id"`
	Reason     string      `json:"reason"`
	Status     string      `json:"status"`
	Resolution string      `json:"resolution,omitempty"`
	CreatedAt  string      `json:"created_at"`
	ResolvedAt *string     `json:"resolved_at"`
	Existing   *memoryBody `json:"existing"`
	Candidate  *memoryBody `json:"candidate"`
}

func newMemoryBody(entry *domainmemory.Entry, detailed bool) memoryBody {
	body := memoryBody{
		ID:           entry.ID,
		Area:         string(entry.Area()),
		Category:     string(entry.Category),
		Title:        entry.Title,
		Content:      entry.Content,
		Certainty:    string(entry.Certainty),
		Confidence:   entry.Confidence,
		Status:       string(entry.Status),
		SupersededBy: entry.SupersededBy,
		Entities:     entry.Entities,
		CreatedAt:    formatTime(entry.CreatedAt),
		UpdatedAt:    formatTime(entry.UpdatedAt),
		Version:      entry.Version,
	}
	if body.Entities == nil {
		body.Entities = []string{}
	}
	for _, source := range entry.Sources {
		label := source.Label
		excerpt := source.Excerpt
		if !detailed && len([]rune(excerpt)) > 280 {
			excerpt = string([]rune(excerpt)[:280]) + "…"
		}
		body.Sources = append(body.Sources, memorySourceBody{
			Kind: string(source.Kind), SourceID: source.SourceID, Label: label,
			Excerpt: excerpt, ObservedAt: formatTime(source.ObservedAt),
		})
	}
	if body.Sources == nil {
		body.Sources = []memorySourceBody{}
	}
	return body
}

// listMemory handles GET /api/v1/memory.
func (h *Handler) listMemory(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	filter := appmemory.Filter{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("area")); raw != "" {
		area := domainmemory.Area(raw)
		if area != domainmemory.AreaPersonal && area != domainmemory.AreaKnowledge {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid area")
			return
		}
		filter.Area = &area
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("category")); raw != "" {
		category := domainmemory.Category(raw)
		if !category.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid category")
			return
		}
		filter.Category = &category
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status := domainmemory.Status(raw)
		if !status.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
			return
		}
		filter.Status = &status
	}
	if r.URL.Query().Get("include_disputed") == "true" {
		filter.IncludeDisputed = true
	}
	filter.Limit = queryInt(r, "limit", 30)
	filter.Offset = queryInt(r, "offset", 0)

	entries, total, err := h.services.Memory.List(r.Context(), filter)
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	data := make([]memoryBody, 0, len(entries))
	for _, entry := range entries {
		data = append(data, newMemoryBody(entry, false))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": filter.Limit, "offset": filter.Offset, "total": total},
	})
}

// getMemory handles GET /api/v1/memory/{id}.
func (h *Handler) getMemory(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	view, err := h.services.Memory.Get(r.Context(), id)
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	body := newMemoryBody(view.Entry, true)
	for _, link := range view.Links {
		body.Links = append(body.Links, memoryLinkBody{
			ID: link.ID, EntryID: link.EntryID, TargetType: string(link.TargetType),
			TargetID: link.TargetID, Relation: link.Relation, CreatedAt: formatTime(link.CreatedAt),
		})
	}
	h.writeJSON(w, http.StatusOK, body)
}

// createMemory handles POST /api/v1/memory.
func (h *Handler) createMemory(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	var body struct {
		Title      string   `json:"title"`
		Content    string   `json:"content"`
		Category   string   `json:"category"`
		Certainty  string   `json:"certainty"`
		Confidence *float64 `json:"confidence"`
		Entities   []string `json:"entities"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	category := domainmemory.Category(body.Category)
	if !category.IsValid() {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid category")
		return
	}
	certainty := domainmemory.Certainty(body.Certainty)
	if certainty == "" {
		certainty = domainmemory.CertaintyFact
	}
	if !certainty.IsValid() {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid certainty")
		return
	}
	result, err := h.services.Memory.Create(r.Context(), appmemory.CreateInput{
		Title: body.Title, Content: body.Content, Category: category,
		Certainty: certainty, Confidence: body.Confidence, Entities: body.Entities,
		Source: domainmemory.Source{Kind: domainmemory.SourceUser, Label: "Ручная запись"},
	})
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	response := map[string]any{"entry": newMemoryBody(result.Entry, true), "duplicate": result.Duplicate}
	if result.Conflict != nil {
		response["conflict_id"] = result.Conflict.ID
	}
	h.writeJSON(w, http.StatusCreated, response)
}

// updateMemory handles PATCH /api/v1/memory/{id} under If-Match.
func (h *Handler) updateMemory(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	version, ok := h.expectedVersion(w, r)
	if !ok {
		return
	}
	var body struct {
		Title      *string   `json:"title"`
		Content    *string   `json:"content"`
		Category   *string   `json:"category"`
		Certainty  *string   `json:"certainty"`
		Confidence *float64  `json:"confidence"`
		Entities   *[]string `json:"entities"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	input := appmemory.UpdateInput{
		Title: body.Title, Content: body.Content, Confidence: body.Confidence, Entities: body.Entities,
	}
	if body.Category != nil {
		category := domainmemory.Category(*body.Category)
		if !category.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid category")
			return
		}
		input.Category = &category
	}
	if body.Certainty != nil {
		certainty := domainmemory.Certainty(*body.Certainty)
		if !certainty.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid certainty")
			return
		}
		input.Certainty = &certainty
	}
	entry, err := h.services.Memory.Update(r.Context(), id, version, input)
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newMemoryBody(entry, true))
}

// deleteMemory handles DELETE /api/v1/memory/{id} under If-Match.
func (h *Handler) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	version, ok := h.expectedVersion(w, r)
	if !ok {
		return
	}
	if err := h.services.Memory.Delete(r.Context(), id, version); err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// linkMemory handles POST /api/v1/memory/{id}/links.
func (h *Handler) linkMemory(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		TargetType string `json:"target_type"`
		TargetID   int64  `json:"target_id"`
		Relation   string `json:"relation"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	targetType := domainmemory.TargetType(body.TargetType)
	if !targetType.IsValid() {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid target_type")
		return
	}
	link, err := h.services.Memory.Link(r.Context(), &domainmemory.Link{
		EntryID: id, TargetType: targetType, TargetID: body.TargetID, Relation: strings.TrimSpace(body.Relation),
	})
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, memoryLinkBody{
		ID: link.ID, EntryID: link.EntryID, TargetType: string(link.TargetType),
		TargetID: link.TargetID, Relation: link.Relation, CreatedAt: formatTime(link.CreatedAt),
	})
}

// unlinkMemory handles DELETE /api/v1/memory/{id}/links/{linkID}.
func (h *Handler) unlinkMemory(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	linkID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("linkID")), 10, 64)
	if err != nil || linkID <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid link id")
		return
	}
	if err := h.services.Memory.Unlink(r.Context(), linkID, id); err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listMemoryConflicts handles GET /api/v1/memory/conflicts.
func (h *Handler) listMemoryConflicts(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	pendingOnly := r.URL.Query().Get("all") != "true"
	views, err := h.services.Memory.Conflicts(r.Context(), pendingOnly)
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	data := make([]memoryConflictBody, 0, len(views))
	for _, view := range views {
		existing := newMemoryBody(view.Existing, true)
		candidate := newMemoryBody(view.Candidate, true)
		body := memoryConflictBody{
			ID: view.Conflict.ID, Reason: view.Conflict.Reason, Status: string(view.Conflict.Status),
			Resolution: view.Conflict.Resolution, CreatedAt: formatTime(view.Conflict.CreatedAt),
			Existing: &existing, Candidate: &candidate,
		}
		if view.Conflict.ResolvedAt != nil {
			resolved := formatTime(*view.Conflict.ResolvedAt)
			body.ResolvedAt = &resolved
		}
		data = append(data, body)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]any{"total": len(data)}})
}

// resolveMemoryConflict handles POST /api/v1/memory/conflicts/{id}/resolve.
func (h *Handler) resolveMemoryConflict(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Resolution    string `json:"resolution"`
		MergedTitle   string `json:"merged_title"`
		MergedContent string `json:"merged_content"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	err := h.services.Memory.Resolve(r.Context(), id, appmemory.ResolveInput{
		Resolution: body.Resolution, MergedTitle: body.MergedTitle, MergedContent: body.MergedContent,
	})
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"id": id, "resolution": body.Resolution})
}

// memoryStats handles GET /api/v1/memory/stats.
func (h *Handler) memoryStats(w http.ResponseWriter, r *http.Request) {
	if h.services.Memory == nil {
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Memory is not configured")
		return
	}
	stats, err := h.services.Memory.Stats(r.Context())
	if err != nil {
		h.writeMemoryError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"personal": stats.Personal, "knowledge": stats.Knowledge, "pending_conflicts": stats.PendingConflicts,
	})
}

func (h *Handler) writeMemoryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appmemory.ErrNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Memory entry not found")
	case errors.Is(err, appmemory.ErrLinkNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Memory link not found")
	case errors.Is(err, appmemory.ErrLinkTargetNotFound):
		h.writeProblem(w, r, http.StatusUnprocessableEntity, "invalid-link", "Invalid Link", "Linked object does not exist")
	case errors.Is(err, appmemory.ErrConflictNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "Memory conflict not found")
	case errors.Is(err, appmemory.ErrVersionConflict):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "precondition-failed", "Precondition Failed", "Memory entry was changed; reload it and retry")
	case errors.Is(err, appmemory.ErrConflictResolved):
		h.writeProblem(w, r, http.StatusConflict, "already-resolved", "Already Resolved", "This conflict has already been resolved")
	case errors.Is(err, appmemory.ErrInvalidResolution):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "resolution must be existing, candidate, both or merge")
	case errors.Is(err, domainmemory.ErrInvalidTitle), errors.Is(err, domainmemory.ErrInvalidContent),
		errors.Is(err, domainmemory.ErrInvalidCategory), errors.Is(err, domainmemory.ErrInvalidCertainty),
		errors.Is(err, domainmemory.ErrInvalidConfidence), errors.Is(err, domainmemory.ErrInvalidSource),
		errors.Is(err, domainmemory.ErrInvalidEntities), errors.Is(err, domainmemory.ErrInvalidLink),
		errors.Is(err, domainmemory.ErrInvalidRelation):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", "Internal error")
	}
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
