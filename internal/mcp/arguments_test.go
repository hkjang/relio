package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const sampleID = "42d8a59d-e4f6-4ba7-bef6-73cd846c08c8"

var activitySchema = schema([]string{"id"}, map[string]any{
	"id":         str("고객 ID"),
	"customerId": str("고객 ID · 비우면 전체"),
	"limit":      integer("최대 결과 수"),
	"amount":     number("금액"),
	"unreadOnly": boolean("읽지 않은 것만"),
	"tags":       map[string]any{"type": "array"},
})

// A filter spelled the way a model often spells it used to be ignored, and the
// tool answered with every record as if the filter had applied.
func TestSpellingVariantsReachTheRealParameter(t *testing.T) {
	for _, spelling := range []string{"customer_id", "CustomerID", "customer-id", "customerid"} {
		got, err := validateArguments(activitySchema, map[string]any{"id": sampleID, spelling: sampleID})
		if err != nil {
			t.Fatalf("%s: %v", spelling, err)
		}
		if got["customerId"] != sampleID {
			t.Fatalf("%s did not become customerId: %v", spelling, got)
		}
		if _, left := got[spelling]; left {
			t.Fatalf("%s was kept alongside customerId", spelling)
		}
	}
}

func TestUnknownArgumentsAreRefusedNotIgnored(t *testing.T) {
	_, err := validateArguments(activitySchema, map[string]any{"id": sampleID, "owner": "me"})
	var arg *argumentError
	if !errors.As(err, &arg) {
		t.Fatalf("want argumentError, got %v", err)
	}
	if !strings.Contains(arg.message, "owner") || !strings.Contains(arg.message, "customerId") {
		t.Fatalf("message must name the bad field and the valid ones: %s", arg.message)
	}
}

// A name where a UUID belongs reached PostgreSQL and came back as
// "invalid input syntax for type uuid (SQLSTATE 22P02)".
func TestNonUUIDIdentifiersAreStoppedWithALookupHint(t *testing.T) {
	_, err := validateArguments(activitySchema, map[string]any{"id": sampleID, "customerId": "ACME 주식회사"})
	if err == nil || !strings.Contains(err.Error(), "search_customers") {
		t.Fatalf("want a pointer to search_customers, got %v", err)
	}
	if strings.Contains(err.Error(), "SQLSTATE") {
		t.Fatalf("must not look like a database error: %v", err)
	}
}

func TestEmptyOptionalIdentifierMeansNotProvided(t *testing.T) {
	got, err := validateArguments(activitySchema, map[string]any{"id": sampleID, "customerId": "  "})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got["customerId"]; present {
		t.Fatalf("empty optional id must be dropped: %v", got)
	}
}

func TestMissingAndEmptyRequiredArgumentsAreNamed(t *testing.T) {
	for _, args := range []map[string]any{{}, {"id": ""}, {"id": nil}} {
		_, err := validateArguments(activitySchema, args)
		if err == nil || !strings.Contains(err.Error(), "필수 입력") || !strings.Contains(err.Error(), "id(고객 ID)") {
			t.Fatalf("%v: want the missing field named with its description, got %v", args, err)
		}
	}
	name := schema([]string{"name"}, map[string]any{"name": str("고객명")})
	if _, err := validateArguments(name, map[string]any{"name": "   "}); err == nil {
		t.Fatal("a blank required name must count as missing")
	}
}

// "5" as a limit used to become the default limit without a word.
func TestScalarsAModelSendsAsStringsAreConverted(t *testing.T) {
	got, err := validateArguments(activitySchema, map[string]any{"id": sampleID, "limit": "5", "amount": "12.5", "unreadOnly": "TRUE"})
	if err != nil {
		t.Fatal(err)
	}
	if got["limit"] != 5.0 || got["amount"] != 12.5 || got["unreadOnly"] != true {
		t.Fatalf("conversion: %#v", got)
	}
}

func TestWrongTypesAreReportedTogether(t *testing.T) {
	_, err := validateArguments(activitySchema, map[string]any{"id": sampleID, "limit": "many", "unreadOnly": "yes", "tags": "a,b"})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, field := range []string{"limit", "unreadOnly", "tags"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("one call should report every bad field, %s missing: %v", field, err)
		}
	}
	if _, err := validateArguments(activitySchema, map[string]any{"id": sampleID, "limit": 2.5}); err == nil {
		t.Fatal("a fractional integer must be refused")
	}
}

func TestOpenSchemasAcceptExtraFields(t *testing.T) {
	open := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}
	got, err := validateArguments(open, map[string]any{"anything": 1})
	if err != nil || got["anything"] != 1 {
		t.Fatalf("open schema: %v %v", got, err)
	}
}

// structuredContent is an object by definition. A bare array made every
// SDK-based client reject the call before the model saw it.
func TestStructuredContentIsAlwaysAnObject(t *testing.T) {
	var nilSlice []map[string]any
	cases := []struct {
		name  string
		value any
		items int
	}{
		{"list", []map[string]any{{"id": 1}, {"id": 2}}, 2},
		{"empty list", []string{}, 0},
		{"nil list", nilSlice, 0},
	}
	for _, c := range cases {
		result := toolResult(c.value)
		structured, ok := result["structuredContent"].(map[string]any)
		if !ok {
			t.Fatalf("%s: structuredContent is %T", c.name, result["structuredContent"])
		}
		items, _ := structured["items"].([]any)
		if len(items) != c.items || structured["count"] != c.items {
			t.Fatalf("%s: %v", c.name, structured)
		}
	}
	object := toolResult(map[string]any{"name": "x"})["structuredContent"].(map[string]any)
	if object["name"] != "x" {
		t.Fatalf("an object result must pass through unchanged: %v", object)
	}
	scalar := toolResult(42)["structuredContent"].(map[string]any)
	if scalar["value"] != 42.0 {
		t.Fatalf("scalar: %v", scalar)
	}
}

// The text block is what non-SDK clients read; it must not change shape.
func TestTextContentKeepsTheOriginalSerialisation(t *testing.T) {
	result := toolResult([]int{1, 2})
	text := result["content"].([]map[string]any)[0]["text"]
	if text != "[1,2]" {
		t.Fatalf("text = %v", text)
	}
	var nilSlice []int
	if text := toolResult(nilSlice)["content"].([]map[string]any)[0]["text"]; text != "[]" {
		t.Fatalf("nil list text = %v, want []", text)
	}
}

func TestDatabaseErrorsNeverReachTheModelVerbatim(t *testing.T) {
	cases := map[string]error{
		"22P02":   &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "x"`},
		"23505":   &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint \"customers_registration_no_key\""},
		"wrapped": fmt.Errorf("load: %w", &pgconn.PgError{Code: "XX000", Message: "internal"}),
		"string":  errors.New(`ERROR: relation "secret_table" does not exist (SQLSTATE 42P01)`),
	}
	for name, err := range cases {
		got := sanitizeToolError(err, "req-1")
		if strings.Contains(got, "SQLSTATE") || strings.Contains(got, "constraint") || strings.Contains(got, "secret_table") || strings.Contains(got, "uuid:") {
			t.Fatalf("%s leaked: %s", name, got)
		}
	}
	if got := sanitizeToolError(fmt.Errorf("x: %w", pgx.ErrNoRows), ""); !strings.Contains(got, "찾을 수 없") {
		t.Fatalf("no rows: %s", got)
	}
	if got := sanitizeToolError(errors.New("contact not found"), ""); !strings.Contains(got, "찾을 수 없") {
		t.Fatalf("english not found: %s", got)
	}
	// A validation message written for the model passes through untouched.
	if got := sanitizeToolError(errors.New("name is required"), ""); got != "name is required" {
		t.Fatalf("ordinary message changed: %s", got)
	}
}

// JSON-RPC 2.0 requires "id": null when the request id could not be read,
// and every success to carry a result.
func TestResponsesAlwaysHaveAValidShape(t *testing.T) {
	encode := func(r response) map[string]json.RawMessage {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	parseError := encode(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "Parse error"}})
	if string(parseError["id"]) != "null" {
		t.Fatalf("error without id must carry id:null, got %s", parseError["id"])
	}
	if _, has := parseError["result"]; has {
		t.Fatal("an error must not carry a result")
	}
	empty := encode(response{JSONRPC: "2.0", ID: json.RawMessage("7")})
	if string(empty["result"]) != "{}" || string(empty["id"]) != "7" {
		t.Fatalf("empty success = %v", empty)
	}
	zero := encode(response{JSONRPC: "2.0", ID: json.RawMessage("0"), Result: map[string]any{}})
	if string(zero["id"]) != "0" {
		t.Fatalf("id 0 must survive: %s", zero["id"])
	}
}
