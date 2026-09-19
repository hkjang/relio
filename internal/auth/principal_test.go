package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// guards: Authenticate vs AuthenticateMCP — an SSO access token is an MCP
// credential only. The REST door never consults the validator, so a token
// there is refused exactly like any unknown bearer.
func TestAnSSOAccessTokenOnlyOpensTheMCPDoor(t *testing.T) {
	jwt := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln"
	calls := 0
	refusal := &Refusal{Message: "이 SSO 계정은 등록되지 않았습니다.", Cause: errors.New("no account")}
	s := &Service{MCPOAuth: func(context.Context, string) (OAuthGrant, error) {
		calls++
		return OAuthGrant{}, refusal
	}}
	withBearer := func(value string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("Authorization", "Bearer "+value)
		return r
	}

	if _, err := s.Authenticate(withBearer(jwt)); err == nil || calls != 0 {
		t.Fatalf("REST must refuse a JWT without consulting the validator: err=%v calls=%d", err, calls)
	}
	var got *Refusal
	_, err := s.AuthenticateMCP(withBearer(jwt))
	if calls != 1 {
		t.Fatalf("MCP consults the validator once, got %d", calls)
	}
	if !errors.As(err, &got) || got.Message != refusal.Message {
		t.Fatalf("the validator's refusal reaches the caller intact: %v", err)
	}

	// A bearer that is neither a key nor JWT-shaped never reaches the
	// validator on either door, so a switched-off install leaks no new words.
	for _, bearer := range []string{"not-a-token", "a.b", "a..c", ".b.c"} {
		if _, err := s.AuthenticateMCP(withBearer(bearer)); err == nil || calls != 1 {
			t.Fatalf("%q must be refused before the validator: err=%v calls=%d", bearer, err, calls)
		}
	}
	// Without a validator wired the MCP door is the key-only door it was.
	none := &Service{}
	if _, err := none.AuthenticateMCP(withBearer(jwt)); err == nil || err.Error() != "invalid bearer token" {
		t.Fatalf("no validator: %v", err)
	}
}

// guards: Has, ChannelAllowed for the principal an SSO token produces — the
// administrator's scope ceiling narrows the person's permissions and the
// MCP channel is the only one open, exactly as a Personal Key would be held.
func TestAnOAuthPrincipalPassesTheSameGateAsAKey(t *testing.T) {
	p := newPrincipal("u1")
	p.perm["admin:*"] = true
	p.AuthMethod = "OIDC_ACCESS_TOKEN"
	p.KeyID = OAuthKeyID
	p.KeyScopes = []string{"mcp:use", "customer:read"}
	p.KeyChannels = []string{"MCP"}

	if !p.Has("mcp:use") || !p.Has("customer:read") {
		t.Fatal("scopes inside the ceiling are granted")
	}
	if p.Has("customer:write") || p.Has("admin:write") {
		t.Fatal("an administrator's own permissions do not widen the ceiling")
	}
	if !p.ChannelAllowed("MCP") || p.ChannelAllowed("REST") {
		t.Fatal("the token opens MCP only")
	}
	// The person without the ceiling permission never gains it from a scope.
	q := newPrincipal("u2")
	q.perm["customer:read"] = true
	q.KeyID = OAuthKeyID
	q.KeyScopes = []string{"mcp:use", "customer:read"}
	if q.Has("mcp:use") {
		t.Fatal("a scope never grants a permission the person's roles lack")
	}
}
