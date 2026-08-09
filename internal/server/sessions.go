package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/httpx"
)

func currentSessionID(r *http.Request) string {
	cookie, err := r.Cookie(auth.SessionCookie)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	return hex.EncodeToString(digest[:])
}

func (s *Server) mySessions(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.DB.Query(r.Context(), `SELECT encode(id_hash,'hex'),auth_method,COALESCE(ip::text,''),COALESCE(user_agent,''),created_at,last_seen_at,expires_at FROM sessions WHERE user_id=$1 AND expires_at>now() ORDER BY last_seen_at DESC`, p.UserID)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	current := currentSessionID(r)
	items := []map[string]any{}
	for rows.Next() {
		var id, method, ip, userAgent string
		var created, seen, expires time.Time
		if err = rows.Scan(&id, &method, &ip, &userAgent, &created, &seen, &expires); err != nil {
			s.serviceError(w, r, err)
			return
		}
		items = append(items, map[string]any{"id": id, "authMethod": method, "ip": ip, "userAgent": userAgent, "createdAt": created, "lastSeenAt": seen, "expiresAt": expires, "current": id == current})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id := r.PathValue("id")
	if len(id) != 64 {
		httpx.ErrorJSON(w, r, http.StatusBadRequest, "invalid_session", "유효하지 않은 Session ID입니다.", nil)
		return
	}
	cmd, err := s.DB.Exec(r.Context(), `DELETE FROM sessions WHERE id_hash=decode($1,'hex') AND user_id=$2`, id, p.UserID)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if cmd.RowsAffected() == 0 {
		httpx.ErrorJSON(w, r, http.StatusNotFound, "not_found", "Session을 찾을 수 없습니다.", nil)
		return
	}
	if id == currentSessionID(r) {
		s.Auth.ClearSessionCookie(w)
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "WEB", Action: "SESSION_REVOKE", Resource: "session", ResourceID: id[:12], IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	w.WriteHeader(http.StatusNoContent)
}
