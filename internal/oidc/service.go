package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
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
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint,omitempty"`
}

// Config is the stored provider. AutoLogin lets the browser try prompt=none
// before it shows the login screen, so a visitor with a live Keycloak session
// enters directly; it is off unless the administrator turns it on.
type Config struct {
	ID                     string     `json:"id,omitempty"`
	Version                int        `json:"version"`
	Enabled                bool       `json:"enabled"`
	IssuerURL              string     `json:"issuerUrl"`
	ClientID               string     `json:"clientId"`
	ClientSecret           string     `json:"clientSecret,omitempty"`
	ClientSecretConfigured bool       `json:"clientSecretConfigured"`
	Scopes                 []string   `json:"scopes"`
	UsernameClaim          string     `json:"usernameClaim"`
	EmailClaim             string     `json:"emailClaim"`
	NameClaim              string     `json:"nameClaim"`
	GroupClaim             string     `json:"groupClaim"`
	RoleClaim              string     `json:"roleClaim"`
	AutoProvision          bool       `json:"autoProvision"`
	AutoLogin              bool       `json:"autoLogin"`
	DefaultRoleID          string     `json:"defaultRoleId,omitempty"`
	RootCAPEM              string     `json:"rootCaPem,omitempty"`
	CallbackURL            string     `json:"callbackUrl"`
	Discovery              *Discovery `json:"discovery,omitempty"`
	LastTestedAt           *time.Time `json:"lastTestedAt,omitempty"`
	LastTestResult         any        `json:"lastTestResult,omitempty"`
}
type TestResult struct {
	Success     bool              `json:"success"`
	Checks      map[string]string `json:"checks"`
	Discovery   *Discovery        `json:"discovery,omitempty"`
	CallbackURL string            `json:"callbackUrl"`
	TestedAt    time.Time         `json:"testedAt"`
}
type Service struct {
	DB      *pgxpool.Pool
	Secrets *secrets.Manager
	Auth    *auth.Service
	Audit   *audit.Service
	Log     *slog.Logger
	// mcp caches Keycloak discovery and signing keys for the MCP resource
	// server (mcp_oauth.go). Zero value is ready to use.
	mcp mcpKeyCache
}

func defaults(c *Config) {
	if len(c.Scopes) == 0 {
		c.Scopes = []string{"openid", "profile", "email"}
	}
	if c.UsernameClaim == "" {
		c.UsernameClaim = "preferred_username"
	}
	if c.EmailClaim == "" {
		c.EmailClaim = "email"
	}
	if c.NameClaim == "" {
		c.NameClaim = "name"
	}
	if c.GroupClaim == "" {
		c.GroupClaim = "groups"
	}
	if c.RoleClaim == "" {
		c.RoleClaim = "realm_access.roles"
	}
}
func (s *Service) baseURL(ctx context.Context) string {
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE namespace='system' AND key='service_url'`).Scan(&raw); err == nil {
		var v string
		if json.Unmarshal(raw, &v) == nil && v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return "http://localhost:8080"
}
func (s *Service) callbackURL(ctx context.Context) string {
	return s.baseURL(ctx) + "/api/v1/auth/oidc/callback"
}
func (s *Service) Get(ctx context.Context) (Config, error) {
	var c Config
	var secret string
	var scopes []string
	var role *string
	var discovery, result []byte
	err := s.DB.QueryRow(ctx, `SELECT id,version,enabled,issuer_url,client_id,client_secret_encrypted,scopes,username_claim,email_claim,name_claim,group_claim,role_claim,auto_provision,auto_login,default_role_id,COALESCE(root_ca_pem,''),discovery,last_tested_at,last_test_result FROM oidc_providers ORDER BY created_at LIMIT 1`).Scan(&c.ID, &c.Version, &c.Enabled, &c.IssuerURL, &c.ClientID, &secret, &scopes, &c.UsernameClaim, &c.EmailClaim, &c.NameClaim, &c.GroupClaim, &c.RoleClaim, &c.AutoProvision, &c.AutoLogin, &role, &c.RootCAPEM, &discovery, &c.LastTestedAt, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		c.CallbackURL = s.callbackURL(ctx)
		defaults(&c)
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.Scopes = scopes
	c.ClientSecretConfigured = secret != ""
	if role != nil {
		c.DefaultRoleID = *role
	}
	if len(discovery) > 0 && string(discovery) != "null" {
		_ = json.Unmarshal(discovery, &c.Discovery)
	}
	if len(result) > 0 && string(result) != "null" {
		_ = json.Unmarshal(result, &c.LastTestResult)
	}
	c.CallbackURL = s.callbackURL(ctx)
	return c, nil
}
func (s *Service) privateConfig(ctx context.Context) (Config, error) {
	c, err := s.Get(ctx)
	if err != nil {
		return c, err
	}
	var encrypted string
	if err = s.DB.QueryRow(ctx, `SELECT client_secret_encrypted FROM oidc_providers WHERE id=$1`, c.ID).Scan(&encrypted); err != nil {
		return c, err
	}
	c.ClientSecret, err = s.Secrets.Decrypt(encrypted)
	if err != nil {
		return c, errors.New("OIDC Client Secret cannot be decrypted; restore the matching relio-data volume")
	}
	return c, err
}

func validate(c Config) error {
	u, err := url.Parse(c.IssuerURL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return errors.New("valid issuer URL is required")
	}
	if c.ClientID == "" {
		return errors.New("clientId is required")
	}
	if c.ClientSecret == "" && !c.ClientSecretConfigured {
		return errors.New("clientSecret is required")
	}
	for _, scope := range c.Scopes {
		if scope == "openid" {
			return nil
		}
	}
	return errors.New("scopes must include openid")
}
func (s *Service) Save(ctx context.Context, p *auth.Principal, c Config, ip, requestID, ua string) (Config, error) {
	if err := auth.Require(p, "admin:write"); err != nil {
		return Config{}, err
	}
	defaults(&c)
	existing, err := s.Get(ctx)
	if err != nil {
		return Config{}, err
	}
	c.ClientSecretConfigured = existing.ClientSecretConfigured
	if err := validate(c); err != nil {
		return Config{}, err
	}
	encrypted := ""
	if c.ClientSecret != "" {
		var err error
		encrypted, err = s.Secrets.Encrypt(c.ClientSecret)
		if err != nil {
			return Config{}, err
		}
	} else if existing.ID != "" {
		if err = s.DB.QueryRow(ctx, `SELECT client_secret_encrypted FROM oidc_providers WHERE id=$1`, existing.ID).Scan(&encrypted); err != nil {
			return Config{}, fmt.Errorf("preserve OIDC Client Secret: %w", err)
		}
	}
	id := existing.ID
	if id == "" {
		id = ids.New()
		_, err := s.DB.Exec(ctx, `INSERT INTO oidc_providers(id,enabled,issuer_url,client_id,client_secret_encrypted,scopes,username_claim,email_claim,name_claim,group_claim,role_claim,auto_provision,auto_login,default_role_id,root_ca_pem,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, id, c.Enabled, strings.TrimRight(c.IssuerURL, "/"), c.ClientID, encrypted, c.Scopes, c.UsernameClaim, c.EmailClaim, c.NameClaim, c.GroupClaim, c.RoleClaim, c.AutoProvision, c.AutoLogin, nullString(c.DefaultRoleID), nullString(c.RootCAPEM), p.UserID)
		if err != nil {
			return Config{}, err
		}
	} else {
		expectedVersion := c.Version
		if expectedVersion == 0 {
			expectedVersion = existing.Version
		}
		result, err := s.DB.Exec(ctx, `UPDATE oidc_providers SET enabled=$2,issuer_url=$3,client_id=$4,client_secret_encrypted=$5,scopes=$6,username_claim=$7,email_claim=$8,name_claim=$9,group_claim=$10,role_claim=$11,auto_provision=$12,auto_login=$13,default_role_id=$14,root_ca_pem=$15,updated_by=$16,updated_at=now(),version=version+1 WHERE id=$1 AND version=$17`, id, c.Enabled, strings.TrimRight(c.IssuerURL, "/"), c.ClientID, encrypted, c.Scopes, c.UsernameClaim, c.EmailClaim, c.NameClaim, c.GroupClaim, c.RoleClaim, c.AutoProvision, c.AutoLogin, nullString(c.DefaultRoleID), nullString(c.RootCAPEM), p.UserID, expectedVersion)
		if err != nil {
			return Config{}, err
		}
		if result.RowsAffected() == 0 {
			return Config{}, errors.New("OIDC configuration was changed by another user")
		}
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "OIDC_CONFIG_UPDATE", Resource: "oidc_provider", ResourceID: id, Before: map[string]any{"enabled": existing.Enabled, "issuerUrl": existing.IssuerURL, "clientId": existing.ClientID, "autoLogin": existing.AutoLogin}, After: map[string]any{"enabled": c.Enabled, "issuerUrl": c.IssuerURL, "clientId": c.ClientID, "clientSecret": "***", "autoLogin": c.AutoLogin}, IP: ip, RequestID: requestID, UserAgent: ua})
	return s.Get(ctx)
}
func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}

func newHTTPClient(rootPEM string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if rootPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(rootPEM)) {
			return nil, errors.New("root CA PEM is invalid")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	}
	return &http.Client{Timeout: 12 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("too many redirects")
		}
		return nil
	}}, nil
}

func fetchDiscovery(ctx context.Context, c Config) (Discovery, error) {
	client, err := newHTTPClient(c.RootCAPEM)
	if err != nil {
		return Discovery{}, err
	}
	endpoint := strings.TrimRight(c.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Discovery{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Discovery{}, fmt.Errorf("discovery returned HTTP %d", resp.StatusCode)
	}
	var d Discovery
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return d, err
	}
	if strings.TrimRight(d.Issuer, "/") != strings.TrimRight(c.IssuerURL, "/") {
		return d, errors.New("discovery issuer does not match configured issuer")
	}
	for name, v := range map[string]string{"authorization_endpoint": d.AuthorizationEndpoint, "token_endpoint": d.TokenEndpoint, "jwks_uri": d.JWKSURI} {
		u, e := url.Parse(v)
		if e != nil || u.Scheme == "" || u.Host == "" {
			return d, fmt.Errorf("discovery %s is invalid", name)
		}
	}
	return d, nil
}

func (s *Service) Test(ctx context.Context, p *auth.Principal) (TestResult, error) {
	if err := auth.Require(p, "admin:write"); err != nil {
		return TestResult{}, err
	}
	c, err := s.privateConfig(ctx)
	if err != nil {
		return TestResult{}, err
	}
	result := TestResult{Checks: map[string]string{}, CallbackURL: s.callbackURL(ctx), TestedAt: time.Now()}
	d, err := fetchDiscovery(ctx, c)
	if err != nil {
		result.Checks["issuer"] = "failed: " + err.Error()
		s.storeTest(ctx, c.ID, result)
		return result, nil
	}
	result.Discovery = &d
	result.Checks["issuer"] = "ok"
	result.Checks["discovery"] = "ok"
	result.Checks["authorizationEndpoint"] = "ok"
	result.Checks["tokenEndpoint"] = "ok"
	client, err := newHTTPClient(c.RootCAPEM)
	if err != nil {
		result.Checks["tls"] = "failed: " + err.Error()
		s.storeTest(ctx, c.ID, result)
		return result, nil
	}
	result.Checks["tls"] = "ok"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, d.JWKSURI, nil)
	resp, err := client.Do(req)
	if err != nil {
		result.Checks["jwks"] = "failed: " + err.Error()
	} else {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var keys struct {
				Keys []json.RawMessage `json:"keys"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&keys) == nil && len(keys.Keys) > 0 {
				result.Checks["jwks"] = "ok"
			} else {
				result.Checks["jwks"] = "failed: no signing keys"
			}
		} else {
			result.Checks["jwks"] = fmt.Sprintf("failed: HTTP %d", resp.StatusCode)
		}
	}
	// A client_credentials probe distinguishes an invalid confidential-client
	// secret from a client that simply has service accounts disabled.
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "scope": {"openid"}}
	tokenReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResp, e := client.Do(tokenReq)
	if e != nil {
		result.Checks["clientCredential"] = "failed: " + e.Error()
	} else {
		defer tokenResp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(tokenResp.Body, 64<<10))
		if tokenResp.StatusCode >= 200 && tokenResp.StatusCode < 300 {
			result.Checks["clientCredential"] = "ok"
		} else {
			var oauthErr struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(body, &oauthErr)
			if oauthErr.Error == "unauthorized_client" {
				result.Checks["clientCredential"] = "warning: client is confidential but service account is disabled"
			} else {
				result.Checks["clientCredential"] = "failed: " + oauthErr.Error
			}
		}
	}
	result.Checks["redirectUri"] = "register in Keycloak: " + result.CallbackURL
	result.Checks["claims"] = "verified on the first interactive login"
	result.Success = true
	for _, v := range result.Checks {
		if strings.HasPrefix(v, "failed:") {
			result.Success = false
		}
	}
	s.storeTest(ctx, c.ID, result)
	return result, nil
}
func (s *Service) storeTest(ctx context.Context, id string, result TestResult) {
	raw, _ := json.Marshal(result)
	disc, _ := json.Marshal(result.Discovery)
	_, _ = s.DB.Exec(ctx, `UPDATE oidc_providers SET discovery=$2,last_tested_at=$3,last_test_result=$4,updated_at=now() WHERE id=$1`, id, disc, result.TestedAt, raw)
}

// LoginRequest carries what the browser asked for when it started a login.
type LoginRequest struct {
	// Silent asks for prompt=none: the provider answers from an existing
	// session only and never renders a screen. Honoured only while the
	// administrator has turned auto login on, so a "?prompt=none" pasted into
	// the address bar cannot change the flow by itself.
	Silent bool
	// ReturnTo is where the browser lands after the callback. Anything that is
	// not a same-origin application path falls back to DefaultReturnTo.
	ReturnTo string
}

// DefaultReturnTo is where a login lands when the browser did not say.
const DefaultReturnTo = "/app"

// SafeReturnTo reports whether value may be used as a post-login redirect: a
// same-origin path that starts with "/" but not "//" (a scheme-relative URL
// would leave the site), and that the single-page application answers. The
// API, MCP and login paths are refused because landing a fresh session on
// /api/v1/auth/oidc/start or /login would start the very loop silent SSO
// must avoid.
func SafeReturnTo(value string) bool {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return false
	}
	path := parsed.Path
	switch {
	case strings.HasPrefix(path, "/api/"), path == "/api":
		return false
	case strings.HasPrefix(path, "/mcp/"), path == "/mcp":
		return false
	case strings.HasPrefix(path, "/.well-known/"):
		return false
	case strings.HasPrefix(path, "/login/"), path == "/login":
		return false
	}
	return true
}

// returnToOrDefault keeps a valid destination and replaces everything else.
func returnToOrDefault(value string) string {
	if SafeReturnTo(value) {
		return value
	}
	return DefaultReturnTo
}

// silentAllowed is the one place that decides whether a prompt=none attempt
// goes out. The browser's request alone is never enough.
func silentAllowed(c Config, req LoginRequest) bool {
	return req.Silent && c.Enabled && c.AutoLogin
}

// authorizeQuery builds the authorization request. prompt=none is appended
// only for a permitted silent attempt.
func authorizeQuery(c Config, callback, state, nonce, challenge string, silent bool) url.Values {
	q := url.Values{"client_id": {c.ClientID}, "response_type": {"code"}, "scope": {strings.Join(c.Scopes, " ")}, "redirect_uri": {callback}, "state": {state}, "nonce": {nonce}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	if silent {
		q.Set("prompt", "none")
	}
	return q
}

func (s *Service) LoginURL(ctx context.Context, req LoginRequest) (string, error) {
	c, err := s.privateConfig(ctx)
	if err != nil {
		return "", errors.New("SSO is not configured")
	}
	if !c.Enabled {
		return "", errors.New("SSO is disabled")
	}
	d, err := fetchDiscovery(ctx, c)
	if err != nil {
		return "", err
	}
	silent := silentAllowed(c, req)
	returnTo := returnToOrDefault(req.ReturnTo)
	state, nonce, verifier := ids.Token(32), ids.Token(24), ids.Token(48)
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	stateHash := sha256.Sum256([]byte(state))
	callback := s.callbackURL(ctx)
	_, err = s.DB.Exec(ctx, `INSERT INTO oidc_login_states(state_hash,provider_id,nonce,code_verifier,redirect_uri,silent,return_to,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '10 minutes')`, stateHash[:], c.ID, nonce, verifier, callback, silent, returnTo)
	if err != nil {
		return "", err
	}
	return d.AuthorizationEndpoint + "?" + authorizeQuery(c, callback, state, nonce, challenge, silent).Encode(), nil
}

// Refusal describes a callback the provider answered with an error instead of
// a code. For a silent attempt that is the ordinary "no session" answer.
type Refusal struct {
	Silent   bool
	ReturnTo string
}

// AbandonLogin consumes the state row of a login the provider refused and
// reports whether the attempt was silent. An unknown or expired state reads
// as a non-silent refusal, which is the safe side: the browser shows the
// error and never retries on its own.
func (s *Service) AbandonLogin(ctx context.Context, state string) Refusal {
	if state == "" {
		return Refusal{ReturnTo: DefaultReturnTo}
	}
	stateHash := sha256.Sum256([]byte(state))
	var r Refusal
	if err := s.DB.QueryRow(ctx, `DELETE FROM oidc_login_states WHERE state_hash=$1 RETURNING silent,return_to`, stateHash[:]).Scan(&r.Silent, &r.ReturnTo); err != nil {
		return Refusal{ReturnTo: DefaultReturnTo}
	}
	r.ReturnTo = returnToOrDefault(r.ReturnTo)
	return r
}

// RefusalRedirect is where the browser goes after the provider refused. A
// silent attempt lands on the login screen with the "sso=none" marker that
// tells the browser not to try again — even when its sessionStorage was
// cleared in between — so a signed-out visitor is never bounced in a loop.
// An interactive attempt keeps its error code so the login screen can
// explain what went wrong.
func RefusalRedirect(r Refusal, providerError string) string {
	if r.Silent {
		return "/login?sso=none"
	}
	return "/login?sso_error=" + url.QueryEscape(providerError)
}

// SilentRefusalCodes are the answers prompt=none gives when the provider has
// no usable session. They are outcomes, not failures, and are logged as such.
var SilentRefusalCodes = map[string]bool{"login_required": true, "interaction_required": true, "consent_required": true, "account_selection_required": true}

type callbackResult struct {
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Session is a completed SSO login: the cookie value, who signed in, and where
// the browser should land.
type Session struct {
	Token     string
	Principal *auth.Principal
	// Silent records that the session came from a prompt=none attempt.
	Silent   bool
	ReturnTo string
}

func (s *Service) Callback(ctx context.Context, state, code, ip, ua string) (Session, error) {
	if state == "" || code == "" {
		return Session{}, errors.New("OIDC callback is missing state or code")
	}
	stateHash := sha256.Sum256([]byte(state))
	var providerID, nonce, verifier, redirect, returnTo string
	var silent bool
	err := s.DB.QueryRow(ctx, `DELETE FROM oidc_login_states WHERE state_hash=$1 AND expires_at>now() RETURNING provider_id,nonce,code_verifier,redirect_uri,silent,return_to`, stateHash[:]).Scan(&providerID, &nonce, &verifier, &redirect, &silent, &returnTo)
	if err != nil {
		return Session{}, errors.New("OIDC state is invalid or expired")
	}
	c, err := s.privateConfig(ctx)
	if err != nil || c.ID != providerID || !c.Enabled {
		return Session{}, errors.New("OIDC provider is unavailable")
	}
	d, err := fetchDiscovery(ctx, c)
	if err != nil {
		return Session{}, err
	}
	client, err := newHTTPClient(c.RootCAPEM)
	if err != nil {
		return Session{}, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Session{}, err
	}
	defer resp.Body.Close()
	var tokens callbackResult
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokens); err != nil {
		return Session{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Session{}, fmt.Errorf("token exchange failed: %s", tokens.Error)
	}
	claims, err := verifyToken(ctx, client, d, tokens.IDToken, c.ClientID, nonce)
	if err != nil {
		return Session{}, err
	}
	userID, err := s.resolveUser(ctx, c, claims)
	if err != nil {
		return Session{}, err
	}
	token, p, err := s.Auth.CreateSession(ctx, userID, "OIDC", ip, ua)
	if err != nil {
		return Session{}, err
	}
	_, _ = s.DB.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
	s.Audit.Record(ctx, audit.Event{ActorID: userID, ActorName: p.Username, Channel: "SSO", Action: "LOGIN", Resource: "session", IP: ip, UserAgent: ua, Metadata: map[string]any{"silent": silent}})
	return Session{Token: token, Principal: p, Silent: silent, ReturnTo: returnToOrDefault(returnTo)}, nil
}

func verifyToken(ctx context.Context, client *http.Client, d Discovery, raw, clientID, nonce string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid ID token")
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
		return nil, errors.New("unsupported ID token signature")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, d.JWKSURI, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var set struct {
		Keys []struct{ Kid, Kty, Alg, N, E string } `json:"keys"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	var pub *rsa.PublicKey
	for _, k := range set.Keys {
		if k.Kid == header.Kid && k.Kty == "RSA" {
			nBytes, e1 := base64.RawURLEncoding.DecodeString(k.N)
			eBytes, e2 := base64.RawURLEncoding.DecodeString(k.E)
			if e1 != nil || e2 != nil || len(eBytes) > 4 {
				continue
			}
			padded := make([]byte, 4)
			copy(padded[4-len(eBytes):], eBytes)
			pub = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(binary.BigEndian.Uint32(padded))}
			break
		}
	}
	if pub == nil {
		return nil, errors.New("ID token signing key not found")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return nil, errors.New("ID token signature verification failed")
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
		return nil, errors.New("ID token issuer mismatch")
	}
	exp, ok := claims["exp"].(float64)
	if !ok || time.Unix(int64(exp), 0).Before(time.Now().Add(-time.Minute)) {
		return nil, errors.New("ID token expired")
	}
	if nonce != "" && fmt.Sprint(claims["nonce"]) != nonce {
		return nil, errors.New("ID token nonce mismatch")
	}
	if !audienceContains(claims["aud"], clientID) {
		return nil, errors.New("ID token audience mismatch")
	}
	return claims, nil
}
func audienceContains(v any, want string) bool {
	switch a := v.(type) {
	case string:
		return a == want
	case []any:
		for _, v := range a {
			if fmt.Sprint(v) == want {
				return true
			}
		}
	}
	return false
}
func claim(claims map[string]any, path string) any {
	var current any = claims
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}

// fallbackRoleID resolves the Role a Keycloak user receives when no Claim
// mapping applies. Without it a provisioned user has no permission at all and
// every CRM request answers HTTP 403 even though the SSO login succeeded.
func (s *Service) fallbackRoleID(ctx context.Context, c Config) (string, error) {
	if c.DefaultRoleID != "" {
		var id string
		err := s.DB.QueryRow(ctx, `SELECT id FROM roles WHERE id=$1`, c.DefaultRoleID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	var id string
	err := s.DB.QueryRow(ctx, `SELECT id FROM roles WHERE is_default LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// repairRoles attaches the default Role to an SSO user that has none. Users
// provisioned by earlier releases are in exactly that state, so signing in once
// is enough to make Relio usable again.
func (s *Service) repairRoles(ctx context.Context, c Config, userID, username string) {
	if !c.AutoProvision {
		return
	}
	var roles int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE user_id=$1`, userID).Scan(&roles); err != nil || roles > 0 {
		return
	}
	roleID, err := s.fallbackRoleID(ctx, c)
	if err != nil || roleID == "" {
		return
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, roleID); err != nil {
		return
	}
	s.Audit.Record(ctx, audit.Event{ActorID: userID, ActorName: username, Channel: "SSO", Action: "USER_ROLES_REPAIR", Resource: "user", ResourceID: userID, After: map[string]any{"roleIds": []string{roleID}, "reason": "SSO user had no Role and could not use Relio"}})
}

func (s *Service) resolveUser(ctx context.Context, c Config, claims map[string]any) (string, error) {
	subject := fmt.Sprint(claims["sub"])
	if subject == "" {
		return "", errors.New("ID token has no subject")
	}
	var id, existingName string
	err := s.DB.QueryRow(ctx, `SELECT id,display_name FROM users WHERE oidc_subject=$1 AND active=true`, subject).Scan(&id, &existingName)
	if err == nil {
		s.repairRoles(ctx, c, id, existingName)
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if !c.AutoProvision {
		return "", errors.New("user is not provisioned and auto provisioning is disabled")
	}
	username := strings.TrimSpace(fmt.Sprint(claim(claims, c.UsernameClaim)))
	email := strings.TrimSpace(fmt.Sprint(claim(claims, c.EmailClaim)))
	name := strings.TrimSpace(fmt.Sprint(claim(claims, c.NameClaim)))
	if username == "" || username == "<nil>" {
		username = email
	}
	if username == "" || username == "<nil>" {
		return "", errors.New("configured username claim is missing")
	}
	if name == "" || name == "<nil>" {
		name = username
	}
	if email == "<nil>" {
		email = ""
	}
	id = ids.New()
	var orgID string
	err = s.DB.QueryRow(ctx, `SELECT id FROM organizations WHERE code='RELIO'`).Scan(&orgID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("resolve default OIDC organization: %w", err)
	}
	externalGroups := stringSlice(claim(claims, c.GroupClaim))
	if len(externalGroups) > 0 {
		err = s.DB.QueryRow(ctx, `SELECT organization_id FROM oidc_group_mappings WHERE provider_id=$1 AND external_group=ANY($2) ORDER BY external_group LIMIT 1`, c.ID, externalGroups).Scan(&orgID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("resolve OIDC group mapping: %w", err)
		}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO users(id,username,email,display_name,auth_source,oidc_subject,organization_id,active) VALUES($1,$2,$3,$4,'OIDC',$5,$6,true)`, id, username, nullString(email), name, subject, nullString(orgID))
	if err != nil {
		return "", fmt.Errorf("provision OIDC user: %w", err)
	}
	roleIDs := []string{}
	externalRoles := stringSlice(claim(claims, c.RoleClaim))
	if len(externalRoles) > 0 {
		rows, err := tx.Query(ctx, `SELECT role_id FROM oidc_role_mappings WHERE provider_id=$1 AND external_role=ANY($2)`, c.ID, externalRoles)
		if err != nil {
			return "", fmt.Errorf("resolve OIDC role mapping: %w", err)
		}
		for rows.Next() {
			var role string
			if err = rows.Scan(&role); err != nil {
				rows.Close()
				return "", err
			}
			roleIDs = append(roleIDs, role)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
	}
	// Only fall back when no Claim mapping matched. A user must always end up
	// with at least one Role, otherwise the login succeeds and the application
	// is unusable.
	if len(roleIDs) == 0 {
		fallback, err := s.fallbackRoleID(ctx, c)
		if err != nil {
			return "", fmt.Errorf("resolve default sign-in role: %w", err)
		}
		if fallback == "" {
			return "", errors.New("no default sign-in Role is configured; set one in the administrator console before enabling SSO auto provisioning")
		}
		roleIDs = append(roleIDs, fallback)
	}
	seen := map[string]bool{}
	for _, roleID := range roleIDs {
		if roleID != "" && !seen[roleID] {
			if _, err = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, roleID); err != nil {
				return "", fmt.Errorf("assign OIDC role: %w", err)
			}
			seen[roleID] = true
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}
func stringSlice(v any) []string {
	out := []string{}
	switch x := v.(type) {
	case []any:
		for _, i := range x {
			out = append(out, fmt.Sprint(i))
		}
	case []string:
		return x
	case string:
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// CallbackReason maps a callback failure onto a stable, non-sensitive code so
// the login screen can tell a user whose account simply is not provisioned apart
// from a genuine Keycloak connectivity problem.
func CallbackReason(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "auto provisioning is disabled"):
		return "not_provisioned"
	case strings.Contains(message, "default sign-in Role"):
		return "no_default_role"
	case strings.Contains(message, "state is invalid or expired"):
		return "state_expired"
	case strings.Contains(message, "token exchange failed"):
		return "token_exchange_failed"
	case strings.Contains(message, "ID token"):
		return "token_invalid"
	case strings.Contains(message, "username claim"):
		return "claim_missing"
	case strings.Contains(message, "discovery"), strings.Contains(message, "provider is unavailable"):
		return "discovery_failed"
	default:
		return "callback_failed"
	}
}

func (s *Service) PublicStatus(ctx context.Context) map[string]any {
	c, err := s.Get(ctx)
	if err != nil || c.ID == "" {
		return map[string]any{"enabled": false}
	}
	// autoLogin is published so the browser knows whether to try a silent
	// sign-in before it renders the login screen.
	return map[string]any{"enabled": c.Enabled, "issuer": c.IssuerURL, "autoLogin": c.Enabled && c.AutoLogin}
}

func ClientIP(r *http.Request) string { return httpx.ClientIP(r) }
