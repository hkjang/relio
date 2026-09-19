package admin

import "testing"

func TestEmptySecretValue(t *testing.T) {
	for _, value := range []any{nil, "", "   "} {
		if !emptySecretValue(value) {
			t.Fatalf("%#v must be treated as an omitted masked secret", value)
		}
	}
	for _, value := range []any{"secret", false, 0, map[string]any{"token": "value"}} {
		if emptySecretValue(value) {
			t.Fatalf("%#v must be treated as an explicit secret value", value)
		}
	}
}

func TestSettingKeyMayCarryAnInnerDotButANamespaceMayNot(t *testing.T) {
	for _, key := range []string{"enabled", "oauth.enabled", "oauth.resource", "rate_limit_per_minute"} {
		if !validSettingKey(key) {
			t.Fatalf("key %q must be accepted", key)
		}
	}
	for _, key := range []string{"", ".enabled", "oauth.", "oauth..enabled", "Oauth.enabled", "oauth enabled", "oauth/enabled"} {
		if validSettingKey(key) {
			t.Fatalf("key %q must be refused", key)
		}
	}
	if validSettingName("mcp.oauth") {
		t.Fatal("a namespace must stay dot-free so namespace.key reads back unambiguously")
	}
}
