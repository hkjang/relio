package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/database"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/version"
	"github.com/hkjang/relio/internal/webui"
)

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, map[string]any{"status": "ok", "uptimeSeconds": int(time.Since(s.started).Seconds())})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.DB.Ping(ctx); err != nil {
		httpx.JSON(w, 503, map[string]any{"status": "not_ready", "postgres": "error", "schema": "unknown"})
		return
	}
	migration := database.MigrationStatus(ctx, s.DB)
	if migration.Status != "ok" {
		httpx.JSON(w, 503, map[string]any{"status": "not_ready", "postgres": "ok", "schema": migration})
		return
	}
	httpx.JSON(w, 200, map[string]any{"status": "ready", "postgres": "ok", "schema": migration})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.Ping(r.Context()); err != nil {
		httpx.JSON(w, 503, map[string]any{"status": "degraded", "postgres": "error"})
		return
	}
	httpx.JSON(w, 200, map[string]any{"status": "ok", "version": version.Current().Version, "uptimeSeconds": int(time.Since(s.started).Seconds())})
}
func (s *Server) systemVersion(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, version.Current())
}
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	var local bool = true
	_ = s.DB.QueryRow(r.Context(), `SELECT (value #>> '{}')::boolean FROM system_settings WHERE namespace='auth' AND key='local_login_enabled'`).Scan(&local)
	httpx.JSON(w, 200, map[string]any{"localLoginEnabled": local, "sso": s.OIDC.PublicStatus(r.Context()), "version": version.Current()})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := httpx.ClientIP(r)
	if !s.limiter.allow(ip) {
		httpx.ErrorJSON(w, r, 429, "login_rate_limited", "로그인 시도가 너무 많습니다. 잠시 후 다시 시도하세요.", nil)
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	var local = true
	_ = s.DB.QueryRow(r.Context(), `SELECT (value #>> '{}')::boolean FROM system_settings WHERE namespace='auth' AND key='local_login_enabled'`).Scan(&local)
	if !local {
		var bootstrap bool
		_ = s.DB.QueryRow(r.Context(), `SELECT is_bootstrap FROM users WHERE lower(username)=lower($1) AND active=true`, in.Username).Scan(&bootstrap)
		if !bootstrap {
			httpx.ErrorJSON(w, r, 403, "local_login_disabled", "일반 로컬 로그인이 비활성화되어 있습니다.", nil)
			return
		}
	}
	token, p, err := s.Auth.Login(r.Context(), in.Username, in.Password, ip, r.UserAgent())
	if err != nil {
		s.Audit.Record(r.Context(), audit.Event{ActorName: in.Username, Channel: "LOGIN", Action: "LOGIN_FAILED", Resource: "session", IP: ip, RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
		httpx.ErrorJSON(w, r, 401, "invalid_credentials", "아이디 또는 비밀번호가 올바르지 않습니다.", nil)
		return
	}
	s.limiter.success(ip)
	s.Auth.SetSessionCookie(w, r, token)
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "LOGIN", Action: "LOGIN", Resource: "session", IP: ip, RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent(), Metadata: map[string]any{"bootstrap": p.IsBootstrap}})
	httpx.JSON(w, 200, map[string]any{"user": p})
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, map[string]any{"user": principal(r), "version": version.Current()})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		s.Auth.Logout(r.Context(), c.Value)
	}
	p := principal(r)
	s.Auth.ClearSessionCookie(w)
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "LOGIN", Action: "LOGOUT", Resource: "session", IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	w.WriteHeader(204)
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p := principal(r)
	if err := s.Auth.ChangePassword(r.Context(), p, in.CurrentPassword, in.NewPassword); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "WEB", Action: "PASSWORD_CHANGE", Resource: "user", ResourceID: p.UserID, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, 200, map[string]any{"changed": true})
}
func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	target, err := s.OIDC.LoginURL(r.Context())
	if err != nil {
		httpx.ErrorJSON(w, r, 503, "sso_unavailable", err.Error(), nil)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		http.Redirect(w, r, "/login?sso_error="+urlQuery(oauthErr), http.StatusFound)
		return
	}
	token, _, err := s.OIDC.Callback(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"), httpx.ClientIP(r), r.UserAgent())
	if err != nil {
		s.Log.Warn("OIDC callback failed", "error", err)
		http.Redirect(w, r, "/login?sso_error=callback_failed", http.StatusFound)
		return
	}
	s.Auth.SetSessionCookie(w, r, token)
	http.Redirect(w, r, "/app", http.StatusFound)
}
func urlQuery(v string) string { return strings.NewReplacer(" ", "%20", "&", "", "?", "").Replace(v) }

func (s *Server) apiDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, `<!doctype html><html lang="ko"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Relio API</title><style>body{font-family:system-ui;background:#f4f7fb;color:#172033;margin:0}.wrap{max-width:900px;margin:48px auto;padding:36px;background:white;border-radius:18px;box-shadow:0 16px 50px #18315318}h1{margin:0;color:#153e75}code{background:#eef3f9;padding:3px 7px;border-radius:5px}.row{padding:14px 0;border-bottom:1px solid #e6ecf3}.method{display:inline-block;width:58px;color:#087f5b;font-weight:700}a{color:#155eef}</style></head><body><main class="wrap"><h1>Relio REST API</h1><p>오프라인 내장 API 문서입니다. 전체 OpenAPI 3.1 정의는 <a href="/api/openapi.json">/api/openapi.json</a>에서 확인할 수 있습니다.</p><div class="row"><span class="method">GET</span><code>/api/v1/customers</code> 고객 검색</div><div class="row"><span class="method">POST</span><code>/api/v1/customers</code> 고객 생성</div><div class="row"><span class="method">GET</span><code>/api/v1/customers/{id}/360</code> Customer 360</div><div class="row"><span class="method">GET</span><code>/api/v1/opportunities</code> 영업기회 조회</div><div class="row"><span class="method">POST</span><code>/api/v1/activities</code> 영업활동 등록</div><div class="row"><span class="method">POST</span><code>/mcp</code> MCP Streamable HTTP</div><p>인증: <code>Authorization: Bearer relio_...</code>. 모든 결과에는 사용자 Permission, Data Scope, Key Scope의 교집합이 적용됩니다.</p></main></body></html>`)
}
func (s *Server) spaHandler() http.Handler {
	assets, err := fs.Sub(webui.Assets, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" && strings.Contains(path, ".") {
			if _, err := fs.Stat(assets, path); err == nil {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "frontend unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func prettyJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
