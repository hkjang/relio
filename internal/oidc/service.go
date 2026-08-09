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
type Config struct {
	ID                     string     `json:"id,omitempty"`
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
	err := s.DB.QueryRow(ctx, `SELECT id,enabled,issuer_url,client_id,client_secret_encrypted,scopes,username_claim,email_claim,name_claim,group_claim,role_claim,auto_provision,default_role_id,COALESCE(root_ca_pem,''),discovery,last_tested_at,last_test_result FROM oidc_providers ORDER BY created_at LIMIT 1`).Scan(&c.ID, &c.Enabled, &c.IssuerURL, &c.ClientID, &secret, &scopes, &c.UsernameClaim, &c.EmailClaim, &c.NameClaim, &c.GroupClaim, &c.RoleClaim, &c.AutoProvision, &role, &c.RootCAPEM, &discovery, &c.LastTestedAt, &result)
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
	existing, _ := s.Get(ctx)
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
		_ = s.DB.QueryRow(ctx, `SELECT client_secret_encrypted FROM oidc_providers WHERE id=$1`, existing.ID).Scan(&encrypted)
	}
	id := existing.ID
	if id == "" {
		id = ids.New()
		_, err := s.DB.Exec(ctx, `INSERT INTO oidc_providers(id,enabled,issuer_url,client_id,client_secret_encrypted,scopes,username_claim,email_claim,name_claim,group_claim,role_claim,auto_provision,default_role_id,root_ca_pem,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, id, c.Enabled, strings.TrimRight(c.IssuerURL, "/"), c.ClientID, encrypted, c.Scopes, c.UsernameClaim, c.EmailClaim, c.NameClaim, c.GroupClaim, c.RoleClaim, c.AutoProvision, nullString(c.DefaultRoleID), nullString(c.RootCAPEM), p.UserID)
		if err != nil {
			return Config{}, err
		}
	} else {
		_, err := s.DB.Exec(ctx, `UPDATE oidc_providers SET enabled=$2,issuer_url=$3,client_id=$4,client_secret_encrypted=$5,scopes=$6,username_claim=$7,email_claim=$8,name_claim=$9,group_claim=$10,role_claim=$11,auto_provision=$12,default_role_id=$13,root_ca_pem=$14,updated_by=$15,updated_at=now() WHERE id=$1`, id, c.Enabled, strings.TrimRight(c.IssuerURL, "/"), c.ClientID, encrypted, c.Scopes, c.UsernameClaim, c.EmailClaim, c.NameClaim, c.GroupClaim, c.RoleClaim, c.AutoProvision, nullString(c.DefaultRoleID), nullString(c.RootCAPEM), p.UserID)
		if err != nil {
			return Config{}, err
		}
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "OIDC_CONFIG_UPDATE", Resource: "oidc_provider", ResourceID: id, Before: map[string]any{"enabled": existing.Enabled, "issuerUrl": existing.IssuerURL, "clientId": existing.ClientID}, After: map[string]any{"enabled": c.Enabled, "issuerUrl": c.IssuerURL, "clientId": c.ClientID, "clientSecret": "***"}, IP: ip, RequestID: requestID, UserAgent: ua})
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

func (s *Service) LoginURL(ctx context.Context) (string, error) {
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
	state, nonce, verifier := ids.Token(32), ids.Token(24), ids.Token(48)
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	stateHash := sha256.Sum256([]byte(state))
	callback := s.callbackURL(ctx)
	_, err = s.DB.Exec(ctx, `INSERT INTO oidc_login_states(state_hash,provider_id,nonce,code_verifier,redirect_uri,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '10 minutes')`, stateHash[:], c.ID, nonce, verifier, callback)
	if err != nil {
		return "", err
	}
	q := url.Values{"client_id": {c.ClientID}, "response_type": {"code"}, "scope": {strings.Join(c.Scopes, " ")}, "redirect_uri": {callback}, "state": {state}, "nonce": {nonce}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return d.AuthorizationEndpoint + "?" + q.Encode(), nil
}

type callbackResult struct {
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (s *Service) Callback(ctx context.Context, state, code, ip, ua string) (string, *auth.Principal, error) {
	if state == "" || code == "" {
		return "", nil, errors.New("OIDC callback is missing state or code")
	}
	stateHash := sha256.Sum256([]byte(state))
	var providerID, nonce, verifier, redirect string
	err := s.DB.QueryRow(ctx, `DELETE FROM oidc_login_states WHERE state_hash=$1 AND expires_at>now() RETURNING provider_id,nonce,code_verifier,redirect_uri`, stateHash[:]).Scan(&providerID, &nonce, &verifier, &redirect)
	if err != nil {
		return "", nil, errors.New("OIDC state is invalid or expired")
	}
	c, err := s.privateConfig(ctx)
	if err != nil || c.ID != providerID || !c.Enabled {
		return "", nil, errors.New("OIDC provider is unavailable")
	}
	d, err := fetchDiscovery(ctx, c)
	if err != nil {
		return "", nil, err
	}
	client, err := newHTTPClient(c.RootCAPEM)
	if err != nil {
		return "", nil, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var tokens callbackResult
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokens); err != nil {
		return "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("token exchange failed: %s", tokens.Error)
	}
	claims, err := verifyToken(ctx, client, d, tokens.IDToken, c.ClientID, nonce)
	if err != nil {
		return "", nil, err
	}
	userID, err := s.resolveUser(ctx, c, claims)
	if err != nil {
		return "", nil, err
	}
	token, p, err := s.Auth.CreateSession(ctx, userID, "OIDC", ip, ua)
	if err != nil {
		return "", nil, err
	}
	_, _ = s.DB.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
	s.Audit.Record(ctx, audit.Event{ActorID: userID, ActorName: p.Username, Channel: "SSO", Action: "LOGIN", Resource: "session", IP: ip, UserAgent: ua})
	return token, p, nil
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
func (s *Service) resolveUser(ctx context.Context, c Config, claims map[string]any) (string, error) {
	subject := fmt.Sprint(claims["sub"])
	if subject == "" {
		return "", errors.New("ID token has no subject")
	}
	var id string
	err := s.DB.QueryRow(ctx, `SELECT id FROM users WHERE oidc_subject=$1 AND active=true`, subject).Scan(&id)
	if err == nil {
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
	_ = s.DB.QueryRow(ctx, `SELECT id FROM organizations WHERE code='RELIO'`).Scan(&orgID)
	externalGroups := stringSlice(claim(claims, c.GroupClaim))
	if len(externalGroups) > 0 {
		_ = s.DB.QueryRow(ctx, `SELECT organization_id FROM oidc_group_mappings WHERE provider_id=$1 AND external_group=ANY($2) ORDER BY external_group LIMIT 1`, c.ID, externalGroups).Scan(&orgID)
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
	if c.DefaultRoleID != "" {
		roleIDs = append(roleIDs, c.DefaultRoleID)
	}
	externalRoles := stringSlice(claim(claims, c.RoleClaim))
	if len(externalRoles) > 0 {
		rows, e := tx.Query(ctx, `SELECT role_id FROM oidc_role_mappings WHERE provider_id=$1 AND external_role=ANY($2)`, c.ID, externalRoles)
		if e == nil {
			for rows.Next() {
				var role string
				if rows.Scan(&role) == nil {
					roleIDs = append(roleIDs, role)
				}
			}
			rows.Close()
		}
	}
	seen := map[string]bool{}
	for _, roleID := range roleIDs {
		if roleID != "" && !seen[roleID] {
			_, _ = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, roleID)
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

func (s *Service) PublicStatus(ctx context.Context) map[string]any {
	c, err := s.Get(ctx)
	if err != nil || c.ID == "" {
		return map[string]any{"enabled": false}
	}
	return map[string]any{"enabled": c.Enabled, "issuer": c.IssuerURL}
}

func (s *Service) ValidateAccessToken(ctx context.Context, raw string) (string, error) {
	c, err := s.privateConfig(ctx)
	if err != nil || !c.Enabled {
		return "", errors.New("OIDC access tokens are disabled")
	}
	d, err := fetchDiscovery(ctx, c)
	if err != nil {
		return "", err
	}
	client, err := newHTTPClient(c.RootCAPEM)
	if err != nil {
		return "", err
	}
	claims, err := verifyToken(ctx, client, d, raw, c.ClientID, "")
	if err != nil {
		return "", err
	}
	subject := strings.TrimSpace(fmt.Sprint(claims["sub"]))
	if subject == "" || subject == "<nil>" {
		return "", errors.New("access token has no subject")
	}
	var userID string
	if err = s.DB.QueryRow(ctx, `SELECT id FROM users WHERE oidc_subject=$1 AND active=true`, subject).Scan(&userID); err != nil {
		return "", errors.New("OIDC access token user is not provisioned")
	}
	return userID, nil
}
func ClientIP(r *http.Request) string { return httpx.ClientIP(r) }
