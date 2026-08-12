package mcp

import (
	"encoding/json"
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

func TestSchemaOmitsRequiredWhenThereAreNoRequiredProperties(t *testing.T) {
	value := schema(nil, map[string]any{"query": str("검색어")})
	if _, exists := value["required"]; exists {
		t.Fatal("required must be omitted instead of encoded as null")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("tool schema is not valid JSON: %s", encoded)
	}
}

func TestSchemaKeepsNonEmptyRequiredProperties(t *testing.T) {
	value := schema([]string{"id"}, map[string]any{"id": str("고객 ID")})
	required, ok := value["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "id" {
		t.Fatalf("required property was lost: %#v", value["required"])
	}
}
