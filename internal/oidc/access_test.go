package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hkjang/relio/internal/auth"
)

func sampleOAuth() MCPOAuth {
	return MCPOAuth{
		Enabled: true, Available: true, SSOEnabled: true,
		Issuer: "https://sso.example.test/realms/corp", ClientID: "relio",
		Resource: "https://crm.example.test/mcp", MetadataURL: "https://crm.example.test/.well-known/oauth-protected-resource/mcp",
		Audiences: []string{"https://crm.example.test/mcp", "https://crm.example.test", "relio"},
		Scopes:    []string{"profile", "email", "relio-mcp"},
	}
}

// The challenge is how a client finds Keycloak. Without resource_metadata an
// OAuth-capable client has nowhere to go.
func TestChallengeNamesTheMetadataOnlyWhenOAuthIsAvailable(t *testing.T) {
	on := sampleOAuth().Challenge(nil)
	for _, want := range []string{`Bearer realm="Relio MCP"`, `resource_metadata="https://crm.example.test/.well-known/oauth-protected-resource/mcp"`, `scope="profile email relio-mcp"`} {
		if !strings.Contains(on, want) {
			t.Fatalf("challenge %q lacks %s", on, want)
		}
	}
	if strings.Contains(on, "error=") {
		t.Fatalf("a request without a token must not get an error code: %s", on)
	}
	off := MCPOAuth{}.Challenge(nil)
	if off != `Bearer realm="Relio MCP"` {
		t.Fatalf("with OAuth off, clients must stay on keys: %s", off)
	}
}

func TestChallengeCarriesTheRefusalInASCII(t *testing.T) {
	refused := &auth.BearerError{Code: "insufficient_scope", Description: `Needs "relio-mcp" — 스코프`, Scope: "profile email relio-mcp"}
	got := sampleOAuth().Challenge(refused)
	if !strings.Contains(got, `error="insufficient_scope"`) || !strings.Contains(got, `scope="profile email relio-mcp"`) {
		t.Fatalf("challenge = %s", got)
	}
	for _, r := range got {
		if r > 0x7e {
			t.Fatalf("WWW-Authenticate must stay ASCII (RFC 6750): %q", got)
		}
	}
	if strings.Contains(got, `"relio-mcp" `) {
		t.Fatalf("quotes inside error_description break the header: %s", got)
	}
}

// Clients copy scopes_supported into their registration request, and Keycloak
// refuses any registration asking for openid.
func TestMetadataAdvertisesNoOpenIDScope(t *testing.T) {
	doc := sampleOAuth().ProtectedResourceMetadata()
	if doc["resource"] != "https://crm.example.test/mcp" {
		t.Fatalf("resource = %v", doc["resource"])
	}
	if servers, _ := doc["authorization_servers"].([]string); len(servers) != 1 || servers[0] != "https://sso.example.test/realms/corp" {
		t.Fatalf("authorization_servers = %v", doc["authorization_servers"])
	}
	for _, scope := range doc["scopes_supported"].([]string) {
		if scope == "openid" {
			t.Fatal("openid must not be advertised")
		}
	}
}

func TestAudienceAcceptsTheResourceOrTheClient(t *testing.T) {
	allowed := sampleOAuth().Audiences
	for _, aud := range []any{"relio", []any{"account", "relio"}, "https://crm.example.test/mcp", "https://crm.example.test/mcp/", []any{"https://crm.example.test"}} {
		if !audienceAny(aud, allowed) {
			t.Fatalf("aud %v should be accepted", aud)
		}
	}
	for _, aud := range []any{"account", []any{"account", "other-app"}, "https://crm.example.test/api", nil, ""} {
		if audienceAny(aud, allowed) {
			t.Fatalf("aud %v must be refused", aud)
		}
	}
}

// signer issues RS256 tokens and serves its key as a JWKS, counting fetches.
type signer struct {
	key     *rsa.PrivateKey
	kid     string
	fetches atomic.Int32
	server  *httptest.Server
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &signer{key: key, kid: "k1"}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.fetches.Add(1)
		e := big.NewInt(int64(key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kid": "enc", "kty": "RSA", "use": "enc", "n": "AQAB", "e": "AQAB"},
			{"kid": s.kid, "kty": "RSA", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)},
		}})
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *signer) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": s.kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestSignedTokensVerifyAndKeysAreCached(t *testing.T) {
	providerKeys.reset()
	s := newSigner(t)
	d := Discovery{Issuer: "https://sso.example.test/realms/corp", JWKSURI: s.server.URL}
	valid := map[string]any{"iss": d.Issuer, "sub": "u1", "exp": float64(time.Now().Add(time.Hour).Unix())}
	for i := 0; i < 5; i++ {
		if _, err := verifySigned(context.Background(), s.server.Client(), d, s.token(t, valid)); err != nil {
			t.Fatalf("valid token: %v", err)
		}
	}
	// Every bearer request used to fetch the key set; five verifications
	// must now cost one fetch.
	if got := s.fetches.Load(); got != 1 {
		t.Fatalf("JWKS fetched %d times for 5 verifications, want 1", got)
	}
}

func TestSignedTokensAreRefusedWhenAnythingIsOff(t *testing.T) {
	providerKeys.reset()
	s := newSigner(t)
	d := Discovery{Issuer: "https://sso.example.test/realms/corp", JWKSURI: s.server.URL}
	future := float64(time.Now().Add(time.Hour).Unix())
	cases := map[string]string{
		"other issuer": s.token(t, map[string]any{"iss": "https://evil.test/realms/corp", "exp": future}),
		"expired":      s.token(t, map[string]any{"iss": d.Issuer, "exp": float64(time.Now().Add(-time.Hour).Unix())}),
		"not yet":      s.token(t, map[string]any{"iss": d.Issuer, "exp": future, "nbf": float64(time.Now().Add(time.Hour).Unix())}),
		"no exp":       s.token(t, map[string]any{"iss": d.Issuer}),
		"not a jwt":    "abc.def",
	}
	good := s.token(t, map[string]any{"iss": d.Issuer, "exp": future})
	parts := strings.Split(good, ".")
	forged, _ := json.Marshal(map[string]any{"iss": d.Issuer, "exp": future, "sub": "admin"})
	cases["tampered"] = parts[0] + "." + base64.RawURLEncoding.EncodeToString(forged) + "." + parts[2]
	for name, raw := range cases {
		if _, err := verifySigned(context.Background(), s.server.Client(), d, raw); err == nil {
			t.Fatalf("%s: token was accepted", name)
		}
	}
}

// An unknown kid may mean Keycloak rotated keys, so it triggers a refresh —
// but never more than once per interval, or junk tokens could hammer Keycloak.
func TestUnknownKeyIDRefreshesAtMostOncePerInterval(t *testing.T) {
	providerKeys.reset()
	s := newSigner(t)
	d := Discovery{Issuer: "https://sso.example.test/realms/corp", JWKSURI: s.server.URL}
	future := float64(time.Now().Add(time.Hour).Unix())
	s.kid = "rotated-away"
	junk := s.token(t, map[string]any{"iss": d.Issuer, "exp": future})
	s.kid = "k1"
	for i := 0; i < 10; i++ {
		if _, err := verifySigned(context.Background(), s.server.Client(), d, junk); err == nil {
			t.Fatal("a token signed with an unpublished key was accepted")
		}
	}
	if got := s.fetches.Load(); got != 1 {
		t.Fatalf("10 unknown-kid tokens caused %d JWKS fetches, want 1", got)
	}
}
