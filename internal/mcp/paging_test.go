package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The tools whose answer is one page of a larger set. Their result carries
// hasMore and nextCursor, so their schema has to accept a cursor back.
var pagedListTools = map[string]map[string]any{
	"search_customers":   searchCustomersSchema,
	"list_opportunities": listOpportunitiesSchema,
}

// Both schemas set additionalProperties:false. A tool that reports nextCursor
// but declares no cursor argument leaves every page after the first
// unreachable: a strict client refuses to send an undeclared argument, and the
// server would have ignored it anyway.
func TestPagedListToolsDeclareTheCursorTheyHandBack(t *testing.T) {
	for name, tool := range pagedListTools {
		properties, ok := tool["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no properties object", name)
		}
		cursor, ok := properties["cursor"].(map[string]any)
		if !ok {
			t.Fatalf("%s answers with nextCursor but takes no cursor argument, so nothing past the first page can be read", name)
		}
		if cursor["type"] != "string" {
			t.Fatalf("%s declares cursor as %v, but a cursor is an opaque string", name, cursor["type"])
		}
		if description, _ := cursor["description"].(string); !strings.Contains(description, "nextCursor") {
			t.Fatalf("%s must tell the caller which response field to send back, got %q", name, description)
		}
		if allowed, _ := tool["additionalProperties"].(bool); allowed {
			t.Fatalf("%s no longer pins additionalProperties to false", name)
		}
	}
}

// Declaring an argument and then dropping it on the way to the query is the
// same failure as never declaring it, so every advertised argument has to reach
// the service filter.
func TestPagedListToolsPassOnEveryArgumentTheyAdvertise(t *testing.T) {
	mappers := map[string]func(map[string]any) any{
		"search_customers":   func(a map[string]any) any { return customerSearchArgs(a) },
		"list_opportunities": func(a map[string]any) any { return opportunityFilterArgs(a) },
	}
	for name, tool := range pagedListTools {
		properties := tool["properties"].(map[string]any)
		args := map[string]any{}
		want := map[string]string{}
		for key, raw := range properties {
			switch raw.(map[string]any)["type"] {
			case "string":
				// Uppercase so a filter that normalises its value still
				// contains the marker.
				value := strings.ToUpper("arg-" + key)
				args[key] = value
				want[key] = value
			case "integer":
				args[key] = float64(137)
				want[key] = "137"
			}
		}
		encoded, err := json.Marshal(mappers[name](args))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for key, value := range want {
			if !strings.Contains(string(encoded), value) {
				t.Fatalf("%s advertises %q but never passes it to the query: %s", name, key, encoded)
			}
		}
	}
}

// The opportunity query compares o.status to the stored uppercase value, so a
// model that sends the status in the case it reads in prose used to get an
// empty list rather than a match or a complaint.
func TestOpportunityStatusIsNormalisedBeforeItReachesTheQuery(t *testing.T) {
	for _, given := range []string{"open", " Open ", "OPEN"} {
		if got := opportunityFilterArgs(map[string]any{"status": given}).Status; got != "OPEN" {
			t.Fatalf("status %q reached the query as %q, which matches no row", given, got)
		}
	}
	if got := opportunityFilterArgs(map[string]any{}).Status; got != "" {
		t.Fatalf("an absent status must stay empty so the filter is skipped, got %q", got)
	}
}

func TestListArgumentsFallBackToADefaultLimit(t *testing.T) {
	if got := customerSearchArgs(map[string]any{}).Limit; got != 50 {
		t.Fatalf("customer search limit fell back to %d", got)
	}
	if got := opportunityFilterArgs(map[string]any{}).Limit; got != 50 {
		t.Fatalf("opportunity limit fell back to %d", got)
	}
}
