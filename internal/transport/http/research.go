package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	appresearch "github.com/theDenuvo/LifeOS/internal/application/research"
	domainresearch "github.com/theDenuvo/LifeOS/internal/domain/research"
)

// Roadmap stage 7: research and external sources.

type researchCitationBody struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Site    string `json:"site,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
	Trust   string `json:"trust"`
}

type researchFindingBody struct {
	Statement     string                 `json:"statement"`
	Certainty     string                 `json:"certainty"`
	Confidence    float64                `json:"confidence"`
	Corroboration int                    `json:"corroboration"`
	Domains       []string               `json:"domains"`
	Citations     []researchCitationBody `json:"citations"`
	Conflict      string                 `json:"conflict,omitempty"`
}

type researchReportBody struct {
	ID          int64                 `json:"id"`
	Question    string                `json:"question"`
	Queries     []string              `json:"queries"`
	Status      string                `json:"status"`
	Summary     string                `json:"summary"`
	Findings    []researchFindingBody `json:"findings"`
	Sources     int                   `json:"sources"`
	KnowledgeID *int64                `json:"knowledge_entry_id,omitempty"`
	Error       string                `json:"error,omitempty"`
	StartedAt   string                `json:"started_at"`
	FinishedAt  *string               `json:"finished_at,omitempty"`
	Version     int                   `json:"version"`
}

func newResearchReportBody(report *domainresearch.Report) researchReportBody {
	body := researchReportBody{
		ID:          report.ID,
		Question:    report.Question,
		Queries:     report.Queries,
		Status:      string(report.Status),
		Summary:     report.Summary,
		Findings:    make([]researchFindingBody, 0, len(report.Findings)),
		Sources:     report.Sources,
		KnowledgeID: report.KnowledgeEntryID,
		Error:       report.Error,
		StartedAt:   formatTime(report.StartedAt),
		FinishedAt:  formatOptionalTime(report.FinishedAt),
		Version:     report.Version,
	}
	for _, finding := range report.Findings {
		item := researchFindingBody{
			Statement:     finding.Statement,
			Certainty:     string(finding.Certainty),
			Confidence:    finding.Confidence,
			Corroboration: finding.Corroboration,
			Domains:       finding.Domains,
			Citations:     make([]researchCitationBody, 0, len(finding.Citations)),
			Conflict:      finding.Conflict,
		}
		for _, citation := range finding.Citations {
			item.Citations = append(item.Citations, researchCitationBody{
				URL: citation.URL, Title: citation.Title, Site: citation.Site,
				Excerpt: citation.Excerpt, Trust: string(citation.Trust),
			})
		}
		body.Findings = append(body.Findings, item)
	}
	return body
}

type researchSourceBody struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Mode       string  `json:"mode"`
	Trust      string  `json:"trust"`
	Format     string  `json:"format,omitempty"`
	Endpoint   string  `json:"endpoint"`
	Notes      string  `json:"notes"`
	Enabled    bool    `json:"enabled"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	Version    int     `json:"version"`
}

func newResearchSourceBody(connection *domainresearch.Connection) researchSourceBody {
	return researchSourceBody{
		ID:         connection.ID,
		Name:       connection.Name,
		Kind:       string(connection.Kind),
		Mode:       string(connection.Mode),
		Trust:      string(connection.Trust),
		Format:     string(connection.Format),
		Endpoint:   connection.Endpoint,
		Notes:      connection.Notes,
		Enabled:    connection.Enabled,
		LastUsedAt: formatOptionalTime(connection.LastUsedAt),
		CreatedAt:  formatTime(connection.CreatedAt),
		UpdatedAt:  formatTime(connection.UpdatedAt),
		Version:    connection.Version,
	}
}

type researchCallBody struct {
	ID           int64  `json:"id"`
	ConnectionID int64  `json:"connection_id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Mode         string `json:"mode"`
	Operation    string `json:"operation"`
	Outcome      string `json:"outcome"`
	Detail       string `json:"detail,omitempty"`
	DurationMS   int64  `json:"duration_ms"`
	CreatedAt    string `json:"created_at"`
}

func newResearchCallBody(record *appresearch.CallRecord) researchCallBody {
	return researchCallBody{
		ID: record.ID, ConnectionID: record.ConnectionID, Name: record.Name,
		Kind: string(record.Kind), Mode: string(record.Mode), Operation: record.Operation,
		Outcome: record.Outcome, Detail: record.Detail, DurationMS: record.DurationMS,
		CreatedAt: formatTime(record.CreatedAt),
	}
}

type researchWatchBody struct {
	ID                int64    `json:"id"`
	Topic             string   `json:"topic"`
	Query             string   `json:"query"`
	Queries           []string `json:"queries"`
	Cadence           string   `json:"cadence"`
	Enabled           bool     `json:"enabled"`
	NextCheckAt       *string  `json:"next_check_at,omitempty"`
	LastCheckedAt     *string  `json:"last_checked_at,omitempty"`
	LastChangeAt      *string  `json:"last_change_at,omitempty"`
	LastChangeSummary string   `json:"last_change_summary"`
	Version           int      `json:"version"`
}

func newResearchWatchBody(watch *domainresearch.Watch) researchWatchBody {
	return researchWatchBody{
		ID: watch.ID, Topic: watch.Topic, Query: watch.Query, Queries: watch.Queries,
		Cadence: string(watch.Cadence), Enabled: watch.Enabled,
		NextCheckAt:       formatOptionalTime(watch.NextCheckAt),
		LastCheckedAt:     formatOptionalTime(watch.LastCheckedAt),
		LastChangeAt:      formatOptionalTime(watch.LastChangeAt),
		LastChangeSummary: watch.LastChangeSummary,
		Version:           watch.Version,
	}
}

func (h *Handler) researchUnavailable(w http.ResponseWriter, r *http.Request) {
	h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Research is not configured")
}

func (h *Handler) researchID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid id")
		return 0, false
	}
	return id, true
}

// decodeResearchBody reads one JSON body for the research endpoints.
func decodeResearchBody(r *http.Request, target any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return errors.New("unreadable body")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid JSON")
	}
	return nil
}

// listResearchReports handles GET /api/v1/research/reports.
func (h *Handler) listResearchReports(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	filter := appresearch.ReportFilter{
		Query:  strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:  queryInt(r, "limit", 20),
		Offset: queryInt(r, "offset", 0),
	}
	reports, total, err := h.services.Research.ListReports(r.Context(), filter)
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	data := make([]researchReportBody, 0, len(reports))
	for _, report := range reports {
		data = append(data, newResearchReportBody(report))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": filter.Limit, "offset": filter.Offset, "total": total},
	})
}

// runResearch handles POST /api/v1/research/reports: it investigates a
// question and stores the report.
func (h *Handler) runResearch(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	var body struct {
		Question       string   `json:"question"`
		Queries        []string `json:"queries"`
		MaxPages       int      `json:"max_pages"`
		SaveKnowledge  *bool    `json:"save_knowledge"`
		KnowledgeTitle string   `json:"knowledge_title"`
		SourceID       int64    `json:"source_id"`
	}
	if err := decodeResearchBody(r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	save := true
	if body.SaveKnowledge != nil {
		save = *body.SaveKnowledge
	}
	report, err := h.services.Research.Research(r.Context(), appresearch.ResearchInput{
		Question:       body.Question,
		Queries:        body.Queries,
		MaxPages:       body.MaxPages,
		SaveKnowledge:  save,
		KnowledgeTitle: body.KnowledgeTitle,
		ConnectionID:   body.SourceID,
	})
	if report != nil && err != nil {
		// The run is recorded with its failure, so the resource exists: the
		// client gets 201 with status "failed" and the reason inside. Answering
		// 5xx here would replace the explanation with a generic error title.
		h.writeJSON(w, http.StatusCreated, newResearchReportBody(report))
		return
	}
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, newResearchReportBody(report))
}

// getResearchReport handles GET /api/v1/research/reports/{id}.
func (h *Handler) getResearchReport(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	report, err := h.services.Research.GetReport(r.Context(), id)
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newResearchReportBody(report))
}

// deleteResearchReport handles DELETE /api/v1/research/reports/{id}.
func (h *Handler) deleteResearchReport(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	if err := h.services.Research.DeleteReport(r.Context(), id); err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// listResearchSources handles GET /api/v1/research/sources.
func (h *Handler) listResearchSources(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	filter := appresearch.ConnectionFilter{}
	if raw := strings.TrimSpace(r.URL.Query().Get("kind")); raw != "" {
		kind := domainresearch.SourceKind(raw)
		if !kind.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid kind")
			return
		}
		filter.Kind = &kind
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("enabled")); raw != "" {
		value := raw == "true" || raw == "1"
		filter.Enabled = &value
	}
	sources, err := h.services.Research.ListConnections(r.Context(), filter)
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	data := make([]researchSourceBody, 0, len(sources))
	for _, source := range sources {
		data = append(data, newResearchSourceBody(source))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]any{"total": len(data)}})
}

// createResearchSource handles POST /api/v1/research/sources. A source created
// from this endpoint belongs to the user, so it may be a script and may be
// enabled right away — the interface is the user's instrument.
func (h *Handler) createResearchSource(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	var body struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Mode     string `json:"mode"`
		Trust    string `json:"trust"`
		Format   string `json:"format"`
		Endpoint string `json:"endpoint"`
		Notes    string `json:"notes"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := decodeResearchBody(r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	connection := &domainresearch.Connection{
		Name:     body.Name,
		Kind:     domainresearch.SourceKind(body.Kind),
		Mode:     domainresearch.AccessMode(body.Mode),
		Trust:    domainresearch.Trust(body.Trust),
		Format:   domainresearch.SearchFormat(body.Format),
		Endpoint: body.Endpoint,
		Notes:    body.Notes,
		Enabled:  true,
	}
	if connection.Mode == "" {
		connection.Mode = domainresearch.ModeRead
	}
	if body.Enabled != nil {
		connection.Enabled = *body.Enabled
	}
	created, err := h.services.Research.CreateConnection(r.Context(), connection)
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, newResearchSourceBody(created))
}

// patchResearchSource handles PATCH /api/v1/research/sources/{id}.
func (h *Handler) patchResearchSource(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Name            *string `json:"name"`
		Notes           *string `json:"notes"`
		Trust           *string `json:"trust"`
		Mode            *string `json:"mode"`
		Endpoint        *string `json:"endpoint"`
		Format          *string `json:"format"`
		Enabled         *bool   `json:"enabled"`
		ExpectedVersion int     `json:"expected_version"`
	}
	if err := decodeResearchBody(r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	update := appresearch.ConnectionUpdate{
		Name: body.Name, Notes: body.Notes, Endpoint: body.Endpoint, Enabled: body.Enabled,
	}
	if body.Trust != nil {
		trust := domainresearch.Trust(*body.Trust)
		update.Trust = &trust
	}
	if body.Mode != nil {
		mode := domainresearch.AccessMode(*body.Mode)
		update.Mode = &mode
	}
	if body.Format != nil {
		format := domainresearch.SearchFormat(*body.Format)
		update.Format = &format
	}
	updated, err := h.services.Research.UpdateConnection(r.Context(), id, body.ExpectedVersion, update)
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newResearchSourceBody(updated))
}

// deleteResearchSource handles DELETE /api/v1/research/sources/{id}.
func (h *Handler) deleteResearchSource(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	if err := h.services.Research.DeleteConnection(r.Context(), id); err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// callResearchSource handles POST /api/v1/research/sources/{id}/call: one
// interaction with a connected source, journalled.
func (h *Handler) callResearchSource(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Operation string `json:"operation"`
		Method    string `json:"method"`
		Body      string `json:"body"`
		Query     string `json:"query"`
		Write     bool   `json:"write"`
	}
	if err := decodeResearchBody(r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	result, err := h.services.Research.CallSource(r.Context(), id, appresearch.CallRequest{
		Operation: body.Operation,
		Method:    body.Method,
		Body:      body.Body,
		Query:     body.Query,
		Write:     body.Write,
	})
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status":       result.Status,
		"content_type": result.ContentType,
		"body":         result.Body,
		"duration_ms":  result.Duration.Milliseconds(),
	})
}

// listResearchCalls handles GET /api/v1/research/sources/{id}/calls.
func (h *Handler) listResearchCalls(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	records, total, err := h.services.Research.ListCalls(r.Context(), id, queryInt(r, "limit", 20))
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	data := make([]researchCallBody, 0, len(records))
	for _, record := range records {
		data = append(data, newResearchCallBody(record))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]any{"total": total}})
}

// listResearchWatches handles GET /api/v1/research/watches.
func (h *Handler) listResearchWatches(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	includeDisabled := r.URL.Query().Get("include_disabled") == "true"
	watches, err := h.services.Research.ListWatches(r.Context(), includeDisabled)
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	data := make([]researchWatchBody, 0, len(watches))
	for _, watch := range watches {
		data = append(data, newResearchWatchBody(watch))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]any{"total": len(data)}})
}

// createResearchWatch handles POST /api/v1/research/watches.
func (h *Handler) createResearchWatch(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	var body struct {
		Topic   string   `json:"topic"`
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
		Cadence string   `json:"cadence"`
		Enabled *bool    `json:"enabled"`
	}
	if err := decodeResearchBody(r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	watch, err := h.services.Research.CreateWatch(r.Context(), appresearch.WatchInput{
		Topic: body.Topic, Query: body.Query, Queries: body.Queries,
		Cadence: domainresearch.Cadence(body.Cadence), Enabled: body.Enabled,
	})
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, newResearchWatchBody(watch))
}

// patchResearchWatch handles PATCH /api/v1/research/watches/{id}.
func (h *Handler) patchResearchWatch(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Topic           *string  `json:"topic"`
		Query           *string  `json:"query"`
		Queries         []string `json:"queries"`
		Cadence         *string  `json:"cadence"`
		Enabled         *bool    `json:"enabled"`
		ExpectedVersion int      `json:"expected_version"`
	}
	if err := decodeResearchBody(r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	update := appresearch.WatchUpdate{
		Topic: body.Topic, Query: body.Query, Queries: body.Queries, Enabled: body.Enabled,
	}
	if body.Cadence != nil {
		cadence := domainresearch.Cadence(*body.Cadence)
		update.Cadence = &cadence
	}
	watch, err := h.services.Research.UpdateWatch(r.Context(), id, body.ExpectedVersion, update)
	if err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newResearchWatchBody(watch))
}

// deleteResearchWatch handles DELETE /api/v1/research/watches/{id}.
func (h *Handler) deleteResearchWatch(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	if err := h.services.Research.DeleteWatch(r.Context(), id); err != nil {
		h.writeResearchError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// checkResearchWatch handles POST /api/v1/research/watches/{id}/check.
func (h *Handler) checkResearchWatch(w http.ResponseWriter, r *http.Request) {
	if h.services.Research == nil {
		h.researchUnavailable(w, r)
		return
	}
	id, ok := h.researchID(w, r, "id")
	if !ok {
		return
	}
	watch, diff, err := h.services.Research.CheckWatch(r.Context(), id)
	if err != nil && watch == nil {
		h.writeResearchError(w, r, err)
		return
	}
	body := map[string]any{"watch": newResearchWatchBody(watch)}
	if diff != nil {
		body["change"] = map[string]any{
			"summary":   diff.Summary(),
			"new":       researchFingerprintBodies(diff.New),
			"updated":   researchFingerprintBodies(diff.Updated),
			"unchanged": diff.Unchanged,
		}
	}
	if err != nil {
		// The check ran and failed: the watch is returned with the reason, not
		// exchanged for a generic 5xx title the client cannot show.
		body["error"] = err.Error()
	}
	h.writeJSON(w, http.StatusOK, body)
}

func researchFingerprintBodies(items []domainresearch.Fingerprint) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{"url": item.URL, "title": item.Title})
	}
	return result
}

func (h *Handler) writeResearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appresearch.ErrNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "research not found")
	case errors.Is(err, appresearch.ErrSourceNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "source not found")
	case errors.Is(err, appresearch.ErrWatchNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", "watch not found")
	case errors.Is(err, appresearch.ErrVersionConflict):
		h.writeProblem(w, r, http.StatusConflict, "version-conflict", "Version Conflict", err.Error())
	case errors.Is(err, appresearch.ErrSourceDisabled):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "source-disabled", "Source Disabled", err.Error())
	case errors.Is(err, appresearch.ErrSourceReadOnly):
		h.writeProblem(w, r, http.StatusForbidden, "source-read-only", "Source Read Only", err.Error())
	case errors.Is(err, appresearch.ErrScriptNeedsUser):
		h.writeProblem(w, r, http.StatusForbidden, "script-needs-user", "Script Needs User", err.Error())
	case errors.Is(err, appresearch.ErrNoProvider):
		h.writeProblem(w, r, http.StatusServiceUnavailable, "search-unavailable", "Search Unavailable", err.Error())
	case errors.Is(err, appresearch.ErrEmptyQuestion):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	}
}
