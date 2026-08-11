package server

import (
	"strings"
	"testing"
)

func TestBlockedByListsOnlyRealReferences(t *testing.T) {
	if err := blockedBy("Role", reference{"사용자", 0}, reference{"승인 정책", 0}); err != nil {
		t.Fatalf("an unreferenced record must be deletable: %v", err)
	}
	err := blockedBy("Role", reference{"사용자", 3}, reference{"승인 정책", 0}, reference{"OIDC Role 매핑", 1})
	if err == nil {
		t.Fatal("a referenced record must be blocked")
	}
	message := err.Error()
	if !strings.Contains(message, "사용자 3건") || !strings.Contains(message, "OIDC Role 매핑 1건") {
		t.Fatalf("the error must name every blocker: %s", message)
	}
	if strings.Contains(message, "승인 정책") {
		t.Fatalf("zero counts must be omitted: %s", message)
	}
}

func TestReferencingLabelFallsBackToTheTableName(t *testing.T) {
	if referencingLabel("opportunities") != "Opportunity" {
		t.Fatal("a known table must use the console vocabulary")
	}
	if referencingLabel("") != "다른 데이터" {
		t.Fatal("an unknown constraint must still produce a readable message")
	}
	if referencingLabel("some_future_table") != "some_future_table" {
		t.Fatal("an unmapped table name must be surfaced verbatim")
	}
}

func TestNormalizePermissionsDeduplicatesAndLowercases(t *testing.T) {
	got := normalizePermissions([]string{"Customer:Read", " customer:read ", "", "opportunity:read"})
	if len(got) != 2 || got[0] != "customer:read" || got[1] != "opportunity:read" {
		t.Fatalf("unexpected permissions: %#v", got)
	}
}

func TestValidatePermissionsRejectsUnknownEntries(t *testing.T) {
	if err := validatePermissions([]string{"customer:read", "admin:*", "mcp:use"}); err != nil {
		t.Fatalf("catalog permissions must be accepted: %v", err)
	}
	if err := validatePermissions([]string{"customer:reed"}); err == nil {
		t.Fatal("a typo must be rejected instead of creating a Role that grants nothing")
	}
}

func TestPermissionCatalogCoversEveryDataScope(t *testing.T) {
	// The Role editor writes dataScope straight through, so the two lists must
	// agree or an administrator can save a scope the loader ranks as USER.
	for scope := range validDataScopes {
		if !strings.Contains("USER TEAM DEPARTMENT DIVISION COMPANY", scope) {
			t.Fatalf("unexpected data scope %q", scope)
		}
	}
	if len(validDataScopes) != 5 {
		t.Fatalf("expected five data scopes, found %d", len(validDataScopes))
	}
	seen := map[string]bool{}
	for _, entry := range permissionCatalog {
		if entry.Permission == "" || entry.Label == "" || entry.Group == "" {
			t.Fatalf("catalog entry %#v is incomplete", entry)
		}
		if seen[entry.Permission] {
			t.Fatalf("duplicate catalog entry %s", entry.Permission)
		}
		seen[entry.Permission] = true
	}
	// Dashboard access hinges on opportunity:read, so it must stay assignable.
	if !seen["opportunity:read"] {
		t.Fatal("opportunity:read must be assignable or the dashboard answers 403")
	}
}

func TestDefaultStringUsesTheFallbackForBlankInput(t *testing.T) {
	if defaultString("   ", "#64748b") != "#64748b" {
		t.Fatal("a blank value must fall back")
	}
	if defaultString(" DEPARTMENT ", "COMPANY") != "DEPARTMENT" {
		t.Fatal("a provided value must be trimmed and kept")
	}
}
