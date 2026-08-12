package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/intelligence"
	"github.com/hkjang/relio/internal/platform/httpx"
)

// HTTP for the four intelligence records. Reads are filters over the caller's
// Data Scope; the only writes are the human decisions — accept, dismiss, ignore
// — plus running the engine, which is an administrator action.

func (s *Server) listSignals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.Intel.ListSignals(r.Context(), principal(r), intelligence.SignalFilter{
		AccountID:  q.Get("accountId"),
		EntityType: q.Get("entityType"),
		EntityID:   q.Get("entityId"),
		SignalType: q.Get("signalType"),
		Severity:   q.Get("severity"),
		Sentiment:  q.Get("sentiment"),
		Status:     q.Get("status"),
		Limit:      httpx.IntQuery(r, "limit", 50, 1, 200),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) getSignal(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.GetSignal(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) ignoreSignal(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.IgnoreSignal(r.Context(), principal(r), r.PathValue("id"), s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) listRisks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.Intel.ListRisks(r.Context(), principal(r), intelligence.RiskFilter{
		AccountID:  q.Get("accountId"),
		EntityType: q.Get("entityType"),
		EntityID:   q.Get("entityId"),
		RiskType:   q.Get("riskType"),
		Severity:   q.Get("severity"),
		Status:     q.Get("status"),
		MinScore:   httpx.IntQuery(r, "minScore", 0, 0, 100),
		Limit:      httpx.IntQuery(r, "limit", 50, 1, 200),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) getRisk(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.GetRisk(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) explainRisk(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.ExplainRisk(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) acceptRisk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note string `json:"note"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Intel.AcceptRisk(r.Context(), principal(r), r.PathValue("id"), in.Note, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) listInsights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.Intel.ListInsights(r.Context(), principal(r), intelligence.InsightFilter{
		AccountID:     q.Get("accountId"),
		OpportunityID: q.Get("opportunityId"),
		InsightType:   q.Get("insightType"),
		Status:        q.Get("status"),
		Limit:         httpx.IntQuery(r, "limit", 50, 1, 200),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) getInsight(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.GetInsight(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) listRecommendations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.Intel.ListRecommendations(r.Context(), principal(r), intelligence.RecommendationFilter{
		AccountID:     q.Get("accountId"),
		OpportunityID: q.Get("opportunityId"),
		AssigneeID:    q.Get("assigneeId"),
		Mine:          q.Get("mine") == "true",
		Priority:      q.Get("priority"),
		Status:        q.Get("status"),
		Limit:         httpx.IntQuery(r, "limit", 50, 1, 200),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) getRecommendation(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.GetRecommendation(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) acceptRecommendation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AssigneeID string `json:"assigneeId"`
		DueDate    string `json:"dueDate"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	var due *time.Time
	if date := strings.TrimSpace(in.DueDate); date != "" {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			httpx.ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "dueDate는 YYYY-MM-DD 형식이어야 합니다.", nil)
			return
		}
		due = &parsed
	}
	v, err := s.Intel.AcceptRecommendation(r.Context(), principal(r), r.PathValue("id"), in.AssigneeID, due, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) dismissRecommendation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Intel.DismissRecommendation(r.Context(), principal(r), r.PathValue("id"), in.Reason, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

// customerIntelligence is the Customer 360 and Opportunity panel in one call, so
// a detail screen does not fan out into four requests to draw one box.
func (s *Server) customerIntelligence(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.AccountIntelligenceFor(r.Context(), principal(r), r.PathValue("id"), r.URL.Query().Get("opportunityId"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) intelligenceStatus(w http.ResponseWriter, r *http.Request) {
	run, err := s.Intel.LastRun(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"lastRun": run})
}

func (s *Server) runIntelligence(w http.ResponseWriter, r *http.Request) {
	summary, err := s.Intel.RunIntelligence(r.Context(), principal(r), "MANUAL")
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, summary)
}
