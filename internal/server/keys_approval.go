package server

import (
	"net/http"
	"strings"

	"github.com/hkjang/relio/internal/apikey"
	"github.com/hkjang/relio/internal/platform/httpx"
)

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	v, err := s.Keys.List(r.Context(), principal(r), "", false)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v, "allowedScopes": apikey.AllowedScopesFor(principal(r))})
}
func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var in apikey.CreateInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Keys.Create(r.Context(), principal(r), in, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) updateKeyAccess(w http.ResponseWriter, r *http.Request) {
	var in apikey.UpdateAccessInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Keys.UpdateAccess(r.Context(), principal(r), r.PathValue("id"), in, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	v, err := s.Keys.Rotate(r.Context(), principal(r), r.PathValue("id"), httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	err := s.Keys.Revoke(r.Context(), principal(r), r.PathValue("id"), httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent(), false)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) myActivity(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.DB.Query(r.Context(), `SELECT channel,action,resource,COALESCE(resource_id,''),COALESCE(ip::text,''),occurred_at FROM audit_logs WHERE actor_id=$1 ORDER BY occurred_at DESC LIMIT 200`, p.UserID)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var channel, action, resource, resourceID, ip string
		var occurred any
		if err = rows.Scan(&channel, &action, &resource, &resourceID, &ip, &occurred); err != nil {
			s.serviceError(w, r, err)
			return
		}
		items = append(items, map[string]any{"channel": channel, "action": action, "resource": resource, "resourceId": resourceID, "ip": ip, "occurredAt": occurred})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	v, err := s.Approvals.List(r.Context(), principal(r), strings.ToUpper(r.URL.Query().Get("status")))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) approvalStatus(w http.ResponseWriter, r *http.Request) {
	var enabled bool
	if err := s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM approval_policies WHERE active=true)`).Scan(&enabled); err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"enabled": enabled})
}
func (s *Server) approvalCapability(w http.ResponseWriter, r *http.Request) {
	v, err := s.Approvals.Capability(r.Context(), principal(r), r.URL.Query().Get("entityType"), r.URL.Query().Get("entityId"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) submitApproval(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EntityType string `json:"entityType"`
		EntityID   string `json:"entityId"`
		Reason     string `json:"reason"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Approvals.Submit(r.Context(), principal(r), in.EntityType, in.EntityID, in.Reason, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 201, v)
}
func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	v, err := s.Approvals.Get(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request, decision string) {
	var in struct {
		Comment string `json:"comment"`
		Version int    `json:"version"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.Approvals.Decide(r.Context(), principal(r), r.PathValue("id"), decision, in.Comment, in.Version, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) approve(w http.ResponseWriter, r *http.Request) { s.decideApproval(w, r, "APPROVE") }
func (s *Server) reject(w http.ResponseWriter, r *http.Request)  { s.decideApproval(w, r, "REJECT") }
