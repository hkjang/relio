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
