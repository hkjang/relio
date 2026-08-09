package server

import (
	"net/http"

	"github.com/hkjang/relio/internal/intelligence"
	"github.com/hkjang/relio/internal/platform/httpx"
)

func (s *Server) dealHealth(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.DealHealth(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) dealInspection(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.DealInspection(r.Context(), principal(r), r.PathValue("id"), httpx.IntQuery(r, "days", 7, 1, 365))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) dealsAtRisk(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.DealsAtRisk(r.Context(), principal(r), httpx.IntQuery(r, "minimum", 40, 1, 100), httpx.IntQuery(r, "limit", 25, 1, 100))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v, "count": len(v)})
}

func (s *Server) coachingDashboard(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.Coaching(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) opportunityPlaybook(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.Playbook(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) updatePlaybookProgress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Completed bool   `json:"completed"`
		Notes     string `json:"notes"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Intel.SetPlaybookProgress(r.Context(), principal(r), r.PathValue("id"), r.PathValue("itemId"), in.Completed, in.Notes, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) stageReadiness(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.ValidateStageTransition(r.Context(), principal(r), r.PathValue("id"), r.URL.Query().Get("stageId"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) forecastIntelligence(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.ForecastIntelligence(r.Context(), principal(r), httpx.IntQuery(r, "days", 7, 1, 365))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) forecastOverride(w http.ResponseWriter, r *http.Request) {
	var in intelligence.ForecastOverrideInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Intel.SaveForecastOverride(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) adminSalesExecution(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.AdminStageExecutions(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}

func (s *Server) saveSalesExecution(w http.ResponseWriter, r *http.Request) {
	var in intelligence.StageExecutionInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Intel.SaveStageExecution(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) adminDealHealthRules(w http.ResponseWriter, r *http.Request) {
	v, err := s.Intel.AdminHealthRules(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}

func (s *Server) saveDealHealthRule(w http.ResponseWriter, r *http.Request) {
	var in intelligence.HealthRuleInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Intel.SaveHealthRule(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
