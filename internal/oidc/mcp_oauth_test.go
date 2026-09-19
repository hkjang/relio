package oidc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// MCP 를 개인 키 없이 Keycloak 토큰으로.
//
// The authorization flow itself — PKCE, the redirect, the code exchange — is
// Keycloak's and the client's. What is this server's is the resource-server
// half: it says where the authorization server is, it turns a 401 into a
// pointer there, and it accepts exactly the tokens that server issued for
// this resource. These tests stand up a fake identity provider with real key
// pairs, serve its discovery and JWKS over HTTP, and sign real JWTs with it,
// so what is verified is the verification and not a stub of it.

type fakeIDP struct {
	server        *httptest.Server
	rsaKey        *rsa.PrivateKey
	ecKey         *ecdsa.PrivateKey
	rogueKey      *rsa.PrivateKey // never published
	discoveryHits atomic.Int32
	jwksHits      atomic.Int32
	jwksURI       string // overrides the default when set
	// rotated, when set, is the only RSA key the JWKS publishes.
	rotated *rsa.PrivateKey
	// delay is how long every answer takes — a slow Keycloak; discoveryDown
	// makes discovery answer 503 after that delay — an unreachable one.
	delay         atomic.Int64
	discoveryDown atomic.Bool
}

func newIDP(t *testing.T) *fakeIDP {
	t.Helper()
	idp := &fakeIDP{}
	var err error
	if idp.rsaKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	if idp.rogueKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	if idp.ecKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		idp.discoveryHits.Add(1)
		time.Sleep(time.Duration(idp.delay.Load()))
		if idp.discoveryDown.Load() {
			http.Error(w, "keycloak is down", http.StatusServiceUnavailable)
			return
		}
		jwks := idp.server.URL + "/keys"
		if idp.jwksURI != "" {
			jwks = idp.jwksURI
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": idp.server.URL, "authorization_endpoint": idp.server.URL + "/auth",
			"token_endpoint": idp.server.URL + "/token", "jwks_uri": jwks,
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		idp.jwksHits.Add(1)
		time.Sleep(time.Duration(idp.delay.Load()))
		rsaKey, rsaKid := idp.rsaKey, "rsa-1"
		if idp.rotated != nil {
			rsaKey, rsaKid = idp.rotated, "rsa-2"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{
			{"kid": rsaKid, "kty": "RSA", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes())},
			{"kid": "ec-1", "kty": "EC", "crv": "P-256", "use": "sig",
				"x": base64.RawURLEncoding.EncodeToString(idp.ecKey.X.FillBytes(make([]byte, 32))),
				"y": base64.RawURLEncoding.EncodeToString(idp.ecKey.Y.FillBytes(make([]byte, 32)))},
			// An encryption key must never verify anything.
			{"kid": "enc-1", "kty": "RSA", "use": "enc",
				"n": base64.RawURLEncoding.EncodeToString(idp.rogueKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(idp.rogueKey.E)).Bytes())},
		}})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func segment(v any) string {
	raw, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// sign produces a JWS with the named algorithm and key id.
func (idp *fakeIDP) sign(t *testing.T, header map[string]any, claims map[string]any) string {
	t.Helper()
	input := segment(header) + "." + segment(claims)
	digest := sha256.Sum256([]byte(input))
	var sig []byte
	var err error
	switch header["alg"] {
	case "RS256":
		key := idp.rsaKey
		if header["kid"] == "rogue" {
			key = idp.rogueKey
		}
		if header["kid"] == "rsa-2" && idp.rotated != nil {
			key = idp.rotated
		}
		sig, err = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	case "PS256":
		sig, err = rsa.SignPSS(rand.Reader, idp.rsaKey, crypto.SHA256, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	case "ES256":
		var r, s *big.Int
		r, s, err = ecdsa.Sign(rand.Reader, idp.ecKey, digest[:])
		if err == nil {
			sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
		}
	case "HS256":
		mac := hmac.New(sha256.New, []byte("shared-secret"))
		mac.Write([]byte(input))
		sig = mac.Sum(nil)
	case "none":
		sig = []byte("x")
	}
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// accessToken is what Keycloak hands an MCP client after the person signed
// in: signed by the realm key, issued by the issuer, for an audience.
func (idp *fakeIDP) accessToken(t *testing.T, audience any, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"iss": idp.server.URL, "sub": "subject-mcp", "aud": audience, "azp": "claude-mcp",
		"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(),
		"typ": "Bearer", "preferred_username": "hong", "scope": "openid profile email",
	}
	header := map[string]any{"alg": "RS256", "kid": "rsa-1", "typ": "JWT"}
	for k, v := range extra {
		if k == "alg" || k == "kid" || k == "header.typ" {
			header[strings.TrimPrefix(k, "header.")] = v
			continue
		}
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	return idp.sign(t, header, claims)
}

const resource = "https://crm.example.test/mcp"

func settingsFor(idp *fakeIDP) MCPOAuth {
	return resolveMCPOAuth(map[string]string{
		"mcp.oauth.enabled": "true", "mcp.oauth.resource": resource, "system.service_url": "https://crm.example.test",
	}, Config{IssuerURL: idp.server.URL, ClientID: "relio-web", UsernameClaim: "preferred_username"})
}

func refusalOf(t *testing.T, err error) *auth.Refusal {
	t.Helper()
	var refusal *auth.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("expected a client-facing refusal, got %v", err)
	}
	return refusal
}

// guards: resolveMCPOAuth, Active, MetadataURL, Challenge, Document
func TestResourceServerIsOffUntilConfigured(t *testing.T) {
	// A fresh install: no rows beyond the seed, no provider.
	off := resolveMCPOAuth(map[string]string{"mcp.oauth.enabled": "false", "system.service_url": "http://localhost:8080"}, Config{})
	if off.Enabled {
		t.Fatal("must be off by default")
	}
	if active, reason := off.Active(); active || !strings.Contains(reason, "mcp.oauth.enabled") {
		t.Fatalf("inactive with the switch off: %v %q", active, reason)
	}
	if got := off.Challenge(false); got != `Bearer realm="Relio MCP"` {
		t.Fatalf("a switched-off deployment must challenge exactly as before: %q", got)
	}
	if !strings.HasPrefix(strings.Join(off.Scopes, " "), "mcp:use customer:read") {
		t.Fatalf("default scopes must be the read set led by mcp:use: %v", off.Scopes)
	}

	// Switched on without an issuer: still inactive, and the reason says why.
	noIssuer := resolveMCPOAuth(map[string]string{"mcp.oauth.enabled": "true", "system.service_url": "https://crm.example.test/"}, Config{})
	if active, reason := noIssuer.Active(); active || !strings.Contains(reason, "issuer") {
		t.Fatalf("inactive without an issuer: %v %q", active, reason)
	}
	if noIssuer.Resource != "https://crm.example.test/mcp" {
		t.Fatalf("resource derives from service_url + /mcp: %q", noIssuer.Resource)
	}

	// MCP itself off: nothing to advertise.
	mcpOff := resolveMCPOAuth(map[string]string{"mcp.oauth.enabled": "true", "mcp.enabled": "false", "system.service_url": "https://crm.example.test"}, Config{IssuerURL: "https://sso.example.test/realms/x/"})
	if active, reason := mcpOff.Active(); active || !strings.Contains(reason, "mcp.enabled") {
		t.Fatalf("inactive with MCP off: %v %q", active, reason)
	}
	if mcpOff.Issuer != "https://sso.example.test/realms/x" {
		t.Fatalf("issuer keeps no trailing slash: %q", mcpOff.Issuer)
	}

	// An explicit resource wins over the public address; a broken one yields
	// nothing rather than a Host-header guess.
	custom := resolveMCPOAuth(map[string]string{"mcp.oauth.enabled": "true", "mcp.oauth.resource": "https://edge.example.test/relio/mcp/", "system.service_url": "https://crm.example.test"}, Config{IssuerURL: "https://sso.example.test/realms/x"})
	if custom.Resource != "https://edge.example.test/relio/mcp" {
		t.Fatalf("explicit resource: %q", custom.Resource)
	}
	if custom.MetadataURL() != "https://edge.example.test/.well-known/oauth-protected-resource/relio/mcp" {
		t.Fatalf("RFC 9728 puts the well-known segment before the path: %q", custom.MetadataURL())
	}
	for _, bad := range []string{"crm.example.test/mcp", "ftp://x/mcp", "https://x/mcp?x=1", "https://user:pw@x/mcp"} {
		broken := resolveMCPOAuth(map[string]string{"mcp.oauth.enabled": "true", "mcp.oauth.resource": bad, "system.service_url": ""}, Config{IssuerURL: "https://sso.example.test/realms/x"})
		if broken.Resource != "" {
			t.Fatalf("%q must not become a resource identifier: %q", bad, broken.Resource)
		}
		if active, reason := broken.Active(); active || !strings.Contains(reason, "resource") {
			t.Fatalf("inactive without a resource: %v %q", active, reason)
		}
	}

	// Fully configured: active, and every value the client needs is there.
	on := resolveMCPOAuth(map[string]string{"mcp.oauth.enabled": "true", "mcp.oauth.audience": " claude-mcp  cursor-mcp ", "mcp.oauth.scopes": "customer:read report:read", "system.service_url": "https://crm.example.test"}, Config{IssuerURL: "https://sso.example.test/realms/x", UsernameClaim: ""})
	if active, _ := on.Active(); !active {
		t.Fatal("must be active with issuer, resource and MCP on")
	}
	if strings.Join(on.Audiences, ",") != "claude-mcp,cursor-mcp" {
		t.Fatalf("audiences split on whitespace: %v", on.Audiences)
	}
	if strings.Join(on.Scopes, " ") != "mcp:use customer:read report:read" {
		t.Fatalf("mcp:use is always granted so the MCP gate opens: %v", on.Scopes)
	}
	if on.UsernameClaim != "preferred_username" {
		t.Fatalf("username claim default: %q", on.UsernameClaim)
	}
	if got := on.Challenge(false); got != `Bearer realm="Relio MCP", resource_metadata="https://crm.example.test/.well-known/oauth-protected-resource/mcp"` {
		t.Fatalf("challenge: %q", got)
	}
	if got := on.Challenge(true); !strings.HasSuffix(got, `, error="invalid_token"`) {
		t.Fatalf("a refused token is named in the challenge: %q", got)
	}
	doc := on.Document()
	if doc["resource"] != "https://crm.example.test/mcp" || doc["authorization_servers"].([]string)[0] != "https://sso.example.test/realms/x" || doc["bearer_methods_supported"].([]string)[0] != "header" {
		t.Fatalf("document: %#v", doc)
	}
}

// guards: verifyMCPToken (the accepting paths)
func TestATokenIssuedForThisResourceIsAccepted(t *testing.T) {
	idp := newIDP(t)
	svc := &Service{}
	settings := settingsFor(idp)
	ctx := context.Background()

	claims, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, resource, nil), time.Now())
	if err != nil {
		t.Fatalf("a token whose aud is the resource must verify: %v", err)
	}
	if claims.Subject != "subject-mcp" || claims.Username != "hong" || claims.Scope != "openid profile email" {
		t.Fatalf("claims: %#v", claims)
	}
	// aud as a list, with the resource among other values.
	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, []string{"account", resource + "/"}, nil), time.Now()); err != nil {
		t.Fatalf("aud list containing the resource (trailing slash tolerated): %v", err)
	}
	// ES256 and PS256 are asymmetric too.
	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, resource, map[string]any{"alg": "ES256", "kid": "ec-1"}), time.Now()); err != nil {
		t.Fatalf("ES256: %v", err)
	}
	pss := idp.accessToken(t, resource, map[string]any{"alg": "PS256", "kid": "rsa-1"})
	input, sig, _ := strings.Cut(pss[:strings.LastIndex(pss, ".")]+"|"+pss[strings.LastIndex(pss, ".")+1:], "|")
	sigBytes, _ := base64.RawURLEncoding.DecodeString(sig)
	if err := verifySignature(signingKey{kid: "rsa-1", rsa: &idp.rsaKey.PublicKey}, "PS256", crypto.SHA256, []byte(input), sigBytes); err != nil {
		t.Fatalf("PS256: %v", err)
	}
	// ... but a key the JWKS published for RS256 does not verify a PS256
	// token, whatever the key material could do.
	if _, err := svc.verifyMCPToken(ctx, settings, pss, time.Now()); err == nil || !strings.Contains(refusalOf(t, err).Cause.Error(), "published for RS256") {
		t.Fatalf("a key is bound to its published alg: %v", err)
	}
	// The whole set above used discovery once and the JWKS once.
	if hits := idp.jwksHits.Load(); hits != 1 {
		t.Fatalf("keys are cached across requests, fetched %d times", hits)
	}
	// nbf in the recent past and a slightly early clock are fine.
	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, resource, map[string]any{"nbf": time.Now().Add(30 * time.Second).Unix()}), time.Now()); err != nil {
		t.Fatalf("nbf within clock skew: %v", err)
	}
}

// guards: audienceAccepted — the check that keeps another application's
// token out of this one, and the message that lets an operator fix it.
func TestATokenForAnotherApplicationIsRefusedAndTheMessageSaysWhatToChange(t *testing.T) {
	idp := newIDP(t)
	svc := &Service{}
	settings := settingsFor(idp)
	ctx := context.Background()

	// What a real Keycloak 26 issues without an Audience mapper: aud=account,
	// the client in azp.
	token := idp.accessToken(t, "account", map[string]any{"azp": "claude-mcp"})
	_, err := svc.verifyMCPToken(ctx, settings, token, time.Now())
	refusal := refusalOf(t, err)
	for _, want := range []string{"[account]", `"claude-mcp"`, "mcp.oauth.audience", resource} {
		if !strings.Contains(refusal.Message, want) {
			t.Fatalf("the refusal must show what was seen and what to write; missing %q in %q", want, refusal.Message)
		}
	}
	if !strings.Contains(refusal.Cause.Error(), "azp") {
		t.Fatalf("the log line names the audience check: %v", refusal.Cause)
	}

	// The administrator writes the client id in the accepted list: passes
	// without any mapper.
	settings.Audiences = []string{"cursor-mcp", "claude-mcp"}
	if _, err := svc.verifyMCPToken(ctx, settings, token, time.Now()); err != nil {
		t.Fatalf("azp in mcp.oauth.audience must pass: %v", err)
	}
	// ... and an aud value in that list passes too.
	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, "cursor-mcp", map[string]any{"azp": "other"}), time.Now()); err != nil {
		t.Fatalf("aud in mcp.oauth.audience must pass: %v", err)
	}
	// The web sign-in client is not an MCP client: a token issued to it does
	// not open /mcp unless the administrator lists it.
	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, "account", map[string]any{"azp": "relio-web"}), time.Now()); err == nil {
		t.Fatal("the web client id is not implicitly an accepted audience")
	}
	// Empty aud and azp: nothing to match.
	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, nil, map[string]any{"azp": nil}), time.Now()); err == nil {
		t.Fatal("a token with neither aud nor azp is refused")
	}
}

// guards: verifyMCPToken (every refusing path), and that each refusal's cause
// — what the server logs — names the check that failed.
func TestATokenIsRefusedForTheRightReason(t *testing.T) {
	idp := newIDP(t)
	svc := &Service{}
	settings := settingsFor(idp)
	ctx := context.Background()
	now := time.Now()

	cases := []struct {
		name     string
		token    string
		wantLog  string
		wantUser string
	}{
		{"expired", idp.accessToken(t, resource, map[string]any{"exp": now.Add(-2 * time.Minute).Unix()}), "exp", "만료"},
		{"no exp", idp.accessToken(t, resource, map[string]any{"exp": nil}), "exp", "만료"},
		{"not yet valid", idp.accessToken(t, resource, map[string]any{"nbf": now.Add(5 * time.Minute).Unix()}), "nbf", "유효하지"},
		{"other issuer", idp.accessToken(t, resource, map[string]any{"iss": "https://other.example.test/realms/x"}), "iss", "발급자"},
		{"id token", idp.accessToken(t, resource, map[string]any{"typ": "ID"}), "typ is ID", "ID 토큰"},
		{"id token by header", idp.accessToken(t, resource, map[string]any{"header.typ": "ID"}), "typ is ID", "ID 토큰"},
		{"holder-of-key", idp.accessToken(t, resource, map[string]any{"cnf": map[string]any{"jkt": "abc"}}), "cnf", "cnf"},
		{"no subject", idp.accessToken(t, resource, map[string]any{"sub": ""}), "sub", "sub"},
		{"hmac", idp.accessToken(t, resource, map[string]any{"alg": "HS256"}), "HS256", "서명"},
		{"unsigned", idp.accessToken(t, resource, map[string]any{"alg": "none"}), "none", "서명"},
		{"rogue key", idp.accessToken(t, resource, map[string]any{"kid": "rogue"}), "kid", "서명"},
		{"encryption key", idp.accessToken(t, resource, map[string]any{"kid": "enc-1"}), "kid", "서명"},
		{"wrong key type", idp.accessToken(t, resource, map[string]any{"alg": "ES256", "kid": "rsa-1"}), "published for RS256", "서명"},
		{"wrong key type unpublished alg", idp.accessToken(t, resource, map[string]any{"alg": "RS256", "kid": "ec-1"}), "not RSA", "서명"},
		{"tampered", func() string {
			token := idp.accessToken(t, resource, nil)
			parts := strings.Split(token, ".")
			parts[1] = segment(map[string]any{"iss": idp.server.URL, "sub": "someone-else", "aud": resource, "exp": now.Add(time.Hour).Unix(), "typ": "Bearer"})
			return strings.Join(parts, ".")
		}(), "signature", "서명"},
		{"not a jws", "abc.def", "compact jws", "유효하지"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.verifyMCPToken(ctx, settings, tc.token, now)
			refusal := refusalOf(t, err)
			if !strings.Contains(refusal.Message, tc.wantUser) {
				t.Fatalf("client message %q should mention %q", refusal.Message, tc.wantUser)
			}
			if !strings.Contains(refusal.Cause.Error(), tc.wantLog) {
				t.Fatalf("logged cause %q should name the failed check %q", refusal.Cause, tc.wantLog)
			}
			if strings.Contains(refusal.Message, tc.token) || strings.Contains(refusal.Cause.Error(), tc.token) {
				t.Fatal("neither the message nor the log may carry the token")
			}
		})
	}
	// The refusals above never made this server hammer the key set.
	if hits := idp.jwksHits.Load(); hits > 2 {
		t.Fatalf("unknown key ids must not refetch the JWKS more than once a second: %d fetches", hits)
	}
}

// guards: mcpSigningKey — rotation is picked up, forged ids are throttled,
// discovery from a different origin is not trusted.
func TestKeyRotationIsFollowedWithoutHammeringTheProvider(t *testing.T) {
	idp := newIDP(t)
	svc := &Service{}
	settings := settingsFor(idp)
	ctx := context.Background()

	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, resource, nil), time.Now()); err != nil {
		t.Fatal(err)
	}
	// The realm rotates its key. The old kid is gone, the new one unknown to
	// the cache; the first token with the new kid triggers one refetch.
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp.rotated = rotated
	svc.mcp.nextKeyFetch = time.Time{} // the pause from the first fetch has passed
	if _, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, resource, map[string]any{"kid": "rsa-2"}), time.Now()); err != nil {
		t.Fatalf("a token signed with the rotated key must verify after one refetch: %v", err)
	}
	if hits := idp.jwksHits.Load(); hits != 2 {
		t.Fatalf("rotation costs one JWKS fetch, got %d", hits)
	}
	// A stream of unknown ids inside the pause does not fetch again.
	for i := 0; i < 5; i++ {
		_, err := svc.verifyMCPToken(ctx, settings, idp.accessToken(t, resource, map[string]any{"kid": "rogue"}), time.Now())
		if err == nil {
			t.Fatal("a token with an unpublished key must be refused")
		}
	}
	if hits := idp.jwksHits.Load(); hits != 2 {
		t.Fatalf("forged key ids must be throttled to one fetch per second, got %d fetches", hits)
	}

	// A discovery document that sends the key set elsewhere is refused: the
	// key set must come from the issuer the administrator named.
	elsewhere := httptest.NewServer(http.NotFoundHandler())
	defer elsewhere.Close()
	idp.jwksURI = elsewhere.URL + "/keys"
	fresh := &Service{}
	_, err = fresh.verifyMCPToken(ctx, settings, idp.accessToken(t, resource, nil), time.Now())
	refusal := refusalOf(t, err)
	if !strings.Contains(refusal.Cause.Error(), "origin") {
		t.Fatalf("jwks_uri on another origin must be refused: %v", err)
	}
	if hits := idp.jwksHits.Load(); hits != 2 {
		t.Fatal("the foreign key set must not even be fetched")
	}
}

// guards: AuthenticateMCPToken — the log line behind "look at the server
// log" in the administrator guide. This runs the real entry point with a
// real logger: the line carries the request id the client saw on its 401,
// the message the client was told, and the verifier's own cause (which
// signature, issuer, exp or nbf check failed) — and never the token.
func TestARefusedTokenIsLoggedWithItsCauseAndTheRequestID(t *testing.T) {
	idp := newIDP(t)
	var buf bytes.Buffer
	svc := &Service{Log: slog.New(slog.NewTextHandler(&buf, nil))}
	settings := settingsFor(idp)
	svc.mcpSettings = func(context.Context) MCPOAuth { return settings }
	svc.mcpAccountLookup = func(_ context.Context, subject string) (string, error) {
		if subject == "subject-mcp" {
			return "user-1", nil
		}
		return "", pgx.ErrNoRows
	}
	now := time.Now()
	tampered := func() string {
		token := idp.accessToken(t, resource, nil)
		parts := strings.Split(token, ".")
		parts[1] = segment(map[string]any{"iss": idp.server.URL, "sub": "someone-else", "aud": resource, "exp": now.Add(time.Hour).Unix(), "typ": "Bearer"})
		return strings.Join(parts, ".")
	}()
	cases := []struct {
		name, token, wantMessage, wantDetail string
	}{
		{"expired", idp.accessToken(t, resource, map[string]any{"exp": now.Add(-2 * time.Minute).Unix()}), "만료", "token exp is missing or in the past"},
		{"not yet valid", idp.accessToken(t, resource, map[string]any{"nbf": now.Add(5 * time.Minute).Unix()}), "유효하지", "token nbf is in the future"},
		{"other issuer", idp.accessToken(t, resource, map[string]any{"iss": "https://other.example.test/realms/x"}), "발급자", "is not the configured issuer"},
		{"forged signature", tampered, "서명", "token signature verification failed"},
		{"unknown key", idp.accessToken(t, resource, map[string]any{"kid": "rogue"}), "서명", "is not in the issuer's key set"},
		{"unknown account", idp.accessToken(t, resource, map[string]any{"sub": "stranger"}), "등록되지", `no active relio account for sso subject`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			svc.mcp.nextKeyFetch = time.Time{} // each unknown kid may refetch once
			requestID := fmt.Sprintf("req-%d", i)
			if _, err := svc.AuthenticateMCPToken(httpx.WithRequestID(context.Background(), requestID), tc.token); err == nil {
				t.Fatal("the token must be refused")
			}
			line := buf.String()
			if !strings.Contains(line, "msg=\"mcp oauth token refused\"") {
				t.Fatalf("the refusal must be logged: %q", line)
			}
			if !strings.Contains(line, "request_id="+requestID) {
				t.Fatalf("the line must carry the request id %q: %q", requestID, line)
			}
			if !strings.Contains(line, tc.wantMessage) {
				t.Fatalf("the line must carry what the client was told (%q): %q", tc.wantMessage, line)
			}
			if !strings.Contains(line, tc.wantDetail) {
				t.Fatalf("the line must carry the verifier's own cause (%q): %q", tc.wantDetail, line)
			}
			if strings.Contains(line, tc.token) || strings.Contains(line, "request_id=missing") {
				t.Fatalf("the line may not carry the token, and had a request id: %q", line)
			}
		})
	}
	// An accepted token is not a refusal: nothing is logged.
	buf.Reset()
	grant, err := svc.AuthenticateMCPToken(httpx.WithRequestID(context.Background(), "req-ok"), idp.accessToken(t, resource, nil))
	if err != nil || grant.UserID != "user-1" || !slices.Contains(grant.Scopes, "mcp:use") {
		t.Fatalf("accepted: %#v %v", grant, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("an accepted token logs nothing: %q", buf.String())
	}
	// A context without a request id says so rather than logging a blank.
	buf.Reset()
	_, _ = svc.AuthenticateMCPToken(context.Background(), tampered)
	if line := buf.String(); !strings.Contains(line, "request_id=missing") || !strings.Contains(line, "token signature verification failed") {
		t.Fatalf("no request id in ctx must be visible: %q", line)
	}
	// Switched off: the plain (non-refusal) error is logged with the id too.
	buf.Reset()
	svc.mcpSettings = func(context.Context) MCPOAuth { off := settings; off.Enabled = false; return off }
	_, _ = svc.AuthenticateMCPToken(httpx.WithRequestID(context.Background(), "req-off"), tampered)
	if line := buf.String(); !strings.Contains(line, "request_id=req-off") || !strings.Contains(line, "mcp.oauth.enabled is off") {
		t.Fatalf("a switched-off refusal is logged with the request id: %q", line)
	}
}

// guards: mcpDiscovery, mcpKey — no round trip to Keycloak runs under the
// cache lock. Concurrent callers share one; an unreachable issuer costs one
// timeout for all of them and is then remembered for a while rather than
// retried on every request; a caller whose context ends is not held behind
// the round trip, which still completes for everyone else.
func TestConcurrentCallersShareOneRoundTripToTheProvider(t *testing.T) {
	idp := newIDP(t)
	const delay = 300 * time.Millisecond
	idp.delay.Store(int64(delay))
	settings := settingsFor(idp)
	ctx := context.Background()
	const n = 8
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i] = idp.accessToken(t, resource, nil)
	}
	verifyAll := func(svc *Service) (time.Duration, []error) {
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := time.Now()
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = svc.verifyMCPToken(ctx, settings, tokens[i], time.Now())
			}()
		}
		wg.Wait()
		return time.Since(start), errs
	}

	// Keycloak unreachable: discovery fails after the delay. Before, every
	// caller queued behind the lock for its own attempt (n × delay); now
	// they share one.
	idp.discoveryDown.Store(true)
	svc := &Service{}
	elapsed, errs := verifyAll(svc)
	for i, err := range errs {
		refusal := refusalOf(t, err)
		if !strings.Contains(refusal.Message, "발급자 정보") || !strings.Contains(refusal.Cause.Error(), "discovery") {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if hits := idp.discoveryHits.Load(); hits != 1 {
		t.Fatalf("%d concurrent callers must share one discovery attempt, made %d", n, hits)
	}
	if elapsed > 3*delay {
		t.Fatalf("callers were serialized: %d of them took %s for one %s round trip", n, elapsed, delay)
	}
	// Within the retry pause the failure is answered from memory: no round
	// trip, no wait.
	start := time.Now()
	_, err := svc.verifyMCPToken(ctx, settings, tokens[0], time.Now())
	if refusal := refusalOf(t, err); !strings.Contains(refusal.Cause.Error(), "discovery") {
		t.Fatalf("negative cache must answer the same refusal: %v", err)
	}
	if since := time.Since(start); since > delay/2 || idp.discoveryHits.Load() != 1 {
		t.Fatalf("a failed discovery must not be retried on the next request: took %s, %d attempts", since, idp.discoveryHits.Load())
	}
	// The pause over and Keycloak back: one retry, and it succeeds.
	idp.discoveryDown.Store(false)
	svc.mcp.mu.Lock()
	svc.mcp.discoveryRetryAt = time.Time{}
	svc.mcp.mu.Unlock()
	elapsed, errs = verifyAll(svc)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d after recovery: %v", i, err)
		}
	}
	if d, k := idp.discoveryHits.Load(), idp.jwksHits.Load(); d != 2 || k != 1 {
		t.Fatalf("recovery is one discovery and one JWKS read shared by all: %d discovery, %d jwks", d, k)
	}
	if elapsed > 4*delay {
		t.Fatalf("callers were serialized on the key set: %d of them took %s", n, elapsed)
	}

	// A caller that gives up returns with its own context's error while the
	// round trip goes on; the next caller joins that same round trip.
	fresh := &Service{}
	short, cancel := context.WithTimeout(ctx, delay/4)
	defer cancel()
	start = time.Now()
	_, err = fresh.verifyMCPToken(short, settings, tokens[0], time.Now())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cancelled caller gets its context's error, not the provider's answer: %v", err)
	}
	if since := time.Since(start); since >= delay {
		t.Fatalf("a cancelled caller was held for the whole round trip: %s", since)
	}
	if _, err := fresh.verifyMCPToken(ctx, settings, tokens[1], time.Now()); err != nil {
		t.Fatalf("the round trip the first caller started must serve the next: %v", err)
	}
	if d := idp.discoveryHits.Load(); d != 3 {
		t.Fatalf("the abandoned round trip must be finished and reused, not restarted: %d discovery reads in total", d)
	}
}

// guards: grantedScopes, audienceList
func TestScopesAreTheAdministratorsCeilingNarrowedByTheToken(t *testing.T) {
	ceiling := []string{"mcp:use", "customer:read", "report:read"}
	if got := strings.Join(grantedScopes(ceiling, "openid profile email"), " "); got != "mcp:use customer:read report:read" {
		t.Fatalf("a token without Relio's vocabulary gets the whole ceiling: %q", got)
	}
	if got := strings.Join(grantedScopes(ceiling, "openid customer:read customer:write"), " "); got != "mcp:use customer:read" {
		t.Fatalf("a token that speaks the vocabulary is intersected, never widened, and keeps mcp:use: %q", got)
	}
	if got := strings.Join(grantedScopes(ceiling, "customer:write"), " "); got != "mcp:use customer:read report:read" {
		t.Fatalf("vocabulary outside the ceiling is not a narrowing: %q", got)
	}
	if got := audienceList(json.RawMessage(`"one"`)); len(got) != 1 || got[0] != "one" {
		t.Fatalf("aud as a string: %v", got)
	}
	if got := audienceList(json.RawMessage(`["a","b"]`)); len(got) != 2 {
		t.Fatalf("aud as a list: %v", got)
	}
	if got := audienceList(nil); got != nil {
		t.Fatalf("no aud: %v", got)
	}
}
