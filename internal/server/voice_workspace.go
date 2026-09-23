package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/voice"
)

// VOC workspaces: department intake, the knowledge gate, member registration.

func (s *Server) voiceWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := s.Voices.Workspaces(r.Context(), principal(r), false)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items, "causeEvidence": voice.CauseEvidenceLevels, "knowledgeStatuses": voice.KnowledgeStatuses})
}

func (s *Server) reviewVoice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		KnowledgeStatus string `json:"knowledgeStatus"`
		Note            string `json:"note"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Voices.Review(r.Context(), principal(r), r.PathValue("id"), in.KnowledgeStatus, in.Note, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) searchVoiceKnowledge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fields := map[string]string{}
	// Not "fields": that key is the response projection every GET accepts.
	if raw := strings.TrimSpace(q.Get("fieldFilters")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			httpx.ErrorJSON(w, r, http.StatusBadRequest, "invalid_field_filters", `fieldFilters는 {"항목키":"값"} 형태의 JSON이어야 합니다.`, nil)
			return
		}
	}
	items, err := s.Voices.SearchKnowledge(r.Context(), principal(r), voice.KnowledgeQuery{
		Query: q.Get("q"), WorkspaceID: q.Get("workspaceId"), Fields: fields, CauseEvidence: q.Get("causeEvidence"),
		IncludeUnreviewed: q.Get("includeUnreviewed") == "true", Limit: httpx.IntQuery(r, "limit", 10, 1, 50),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) voiceHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := s.Voices.History(r.Context(), principal(r), q.Get("customerId"), q.Get("customerCode"), q.Get("workspaceId"), httpx.IntQuery(r, "limit", 30, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) quickRegisterCustomer(w http.ResponseWriter, r *http.Request) {
	var in voice.QuickCustomerInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Voices.QuickRegister(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}

func (s *Server) importWorkspaceCustomers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rows        []voice.ImportRow `json:"rows"`
		UpdateNames bool              `json:"updateNames"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Voices.ImportCustomers(r.Context(), principal(r), r.PathValue("id"), in.Rows, in.UpdateNames, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

// ---------------------------------------------------------------- admin

func (s *Server) adminVoiceWorkspaces(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	items, err := s.Voices.Workspaces(r.Context(), p, true)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items, "presets": voice.Presets()})
}

func (s *Server) createVoiceWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in voice.WorkspaceInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id, err := s.Voices.CreateWorkspace(r.Context(), in)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "VOICE_WORKSPACE_CREATE", "voice_workspace", id, nil, in)
	httpx.JSON(w, 201, map[string]any{"id": id})
}

func (s *Server) updateVoiceWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in voice.WorkspaceInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if err := s.Voices.UpdateWorkspace(r.Context(), id, in); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "VOICE_WORKSPACE_UPDATE", "voice_workspace", id, nil, in)
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) applyVoicePreset(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		OrganizationID string `json:"organizationId"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.OrganizationID) == "" {
		s.serviceError(w, r, errors.New("구성을 적용할 부서(조직)를 선택해야 합니다"))
		return
	}
	v, err := s.Voices.ApplyPreset(r.Context(), p, r.PathValue("code"), in.OrganizationID)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "VOICE_PRESET_APPLY", "voice_workspace", v.WorkspaceID, nil, map[string]any{"preset": r.PathValue("code"), "organizationId": in.OrganizationID, "created": v.Created, "skipped": v.Skipped})
	httpx.JSON(w, 200, v)
}
