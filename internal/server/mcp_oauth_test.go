package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/oidc"
)

// guards: writeProtectedResourceMetadata — the RFC 9728 document is bare JSON
// (no {data:…} envelope), readable from any origin, and names exactly what a
// client needs to start the flow.
func TestProtectedResourceMetadataIsABareDocumentAnyOriginMayRead(t *testing.T) {
	settings := oidc.MCPOAuth{Enabled: true, MCPEnabled: true, Resource: "https://crm.example.test/mcp", Issuer: "https://sso.example.test/realms/x", Scopes: []string{"mcp:use", "customer:read"}}
	w := httptest.NewRecorder()
	writeProtectedResourceMetadata(w, settings)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("a browser-hosted client on another origin must be able to read the document")
	}
	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		BearerMethods        []string `json:"bearer_methods_supported"`
		Scopes               []string `json:"scopes_supported"`
		Data                 any      `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Data != nil {
		t.Fatal("the document must not be wrapped in the product envelope")
	}
	if doc.Resource != settings.Resource || len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != settings.Issuer || len(doc.BearerMethods) != 1 || doc.BearerMethods[0] != "header" || len(doc.Scopes) != 2 {
		t.Fatalf("document: %s", w.Body.String())
	}
}

// guards: serveProtectedResourceMetadata, metadataPathServes — RFC 9728 §3:
// the per-path document is the metadata of *that* resource. The root
// document and the resource's own path answer; every other path is 404, so
// a client can never be told that /anything is protected the way /mcp is.
func TestProtectedResourceMetadataAnswersOnlyForTheResourcePath(t *testing.T) {
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	settings := oidc.MCPOAuth{Enabled: true, MCPEnabled: true, Resource: "https://crm.example.test/mcp", Issuer: "https://sso.example.test/realms/x", Scopes: []string{"mcp:use"}}
	serve := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.serveProtectedResourceMetadata(w, httptest.NewRequest(http.MethodGet, path, nil), settings)
		return w
	}
	for _, path := range []string{ProtectedResourceMetadataPath, ProtectedResourceMetadataPath + "/", ProtectedResourceMetadataPath + "/mcp", ProtectedResourceMetadataPath + "/mcp/"} {
		if w := serve(path); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"resource":"https://crm.example.test/mcp"`) {
			t.Fatalf("%s must serve the document: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{ProtectedResourceMetadataPath + "/anything", ProtectedResourceMetadataPath + "/mcp/tools", ProtectedResourceMetadataPath + "/api/v1/customers", ProtectedResourceMetadataPath + "/mcpx"} {
		w := serve(path)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "authorization_servers") {
			t.Fatalf("%s is not this resource's metadata and must be 404 without the document: %d %s", path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), ProtectedResourceMetadataPath+"/mcp") {
			t.Fatalf("the 404 must say where the document is: %s", w.Body.String())
		}
	}
	// A resource under a prefix moves the accepted path with it.
	settings.Resource = "https://edge.example.test/relio/mcp"
	if w := serve(ProtectedResourceMetadataPath + "/relio/mcp"); w.Code != http.StatusOK {
		t.Fatalf("prefixed resource path: %d", w.Code)
	}
	if w := serve(ProtectedResourceMetadataPath + "/mcp"); w.Code != http.StatusNotFound {
		t.Fatalf("/mcp is not the prefixed resource: %d", w.Code)
	}
	// Switched off, the path does not matter: 404 with the disabled code.
	settings.Enabled = false
	if w := serve(ProtectedResourceMetadataPath + "/mcp"); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "mcp_oauth_disabled") {
		t.Fatalf("disabled: %d %s", w.Code, w.Body.String())
	}
}

// guards: isMachinePath — the metadata route sits under /.well-known, which
// the SPA fallback must keep treating as machine territory (404, not HTML)
// for everything the router does not answer.
func TestWellKnownStaysOutOfTheSPAShell(t *testing.T) {
	for _, path := range []string{ProtectedResourceMetadataPath, ProtectedResourceMetadataPath + "/mcp", "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		if !isMachinePath(path) {
			t.Fatalf("%s must never fall through to the HTML shell", path)
		}
	}
}
