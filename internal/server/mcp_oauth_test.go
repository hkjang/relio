package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
