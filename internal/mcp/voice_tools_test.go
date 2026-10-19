package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/voice"
)

func TestWorkspaceCustomerSearchSchemaIsStrictAndUsable(t *testing.T) {
	args, err := validateArguments(searchWorkspaceCustomersSchema, map[string]any{
		"workspace_id": "11111111-1111-1111-1111-111111111111",
		"query":        "B124 회원사",
		"limit":        "20",
	})
	if err != nil {
		t.Fatal(err)
	}
	if args["workspaceId"] == nil || args["limit"] != float64(20) {
		t.Fatalf("normalised arguments = %#v", args)
	}
	if _, err = validateArguments(searchWorkspaceCustomersSchema, map[string]any{"query": "회원사"}); err == nil || !strings.Contains(err.Error(), "workspaceId") {
		t.Fatalf("workspace boundary must be mandatory: %v", err)
	}
	if _, err = validateArguments(searchWorkspaceCustomersSchema, map[string]any{
		"workspaceId": "11111111-1111-1111-1111-111111111111", "query": "회원사", "pickFirst": true,
	}); err == nil || !strings.Contains(err.Error(), "pickFirst") {
		t.Fatalf("unknown auto-selection flags must be refused: %v", err)
	}
}

func TestWorkspaceFieldSchemaPreservesSharedKeys(t *testing.T) {
	workspaces := []voice.Workspace{
		{Name: "부서 A", Fields: []voice.Field{{Key: "service_type", Type: "Select", Phase: "INTAKE", Options: []string{"A"}, DefaultValue: "A"}}},
		{Name: "부서 B", Fields: []voice.Field{{Key: "service_type", Type: "Select", Phase: "INTAKE", Options: []string{"B"}}}},
		{Name: "부서 C", Fields: []voice.Field{{Key: "service_type", Type: "Number", Phase: "INTAKE"}, {Key: "resolver", Type: "Select", Phase: "RESOLUTION", Options: []string{"OPS"}}}},
	}
	schema := workspaceFieldSchema(workspaces, "INTAKE")
	properties := schema["properties"].(map[string]any)
	if _, exists := properties["resolver"]; exists {
		t.Fatal("resolution fields leaked into intake")
	}
	property := properties["service_type"].(map[string]any)
	choices := property["anyOf"].([]any)
	if len(choices) != 3 || choices[0].(map[string]any)["enum"].([]string)[0] != "A" || choices[1].(map[string]any)["enum"].([]string)[0] != "B" || choices[2].(map[string]any)["type"] != "number" {
		t.Fatalf("shared key lost an allowed workspace variant: %#v", choices)
	}
	if _, hasDefault := property["default"]; hasDefault {
		t.Fatal("a default from one workspace must not apply to other workspaces")
	}
	encoded, err := json.Marshal(schema)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("tool schema must be valid JSON: %s %v", encoded, err)
	}
	single := workspaceFieldSchema(workspaces[:1], "INTAKE")["properties"].(map[string]any)["service_type"].(map[string]any)
	if single["type"] != "string" || single["default"] != "A" {
		t.Fatalf("single workspace schema changed: %#v", single)
	}
}
