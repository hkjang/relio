package apikey

import (
	"reflect"
	"testing"

	"github.com/hkjang/relio/internal/auth"
)

func TestNormalizeAccessRemovesDuplicatesAndNormalizesChannels(t *testing.T) {
	scopes, channels := normalizeAccess(
		[]string{" customer:read ", "customer:read", "mcp:use", ""},
		[]string{"mcp", " MCP ", "rest"},
	)
	if !reflect.DeepEqual(scopes, []string{"customer:read", "mcp:use"}) {
		t.Fatalf("unexpected scopes: %#v", scopes)
	}
	if !reflect.DeepEqual(channels, []string{"MCP", "REST"}) {
		t.Fatalf("unexpected channels: %#v", channels)
	}
}

func TestAllowedScopesForBootstrapIncludesMCP(t *testing.T) {
	scopes := AllowedScopesFor(&auth.Principal{IsBootstrap: true})
	if !includes(scopes, "mcp:use") || len(scopes) != len(AllowedScopes) {
		t.Fatalf("bootstrap scopes were unexpectedly filtered: %#v", scopes)
	}
}

func TestAllowedScopesIncludeAllPersonalKeyCRMCapabilities(t *testing.T) {
	for _, scope := range []string{"forecast:write", "voice:read", "voice:write"} {
		if !includes(AllowedScopes, scope) {
			t.Fatalf("%s must be delegable to a personal key", scope)
		}
	}
}
