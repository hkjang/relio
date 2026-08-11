package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

func TestConfigurationBundleRejectsSensitiveAndSystemSettings(t *testing.T) {
	for _, setting := range []bundleSetting{
		{Namespace: "oidc", Key: "client_secret", Value: "leak", ValueType: "string"},
		{Namespace: "database", Key: "dsn", Value: "postgres://leak", ValueType: "string"},
	} {
		bundle := configurationBundle{Format: "relio-config/v1", Product: "Relio", Settings: []bundleSetting{setting}}
		if err := validateConfigurationBundle(bundle); err == nil {
			t.Fatalf("expected %s.%s to be rejected", setting.Namespace, setting.Key)
		}
	}
	bundle := configurationBundle{Format: "relio-config/v1", Product: "Relio", Roles: []bundleRole{{Code: "SYSTEM_ADMIN", Name: "System", DataScope: "COMPANY"}}}
	if err := validateConfigurationBundle(bundle); err == nil {
		t.Fatal("expected SYSTEM_ADMIN import to be rejected")
	}
}

func TestConfigurationBundleDiffIsNonDestructive(t *testing.T) {
	current := configurationBundle{
		Format: "relio-config/v1", Product: "Relio",
		Settings: []bundleSetting{{Namespace: "system", Key: "locale", Value: "ko-KR", ValueType: "string"}, {Namespace: "system", Key: "timezone", Value: "Asia/Seoul", ValueType: "string"}},
	}
	incoming := configurationBundle{
		Format: "relio-config/v1", Product: "Relio", SourceVersion: "1.4.0", GeneratedAt: time.Now(),
		Settings: []bundleSetting{{Namespace: "system", Key: "locale", Value: "en-US", ValueType: "string"}, {Namespace: "api", Key: "enabled", Value: true, ValueType: "boolean"}},
	}
	preview, err := diffConfigurationBundles(current, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary["update"] != 1 || preview.Summary["create"] != 1 || preview.Summary["total"] != 2 {
		t.Fatalf("unexpected summary: %#v", preview.Summary)
	}
	for _, change := range preview.Changes {
		if change.Key == "system.timezone" {
			t.Fatal("missing incoming values must not be interpreted as deletions")
		}
	}
}

func TestConfigurationBundleJSONRoundTrip(t *testing.T) {
	original := configurationBundle{Format: "relio-config/v1", Product: "Relio", SourceVersion: "1.4.0", Settings: []bundleSetting{}, Roles: []bundleRole{}, Pipelines: []bundlePipeline{}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded configurationBundle
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err = validateConfigurationBundle(decoded); err != nil {
		t.Fatal(err)
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

func TestRequestLimiterCountsPerIdentityAndWindow(t *testing.T) {
	limiter := newRequestLimiter()
	// A limit of zero or less means unlimited, which is how an administrator
	// disables throttling.
	for i := 0; i < 500; i++ {
		if !limiter.allow("unlimited", 0) {
			t.Fatal("a limit of 0 must never throttle")
		}
	}
	// Within one window a caller gets exactly `limit` requests.
	for i := 1; i <= 3; i++ {
		if !limiter.allow("alice", 3) {
			t.Fatalf("request %d of 3 must be allowed", i)
		}
	}
	if limiter.allow("alice", 3) {
		t.Fatal("the fourth request must be throttled")
	}
	// Identities are independent: one noisy key must not throttle another user.
	if !limiter.allow("bob", 3) {
		t.Fatal("a different identity must have its own budget")
	}
	// A new window resets the count.
	limiter.mu.Lock()
	limiter.entries["alice"] = requestBucket{window: time.Now().Add(-2 * time.Minute), count: 99}
	limiter.mu.Unlock()
	if !limiter.allow("alice", 3) {
		t.Fatal("a stale window must reset the budget")
	}
}

func TestLoginLimiterForgetsAfterSuccess(t *testing.T) {
	limiter := newLoginLimiter()
	for i := 0; i < 10; i++ {
		if !limiter.allow("10.0.0.1") {
			t.Fatalf("attempt %d must be allowed", i+1)
		}
	}
	if limiter.allow("10.0.0.1") {
		t.Fatal("the eleventh attempt within the window must be blocked")
	}
	// A successful login clears the failure record so the user is not locked out
	// of their next session.
	limiter.success("10.0.0.1")
	if !limiter.allow("10.0.0.1") {
		t.Fatal("a successful login must clear the attempt counter")
	}
}
