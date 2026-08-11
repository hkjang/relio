package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/hkjang/relio/internal/analytics"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/httpx"
)

// baseCSP is the policy Relio ships with: no external origin is reachable. Enabled
// analytics providers widen only the three directives they need, from origins an
// administrator validated, so the default build keeps its offline guarantee.
const baseCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// contentSecurityPolicy assembles the header for this request.
func (s *Server) contentSecurityPolicy(r *http.Request) string {
	script := []string{"'self'"}
	connect := []string{"'self'"}
	img := []string{"'self'", "data:"}
	if s.Analytics != nil {
		policy := s.Analytics.CurrentPolicy(r.Context())
		script = append(script, policy.ScriptSrc...)
		connect = append(connect, policy.ConnectSrc...)
		img = append(img, policy.ImgSrc...)
	}
	// report-uri is deprecated but still the only directive Safari honours, and
	// both are harmless together. Without a report the only symptom of a missing
	// origin is an error in each user's console.
	return baseCSP +
		"; script-src " + strings.Join(script, " ") +
		"; connect-src " + strings.Join(connect, " ") +
		"; img-src " + strings.Join(img, " ") +
		"; report-uri /api/v1/csp-report"
}

// cspReport accepts browser violation reports. It is unauthenticated because the
// browser sends it without credentials, so the body is treated as untrusted: the
// payload is size-capped, the values are re-validated, and rows are rolled up by
// origin so a hostile client cannot grow the table.
func (s *Server) cspReport(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil || len(body) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Browsers send either {"csp-report":{...}} or the newer report-to array.
	var legacy struct {
		Report struct {
			Directive   string `json:"effective-directive"`
			Violated    string `json:"violated-directive"`
			BlockedURI  string `json:"blocked-uri"`
			DocumentURI string `json:"document-uri"`
			SourceFile  string `json:"source-file"`
		} `json:"csp-report"`
	}
	if json.Unmarshal(body, &legacy) == nil && legacy.Report.BlockedURI != "" {
		directive := legacy.Report.Directive
		if directive == "" {
			directive = strings.Fields(legacy.Report.Violated + " ")[0]
		}
		_ = s.Analytics.RecordViolation(r.Context(), directive, legacy.Report.BlockedURI, legacy.Report.DocumentURI)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var modern []struct {
		Body struct {
			Directive   string `json:"effectiveDirective"`
			BlockedURL  string `json:"blockedURL"`
			DocumentURL string `json:"documentURL"`
		} `json:"body"`
	}
	if json.Unmarshal(body, &modern) == nil {
		for _, report := range modern {
			if report.Body.BlockedURL == "" {
				continue
			}
			_ = s.Analytics.RecordViolation(r.Context(), report.Body.Directive, report.Body.BlockedURL, report.Body.DocumentURL)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// analyticsLoader serves the generated tracking loader from Relio's own origin so
// script-src 'self' covers it. It is unauthenticated because the login screen may
// be tracked too; the body contains only what an administrator configured.
func (s *Server) analyticsLoader(w http.ResponseWriter, r *http.Request) {
	body, err := s.Analytics.Loader(r.Context())
	if err != nil {
		s.Log.Warn("analytics loader unavailable", "error", err)
		body = "/* 방문자 분석 설정을 읽을 수 없습니다. */\n"
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// Short cache: a configuration change should take effect on the next load
	// without making every page view hit the database.
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(body))
}

// ---------------------------------------------------------------- admin

func (s *Server) listAnalyticsProviders(w http.ResponseWriter, r *http.Request) {
	items, err := s.Analytics.List(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	violations, err := s.Analytics.Violations(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{
		"items":      items,
		"vendors":    analytics.Vendors(),
		"violations": violations,
		"policy":     s.Analytics.CurrentPolicy(r.Context()),
		"loaderPath": "/analytics.js",
	})
}

func (s *Server) saveAnalyticsProvider(w http.ResponseWriter, r *http.Request) {
	var in analytics.Provider
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		in.ID = id
	}
	v, err := s.Analytics.Save(r.Context(), principal(r), in, s.analyticsMeta(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	httpx.JSON(w, status, v)
}

func (s *Server) deleteAnalyticsProvider(w http.ResponseWriter, r *http.Request) {
	if err := s.Analytics.Delete(r.Context(), principal(r), r.PathValue("id"), s.analyticsMeta(r)); err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resolveCSPViolation(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAnalytics(p); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		Directive     string `json:"directive"`
		BlockedOrigin string `json:"blockedOrigin"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if err := s.Analytics.ResolveViolation(r.Context(), in.Directive, in.BlockedOrigin); err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"resolved": true})
}

func requireAnalytics(p *auth.Principal) error {
	return auth.Require(p, "analytics:manage")
}

func (s *Server) analyticsMeta(r *http.Request) analytics.Meta {
	return analytics.Meta{IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()}
}
