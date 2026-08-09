package server

import (
	"net/http"
	"strings"

	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/httpx"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.Dashboard(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.GlobalSearch(r.Context(), principal(r), r.URL.Query().Get("q"), httpx.IntQuery(r, "limit", 10, 1, 50))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListCustomers(r.Context(), principal(r), r.URL.Query().Get("q"), r.URL.Query().Get("cursor"), r.URL.Query().Get("sort"), httpx.IntQuery(r, "limit", 50, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request) {
	var in crm.CustomerInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateCustomer(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/customers/"+v.ID)
	httpx.JSON(w, 201, v)
}
func (s *Server) getCustomer(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.GetCustomer(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) updateCustomer(w http.ResponseWriter, r *http.Request) {
	var in crm.CustomerInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.UpdateCustomer(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) customer360(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.Customer360(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) customerDuplicates(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.DuplicateCustomers(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v, "count": len(v)})
}
func (s *Server) mergeCustomers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SourceIDs []string `json:"sourceIds"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.MergeCustomers(r.Context(), principal(r), r.PathValue("id"), in.SourceIDs, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) listContacts(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.SearchContacts(r.Context(), principal(r), r.URL.Query().Get("q"), r.URL.Query().Get("customerId"), httpx.IntQuery(r, "limit", 50, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createContact(w http.ResponseWriter, r *http.Request) {
	var in crm.ContactInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateContact(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) listOpportunities(w http.ResponseWriter, r *http.Request) {
	f := crm.OpportunityFilter{Query: r.URL.Query().Get("q"), CustomerID: r.URL.Query().Get("customerId"), Status: strings.ToUpper(r.URL.Query().Get("status")), StageID: r.URL.Query().Get("stageId"), Cursor: r.URL.Query().Get("cursor"), Sort: r.URL.Query().Get("sort"), Limit: httpx.IntQuery(r, "limit", 50, 1, 200), StaleOnly: r.URL.Query().Get("stale") == "true"}
	v, err := s.CRM.ListOpportunities(r.Context(), principal(r), f)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) createOpportunity(w http.ResponseWriter, r *http.Request) {
	var in crm.OpportunityInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateOpportunity(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/opportunities/"+v.ID)
	httpx.JSON(w, 201, v)
}
func (s *Server) getOpportunity(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.GetOpportunity(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) updateOpportunity(w http.ResponseWriter, r *http.Request) {
	var in crm.OpportunityInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.UpdateOpportunity(r.Context(), principal(r), r.PathValue("id"), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) changeStage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		StageID string `json:"stageId"`
		Version int    `json:"version"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.ChangeOpportunityStage(r.Context(), principal(r), r.PathValue("id"), in.StageID, in.Version, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) pipelines(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.Pipelines(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) listActivities(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListActivities(r.Context(), principal(r), r.URL.Query().Get("customerId"), r.URL.Query().Get("opportunityId"), httpx.IntQuery(r, "limit", 50, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) addActivity(w http.ResponseWriter, r *http.Request) {
	var in crm.ActivityInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.AddActivity(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) forecast(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.Forecast(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) salesKPI(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.SalesKPI(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) dueActions(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.DueActions(r.Context(), principal(r), httpx.IntQuery(r, "days", 7, 0, 365), httpx.IntQuery(r, "limit", 50, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) listQuotations(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListQuotations(r.Context(), principal(r), r.URL.Query().Get("customerId"), httpx.IntQuery(r, "limit", 50, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createQuotation(w http.ResponseWriter, r *http.Request) {
	var in crm.QuotationInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateQuotation(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) listContracts(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.Contracts(r.Context(), principal(r), r.URL.Query().Get("customerId"), httpx.IntQuery(r, "expiringDays", 0, 0, 3650), r.URL.Query().Get("renewalOnly") == "true", httpx.IntQuery(r, "limit", 50, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) winLoss(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.WinLossAnalysis(r.Context(), principal(r), httpx.IntQuery(r, "months", 12, 1, 60))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
