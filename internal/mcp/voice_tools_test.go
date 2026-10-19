package mcp

import (
	"strings"
	"testing"
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
