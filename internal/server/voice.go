package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/voice"
)

func (s *Server) listVoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.Voices.List(r.Context(), principal(r), voice.Query{
		CustomerID: q.Get("customerId"),
		Status:     q.Get("status"),
		VoiceType:  q.Get("voiceType"),
		Severity:   q.Get("severity"),
		OwnerID:    q.Get("ownerId"),
		Overdue:    q.Get("overdue") == "true",
		OpenOnly:   q.Get("open") == "true",
		Limit:      httpx.IntQuery(r, "limit", 50, 1, 200),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createVoice(w http.ResponseWriter, r *http.Request) {
	var in voice.Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Voices.Create(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}

func (s *Server) getVoice(w http.ResponseWriter, r *http.Request) {
	v, events, err := s.Voices.Get(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"voice": v, "events": events})
}

func (s *Server) updateVoice(w http.ResponseWriter, r *http.Request) {
	var in voice.UpdateInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Voices.Update(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) commentVoice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EventType string `json:"eventType"`
		Note      string `json:"note"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.EventType) == "" {
		in.EventType = "COMMENT"
	}
	events, err := s.Voices.Comment(r.Context(), principal(r), r.PathValue("id"), in.EventType, in.Note, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"events": events})
}

func (s *Server) voiceSummary(w http.ResponseWriter, r *http.Request) {
	v, err := s.Voices.Summary(r.Context(), principal(r), r.URL.Query().Get("customerId"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) voiceCategories(w http.ResponseWriter, r *http.Request) {
	items, err := s.Voices.Categories(r.Context(), principal(r), r.URL.Query().Get("includeInactive") == "true")
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

// ---------------------------------------------------------------- admin

func (s *Server) adminVoiceCategories(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT c.id,c.code,c.name,c.voice_type,c.response_hours,c.resolution_hours,c.active,c.display_order,
		(SELECT count(*) FROM customer_voices v WHERE v.category_id=c.id) FROM voice_categories c ORDER BY c.display_order,c.name`)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, code, name, kind string
		var response, resolution, order, used int
		var active bool
		if err = rows.Scan(&id, &code, &name, &kind, &response, &resolution, &active, &order, &used); err != nil {
			s.serviceError(w, r, err)
			return
		}
		items = append(items, map[string]any{"id": id, "code": code, "name": name, "voiceType": kind,
			"responseHours": response, "resolutionHours": resolution, "active": active, "displayOrder": order, "usedCount": used})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

type voiceCategoryInput struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	VoiceType       string `json:"voiceType"`
	ResponseHours   int    `json:"responseHours"`
	ResolutionHours int    `json:"resolutionHours"`
	Active          *bool  `json:"active"`
	DisplayOrder    int    `json:"displayOrder"`
}

func (in voiceCategoryInput) validate() error {
	return voice.ValidateCategory(in.Code, in.Name, in.VoiceType, in.ResponseHours, in.ResolutionHours)
}

func (s *Server) createVoiceCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in voiceCategoryInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		s.serviceError(w, r, err)
		return
	}
	id, err := s.Voices.CreateCategory(r.Context(), voice.Category{
		Code: in.Code, Name: in.Name, VoiceType: in.VoiceType,
		ResponseHours: in.ResponseHours, ResolutionHours: in.ResolutionHours, DisplayOrder: in.DisplayOrder,
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "VOICE_CATEGORY_CREATE", "voice_category", id, nil, in)
	httpx.JSON(w, 201, map[string]any{"id": id})
}

func (s *Server) updateVoiceCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in voiceCategoryInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		s.serviceError(w, r, err)
		return
	}
	id := r.PathValue("id")
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	_, err := s.DB.Exec(r.Context(), `UPDATE voice_categories SET name=$2,voice_type=$3,response_hours=$4,resolution_hours=$5,active=$6,display_order=$7,updated_at=now() WHERE id=$1`,
		id, strings.TrimSpace(in.Name), strings.ToUpper(in.VoiceType), in.ResponseHours, in.ResolutionHours, active, in.DisplayOrder)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "VOICE_CATEGORY_UPDATE", "voice_category", id, nil, in)
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) deleteVoiceCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var name string
	var used int
	err := s.DB.QueryRow(r.Context(), `SELECT c.name,(SELECT count(*) FROM customer_voices v WHERE v.category_id=c.id) FROM voice_categories c WHERE c.id=$1`, id).Scan(&name, &used)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if used > 0 {
		// Historical records keep their category, so retire it instead.
		if _, err = s.DB.Exec(r.Context(), `UPDATE voice_categories SET active=false,updated_at=now() WHERE id=$1`, id); err != nil {
			s.serviceError(w, r, err)
			return
		}
		s.auditAdmin(r, p, "VOICE_CATEGORY_RETIRE", "voice_category", id, map[string]any{"name": name, "usedCount": used}, map[string]any{"active": false})
		httpx.JSON(w, 200, map[string]any{"retired": true, "usedCount": used, "note": "이미 사용된 유형이라 사용 중지 처리했습니다. 기존 접수 이력은 그대로 유지됩니다."})
		return
	}
	if err = s.deleteGuarded(r.Context(), "고객 요청 유형", `DELETE FROM voice_categories WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "VOICE_CATEGORY_DELETE", "voice_category", id, map[string]any{"name": name}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// exportVoices honours the administrator export policy and audits every download,
// because a VOC extract contains customer complaints verbatim.
func (s *Server) exportVoices(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.policyEnabled(r.Context(), "security", "export_enabled", true) {
		httpx.ErrorJSON(w, r, http.StatusForbidden, "export_disabled", "관리자 정책으로 내보내기가 비활성화되어 있습니다.", nil)
		return
	}
	q := r.URL.Query()
	body, count, err := s.Voices.CSV(r.Context(), p, voice.Query{
		CustomerID: q.Get("customerId"),
		Status:     q.Get("status"),
		VoiceType:  q.Get("voiceType"),
		Severity:   q.Get("severity"),
		OwnerID:    q.Get("ownerId"),
		Overdue:    q.Get("overdue") == "true",
		OpenOnly:   q.Get("open") == "true",
		Limit:      httpx.IntQuery(r, "limit", 200, 1, 200),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "WEB",
		Action: "VOICE_EXPORT", Resource: "customer_voice",
		After: map[string]any{"rows": count, "filters": r.URL.RawQuery},
		IP:    httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="relio-voices-`+time.Now().Format("20060102")+`.csv"`)
	w.WriteHeader(http.StatusOK)
	// Excel needs the BOM to read UTF-8 Korean correctly.
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	_, _ = w.Write([]byte(body))
}
