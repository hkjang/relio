package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The single-page shell answered every path and every method, so an MCP client
// configured with a trailing slash — or one probing for OAuth metadata this
// server does not publish — received `200 text/html` and reported
// "failed to parse json".

func TestMachinePathsNeverFallThroughToTheSPA(t *testing.T) {
	machine := []string{
		"/api/v1/customers",
		"/mcp",
		"/mcp/",
		"/mcp/anything",
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-authorization-server",
	}
	for _, path := range machine {
		if !isMachinePath(path) {
			t.Fatalf("%s must not be served by the SPA fallback", path)
		}
	}
	browser := []string{"/", "/app", "/app/customers", "/login", "/me/keys", "/assets/index.js"}
	for _, path := range browser {
		if isMachinePath(path) {
			t.Fatalf("%s is a browser route and must still reach the SPA", path)
		}
	}
}

func TestSPAShellOnlyAnswersNavigation(t *testing.T) {
	handler := (&Server{}).spaHandler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/app", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /app = %d, want 405 rather than the HTML shell", method, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s /app content-type = %q, want JSON a client can parse", method, ct)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/app/customers", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("GET /app/customers = %d %q, want the SPA shell", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestMachinePathsAnswerJSONNotHTML(t *testing.T) {
	handler := (&Server{}).spaHandler()
	for _, path := range []string{"/mcp/", "/.well-known/oauth-protected-resource"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("GET %s content-type = %q, want JSON", path, ct)
		}
	}
}
