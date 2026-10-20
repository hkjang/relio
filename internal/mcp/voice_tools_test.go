package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/voice"
	"github.com/jackc/pgx/v5/pgxpool"
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

func TestCatalogFieldSchemaIncludesDefaultFieldsWithoutWorkspaces(t *testing.T) {
	catalog := voice.FieldCatalog{Fields: []voice.Field{
		{Key: "service_type", Label: "서비스 구분", Type: "Select", Phase: "INTAKE", Required: true, Options: []string{"PHONE"}},
		{Key: "dev_method", Label: "개발방식", Type: "Select", Phase: "INTAKE", Options: []string{"POPUP_MOD"}},
		{Key: "error_code", Label: "오류코드", Type: "Text", Phase: "INTAKE"},
		{Key: "resolver", Label: "해결 주체", Type: "Select", Phase: "RESOLUTION", Options: []string{"OPS"}},
	}}
	intake := catalogFieldSchema(catalog, "INTAKE")
	props := intake["properties"].(map[string]any)
	if len(props) != 3 {
		t.Fatalf("default-only intake must declare all three fields: %#v", intake)
	}
	if props["service_type"].(map[string]any)["enum"].([]string)[0] != "PHONE" ||
		props["dev_method"].(map[string]any)["enum"].([]string)[0] != "POPUP_MOD" ||
		props["error_code"].(map[string]any)["type"] != "string" {
		t.Fatalf("default fields must retain canonical keys and allowed values: %#v", props)
	}
	if intake["additionalProperties"] != false {
		t.Fatal("fix must not bypass unknown field validation")
	}
	if _, ok := intake["required"]; ok {
		t.Fatal("one context's required fields must not be imposed on other request types")
	}
	resolution := catalogFieldSchema(catalog, "RESOLUTION")["properties"].(map[string]any)
	if len(resolution) != 1 || resolution["resolver"] == nil {
		t.Fatalf("resolution must publish only resolution fields: %#v", resolution)
	}
	if all := catalogFieldSchema(catalog)["properties"].(map[string]any); len(all) != 4 {
		t.Fatalf("search must include both phases: %#v", all)
	}
}

func TestCatalogFieldSchemaPreservesDefaultAndWorkspaceVariants(t *testing.T) {
	catalog := voice.FieldCatalog{
		Fields: []voice.Field{{Key: "service_type", Type: "Select", Phase: "INTAKE", Options: []string{"PHONE"}}},
		Workspaces: []voice.Workspace{{Name: "부서", Fields: []voice.Field{
			{Key: "service_type", Type: "Select", Phase: "INTAKE", Options: []string{"IPIN"}},
		}}},
	}
	field := catalogFieldSchema(catalog, "INTAKE")["properties"].(map[string]any)["service_type"].(map[string]any)
	choices := field["anyOf"].([]any)
	if len(choices) != 2 || choices[0].(map[string]any)["enum"].([]string)[0] != "PHONE" ||
		choices[1].(map[string]any)["enum"].([]string)[0] != "IPIN" {
		t.Fatalf("default and workspace definitions must coexist: %#v", field)
	}
	if len(catalog.Workspaces) != 1 || catalog.Workspaces[0].Name != "부서" {
		t.Fatal("schema assembly must not mutate the workspace catalog")
	}
}

func TestVoiceSchemaLoadFailureCannotPublishAnEmptySuccessfulCatalog(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@127.0.0.1:1/unused?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // deterministic query failure without a running database
	s := &Server{DB: pool, Voices: &voice.Service{DB: pool}}
	p := &auth.Principal{IsBootstrap: true}
	if items, err := s.tools(ctx, p); err == nil || items != nil {
		t.Fatalf("failed schema lookup returned tools: %#v, %v", items, err)
	}
	if catalog, err := s.ToolCatalog(ctx, p); err == nil || catalog != nil {
		t.Fatalf("key UI must not receive an incomplete catalog: %#v, %v", catalog, err)
	}
	r := httptest.NewRequest("POST", "/mcp", nil).WithContext(ctx)
	result, rpcErr, _ := s.dispatch(r, p, request{Method: "tools/list"}, ProtocolVersion)
	if result != nil || rpcErr == nil || rpcErr.Code != -32603 || !strings.Contains(rpcErr.Message, "다시 불러오세요") {
		t.Fatalf("lookup failure must be a retryable RPC error: %#v, %#v", result, rpcErr)
	}
	if strings.Contains(rpcErr.Message, "postgres") || strings.Contains(rpcErr.Message, "context canceled") {
		t.Fatalf("internal error leaked: %s", rpcErr.Message)
	}
	if _, err := s.callTool(ctx, p, toolCall{Name: "file_customer_voice"}, r); err == nil {
		t.Fatal("write must not run with unavailable field definitions")
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
