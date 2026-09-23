package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/jackc/pgx/v5"
)

// MCP clients such as Qwen Code and OpenCode can sign in with the user's
// organisation account instead of a Personal Key. The MCP authorization
// specification makes Relio an OAuth resource server: it publishes Protected
// Resource Metadata (RFC 9728) naming Keycloak as the authorization server,
// answers 401 with a WWW-Authenticate header pointing at that document, and
// accepts only access tokens Keycloak issued for Relio. Keycloak does the
// login, consent and token issuing; Relio never sees a password.

// MCPOAuth is the resource-server side of that arrangement as currently
// configured. It is derived, not stored: the SSO provider supplies the issuer,
// the service URL supplies the resource, and two settings supply the policy.
type MCPOAuth struct {
	// Enabled is the administrator switch. Available additionally requires a
	// working SSO provider, and is what clients are told.
	Enabled       bool   `json:"enabled"`
	Available     bool   `json:"available"`
	SSOEnabled    bool   `json:"ssoEnabled"`
	RequiredScope string `json:"requiredScope"`
	// PublicClientID is the Keycloak client the administrator registered for
	// MCP agents, shown to users in their client configuration.
	PublicClientID string   `json:"publicClientId"`
	Issuer         string   `json:"issuer,omitempty"`
	ClientID       string   `json:"clientId,omitempty"`
	Resource       string   `json:"resource"`
	MetadataURL    string   `json:"metadataUrl"`
	Audiences      []string `json:"audiences"`
	Scopes         []string `json:"scopes"`
}

func (s *Service) setting(ctx context.Context, namespace, key string, target any) bool {
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE namespace=$1 AND key=$2`, namespace, key).Scan(&raw); err != nil {
		return false
	}
	return json.Unmarshal(raw, target) == nil
}

// MCPOAuth reports the current MCP OAuth configuration.
func (s *Service) MCPOAuth(ctx context.Context) MCPOAuth {
	c, err := s.Get(ctx)
	if err != nil {
		c = Config{}
	}
	return s.mcpOAuthFor(ctx, c)
}

func (s *Service) mcpOAuthFor(ctx context.Context, c Config) MCPOAuth {
	base := s.baseURL(ctx)
	out := MCPOAuth{
		Resource:    base + "/mcp",
		MetadataURL: base + "/.well-known/oauth-protected-resource/mcp",
		SSOEnabled:  c.ID != "" && c.Enabled,
	}
	s.setting(ctx, "mcp", "oauth_enabled", &out.Enabled)
	s.setting(ctx, "mcp", "oauth_required_scope", &out.RequiredScope)
	s.setting(ctx, "mcp", "oauth_client_id", &out.PublicClientID)
	out.RequiredScope = strings.TrimSpace(out.RequiredScope)
	out.PublicClientID = strings.TrimSpace(out.PublicClientID)
	out.Available = out.Enabled && out.SSOEnabled
	// profile and email let a first-time user be provisioned from the token
	// alone, exactly as a first browser login would. openid is deliberately
	// absent: an access token for MCP does not need it (Keycloak supplies sub
	// through its basic scope), and clients copy these scopes into their
	// Dynamic Client Registration request, which Keycloak's Allowed Client
	// Scopes policy rejects outright when it contains openid.
	out.Scopes = []string{"profile", "email"}
	if out.RequiredScope != "" {
		out.Scopes = append(out.Scopes, out.RequiredScope)
	}
	// Accepted audiences, most specific first. RFC 8707 has the client name
	// the MCP endpoint as its resource; Keycloak puts a client id in aud
	// through an Audience mapper. Either proves the token was meant for Relio.
	out.Audiences = []string{out.Resource, base}
	if out.SSOEnabled {
		out.Issuer = strings.TrimRight(c.IssuerURL, "/")
		out.ClientID = c.ClientID
		out.Audiences = append(out.Audiences, c.ClientID)
	}
	return out
}

// ProtectedResourceMetadata is the RFC 9728 document MCP clients fetch to learn
// where to sign in.
func (m MCPOAuth) ProtectedResourceMetadata() map[string]any {
	return map[string]any{
		"resource":                 m.Resource,
		"resource_name":            "Relio CRM MCP",
		"authorization_servers":    []string{m.Issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         m.Scopes,
	}
}

// Challenge is the WWW-Authenticate value for a refused MCP request. With OAuth
// available it carries resource_metadata, which is how a client discovers the
// authorization server; without it, it names only the scheme.
func (m MCPOAuth) Challenge(refused *auth.BearerError) string {
	parts := []string{`realm="Relio MCP"`}
	if m.Available {
		parts = append(parts, fmt.Sprintf(`resource_metadata="%s"`, m.MetadataURL))
		scope := strings.Join(m.Scopes, " ")
		if refused != nil && refused.Scope != "" {
			scope = refused.Scope
		}
		parts = append(parts, fmt.Sprintf(`scope="%s"`, scope))
	}
	if refused != nil && refused.Code != "" {
		parts = append(parts, fmt.Sprintf(`error="%s"`, refused.Code))
		if refused.Description != "" {
			parts = append(parts, fmt.Sprintf(`error_description="%s"`, headerSafe(refused.Description)))
		}
	}
	return "Bearer " + strings.Join(parts, ", ")
}

// headerSafe keeps an error_description within the characters RFC 6750 allows.
func headerSafe(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r >= 0x20 && r <= 0x7e && r != '"' && r != '\\' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func refuse(description, message string) *auth.BearerError {
	return &auth.BearerError{Code: "invalid_token", Description: description, Message: message}
}

// ValidateAccessToken verifies a Keycloak access token presented as a bearer
// credential. For the REST API it keeps the rules that have always applied.
// For MCP it applies the authorization specification: the administrator must
// have enabled it, the token must be an access token issued for Relio, and it
// must carry the required scope when one is configured.
func (s *Service) ValidateAccessToken(ctx context.Context, raw string, forMCP bool) (*auth.AccessToken, error) {
	// Get, not privateConfig: validating a token never needs the client
	// secret, and decrypting it on every request was pure cost.
	c, err := s.Get(ctx)
	if err != nil || c.ID == "" || !c.Enabled {
		return nil, refuse("SSO is not configured on this server", "조직 계정(SSO)이 설정되지 않아 액세스 토큰을 사용할 수 없습니다.")
	}
	var policy MCPOAuth
	if forMCP {
		policy = s.mcpOAuthFor(ctx, c)
		if !policy.Enabled {
			return nil, refuse("OAuth is not enabled for MCP on this server",
				"이 서버는 MCP에서 조직 계정(OAuth) 토큰을 허용하지 않습니다. 관리자가 연동 키 · API · MCP 화면에서 켜기 전에는 개인 연동 키를 사용하세요.")
		}
	}
	client, err := newHTTPClient(c.RootCAPEM)
	if err != nil {
		return nil, err
	}
	d, err := providerKeys.discovery(ctx, client, c)
	if err != nil {
		return nil, refuse("The authorization server is unreachable", "조직 계정 서버(Keycloak)에 연결할 수 없어 토큰을 확인하지 못했습니다.")
	}
	claims, err := verifySigned(ctx, client, d, raw)
	if err != nil {
		return nil, refuse("The access token is invalid or expired", "액세스 토큰이 유효하지 않거나 만료되었습니다. 다시 로그인하세요.")
	}
	allowed := []string{c.ClientID}
	if forMCP {
		// Keycloak marks the token kind in typ. An ID token is addressed to the
		// client that logged in, not to an API, and must not open one.
		switch fmt.Sprint(claims["typ"]) {
		case "ID", "Refresh", "Offline", "Logout":
			return nil, refuse("An ID or refresh token cannot be used as an access token", "ID 토큰이나 Refresh 토큰이 아니라 액세스 토큰을 보내야 합니다.")
		}
		allowed = policy.Audiences
	}
	if !audienceAny(claims["aud"], allowed) {
		return nil, refuse("The access token was not issued for this resource",
			"이 토큰은 Relio를 대상으로 발급되지 않았습니다(aud). Keycloak Client Scope에 Relio Audience Mapper를 추가해야 합니다.")
	}
	scopes := strings.Fields(fmt.Sprint(claims["scope"]))
	if forMCP && policy.RequiredScope != "" && !contains(scopes, policy.RequiredScope) {
		return nil, &auth.BearerError{Code: "insufficient_scope", Description: "The access token lacks the required scope",
			Message: fmt.Sprintf("토큰에 MCP 사용에 필요한 Scope(%s)가 없습니다. 클라이언트가 이 Scope를 요청하도록 다시 로그인하세요.", policy.RequiredScope),
			Scope:   strings.Join(policy.Scopes, " ")}
	}
	subject := strings.TrimSpace(fmt.Sprint(claims["sub"]))
	if subject == "" || subject == "<nil>" {
		return nil, refuse("The access token has no subject", "토큰에 사용자 식별자(sub)가 없습니다.")
	}
	var userID string
	err = s.DB.QueryRow(ctx, `SELECT id FROM users WHERE oidc_subject=$1 AND active=true`, subject).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) && forMCP {
		// A user whose first contact with Relio is through an agent is
		// provisioned the same way a first browser login would do it, under
		// the same auto-provisioning setting and default Role.
		userID, err = s.resolveUser(ctx, c, claims)
	}
	if err != nil {
		return nil, refuse("The user is not provisioned in Relio", "Relio에 등록되지 않은 계정입니다. 먼저 브라우저로 한 번 로그인하거나 관리자에게 계정 생성을 요청하세요.")
	}
	return &auth.AccessToken{UserID: userID, Subject: subject, ClientID: fmt.Sprint(claims["azp"]), Scopes: scopes}, nil
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func audienceAny(aud any, allowed []string) bool {
	for _, want := range allowed {
		if want != "" && (audienceContains(aud, want) || audienceContains(aud, strings.TrimRight(want, "/")+"/")) {
			return true
		}
	}
	return false
}

// providerKeys caches discovery documents and signing keys. Every bearer
// request used to fetch both from Keycloak, which put two network round trips
// in front of each MCP tool call and turned a Keycloak blip into a burst of
// 401s in the middle of an agent's session.
var providerKeys = &keyCache{discoveries: map[string]cachedDiscovery{}, keys: map[string]cachedKeys{}}

const (
	cacheTTL = 15 * time.Minute
	// A token signed with a key we have never seen triggers a refresh, but
	// not more often than this, so garbage tokens cannot hammer Keycloak.
	minKeyRefresh = 30 * time.Second
	// While Keycloak cannot be reached, keys fetched this recently still
	// verify signatures. Rotation leaves old keys valid for longer than this.
	staleGrace = 6 * time.Hour
)

type keyCache struct {
	mu          sync.Mutex
	discoveries map[string]cachedDiscovery
	keys        map[string]cachedKeys
}
type cachedDiscovery struct {
	value Discovery
	at    time.Time
}
type cachedKeys struct {
	value     map[string]*rsa.PublicKey
	at        time.Time
	attempted time.Time
}

// reset drops everything, used when the administrator changes the provider.
func (k *keyCache) reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.discoveries = map[string]cachedDiscovery{}
	k.keys = map[string]cachedKeys{}
}

func (k *keyCache) discovery(ctx context.Context, client *http.Client, c Config) (Discovery, error) {
	id := strings.TrimRight(c.IssuerURL, "/") + "\x00" + c.RootCAPEM
	k.mu.Lock()
	cached, ok := k.discoveries[id]
	k.mu.Unlock()
	if ok && time.Since(cached.at) < cacheTTL {
		return cached.value, nil
	}
	fresh, err := fetchDiscovery(ctx, c)
	if err != nil {
		if ok && time.Since(cached.at) < staleGrace {
			return cached.value, nil
		}
		return Discovery{}, err
	}
	k.mu.Lock()
	k.discoveries[id] = cachedDiscovery{value: fresh, at: time.Now()}
	k.mu.Unlock()
	return fresh, nil
}

func (k *keyCache) key(ctx context.Context, client *http.Client, jwksURI, kid string) (*rsa.PublicKey, error) {
	k.mu.Lock()
	cached, ok := k.keys[jwksURI]
	k.mu.Unlock()
	if ok {
		if key := cached.value[kid]; key != nil && time.Since(cached.at) < cacheTTL {
			return key, nil
		}
		if time.Since(cached.attempted) < minKeyRefresh {
			if key := cached.value[kid]; key != nil {
				return key, nil
			}
			return nil, errors.New("token signing key not found")
		}
	}
	fresh, err := fetchKeys(ctx, client, jwksURI)
	k.mu.Lock()
	if err != nil {
		cached.attempted = time.Now()
		k.keys[jwksURI] = cached
		k.mu.Unlock()
		if key := cached.value[kid]; key != nil && time.Since(cached.at) < staleGrace {
			return key, nil
		}
		return nil, err
	}
	k.keys[jwksURI] = cachedKeys{value: fresh, at: time.Now(), attempted: time.Now()}
	k.mu.Unlock()
	if key := fresh[kid]; key != nil {
		return key, nil
	}
	return nil, errors.New("token signing key not found")
}

func fetchKeys(ctx context.Context, client *http.Client, jwksURI string) (map[string]*rsa.PublicKey, error) {
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
		return nil, fmt.Errorf("JWKS returned HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct{ Kid, Kty, Use, N, E string } `json:"keys"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		// Keycloak also publishes an encryption key; only signing keys verify.
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		nBytes, e1 := base64.RawURLEncoding.DecodeString(k.N)
		eBytes, e2 := base64.RawURLEncoding.DecodeString(k.E)
		if e1 != nil || e2 != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			continue
		}
		padded := make([]byte, 4)
		copy(padded[4-len(eBytes):], eBytes)
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(binary.BigEndian.Uint32(padded))}
	}
	return out, nil
}

// verifySigned checks a compact RS256 JWS and the claims every token must
// satisfy whatever it is for: the issuer and the validity window.
func verifySigned(ctx context.Context, client *http.Client, d Discovery, raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("token is not a JWT")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Alg != "RS256" || header.Kid == "" {
		return nil, errors.New("unsupported token signature")
	}
	pub, err := providerKeys.key(ctx, client, d.JWKSURI, header.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return nil, errors.New("token signature verification failed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	claims := map[string]any{}
	if err = json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if strings.TrimRight(fmt.Sprint(claims["iss"]), "/") != strings.TrimRight(d.Issuer, "/") {
		return nil, errors.New("token issuer mismatch")
	}
	now := time.Now()
	exp, ok := claims["exp"].(float64)
	if !ok || time.Unix(int64(exp), 0).Before(now.Add(-time.Minute)) {
		return nil, errors.New("token expired")
	}
	if nbf, ok := claims["nbf"].(float64); ok && time.Unix(int64(nbf), 0).After(now.Add(time.Minute)) {
		return nil, errors.New("token is not valid yet")
	}
	return claims, nil
}

// MCPOAuthCheck is one line of the administrator's readiness list.
type MCPOAuthCheck struct {
	Key    string `json:"key"`
	Status string `json:"status"` // ok, warn, fail
	Detail string `json:"detail"`
}

// MCPOAuthDiagnostics reports the configuration together with the conditions
// under which a client sign-in will actually succeed. Each of these was a
// separate way for the flow to fail with an unhelpful client-side error.
func (s *Service) MCPOAuthDiagnostics(ctx context.Context) map[string]any {
	c, err := s.Get(ctx)
	if err != nil {
		c = Config{}
	}
	policy := s.mcpOAuthFor(ctx, c)
	checks := []MCPOAuthCheck{}
	add := func(key, status, detail string) { checks = append(checks, MCPOAuthCheck{key, status, detail}) }

	if policy.SSOEnabled {
		add("sso", "ok", "조직 계정(SSO)이 활성화되어 있습니다: "+policy.Issuer)
	} else {
		add("sso", "fail", "SSO 설정에서 Keycloak 연결을 먼저 활성화해야 합니다.")
	}
	base := s.baseURL(ctx)
	if strings.Contains(base, "://localhost") || strings.Contains(base, "://127.0.0.1") {
		add("serviceUrl", "warn", "서비스 URL이 "+base+"입니다. 다른 PC의 MCP 클라이언트는 이 주소로 리소스를 확인하므로, 사용자가 접속하는 실제 주소로 바꿔야 합니다.")
	} else {
		add("serviceUrl", "ok", "리소스 식별자: "+policy.Resource)
	}
	registration := false
	if policy.SSOEnabled {
		client, cerr := newHTTPClient(c.RootCAPEM)
		if cerr == nil {
			if d, derr := providerKeys.discovery(ctx, client, c); derr == nil {
				add("discovery", "ok", "Keycloak 메타데이터를 읽었습니다.")
				registration = d.RegistrationEndpoint != ""
			} else {
				add("discovery", "fail", "Keycloak 메타데이터를 읽을 수 없습니다: "+derr.Error())
			}
		}
	}
	if registration {
		add("registration", "ok", "동적 클라이언트 등록 엔드포인트가 있습니다. 허용 여부는 Keycloak Client Registration 정책(Trusted Hosts)이 결정합니다.")
	} else if policy.SSOEnabled {
		add("registration", "warn", "동적 클라이언트 등록을 쓸 수 없습니다. MCP 클라이언트에 사전 등록한 Public Client ID를 지정하세요.")
	}
	add("audience", "warn", fmt.Sprintf("Keycloak에서 MCP 클라이언트가 받는 토큰의 aud에 %s 또는 %s가 들어가도록 Audience Mapper를 설정해야 합니다.", policy.ClientID, policy.Resource))
	if policy.RequiredScope != "" {
		add("scope", "ok", "필수 Scope: "+policy.RequiredScope+" — 이 Client Scope에 Audience Mapper를 두면 두 조건을 한 번에 충족합니다.")
	}
	return map[string]any{"config": policy, "checks": checks, "registrationSupported": registration}
}
