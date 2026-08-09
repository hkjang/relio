package server

import (
	"net/http"

	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/relationship"
)

func (s *Server) customerRelationships(w http.ResponseWriter, r *http.Request) {
	v, err := s.Relations.Graph(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

func (s *Server) saveCustomerRelationship(w http.ResponseWriter, r *http.Request) {
	var in relationship.ContactRelationshipInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Relations.SaveRelationship(r.Context(), principal(r), r.PathValue("id"), r.PathValue("relationshipId"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	status := http.StatusOK
	if r.PathValue("relationshipId") == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, v)
}

func (s *Server) deleteCustomerRelationship(w http.ResponseWriter, r *http.Request) {
	err := s.Relations.DeleteRelationship(r.Context(), principal(r), r.PathValue("id"), r.PathValue("relationshipId"), httpx.IntQuery(r, "version", -1, 0, 1_000_000), s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) accountPlan(w http.ResponseWriter, r *http.Request) {
	v, err := s.Relations.GetAccountPlan(r.Context(), principal(r), r.PathValue("id"), httpx.IntQuery(r, "year", 0, 2000, 2200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

func (s *Server) saveAccountPlan(w http.ResponseWriter, r *http.Request) {
	var in relationship.AccountPlanInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Relations.SaveAccountPlan(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

func (s *Server) crossSell(w http.ResponseWriter, r *http.Request) {
	v, err := s.Relations.CrossSellOpportunities(r.Context(), principal(r), r.PathValue("id"), httpx.IntQuery(r, "year", 0, 2000, 2200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": v, "count": len(v)})
}

func (s *Server) opportunityTeam(w http.ResponseWriter, r *http.Request) {
	v, err := s.Relations.OpportunityTeam(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": v})
}

func (s *Server) saveOpportunityMember(w http.ResponseWriter, r *http.Request) {
	var in relationship.OpportunityMemberInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Relations.SaveOpportunityMember(r.Context(), principal(r), r.PathValue("id"), r.PathValue("userId"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

func (s *Server) deleteOpportunityMember(w http.ResponseWriter, r *http.Request) {
	err := s.Relations.DeleteOpportunityMember(r.Context(), principal(r), r.PathValue("id"), r.PathValue("userId"), httpx.IntQuery(r, "version", -1, 0, 1_000_000), s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) collaborators(w http.ResponseWriter, r *http.Request) {
	v, err := s.Relations.Collaborators(r.Context(), principal(r), r.URL.Query().Get("q"), httpx.IntQuery(r, "limit", 100, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": v})
}
