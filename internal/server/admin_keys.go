package server

import (
	"net/http"

	"github.com/hkjang/relio/internal/platform/httpx"
)

func (s *Server) adminPersonalKeys(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	items, err := s.Keys.ListAll(r.Context(), principal(r), r.URL.Query().Get("userId"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) adminRevokeKey(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if err := s.Keys.Revoke(r.Context(), principal(r), r.PathValue("id"), httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent(), true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminRevokeAllKeys(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	count, err := s.Keys.RevokeAll(r.Context(), principal(r), r.PathValue("id"), httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"revoked": count})
}
