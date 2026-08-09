package server

import (
	"strings"
	"testing"
)

func TestProjectionFields(t *testing.T) {
	fields, err := projectionFields("id, name,id")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields[0] != "id" || fields[1] != "name" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
	if _, err = projectionFields("id,password.hash"); err == nil {
		t.Fatal("expected an invalid field error")
	}
}

func TestProjectObject(t *testing.T) {
	projected := projectObject(map[string]any{"id": "1", "name": "Relio", "secret": "hidden"}, []string{"id", "name"})
	if len(projected) != 2 || projected["id"] != "1" || projected["name"] != "Relio" {
		t.Fatalf("unexpected projection: %#v", projected)
	}
	if _, ok := projected["secret"]; ok {
		t.Fatal("unselected field leaked into projection")
	}
}

func TestDiagnosticReadinessOnlyCountsRequiredChecks(t *testing.T) {
	checks := []adminDiagnosticCheck{
		{Status: "HEALTHY", Required: true},
		{Status: "WARNING", Required: true},
		{Status: "DISABLED", Required: false},
	}
	if got := diagnosticReadiness(checks); got != 50 {
		t.Fatalf("readiness = %d, want 50", got)
	}
	if got := diagnosticReadiness(nil); got != 100 {
		t.Fatalf("empty readiness = %d, want 100", got)
	}
}

func TestRedactDiagnostic(t *testing.T) {
	value := "connect postgres://relio:secret@postgres:5432/relio password=hidden token:abc"
	redacted := redactDiagnostic(value)
	for _, secret := range []string{"secret", "hidden", "abc"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q leaked in %q", secret, redacted)
		}
	}
}
