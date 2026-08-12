package mcp

import (
	"net/http/httptest"
	"testing"
)

// This used to assert that Accept had to carry both application/json and
// text/event-stream, mirroring what the specification asks of clients. That
// turned a client's header preference into a hard failure even though every
// response this server produces is JSON and it never opens a stream. The rule
// now refuses only a client that cannot accept JSON at all.
func TestAcceptHeaderRefusesOnlyClientsThatCannotTakeJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Accept", "application/json, text/event-stream")
	if !acceptsMCP(r) {
		t.Fatal("the canonical Streamable HTTP Accept header was rejected")
	}
	r.Header.Set("Accept", "application/json")
	if !acceptsMCP(r) {
		t.Fatal("a JSON-only client must be served: every response is JSON")
	}
	r.Header.Set("Accept", "text/plain")
	if acceptsMCP(r) {
		t.Fatal("a client that accepts neither JSON nor a stream must be refused")
	}
}
