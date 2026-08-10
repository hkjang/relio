package server

import (
	"net/http"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/httpx"
)

func (s *Server) listLeads(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListLeads(r.Context(), principal(r), r.URL.Query().Get("q"), httpx.IntQuery(r, "limit", 50, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createLead(w http.ResponseWriter, r *http.Request) {
	var in crm.LeadInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateLead(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListProducts(r.Context(), principal(r), r.URL.Query().Get("q"), httpx.IntQuery(r, "limit", 100, 1, 500))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createProduct(w http.ResponseWriter, r *http.Request) {
	var in crm.ProductInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateProduct(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) createContract(w http.ResponseWriter, r *http.Request) {
	var in crm.ContractInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateContract(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/contracts/"+v.ID)
	httpx.JSON(w, 201, v)
}
func (s *Server) getContract(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.GetContract(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) activateContract(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int `json:"version"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.ActivateContract(r.Context(), principal(r), r.PathValue("id"), in.Version, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) revenueSchedule(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListRevenueSchedules(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) recognizeRevenue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RecognizedDate string `json:"recognizedDate"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.RecognizeRevenueSchedule(r.Context(), principal(r), r.PathValue("id"), in.RecognizedDate, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) updateContractRenewal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status  string `json:"renewalStatus"`
		Action  string `json:"renewalAction"`
		Version int    `json:"version"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.UpdateContractRenewal(r.Context(), principal(r), r.PathValue("id"), in.Status, in.Action, in.Version, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) listSales(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListSales(r.Context(), principal(r), httpx.IntQuery(r, "limit", 100, 1, 500))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createSale(w http.ResponseWriter, r *http.Request) {
	var in crm.SaleInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateSale(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.ListTargets(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var in crm.TargetInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.CRM.CreateTarget(r.Context(), principal(r), in, s.meta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	v, err := s.CRM.Notifications(r.Context(), principal(r), r.URL.Query().Get("unread") == "true", httpx.IntQuery(r, "limit", 100, 1, 200))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) readNotification(w http.ResponseWriter, r *http.Request) {
	if err := s.CRM.ReadNotification(r.Context(), principal(r), r.PathValue("id"), s.meta(r)); err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"read": true})
}
func (s *Server) reports(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := auth.Require(p, "report:read"); err != nil {
		s.serviceError(w, r, err)
		return
	}
	dashboard, err := s.CRM.Dashboard(r.Context(), p)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	forecast, err := s.CRM.Forecast(r.Context(), p)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	winLoss, err := s.CRM.WinLossAnalysis(r.Context(), p, httpx.IntQuery(r, "months", 12, 1, 60))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"dashboard": dashboard, "forecast": forecast, "winLoss": winLoss})
}
