package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// The transport is where MCP clients actually break. These pin the three
// behaviours that made a working key fail at tools/list.

func TestNegotiateVersionAnswersWhatTheClientAsked(t *testing.T) {
	for _, version := range supportedVersionOrder {
		if got := negotiateVersion(version); got != version {
			t.Fatalf("negotiateVersion(%q) = %q, want the client's own version", version, got)
		}
	}
	// A version we do not speak falls back to our newest, which is what the
	// specification asks the server to do.
	if got := negotiateVersion("1999-01-01"); got != ProtocolVersion {
		t.Fatalf("negotiateVersion(unknown) = %q, want %q", got, ProtocolVersion)
	}
	if got := negotiateVersion(""); got != ProtocolVersion {
		t.Fatalf("negotiateVersion(empty) = %q, want %q", got, ProtocolVersion)
	}
}

func TestOlderProtocolRevisionsAreAccepted(t *testing.T) {
	// 2024-11-05 is still what several shipping clients negotiate. Rejecting it
	// let initialize succeed and every later call fail.
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"} {
		if !supportedVersions[version] {
			t.Fatalf("protocol revision %s must be accepted", version)
		}
	}
}

func TestAcceptHeaderIsNotStricterThanItNeedsToBe(t *testing.T) {
	cases := map[string]bool{
		"application/json, text/event-stream": true,
		"application/json":                    true, // every response we send is JSON
		"*/*":                                 true,
		"":                                    true, // absent means "no preference"
		"text/event-stream":                   true,
		"APPLICATION/JSON":                    true,
		"text/plain":                          false,
	}
	for header, want := range cases {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if header != "" {
			r.Header.Set("Accept", header)
		}
		if got := acceptsMCP(r); got != want {
			t.Fatalf("acceptsMCP(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestParseRequestsAcceptsSingleAndBatch(t *testing.T) {
	batch, items, err := parseRequests([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil || batch || len(items) != 1 || items[0].Method != "tools/list" {
		t.Fatalf("single message: batch=%v items=%d err=%v", batch, len(items), err)
	}
	// Revisions before 2025-06-18 allow an array, and clients still send one.
	batch, items, err = parseRequests([]byte(`[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	if err != nil || !batch || len(items) != 2 {
		t.Fatalf("batch: batch=%v items=%d err=%v", batch, len(items), err)
	}
	// Leading whitespace must not hide the array.
	if batch, _, err = parseRequests([]byte("  \n[{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}]")); err != nil || !batch {
		t.Fatalf("whitespace-prefixed batch: batch=%v err=%v", batch, err)
	}
	if _, _, err = parseRequests([]byte(`not json`)); err == nil {
		t.Fatal("malformed JSON must be reported")
	}
}

func TestNotificationsAreRecognisedByAbsentID(t *testing.T) {
	cases := map[string]bool{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`:         true,
		`{"jsonrpc":"2.0","id":null,"method":"notifications/cancelled"}`: true,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`:                 false,
		`{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`:             false,
		`{"jsonrpc":"2.0","id":0,"method":"tools/list"}`:                 false,
	}
	for body, want := range cases {
		var req request
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if got := isNotification(req); got != want {
			t.Fatalf("isNotification(%s) = %v, want %v", body, got, want)
		}
	}
}

func TestFirstIDSkipsNotifications(t *testing.T) {
	_, items, err := parseRequests([]byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":7,"method":"tools/list"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(firstID(items)); got != "7" {
		t.Fatalf("firstID = %q, want the first answerable request's id", got)
	}
	_, onlyNotes, _ := parseRequests([]byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	if firstID(onlyNotes) != nil {
		t.Fatal("a batch of notifications has no id to answer with")
	}
}
