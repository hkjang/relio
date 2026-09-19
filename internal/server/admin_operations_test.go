package server

import (
	"reflect"
	"testing"
	"time"
)

func TestAuditItemExportsMetadataLikeBeforeAndAfter(t *testing.T) {
	occurred := time.Date(2026, 9, 20, 3, 33, 51, 0, time.UTC)
	row := auditRow{id: "a1", actorID: "u1", actor: "admin", channel: "LOGIN", action: "LOGIN", resource: "session", ip: "10.0.0.1", requestID: "req-1", userAgent: "curl", occurredAt: occurred}

	t.Run("null metadata stays null", func(t *testing.T) {
		item := auditItem(row)
		if _, ok := item["metadata"]; !ok {
			t.Fatal("every item must carry a metadata key")
		}
		if item["metadata"] != nil {
			t.Fatalf("a NULL column must be exported as null, got %#v", item["metadata"])
		}
	})

	t.Run("valid json is decoded", func(t *testing.T) {
		r := row
		r.metadata = []byte(`{"bootstrap":true,"silent":false}`)
		item := auditItem(r)
		want := map[string]any{"bootstrap": true, "silent": false}
		if !reflect.DeepEqual(item["metadata"], want) {
			t.Fatalf("metadata must be decoded the same way as before/after: got %#v want %#v", item["metadata"], want)
		}
	})

	t.Run("broken bytes fall back to null like before and after", func(t *testing.T) {
		r := row
		r.before = []byte(`{"a":`)
		r.after = []byte(`{"b":1}`)
		r.metadata = []byte(`not json`)
		item := auditItem(r)
		if item["metadata"] != nil {
			t.Fatalf("undecodable metadata must be null, got %#v", item["metadata"])
		}
		if item["before"] != nil {
			t.Fatalf("undecodable before must be null, got %#v", item["before"])
		}
		if !reflect.DeepEqual(item["after"], map[string]any{"b": float64(1)}) {
			t.Fatalf("after must still decode independently, got %#v", item["after"])
		}
	})

	t.Run("scalar columns are passed through unchanged", func(t *testing.T) {
		item := auditItem(row)
		for key, want := range map[string]any{"id": "a1", "actorId": "u1", "actor": "admin", "channel": "LOGIN", "action": "LOGIN", "resource": "session", "resourceId": "", "ip": "10.0.0.1", "requestId": "req-1", "userAgent": "curl", "occurredAt": occurred} {
			if got := item[key]; got != want {
				t.Fatalf("%s: got %#v want %#v", key, got, want)
			}
		}
	})
}
