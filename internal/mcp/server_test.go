package mcp

import (
	"net/http/httptest"
	"testing"
)

func TestAcceptHeaderRequiresBothTransports(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Accept", "application/json")
	if acceptsMCP(r) {
		t.Fatal("application/json alone must be rejected")
	}
	r.Header.Set("Accept", "application/json, text/event-stream")
	if !acceptsMCP(r) {
		t.Fatal("valid Streamable HTTP Accept header rejected")
	}
}
