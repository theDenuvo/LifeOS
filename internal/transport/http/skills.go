package httptransport

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	appskills "github.com/theDenuvo/LifeOS/internal/application/skills"
	domainskill "github.com/theDenuvo/LifeOS/internal/domain/skill"
)

// Roadmap stage 8: skills and assistant self-improvement.

type skillVersionBody struct {
	VersionNo  int    `json:"version_no"`
	Name       string `json:"name"`
	Purpose    string `json:"purpose"`
	WhenToUse  string `json:"when_to_use"`
	ChangeNote string `json:"change_note"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
}

type skillProposalBody struct {
	ID             int64            `json:"id"`
	SkillID        *int64           `json:"skill_id,omitempty"`
	Kind           string           `json:"kind"`
	Status         string           `json:"status"`
	Problem        string           `json:"problem"`
	Evidence       []string         `json:"evidence"`
	Proposed       skillContentBody `json:"proposed"`
	ExpectedEffect string           `json:"expected_effect"`
	TestScenarios  []string         `json:"test_scenarios"`
	TestResult     string           `json:"test_result"`
	BaseVersion    *int             `json:"base_version,omitempty"`
	ResultSkillID  *int64           `json:"result_skill_id,omitempty"`
	Diff           []string         `json:"diff,omitempty"`
	CreatedAt      string           `json:"created_at"`
	ResolvedAt     *string          `json:"resolved_at,omitempty"`
	Version        int              `json:"version"`
}

type skillContentBody struct {
	Name         string   `json:"name"`
	Purpose      string   `json:"purpose"`
	WhenToUse    string   `json:"when_to_use"`
	Instructions string   `json:"instructions"`
	Limitations  string   `json:"limitations"`
	Examples     string   `json:"examples"`
	Tags         []string `json:"tags"`
}

type skillBody struct {
	ID                 int64               `json:"id"`
	Name               string              `json:"name"`
	Purpose            string              `json:"purpose"`
	WhenToUse          string              `json:"when_to_use"`
	Instructions       string              `json:"instructions"`
	Limitations        string              `json:"limitations"`
	Examples           string              `json:"examples"`
	Tags               []string            `json:"tags"`
	Status             string              `json:"status"`
	CurrentVersion     int                 `json:"current_version"`
	Source             string              `json:"source"`
	CreatedByAssistant bool                `json:"created_by_assistant"`
	UseCount           int64               `json:"use_count"`
	GoodCount          int64               `json:"good_count"`
	BadCount           int64               `json:"bad_count"`
	BadRatio           float64             `json:"bad_ratio"`
	LastUsedAt         *string             `json:"last_used_at,omitempty"`
	CreatedAt          string              `json:"created_at"`
	UpdatedAt          string              `json:"updated_at"`
	Version            int                 `json:"version"`
	Versions           []skillVersionBody  `json:"versions,omitempty"`
	Proposals          []skillProposalBody `json:"proposals,omitempty"`
	Feedback           []skillFeedbackBody `json:"feedback,omitempty"`
	Usages             []skillUsageBody    `json:"usages,omitempty"`
}

type skillFeedbackBody struct {
	ID        int64  `json:"id"`
	VersionNo int    `json:"version_no"`
	Rating    string `json:"rating"`
	Comment   string `json:"comment"`
	CreatedAt string `json:"created_at"`
}

type skillUsageBody struct {
	ID        int64    `json:"id"`
	VersionNo int      `json:"version_no"`
	Context   string   `json:"context"`
	Matched   []string `json:"matched"`
	CreatedAt string   `json:"created_at"`
}

func newSkillBody(view *appskills.View, full bool) skillBody {
	skill := view.Skill
	tags := skill.Tags
	if tags == nil {
		tags = []string{}
	}
	body := skillBody{
		ID: skill.ID, Name: skill.Name, Purpose: skill.Purpose, WhenToUse: skill.WhenToUse,
		Instructions: skill.Instructions, Limitations: skill.Limitations, Examples: skill.Examples,
		Tags: tags, Status: string(skill.Status), CurrentVersion: skill.CurrentVersion,
		Source: string(skill.Source), CreatedByAssistant: skill.CreatedByAssistant,
		UseCount: skill.UseCount, GoodCount: skill.GoodCount, BadCount: skill.BadCount,
		BadRatio: skill.BadRatio(), LastUsedAt: formatOptionalTime(skill.LastUsedAt),
		CreatedAt: formatTime(skill.CreatedAt), UpdatedAt: formatTime(skill.UpdatedAt), Version: skill.Version,
	}
	if !full {
		return body
	}
	// Full views carry instruction bodies; summaries in a list must not
	// dump every skill's text into one response.
	body.Versions = []skillVersionBody{}
	for _, version := range view.Versions {
		body.Versions = append(body.Versions, skillVersionBody{
			VersionNo: version.VersionNo, Name: version.Content.Name,
			Purpose: version.Content.Purpose, WhenToUse: version.Content.WhenToUse,
			ChangeNote: version.ChangeNote, CreatedBy: string(version.CreatedBy),
			CreatedAt: formatTime(version.CreatedAt),
		})
	}
	body.Proposals = []skillProposalBody{}
	for _, proposal := range view.Proposals {
		body.Proposals = append(body.Proposals, newSkillProposalBody(proposal, skill.Content()))
	}
	body.Feedback = []skillFeedbackBody{}
	for _, item := range view.Feedback {
		body.Feedback = append(body.Feedback, skillFeedbackBody{
			ID: item.ID, VersionNo: item.VersionNo, Rating: string(item.Rating),
			Comment: item.Comment, CreatedAt: formatTime(item.CreatedAt),
		})
	}
	body.Usages = []skillUsageBody{}
	for _, item := range view.Usages {
		body.Usages = append(body.Usages, skillUsageBody{
			ID: item.ID, VersionNo: item.VersionNo, Context: item.Context,
			Matched: item.MatchedTerms, CreatedAt: formatTime(item.CreatedAt),
		})
	}
	return body
}

func newSkillProposalBody(proposal *domainskill.Proposal, current domainskill.Content) skillProposalBody {
	evidence := proposal.Evidence
	if evidence == nil {
		evidence = []string{}
	}
	scenarios := proposal.TestScenarios
	if scenarios == nil {
		scenarios = []string{}
	}
	tags := proposal.Proposed.Tags
	if tags == nil {
		tags = []string{}
	}
	body := skillProposalBody{
		ID: proposal.ID, SkillID: proposal.SkillID, Kind: string(proposal.Kind),
		Status: string(proposal.Status), Problem: proposal.Problem, Evidence: evidence,
		Proposed: skillContentBody{
			Name: proposal.Proposed.Name, Purpose: proposal.Proposed.Purpose,
			WhenToUse: proposal.Proposed.WhenToUse, Instructions: proposal.Proposed.Instructions,
			Limitations: proposal.Proposed.Limitations, Examples: proposal.Proposed.Examples, Tags: tags,
		},
		ExpectedEffect: proposal.ExpectedEffect, TestScenarios: scenarios, TestResult: proposal.TestResult,
		BaseVersion: proposal.BaseVersion, ResultSkillID: proposal.ResultSkillID,
		CreatedAt: formatTime(proposal.CreatedAt), ResolvedAt: formatOptionalTime(proposal.ResolvedAt),
		Version: proposal.Version,
	}
	if proposal.Kind == domainskill.ProposalSkillVersion {
		body.Diff = domainskill.Diff(current, proposal.Proposed)
	}
	return body
}

func (h *Handler) skillsUnavailable(w http.ResponseWriter, r *http.Request) {
	h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", "Skills are not configured")
}

func (h *Handler) skillID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid skill id")
		return 0, false
	}
	return id, true
}

// listSkills handles GET /api/v1/skills.
func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	filter := appskills.Filter{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status := domainskill.Status(raw)
		if !status.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
			return
		}
		filter.Status = &status
	}
	filter.Limit = queryInt(r, "limit", 30)
	filter.Offset = queryInt(r, "offset", 0)
	skills, total, err := h.services.Skills.List(r.Context(), filter)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	data := make([]skillBody, 0, len(skills))
	for _, skill := range skills {
		data = append(data, newSkillBody(&appskills.View{Skill: skill}, false))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"limit": filter.Limit, "offset": filter.Offset, "total": total},
	})
}

// createSkill handles POST /api/v1/skills. The created skill always
// starts as a draft: activation is a separate user decision.
func (h *Handler) createSkill(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	var body struct {
		Name         string   `json:"name"`
		Purpose      string   `json:"purpose"`
		WhenToUse    string   `json:"when_to_use"`
		Instructions string   `json:"instructions"`
		Limitations  string   `json:"limitations"`
		Examples     string   `json:"examples"`
		Tags         []string `json:"tags"`
		ChangeNote   string   `json:"change_note"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	view, err := h.services.Skills.Create(r.Context(), appskills.CreateInput{
		Content: domainskill.Content{
			Name: body.Name, Purpose: body.Purpose, WhenToUse: body.WhenToUse,
			Instructions: body.Instructions, Limitations: body.Limitations,
			Examples: body.Examples, Tags: body.Tags,
		},
		ChangeNote: body.ChangeNote,
	}, domainskill.SourceUser)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Itoa(view.Skill.Version))
	h.writeJSON(w, http.StatusCreated, newSkillBody(view, true))
}

// getSkill handles GET /api/v1/skills/{id}.
func (h *Handler) getSkill(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	id, ok := h.skillID(w, r)
	if !ok {
		return
	}
	view, err := h.services.Skills.Get(r.Context(), id)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Itoa(view.Skill.Version))
	h.writeJSON(w, http.StatusOK, newSkillBody(view, true))
}

// patchSkill handles PATCH /api/v1/skills/{id} under If-Match. A real
// edit becomes a new version; history is never rewritten.
func (h *Handler) patchSkill(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	id, ok := h.skillID(w, r)
	if !ok {
		return
	}
	version, ok := h.expectedVersion(w, r)
	if !ok {
		return
	}
	var body struct {
		Name         *string   `json:"name"`
		Purpose      *string   `json:"purpose"`
		WhenToUse    *string   `json:"when_to_use"`
		Instructions *string   `json:"instructions"`
		Limitations  *string   `json:"limitations"`
		Examples     *string   `json:"examples"`
		Tags         *[]string `json:"tags"`
		ChangeNote   string    `json:"change_note"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	view, err := h.services.Skills.Update(r.Context(), id, version, appskills.UpdateInput{
		Name: body.Name, Purpose: body.Purpose, WhenToUse: body.WhenToUse,
		Instructions: body.Instructions, Limitations: body.Limitations,
		Examples: body.Examples, Tags: body.Tags, ChangeNote: body.ChangeNote,
	}, domainskill.SourceUser)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Itoa(view.Skill.Version))
	h.writeJSON(w, http.StatusOK, newSkillBody(view, true))
}

// deleteSkill handles DELETE /api/v1/skills/{id}. Only a draft or
// archived skill can be deleted; the interface confirms first.
func (h *Handler) deleteSkill(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	id, ok := h.skillID(w, r)
	if !ok {
		return
	}
	if err := h.services.Skills.Delete(r.Context(), id); err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// changeSkillStatus handles POST /api/v1/skills/{id}/status.
func (h *Handler) changeSkillStatus(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	id, ok := h.skillID(w, r)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	status := domainskill.Status(body.Status)
	if !status.IsValid() {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
		return
	}
	current, err := h.services.Skills.Get(r.Context(), id)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	view, err := h.services.Skills.ChangeStatus(r.Context(), id, current.Skill.Version, status)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newSkillBody(view, true))
}

// rollbackSkill handles POST /api/v1/skills/{id}/versions/{version}/rollback
// under If-Match: the client sends the version it saw when it asked for
// the confirmation, and a changed skill is refused instead of being
// rolled back from an unseen state.
func (h *Handler) rollbackSkill(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	id, ok := h.skillID(w, r)
	if !ok {
		return
	}
	version, ok := h.expectedVersion(w, r)
	if !ok {
		return
	}
	versionNo, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || versionNo < 1 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid version")
		return
	}
	var body struct {
		ChangeNote string `json:"change_note"`
	}
	_ = decodeJSONBody(w, r, &body)
	view, err := h.services.Skills.Rollback(r.Context(), id, version, versionNo, body.ChangeNote, domainskill.SourceUser)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newSkillBody(view, true))
}

// listSkillVersions handles GET /api/v1/skills/{id}/versions.
func (h *Handler) listSkillVersions(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	id, ok := h.skillID(w, r)
	if !ok {
		return
	}
	view, err := h.services.Skills.Get(r.Context(), id)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": newSkillBody(view, true).Versions})
}

// rateSkill handles POST /api/v1/skills/{id}/feedback.
func (h *Handler) rateSkill(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	id, ok := h.skillID(w, r)
	if !ok {
		return
	}
	var body struct {
		Rating         string `json:"rating"`
		Comment        string `json:"comment"`
		ConversationID *int64 `json:"conversation_id"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	feedback, err := h.services.Skills.Rate(r.Context(), id, domainskill.Rating(body.Rating), body.Comment, body.ConversationID)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id": feedback.ID, "skill_id": feedback.SkillID, "rating": string(feedback.Rating),
		"comment": feedback.Comment, "created_at": formatTime(feedback.CreatedAt),
	})
}

// analyzeSkills handles POST /api/v1/skills/analyze.
func (h *Handler) analyzeSkills(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	var body struct {
		SkillID int64 `json:"skill_id"`
	}
	_ = decodeJSONBody(w, r, &body)
	findings, err := h.services.Skills.Analyze(r.Context(), body.SkillID)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(findings))
	for _, finding := range findings {
		items = append(items, map[string]any{
			"skill_id": finding.SkillID, "skill_name": finding.SkillName, "kind": string(finding.Kind),
			"problem": finding.Problem, "evidence": finding.Evidence,
			"expected_effect": finding.ExpectedEffect, "test_result": finding.TestResult,
			"test_scenarios": finding.TestScenarios,
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"findings": items, "total": len(items)})
}

// proposeSkillImprovements handles POST /api/v1/skills/propose.
func (h *Handler) proposeSkillImprovements(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	var body struct {
		SkillID int64 `json:"skill_id"`
	}
	_ = decodeJSONBody(w, r, &body)
	proposals, err := h.services.Skills.ProposeImprovements(r.Context(), body.SkillID, nil)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	items := make([]skillProposalBody, 0, len(proposals))
	for _, proposal := range proposals {
		items = append(items, newSkillProposalBody(proposal, domainskill.Content{}))
	}
	h.writeJSON(w, http.StatusCreated, map[string]any{"proposals": items, "total": len(items)})
}

// listSkillProposals handles GET /api/v1/skills/proposals.
func (h *Handler) listSkillProposals(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	var skillID int64
	if raw := strings.TrimSpace(r.URL.Query().Get("skill_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid skill_id")
			return
		}
		skillID = parsed
	}
	var status *domainskill.ProposalStatus
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		parsed := domainskill.ProposalStatus(raw)
		if !parsed.IsValid() {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid status")
			return
		}
		status = &parsed
	}
	proposals, err := h.services.Skills.ListProposals(r.Context(), skillID, status)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	items := make([]skillProposalBody, 0, len(proposals))
	for _, proposal := range proposals {
		items = append(items, newSkillProposalBody(proposal, domainskill.Content{}))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": items, "total": len(items)})
}

// createSkillProposal handles POST /api/v1/skills/proposals.
func (h *Handler) createSkillProposal(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	var body struct {
		SkillID        *int64           `json:"skill_id"`
		Kind           string           `json:"kind"`
		Problem        string           `json:"problem"`
		Evidence       []string         `json:"evidence"`
		Proposed       skillContentBody `json:"proposed"`
		ExpectedEffect string           `json:"expected_effect"`
		TestScenarios  []string         `json:"test_scenarios"`
		BaseVersion    *int             `json:"base_version"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	proposal, err := h.services.Skills.CreateProposal(r.Context(), appskills.ProposalInput{
		SkillID: body.SkillID, Kind: domainskill.ProposalKind(body.Kind),
		Problem: body.Problem, Evidence: body.Evidence,
		Proposed: domainskill.Content{
			Name: body.Proposed.Name, Purpose: body.Proposed.Purpose,
			WhenToUse: body.Proposed.WhenToUse, Instructions: body.Proposed.Instructions,
			Limitations: body.Proposed.Limitations, Examples: body.Proposed.Examples,
			Tags: body.Proposed.Tags,
		},
		ExpectedEffect: body.ExpectedEffect, TestScenarios: body.TestScenarios,
		BaseVersion: body.BaseVersion,
	})
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, newSkillProposalBody(proposal, domainskill.Content{}))
}

// decideSkillProposal handles POST /api/v1/skills/proposals/{proposalId}/apply
// and .../reject. One handler serves both: the action comes from the path.
func (h *Handler) decideSkillProposal(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	proposalID, err := strconv.ParseInt(r.PathValue("proposalId"), 10, 64)
	if err != nil || proposalID <= 0 {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", "invalid proposal id")
		return
	}
	proposal, err := h.services.Skills.GetProposal(r.Context(), proposalID)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/apply") {
		applied, _, err := h.services.Skills.ApplyProposal(r.Context(), proposalID, proposal.Version)
		if err != nil {
			h.writeSkillError(w, r, err)
			return
		}
		h.writeJSON(w, http.StatusOK, newSkillProposalBody(applied, domainskill.Content{}))
		return
	}
	rejected, err := h.services.Skills.RejectProposal(r.Context(), proposalID, proposal.Version)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, newSkillProposalBody(rejected, domainskill.Content{}))
}

// getWorkingInstructions handles GET /api/v1/skills/working-instructions.
func (h *Handler) getWorkingInstructions(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	text, err := h.services.Skills.GetWorkingInstructions(r.Context())
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"instructions": text})
}

// putWorkingInstructions handles PUT /api/v1/skills/working-instructions.
func (h *Handler) putWorkingInstructions(w http.ResponseWriter, r *http.Request) {
	if h.services.Skills == nil {
		h.skillsUnavailable(w, r)
		return
	}
	var body struct {
		Instructions string `json:"instructions"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
		return
	}
	text, err := h.services.Skills.SetWorkingInstructions(r.Context(), body.Instructions)
	if err != nil {
		h.writeSkillError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"instructions": text})
}

func (h *Handler) writeSkillError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appskills.ErrNotFound), errors.Is(err, appskills.ErrProposalNotFound), errors.Is(err, appskills.ErrVersionNotFound):
		h.writeProblem(w, r, http.StatusNotFound, "not-found", "Not Found", err.Error())
	case errors.Is(err, appskills.ErrVersionConflict), errors.Is(err, appskills.ErrStaleBase):
		h.writeProblem(w, r, http.StatusConflict, "version-conflict", "Version Conflict", err.Error())
	case errors.Is(err, appskills.ErrInvalidTransition), errors.Is(err, appskills.ErrDeleteActive), errors.Is(err, appskills.ErrProposalResolved), errors.Is(err, appskills.ErrEmptyUpdate):
		h.writeProblem(w, r, http.StatusPreconditionFailed, "invalid-transition", "Invalid Transition", err.Error())
	case errors.Is(err, appskills.ErrNoWorkingStore):
		h.writeProblem(w, r, http.StatusServiceUnavailable, "unavailable", "Unavailable", err.Error())
	case errors.Is(err, domainskill.ErrInvalidName), errors.Is(err, domainskill.ErrInvalidPurpose),
		errors.Is(err, domainskill.ErrInvalidWhen), errors.Is(err, domainskill.ErrInvalidInstructions),
		errors.Is(err, domainskill.ErrInvalidLimitations), errors.Is(err, domainskill.ErrInvalidExamples),
		errors.Is(err, domainskill.ErrInvalidTags), errors.Is(err, domainskill.ErrForbiddenContent),
		errors.Is(err, domainskill.ErrInvalidProblem), errors.Is(err, domainskill.ErrInvalidEffect),
		errors.Is(err, domainskill.ErrInvalidComment):
		h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
	default:
		// Domain validation errors wrap the sentinels above; anything
		// else with a validation flavour still belongs to 400 so the
		// interface can show the reason instead of a generic 500.
		if isSkillValidationError(err) {
			h.writeProblem(w, r, http.StatusBadRequest, "validation-failed", "Validation Failed", err.Error())
			return
		}
		h.writeProblem(w, r, http.StatusInternalServerError, "internal", "Internal Server Error", err.Error())
	}
}

func isSkillValidationError(err error) bool {
	for _, sentinel := range []error{
		domainskill.ErrInvalidName, domainskill.ErrInvalidPurpose, domainskill.ErrInvalidWhen,
		domainskill.ErrInvalidInstructions, domainskill.ErrInvalidLimitations, domainskill.ErrInvalidExamples,
		domainskill.ErrInvalidTags, domainskill.ErrForbiddenContent, domainskill.ErrInvalidProblem,
		domainskill.ErrInvalidEffect, domainskill.ErrInvalidComment,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
