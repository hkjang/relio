package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

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
	s.serveProtectedResourceMetadata(w, r, s.OIDC.MCPOAuthSettings(r.Context()))
}

func (s *Server) serveProtectedResourceMetadata(w http.ResponseWriter, r *http.Request, settings oidc.MCPOAuth) {
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
	if !metadataPathServes(r.URL.Path, settings.Resource) {
		// RFC 9728 §3: the per-path document describes *that* resource. The
		// only resource here is /mcp; a document for any other path would
		// tell a client that some other URL is protected the same way.
		httpx.ErrorJSON(w, r, http.StatusNotFound, "not_found", "이 경로에는 보호 리소스 메타데이터가 없습니다. /.well-known/oauth-protected-resource"+resourcePath(settings.Resource)+" 를 읽으세요.", nil)
		return
	}
	writeProtectedResourceMetadata(w, settings)
}

// metadataPathServes says whether a request path is one of the two RFC 9728
// locations for the resource: the root document, or the well-known segment
// followed by the resource's own path. A trailing slash on either is
// tolerated; anything else is not this resource's metadata.
func metadataPathServes(requestPath, resource string) bool {
	requestPath = strings.TrimRight(requestPath, "/")
	return requestPath == ProtectedResourceMetadataPath || requestPath == ProtectedResourceMetadataPath+resourcePath(resource)
}

// resourcePath is the path component of the resource identifier, without a
// trailing slash (/mcp for https://crm.example.test/mcp).
func resourcePath(resource string) string {
	u, err := url.Parse(resource)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
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
