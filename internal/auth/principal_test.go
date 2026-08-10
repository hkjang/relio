package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewPrincipalSerializesEmptyPermissionsAsArray(t *testing.T) {
	raw, err := json.Marshal(newPrincipal("oidc-user"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"permissions":[]`) {
		t.Fatalf("permissions must be a JSON array for users without role mappings: %s", raw)
	}
}
