package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// MCP 를 SSO 로 — 개인 키 없이, Keycloak 이 발급한 액세스 토큰으로.
//
// The MCP authorization specification (2025-06-18 and later) is OAuth 2.1: the
// MCP server is a *resource server* that says where its authorization server
// is (RFC 9728, /.well-known/oauth-protected-resource), and a client refused
// with 401 reads that document, sends the person through Keycloak with PKCE
// and comes back with an access token whose audience (RFC 8707) is this
// server. Nothing about issuing tokens happens here — Keycloak does that.
// This file answers two questions: where is the authorization server, and is
// this token one it issued for us, for a person Relio already knows.
//
// The Personal Key stays. It is what an automation with no person behind it
// uses, and what a deployment without Keycloak uses. A token is a second door
// into the same room: it authenticates an *existing* account, carries the
// scopes the administrator chose, and passes the gate a key passes. It never
// creates an account — signing in to the web once is what registers one.

// MCPOAuth is the resolved configuration of the MCP resource server.
type MCPOAuth struct {
	// Enabled is mcp.oauth.enabled. Off by default.
	Enabled bool
	// MCPEnabled is mcp.enabled: with the endpoint itself switched off there
	// is nothing to advertise.
	MCPEnabled bool
	// Resource is the identifier this deployment claims for /mcp (RFC 8707):
	// mcp.oauth.resource, or <system.service_url>/mcp when that is empty. It
	// is what the metadata advertises and what a token's aud must name.
	Resource string
	// Audiences is mcp.oauth.audience: values an administrator accepts in aud
	// or azp besides the resource, so a Keycloak client works without an
	// Audience mapper.
	Audiences []string
	// Scopes is mcp.oauth.scopes: the ceiling an SSO subject receives. A token
	// does not carry Relio's scope vocabulary unless somebody teaches Keycloak
	// that vocabulary, and asking every deployment to do so before MCP works
	// is the wrong trade; the administrator states the ceiling once instead.
	Scopes []string
	// Issuer and the claim names are the web sign-in's (oidc_providers).
	Issuer        string
	ClientID      string
	UsernameClaim string
	RootCAPEM     string
}

// mcpOAuthDefaultScopes is the seed of mcp.oauth.scopes and the fallback when
// the setting is emptied: every read permission a Personal Key can carry.
var mcpOAuthDefaultScopes = []string{"mcp:use", "customer:read", "contact:read", "lead:read", "opportunity:read", "activity:read", "product:read", "quotation:read", "contract:read", "sales:read", "target:read", "forecast:read", "notification:read", "report:read", "voice:read", "intelligence:read"}

// mcpResourcePath is where the MCP endpoint is served. The transport is not
// this feature's business; only the identifier is derived from it.
const mcpResourcePath = "/mcp"

// MCPOAuthSettings reads the resource-server configuration: four mcp.oauth.*
// rows, the MCP switch, the public address and the SSO provider.
func (s *Service) MCPOAuthSettings(ctx context.Context) MCPOAuth {
	values := map[string]string{}
	rows, err := s.DB.Query(ctx, `SELECT namespace,key,value FROM system_settings WHERE (namespace='mcp' AND key IN ('enabled','oauth.enabled','oauth.resource','oauth.audience','oauth.scopes')) OR (namespace='system' AND key='service_url')`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var namespace, key string
			var raw []byte
			if rows.Scan(&namespace, &key, &raw) == nil {
				values[namespace+"."+key] = settingText(raw)
			}
		}
	}
	provider, _ := s.Get(ctx)
	return resolveMCPOAuth(values, provider)
}

// settingText renders a JSON setting value as the text an operator typed:
// strings unquoted, everything else as its JSON.
func settingText(raw []byte) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return strings.TrimSpace(string(raw))
}

// resolveMCPOAuth is the pure half of MCPOAuthSettings: values keyed as
// "namespace.key" plus the provider row, with every default applied.
func resolveMCPOAuth(values map[string]string, provider Config) MCPOAuth {
	setting := func(key, fallback string) string {
		if value, ok := values[key]; ok {
			return strings.TrimSpace(value)
		}
		return fallback
	}
	o := MCPOAuth{
		Enabled:       setting("mcp.oauth.enabled", "false") == "true",
		MCPEnabled:    setting("mcp.enabled", "true") != "false",
		Audiences:     strings.Fields(setting("mcp.oauth.audience", "")),
		Scopes:        strings.Fields(setting("mcp.oauth.scopes", "")),
		Issuer:        strings.TrimRight(strings.TrimSpace(provider.IssuerURL), "/"),
		ClientID:      provider.ClientID,
		UsernameClaim: provider.UsernameClaim,
		RootCAPEM:     provider.RootCAPEM,
	}
	if o.UsernameClaim == "" {
		o.UsernameClaim = "preferred_username"
	}
	if len(o.Scopes) == 0 {
		o.Scopes = slices.Clone(mcpOAuthDefaultScopes)
	}
	// MCP needs mcp:use whatever else the administrator listed; a Personal Key
	// for the MCP channel is held to the same rule when it is issued.
	if !slices.Contains(o.Scopes, "mcp:use") {
		o.Scopes = append([]string{"mcp:use"}, o.Scopes...)
	}
	resource := setting("mcp.oauth.resource", "")
	if resource == "" {
		if base := strings.TrimRight(setting("system.service_url", ""), "/"); base != "" {
			resource = base + mcpResourcePath
		}
	}
	if validResource(resource) {
		o.Resource = strings.TrimRight(resource, "/")
	}
	return o
}

// validResource accepts an absolute http(s) URL with a host and nothing a
// client would not send back: no query, fragment or credentials.
func validResource(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

// Active reports whether the resource server is switched on and has what it
// needs; the reason explains an Enabled configuration that still cannot run,
// so the administrator's switch is never silently a no-op.
func (o MCPOAuth) Active() (bool, string) {
	switch {
	case !o.Enabled:
		return false, "mcp.oauth.enabled is off"
	case o.Issuer == "":
		return false, "no SSO issuer: configure 사내 SSO 연결 first"
	case o.Resource == "":
		return false, "no resource identifier: set mcp.oauth.resource or system.service_url to an absolute http(s) URL"
	case !o.MCPEnabled:
		return false, "mcp.enabled is off"
	}
	return true, ""
}

// MetadataURL is where a refused client is sent to learn the above (RFC 9728
// puts the well-known segment between the host and the resource path).
func (o MCPOAuth) MetadataURL() string {
	u, err := url.Parse(o.Resource)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + u.Path
}

// Document is the protected resource metadata. Public by design — it says
// where to sign in, not who is signed in — and bare JSON, not the product's
// envelope, because the reader is an OAuth client library.
func (o MCPOAuth) Document() map[string]any {
	return map[string]any{
		"resource":                 o.Resource,
		"authorization_servers":    []string{o.Issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         o.Scopes,
		"resource_name":            "Relio CRM MCP",
	}
}

// Challenge is the WWW-Authenticate value that turns a 401 on /mcp into an
// invitation. Without resource_metadata a refusal is a dead end; with it the
// client starts the OAuth flow by itself. refused says a token was presented
// and turned down, which RFC 6750 spells error="invalid_token"; the reason
// itself goes in the JSON body, where it may be Korean.
func (o MCPOAuth) Challenge(refused bool) string {
	header := `Bearer realm="Relio MCP"`
	if active, _ := o.Active(); active {
		header += fmt.Sprintf(`, resource_metadata=%q`, o.MetadataURL())
	}
	if refused {
		header += `, error="invalid_token"`
	}
	return header
}

// mcpKeyCache holds discovery and the JWKS per issuer. Both are network round
// trips to Keycloak; doing them on every MCP call would put Keycloak's latency
// in front of every tool. Discovery is re-read after a while, the key set on
// an unknown key id — that is how rotation is picked up — but at most once a
// second, so a stream of forged tokens cannot turn this server into a JWKS
// client that hammers the identity provider.
//
// The mutex guards the fields only; no network call runs under it. A round
// trip in progress is a *fetch* that every caller needing the same thing
// waits on, so a Keycloak that is slow or down costs one timeout shared by
// all concurrent requests, never one timeout per request in a queue behind
// the lock. A failed discovery is remembered for a short while (the negative
// cache) so that a stream of requests during an outage does not retry it
// each time.
type mcpKeyCache struct {
	mu           sync.Mutex
	issuer       string
	discovery    Discovery
	discoveredAt time.Time
	// discoveryRetryAt and discoveryErr are the negative cache: until the
	// time passes, a stale or missing discovery is not re-read and, when
	// nothing older is cached, the error is answered as it was.
	discoveryRetryAt time.Time
	discoveryErr     error
	discoveryFetch   *fetch
	keys             map[string]signingKey
	nextKeyFetch     time.Time
	keyFetch         *fetch
	discoveryTTL     time.Duration
	discoveryRetry   time.Duration
	keyFetchPause    time.Duration
}

// fetch is one round trip to Keycloak in progress. It is started under the
// lock (so a second caller finds it rather than starting its own), runs in a
// goroutine detached from the starting request's cancellation (so a client
// that gives up does not fail everyone waiting behind it), and publishes its
// result by closing done. Waiters select on done and their own ctx.
type fetch struct {
	done      chan struct{}
	discovery Discovery
	keys      map[string]signingKey
	err       error
}

const (
	mcpDiscoveryTTL   = 10 * time.Minute
	mcpDiscoveryRetry = 30 * time.Second
	mcpKeyFetchPause  = time.Second
)

// init applies the defaults; called with the lock held.
func (c *mcpKeyCache) init() {
	if c.discoveryTTL == 0 {
		c.discoveryTTL, c.discoveryRetry, c.keyFetchPause = mcpDiscoveryTTL, mcpDiscoveryRetry, mcpKeyFetchPause
	}
}

// await returns when the fetch is done or the caller's context is over,
// whichever comes first.
func (f *fetch) await(ctx context.Context) error {
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// signingKey is one JWKS entry this server can verify with.
type signingKey struct {
	kid string
	alg string
	rsa *rsa.PublicKey
	ec  *ecdsa.PublicKey
}

func (s *Service) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// AuthenticateMCPToken is the validator the auth service consults for a
// JWT-shaped bearer on /mcp. Every refusal is logged here with its cause —
// the message a client sees names the check, the log names the failure — so
// "look at the server log" in the administrator guide is a promise the code
// keeps. The token itself is never logged. The request id is what ties the
// line to the 401 the client saw (the response carries the same id).
func (s *Service) AuthenticateMCPToken(ctx context.Context, raw string) (auth.OAuthGrant, error) {
	grant, err := s.authenticateMCPToken(ctx, raw)
	if err != nil {
		attrs := []any{"request_id", requestIDForLog(ctx)}
		var refusal *auth.Refusal
		if errors.As(err, &refusal) && refusal.Cause != nil {
			// message is what the client was told; detail is the verifier's
			// own words (which signature, issuer, exp or nbf check failed).
			attrs = append(attrs, "message", refusal.Message, "detail", refusal.Cause)
		} else {
			attrs = append(attrs, "error", err)
		}
		s.logger().Warn("mcp oauth token refused", attrs...)
	}
	return grant, err
}

// requestIDForLog is the request id the HTTP middleware put in ctx. A caller
// without one (a test, a future non-HTTP path) is not hidden behind an empty
// value: the line says the id is missing.
func requestIDForLog(ctx context.Context) string {
	if id := httpx.RequestID(ctx); id != "" {
		return id
	}
	return "missing"
}

// mcpOAuthSettings and mcpAccount are the two database reads on the token
// path, so a test can run AuthenticateMCPToken end to end — the refusal log
// included — without a database.
func (s *Service) mcpOAuthSettings(ctx context.Context) MCPOAuth {
	if s.mcpSettings != nil {
		return s.mcpSettings(ctx)
	}
	return s.MCPOAuthSettings(ctx)
}

func (s *Service) mcpAccount(ctx context.Context, subject string) (string, error) {
	if s.mcpAccountLookup != nil {
		return s.mcpAccountLookup(ctx, subject)
	}
	// The account the web sign-in registered, and nothing else: no
	// provisioning, no reactivation, no role from the token.
	var userID string
	err := s.DB.QueryRow(ctx, `SELECT id FROM users WHERE oidc_subject=$1 AND active=true`, subject).Scan(&userID)
	return userID, err
}

func (s *Service) authenticateMCPToken(ctx context.Context, raw string) (auth.OAuthGrant, error) {
	settings := s.mcpOAuthSettings(ctx)
	// A switched-off deployment refuses exactly as it did before this feature
	// existed: a plain error, the generic 401, no new words about SSO.
	if !settings.Enabled {
		return auth.OAuthGrant{}, errors.New("sso access tokens are not accepted: mcp.oauth.enabled is off")
	}
	if active, reason := settings.Active(); !active {
		return auth.OAuthGrant{}, errors.New("mcp oauth is enabled but cannot run: " + reason)
	}
	claims, err := s.verifyMCPToken(ctx, settings, raw, time.Now())
	if err != nil {
		return auth.OAuthGrant{}, err
	}
	userID, err := s.mcpAccount(ctx, claims.Subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.OAuthGrant{}, &auth.Refusal{
			Message: "이 SSO 계정은 Relio 에 등록되지 않았거나 비활성입니다. 먼저 웹으로 한 번 로그인하세요.",
			Cause:   fmt.Errorf("no active relio account for sso subject %q (%s=%q)", claims.Subject, settings.UsernameClaim, claims.Username),
		}
	}
	if err != nil {
		return auth.OAuthGrant{}, err
	}
	return auth.OAuthGrant{UserID: userID, Scopes: grantedScopes(settings.Scopes, claims.Scope)}, nil
}

// grantedScopes is the administrator's ceiling, narrowed by the token's own
// scope claim when that claim speaks Relio's vocabulary. Keycloak's usual
// "openid profile email" does not, and then the ceiling applies whole.
func grantedScopes(allowed []string, tokenScope string) []string {
	requested := strings.Fields(tokenScope)
	var narrowed []string
	for _, scope := range allowed {
		if slices.Contains(requested, scope) {
			narrowed = append(narrowed, scope)
		}
	}
	if len(narrowed) == 0 {
		return slices.Clone(allowed)
	}
	if !slices.Contains(narrowed, "mcp:use") {
		narrowed = append([]string{"mcp:use"}, narrowed...)
	}
	return narrowed
}

// mcpClaims is what this server reads out of a verified access token.
type mcpClaims struct {
	Subject  string
	Username string
	Scope    string
}

// mcpSigningAlgs are the signatures accepted: asymmetric only. An HS* token
// would be "signed" with a secret this server does not have, and "none" is
// not a signature at all.
var mcpSigningAlgs = map[string]crypto.Hash{
	"RS256": crypto.SHA256, "RS384": crypto.SHA384, "RS512": crypto.SHA512,
	"PS256": crypto.SHA256, "PS384": crypto.SHA384, "PS512": crypto.SHA512,
	"ES256": crypto.SHA256, "ES384": crypto.SHA384, "ES512": crypto.SHA512,
}

// clockSkew is how far the token's clock may differ from ours.
const clockSkew = time.Minute

// verifyMCPToken checks everything that does not need the database: the
// signature against the issuer's key set, the issuer, the validity window,
// the token type, the absence of a holder-of-key binding, the subject and —
// the check that keeps another application's token out of this one — the
// audience.
func (s *Service) verifyMCPToken(ctx context.Context, settings MCPOAuth, raw string, now time.Time) (mcpClaims, error) {
	refuse := func(message string, cause error) (mcpClaims, error) {
		return mcpClaims{}, &auth.Refusal{Message: message, Cause: cause}
	}
	const invalid = "SSO 액세스 토큰이 유효하지 않습니다(서명·발급자·만료). 클라이언트에서 다시 로그인하세요."
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > 32<<10 {
		return refuse(invalid, errors.New("token is not a compact jws"))
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return refuse(invalid, fmt.Errorf("token header: %w", err))
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return refuse(invalid, fmt.Errorf("token header: %w", err))
	}
	hash, ok := mcpSigningAlgs[header.Alg]
	if !ok {
		return refuse(invalid, fmt.Errorf("token alg %q is not an accepted asymmetric signature", header.Alg))
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return refuse(invalid, fmt.Errorf("token payload: %w", err))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return refuse(invalid, fmt.Errorf("token signature: %w", err))
	}
	key, err := s.mcpSigningKey(ctx, settings, header.Kid, header.Alg)
	if err != nil {
		var refusal *auth.Refusal
		if errors.As(err, &refusal) {
			return mcpClaims{}, err
		}
		return refuse(invalid, err)
	}
	if err := verifySignature(key, header.Alg, hash, []byte(parts[0]+"."+parts[1]), signature); err != nil {
		return refuse(invalid, err)
	}
	var payload struct {
		Issuer       string          `json:"iss"`
		Subject      string          `json:"sub"`
		Audience     json.RawMessage `json:"aud"`
		AuthorizedTo string          `json:"azp"`
		Expires      *int64          `json:"exp"`
		NotBefore    *int64          `json:"nbf"`
		Type         string          `json:"typ"`
		Confirmation json.RawMessage `json:"cnf"`
		Scope        string          `json:"scope"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return refuse(invalid, fmt.Errorf("token claims: %w", err))
	}
	if strings.TrimRight(payload.Issuer, "/") != settings.Issuer {
		return refuse(invalid, fmt.Errorf("token iss %q is not the configured issuer %q", payload.Issuer, settings.Issuer))
	}
	if payload.Expires == nil || now.After(time.Unix(*payload.Expires, 0).Add(clockSkew)) {
		return refuse("SSO 액세스 토큰이 만료되었습니다. 클라이언트에서 다시 로그인하세요.", errors.New("token exp is missing or in the past"))
	}
	if payload.NotBefore != nil && now.Add(clockSkew).Before(time.Unix(*payload.NotBefore, 0)) {
		return refuse(invalid, errors.New("token nbf is in the future"))
	}
	// An ID token is proof of a sign-in, not an API credential. Keycloak marks
	// its access tokens typ=Bearer and its ID tokens typ=ID in the payload.
	if strings.EqualFold(payload.Type, "ID") || strings.EqualFold(header.Typ, "ID") {
		return refuse("ID 토큰은 MCP 자격으로 쓸 수 없습니다. 액세스 토큰을 보내세요.", errors.New("token typ is ID"))
	}
	// cnf binds the token to a key (DPoP, mTLS) this server cannot verify, so
	// the binding would be silently ignored — refuse instead.
	if len(payload.Confirmation) > 0 && string(payload.Confirmation) != "null" {
		return refuse("소지자 증명(cnf)이 묶인 토큰은 받지 않습니다. 일반 Bearer 액세스 토큰을 보내세요.", errors.New("token carries a cnf claim"))
	}
	subject := strings.TrimSpace(payload.Subject)
	if subject == "" {
		return refuse("SSO 토큰에 사용자 식별 정보(sub)가 없습니다.", errors.New("token sub is empty"))
	}
	// Whom the token was minted for. A real Keycloak 26 puts the client in
	// azp and only "account" in aud — the client id is *not* in aud, whatever
	// an ID token does. So the binding is "aud names us, or aud/azp names a
	// client the administrator trusts". Either is the token being for this
	// deployment rather than passed through from another application in the
	// realm, which is what RFC 8707 and the MCP specification guard against.
	audience := audienceList(payload.Audience)
	if !audienceAccepted(settings, audience, payload.AuthorizedTo) {
		return refuse(fmt.Sprintf("SSO 토큰이 이 서버를 위해 발급된 것이 아닙니다(aud %v, azp %q). 관리자가 MCP SSO 허용 대상(mcp.oauth.audience)에 %q 를 적거나, Keycloak 클라이언트에 Audience 매퍼로 %q 를 더해야 합니다.",
			audience, payload.AuthorizedTo, payload.AuthorizedTo, settings.Resource),
			fmt.Errorf("token aud %v / azp %q is neither the resource %q nor an accepted audience %v", audience, payload.AuthorizedTo, settings.Resource, settings.Audiences))
	}
	var all map[string]any
	_ = json.Unmarshal(payloadBytes, &all)
	username := strings.TrimSpace(fmt.Sprint(claim(all, settings.UsernameClaim)))
	if username == "<nil>" {
		username = ""
	}
	return mcpClaims{Subject: subject, Username: username, Scope: payload.Scope}, nil
}

// audienceList reads aud, which RFC 7519 allows as a string or an array.
func audienceList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

func audienceAccepted(settings MCPOAuth, audience []string, azp string) bool {
	same := func(a, b string) bool { return strings.TrimRight(a, "/") == strings.TrimRight(b, "/") }
	for _, value := range audience {
		if value == "" {
			continue
		}
		if same(value, settings.Resource) || slices.ContainsFunc(settings.Audiences, func(accepted string) bool { return same(value, accepted) }) {
			return true
		}
	}
	return azp != "" && slices.ContainsFunc(settings.Audiences, func(accepted string) bool { return same(azp, accepted) })
}

// mcpSigningKey finds the key a token names, reading discovery and the JWKS
// through the cache. The lock is taken only to read and write the cache;
// the round trips happen in a fetch that concurrent callers share.
func (s *Service) mcpSigningKey(ctx context.Context, settings MCPOAuth, kid, alg string) (signingKey, error) {
	client, err := newHTTPClient(settings.RootCAPEM)
	if err != nil {
		return signingKey{}, err
	}
	discovery, err := s.mcpDiscovery(ctx, settings)
	if err != nil {
		return signingKey{}, err
	}
	return s.mcpKey(ctx, client, discovery.JWKSURI, kid, alg)
}

// mcpDiscovery answers the issuer's discovery document from the cache, or
// reads it — once, however many callers arrive at the same moment. A failed
// read is remembered for discoveryRetry: with an older document in the cache
// that document keeps serving (a transient outage must not sign every MCP
// client out); without one the refusal is answered from memory until the
// retry time, so an unreachable Keycloak costs one timeout per retry window
// rather than one per request.
func (s *Service) mcpDiscovery(ctx context.Context, settings MCPOAuth) (Discovery, error) {
	c := &s.mcp
	c.mu.Lock()
	c.init()
	if c.issuer != settings.Issuer {
		// A changed issuer invalidates everything cached for the old one. A
		// fetch in flight for the old issuer finishes into the void: it
		// checks the issuer before writing.
		c.issuer, c.discovery, c.discoveredAt, c.discoveryRetryAt, c.discoveryErr, c.keys, c.nextKeyFetch = settings.Issuer, Discovery{}, time.Time{}, time.Time{}, nil, nil, time.Time{}
		c.discoveryFetch, c.keyFetch = nil, nil
	}
	now := time.Now()
	fresh := !c.discoveredAt.IsZero() && now.Sub(c.discoveredAt) <= c.discoveryTTL
	if fresh || now.Before(c.discoveryRetryAt) {
		// Served from the cache: a fresh document, a stale one during the
		// retry pause, or the failure that left nothing to serve.
		discovery, err := c.discovery, c.discoveryErr
		if !c.discoveredAt.IsZero() {
			err = nil
		}
		c.mu.Unlock()
		return discovery, err
	}
	f := c.discoveryFetch
	if f == nil {
		f = &fetch{done: make(chan struct{})}
		c.discoveryFetch = f
		issuer := settings.Issuer
		go func() {
			// Detached from the caller's cancellation (the document is for
			// everyone waiting); the HTTP client's own timeout still bounds it.
			discovery, err := fetchDiscovery(context.WithoutCancel(ctx), Config{IssuerURL: issuer, RootCAPEM: settings.RootCAPEM})
			foreign := false
			if err != nil {
				err = &auth.Refusal{Message: "Keycloak 발급자 정보를 읽지 못해 SSO 토큰을 확인할 수 없습니다. 잠시 후 다시 시도하거나 관리자에게 알리세요.", Cause: fmt.Errorf("discovery %s: %w", issuer, err)}
			} else if originErr := sameOrigin(issuer, discovery.JWKSURI); originErr != nil {
				foreign = true
				err = &auth.Refusal{Message: "Keycloak 의 jwks_uri 가 발급자와 다른 서버를 가리켜 SSO 토큰을 확인할 수 없습니다. 관리자에게 알리세요.", Cause: originErr}
			}
			c.mu.Lock()
			if c.issuer == issuer {
				c.discoveryFetch = nil
				switch {
				case err == nil:
					c.discovery, c.discoveredAt, c.discoveryErr = discovery, time.Now(), nil
				case foreign:
					// The issuer now points at a key set this server will not
					// trust: nothing cached from before is trusted either.
					c.discovery, c.discoveredAt, c.keys = Discovery{}, time.Time{}, nil
					fallthrough
				default:
					c.discoveryRetryAt, c.discoveryErr = time.Now().Add(c.discoveryRetry), err
				}
			}
			c.mu.Unlock()
			f.discovery, f.err = discovery, err
			close(f.done)
		}()
	}
	c.mu.Unlock()
	err := f.await(ctx)
	if err == nil {
		return f.discovery, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() == nil && !c.discoveredAt.IsZero() {
		// A refresh failed but an older document is still on hand.
		return c.discovery, nil
	}
	return Discovery{}, err
}

// mcpKey looks the key id up in the cached set and, when it is unknown,
// re-reads the set — the realm may have rotated — but not more than once a
// second however many unknown ids arrive, and only once for however many
// callers are waiting on the same read.
func (s *Service) mcpKey(ctx context.Context, client *http.Client, jwksURI, kid, alg string) (signingKey, error) {
	c := &s.mcp
	c.mu.Lock()
	if key, ok := c.keys[kid]; ok {
		c.mu.Unlock()
		return keyForAlg(key, alg)
	}
	f := c.keyFetch
	if f == nil {
		now := time.Now()
		if now.Before(c.nextKeyFetch) {
			pause := c.keyFetchPause
			c.mu.Unlock()
			return signingKey{}, fmt.Errorf("token kid %q is not in the cached key set and the set was refreshed less than %s ago", kid, pause)
		}
		c.nextKeyFetch = now.Add(c.keyFetchPause)
		f = &fetch{done: make(chan struct{})}
		c.keyFetch = f
		issuer := c.issuer
		go func() {
			keys, err := fetchSigningKeys(context.WithoutCancel(ctx), client, jwksURI)
			if err != nil {
				err = &auth.Refusal{Message: "Keycloak 서명 키를 읽지 못해 SSO 토큰을 확인할 수 없습니다. 잠시 후 다시 시도하거나 관리자에게 알리세요.", Cause: fmt.Errorf("jwks %s: %w", jwksURI, err)}
			}
			c.mu.Lock()
			if c.issuer == issuer {
				if err == nil {
					c.keys = keys
				}
				c.keyFetch = nil
			}
			c.mu.Unlock()
			f.keys, f.err = keys, err
			close(f.done)
		}()
	}
	c.mu.Unlock()
	if err := f.await(ctx); err != nil {
		return signingKey{}, err
	}
	if key, ok := f.keys[kid]; ok {
		return keyForAlg(key, alg)
	}
	return signingKey{}, fmt.Errorf("token kid %q is not in the issuer's key set", kid)
}

// keyForAlg refuses a key the JWKS dedicated to another algorithm: a token
// may not pick a different signature scheme for a key than its owner did.
func keyForAlg(key signingKey, alg string) (signingKey, error) {
	if key.alg != "" && key.alg != alg {
		return signingKey{}, fmt.Errorf("token alg %s but key %q is published for %s", alg, key.kid, key.alg)
	}
	return key, nil
}

// sameOrigin refuses a jwks_uri on another scheme, host or port than the
// issuer: the key set is the root of trust and must come from the server the
// administrator named, not from wherever a discovery document points.
func sameOrigin(issuer, jwksURI string) error {
	a, err := url.Parse(issuer)
	if err != nil {
		return err
	}
	b, err := url.Parse(jwksURI)
	if err != nil {
		return err
	}
	if a.Scheme != b.Scheme || a.Host != b.Host {
		return fmt.Errorf("jwks_uri %q is not on the issuer's origin %s://%s", jwksURI, a.Scheme, a.Host)
	}
	return nil
}

// fetchSigningKeys reads a JWKS and keeps the RSA and EC keys in it.
func fetchSigningKeys(ctx context.Context, client *http.Client, jwksURI string) (map[string]signingKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks returned HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kid, Kty, Alg, Use, N, E, Crv, X, Y string
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]signingKey{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		key := signingKey{kid: k.Kid, alg: k.Alg}
		switch k.Kty {
		case "RSA":
			nBytes, e1 := base64.RawURLEncoding.DecodeString(k.N)
			eBytes, e2 := base64.RawURLEncoding.DecodeString(k.E)
			if e1 != nil || e2 != nil || len(eBytes) == 0 || len(eBytes) > 4 {
				continue
			}
			padded := make([]byte, 4)
			copy(padded[4-len(eBytes):], eBytes)
			key.rsa = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(binary.BigEndian.Uint32(padded))}
		case "EC":
			curve, ok := map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()}[k.Crv]
			if !ok {
				continue
			}
			xBytes, e1 := base64.RawURLEncoding.DecodeString(k.X)
			yBytes, e2 := base64.RawURLEncoding.DecodeString(k.Y)
			if e1 != nil || e2 != nil {
				continue
			}
			x, y := new(big.Int).SetBytes(xBytes), new(big.Int).SetBytes(yBytes)
			if !curve.IsOnCurve(x, y) {
				continue
			}
			key.ec = &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		default:
			continue
		}
		keys[k.Kid] = key
	}
	return keys, nil
}

// verifySignature checks a JWS signature with the key the JWKS provided. The
// header's alg must fit the key's type: an RSA key cannot vouch for an ES
// signature and vice versa.
func verifySignature(key signingKey, alg string, hash crypto.Hash, signingInput, signature []byte) error {
	digest := hashOf(hash, signingInput)
	switch {
	case strings.HasPrefix(alg, "RS"):
		if key.rsa == nil {
			return fmt.Errorf("token alg %s but key %q is not RSA", alg, key.kid)
		}
		if err := rsa.VerifyPKCS1v15(key.rsa, hash, digest, signature); err != nil {
			return errors.New("token signature verification failed")
		}
	case strings.HasPrefix(alg, "PS"):
		if key.rsa == nil {
			return fmt.Errorf("token alg %s but key %q is not RSA", alg, key.kid)
		}
		if err := rsa.VerifyPSS(key.rsa, hash, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}); err != nil {
			return errors.New("token signature verification failed")
		}
	case strings.HasPrefix(alg, "ES"):
		if key.ec == nil {
			return fmt.Errorf("token alg %s but key %q is not EC", alg, key.kid)
		}
		// JWS encodes an ECDSA signature as r||s of fixed width, not DER.
		size := (key.ec.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*size {
			return errors.New("token signature verification failed")
		}
		r, s := new(big.Int).SetBytes(signature[:size]), new(big.Int).SetBytes(signature[size:])
		if !ecdsa.Verify(key.ec, digest, r, s) {
			return errors.New("token signature verification failed")
		}
	default:
		return fmt.Errorf("token alg %q is not accepted", alg)
	}
	return nil
}

func hashOf(hash crypto.Hash, data []byte) []byte {
	switch hash {
	case crypto.SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		sum := sha256.Sum256(data)
		return sum[:]
	}
}
