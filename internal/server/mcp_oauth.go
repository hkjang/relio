package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/oidc"
	"github.com/hkjang/relio/internal/platform/httpx"
)

// The HTTP half of MCP over SSO (internal/oidc/mcp_oauth.go has the rest):
// the RFC 9728 metadata document a refused client reads, and the 401 that
// points at it. Both exist only on the MCP path — a REST 401 that named an
// authorization server would send browsers and API clients somewhere they
// cannot use.

// ProtectedResourceMetadataPath is the RFC 9728 location; the resource path
// is appended for the per-resource variant (/.well-known/oauth-protected-resource/mcp).
const ProtectedResourceMetadataPath = "/.well-known/oauth-protected-resource"

func (s *Server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	settings := s.OIDC.MCPOAuthSettings(r.Context())
	active, reason := settings.Active()
	if !active {
		if settings.Enabled {
			// The switch is on but something it needs is missing: say so
			// where the administrator looks, not only with a 404.
			s.Log.Warn("mcp oauth is enabled but cannot run", "reason", reason)
		}
		// Off means 404. Metadata that is served while tokens are refused
		// sends the client into a sign-in loop.
		httpx.ErrorJSON(w, r, http.StatusNotFound, "mcp_oauth_disabled", "이 서버의 MCP 는 SSO 토큰을 받지 않습니다. 개인 연동 키(relio_…)를 사용하세요.", nil)
		return
	}
	writeProtectedResourceMetadata(w, settings)
}

// writeProtectedResourceMetadata answers with the bare document. Anyone may
// read it, including a browser-hosted client on another origin, hence the
// wildcard CORS header — it names where to sign in, nothing about anyone.
func writeProtectedResourceMetadata(w http.ResponseWriter, settings oidc.MCPOAuth) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_ = json.NewEncoder(w).Encode(settings.Document())
}

// mcpUnauthorized answers a refused MCP request. The WWW-Authenticate header
// names the scheme (a 401 without one sends clients hunting for metadata that
// may not exist) and, when SSO tokens are accepted, where the metadata is; a
// refused token's reason goes in the body, where it may explain in Korean
// what was seen and what to change.
func (s *Server) mcpUnauthorized(w http.ResponseWriter, r *http.Request, err error) {
	settings := s.OIDC.MCPOAuthSettings(r.Context())
	var refusal *auth.Refusal
	if errors.As(err, &refusal) {
		w.Header().Set("WWW-Authenticate", settings.Challenge(true))
		httpx.ErrorJSON(w, r, http.StatusUnauthorized, "invalid_token", refusal.Message, nil)
		return
	}
	w.Header().Set("WWW-Authenticate", settings.Challenge(false))
	httpx.ErrorJSON(w, r, http.StatusUnauthorized, "authentication_required", "로그인이 필요합니다.", nil)
}
