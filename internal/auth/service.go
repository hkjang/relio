package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/config"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const SessionCookie = "relio_session"

type Principal struct {
	UserID             string   `json:"id"`
	Username           string   `json:"username"`
	DisplayName        string   `json:"displayName"`
	Email              string   `json:"email,omitempty"`
	OrganizationID     string   `json:"organizationId,omitempty"`
	ManagerID          string   `json:"managerId,omitempty"`
	AuthMethod         string   `json:"authMethod"`
	IsBootstrap        bool     `json:"isBootstrap"`
	MustChangePassword bool     `json:"mustChangePassword"`
	DataScope          string   `json:"dataScope"`
	Permissions        []string `json:"permissions"`
	KeyID              string   `json:"-"`
	KeyDBID            string   `json:"-"`
	KeyScopes          []string `json:"-"`
	KeyChannels        []string `json:"-"`
	CSRFToken          string   `json:"csrfToken,omitempty"`
	perm               map[string]bool
}

func (p *Principal) Has(permission string) bool {
	if p == nil {
		return false
	}
	if p.IsBootstrap || p.perm["admin:*"] || p.perm[permission] {
		return p.keyAllows(permission)
	}
	return false
}
func (p *Principal) keyAllows(permission string) bool {
	if p.KeyID == "" {
		return true
	}
	for _, s := range p.KeyScopes {
		if s == permission || s == "admin:*" {
			return true
		}
	}
	return false
}
func (p *Principal) ChannelAllowed(channel string) bool {
	if p.KeyID == "" {
		return true
	}
	for _, c := range p.KeyChannels {
		if strings.EqualFold(c, channel) {
			return true
		}
	}
	return false
}

type Service struct {
	DB            *pgxpool.Pool
	Secrets       *secrets.Manager
	OIDCValidator func(context.Context, string) (string, error)
}

func (s *Service) Bootstrap(ctx context.Context, cfg config.Config) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE is_bootstrap=true)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit(ctx)
	}
	orgID, roleID, userID := ids.New(), ids.New(), ids.New()
	if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,name,code,org_type) VALUES($1,'Relio','RELIO','COMPANY') ON CONFLICT(code) DO NOTHING`, orgID); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM organizations WHERE code='RELIO'`).Scan(&orgID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO roles(id,code,name,description,data_scope,system_role) VALUES($1,'SYSTEM_ADMIN','시스템 관리자','Relio 전체 관리자','COMPANY',true) ON CONFLICT(code) DO NOTHING`, roleID); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM roles WHERE code='SYSTEM_ADMIN'`).Scan(&roleID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO role_permissions(role_id,permission) VALUES($1,'admin:*') ON CONFLICT DO NOTHING`, roleID); err != nil {
		return err
	}
	hash, err := HashPassword(cfg.BootstrapPassword)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO users(id,username,display_name,password_hash,auth_source,organization_id,active,is_bootstrap,must_change_password) VALUES($1,$2,$2,$3,'LOCAL',$4,true,true,true)`, userID, cfg.BootstrapAdmin, hash, orgID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, userID, roleID); err != nil {
		return err
	}
	pipeID := ids.New()
	if _, err = tx.Exec(ctx, `INSERT INTO pipelines(id,name,active,is_default) VALUES($1,'기본 영업 Pipeline',true,true)`, pipeID); err != nil {
		return err
	}
	stages := []struct {
		name        string
		probability int
		cat         string
		won, lost   bool
		color       string
	}{{"Lead", 10, "PIPELINE", false, false, "#94a3b8"}, {"상담", 20, "PIPELINE", false, false, "#60a5fa"}, {"Needs 분석", 30, "PIPELINE", false, false, "#38bdf8"}, {"제안", 50, "BEST_CASE", false, false, "#818cf8"}, {"견적", 60, "BEST_CASE", false, false, "#a78bfa"}, {"협상", 80, "COMMIT", false, false, "#f59e0b"}, {"계약예정", 90, "COMMIT", false, false, "#22c55e"}, {"Won", 100, "CLOSED", true, false, "#16a34a"}, {"Lost", 0, "CLOSED", false, true, "#ef4444"}}
	for i, st := range stages {
		if _, err = tx.Exec(ctx, `INSERT INTO pipeline_stages(id,pipeline_id,name,stage_order,probability,forecast_category,is_won,is_lost,color) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, ids.New(), pipeID, st.name, i+1, st.probability, st.cat, st.won, st.lost, st.color); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) Login(ctx context.Context, username, password, ip, ua string) (string, *Principal, error) {
	var id, hash string
	var active bool
	err := s.DB.QueryRow(ctx, `SELECT id,password_hash,active FROM users WHERE lower(username)=lower($1) AND auth_source='LOCAL'`, username).Scan(&id, &hash, &active)
	if err != nil || !active || !VerifyPassword(hash, password) {
		return "", nil, errors.New("invalid credentials")
	}
	token := ids.Token(32)
	digest := sha256.Sum256([]byte(token))
	csrf := ids.Token(24)
	duration := s.sessionDuration(ctx)
	_, err = s.DB.Exec(ctx, `INSERT INTO sessions(id_hash,user_id,csrf_token,auth_method,ip,user_agent,expires_at) VALUES($1,$2,$3,'LOCAL',NULLIF($4,'')::inet,$5,$6)`, digest[:], id, csrf, ip, ua, time.Now().Add(duration))
	if err != nil {
		return "", nil, err
	}
	_, _ = s.DB.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, id)
	p, err := s.loadPrincipal(ctx, id)
	if err != nil {
		return "", nil, err
	}
	p.AuthMethod = "LOCAL"
	p.CSRFToken = csrf
	return token, p, nil
}

func (s *Service) sessionDuration(ctx context.Context) time.Duration {
	minutes := 480
	_ = s.DB.QueryRow(ctx, `SELECT COALESCE((value #>> '{}')::int,480) FROM system_settings WHERE namespace='security' AND key='session_minutes'`).Scan(&minutes)
	if minutes < 15 || minutes > 10080 {
		minutes = 480
	}
	return time.Duration(minutes) * time.Minute
}

func (s *Service) CreateSession(ctx context.Context, userID, method, ip, ua string) (string, *Principal, error) {
	token := ids.Token(32)
	digest := sha256.Sum256([]byte(token))
	csrf := ids.Token(24)
	_, err := s.DB.Exec(ctx, `INSERT INTO sessions(id_hash,user_id,csrf_token,auth_method,ip,user_agent,expires_at) VALUES($1,$2,$3,$4,NULLIF($5,'')::inet,$6,$7)`, digest[:], userID, csrf, method, ip, ua, time.Now().Add(s.sessionDuration(ctx)))
	if err != nil {
		return "", nil, err
	}
	p, err := s.loadPrincipal(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	p.AuthMethod = method
	p.CSRFToken = csrf
	return token, p, nil
}

func (s *Service) Authenticate(r *http.Request) (*Principal, error) {
	if bearer := httpx.Bearer(r); bearer != "" {
		if strings.HasPrefix(bearer, "relio_") {
			return s.authenticateKey(r.Context(), bearer, httpx.ClientIP(r))
		}
		if s.OIDCValidator != nil {
			userID, err := s.OIDCValidator(r.Context(), bearer)
			if err == nil {
				p, loadErr := s.loadPrincipal(r.Context(), userID)
				if loadErr != nil {
					return nil, loadErr
				}
				p.AuthMethod = "OIDC_ACCESS_TOKEN"
				return p, nil
			}
		}
		return nil, errors.New("invalid bearer token")
	}
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil, errors.New("not authenticated")
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	var userID, csrf, method string
	err = s.DB.QueryRow(r.Context(), `SELECT user_id,csrf_token,auth_method FROM sessions WHERE id_hash=$1 AND expires_at>now()`, digest[:]).Scan(&userID, &csrf, &method)
	if err != nil {
		return nil, errors.New("session expired")
	}
	p, err := s.loadPrincipal(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	p.AuthMethod = method
	p.CSRFToken = csrf
	_, _ = s.DB.Exec(r.Context(), `UPDATE sessions SET last_seen_at=now() WHERE id_hash=$1 AND last_seen_at < now()-interval '5 minutes'`, digest[:])
	return p, nil
}

func (s *Service) authenticateKey(ctx context.Context, raw, ip string) (*Principal, error) {
	parts := strings.SplitN(raw, "_", 3)
	if len(parts) != 3 || parts[0] != "relio" || parts[1] == "" || parts[2] == "" {
		return nil, errors.New("invalid access key")
	}
	digest := s.Secrets.Digest(raw)
	var dbID, userID, keyID, status string
	var scopes, channels []string
	var stored []byte
	var expires *time.Time
	var grace *time.Time
	err := s.DB.QueryRow(ctx, `SELECT id,user_id,key_id,secret_digest,scopes,channels,status,expires_at,grace_expires_at FROM personal_keys WHERE key_id=$1`, parts[1]).Scan(&dbID, &userID, &keyID, &stored, &scopes, &channels, &status, &expires, &grace)
	if err != nil {
		return nil, errors.New("invalid access key")
	}
	if subtle.ConstantTimeCompare(stored, digest) != 1 {
		return nil, errors.New("invalid access key")
	}
	now := time.Now()
	valid := status == "ACTIVE" || (status == "ROTATING" && grace != nil && grace.After(now))
	if !valid || (expires != nil && expires.Before(now)) {
		return nil, errors.New("access key expired or revoked")
	}
	p, err := s.loadPrincipal(ctx, userID)
	if err != nil {
		return nil, err
	}
	p.AuthMethod = "PERSONAL_KEY"
	p.KeyID = keyID
	p.KeyDBID = dbID
	p.KeyScopes = scopes
	p.KeyChannels = channels
	_, _ = s.DB.Exec(ctx, `UPDATE personal_keys SET last_used_at=now(),last_used_ip=NULLIF($2,'')::inet WHERE id=$1`, dbID, ip)
	return p, nil
}

func newPrincipal(userID string) *Principal {
	return &Principal{UserID: userID, perm: map[string]bool{}, Permissions: []string{}, DataScope: "USER"}
}

func (s *Service) loadPrincipal(ctx context.Context, userID string) (*Principal, error) {
	// Keep collection fields non-nil. OIDC users can legitimately be provisioned
	// before a default/mapped role is assigned; JSON null would make clients that
	// apply permission checks with Array.includes fail during initial render.
	p := newPrincipal(userID)
	var email, org, manager *string
	err := s.DB.QueryRow(ctx, `SELECT username,display_name,email,organization_id,manager_id,is_bootstrap,must_change_password FROM users WHERE id=$1 AND active=true`, userID).Scan(&p.Username, &p.DisplayName, &email, &org, &manager, &p.IsBootstrap, &p.MustChangePassword)
	if err != nil {
		return nil, err
	}
	if email != nil {
		p.Email = *email
	}
	if org != nil {
		p.OrganizationID = *org
	}
	if manager != nil {
		p.ManagerID = *manager
	}
	rows, err := s.DB.Query(ctx, `SELECT r.data_scope,rp.permission FROM user_roles ur JOIN roles r ON r.id=ur.role_id LEFT JOIN role_permissions rp ON rp.role_id=r.id WHERE ur.user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rank := map[string]int{"USER": 1, "TEAM": 2, "DEPARTMENT": 3, "DIVISION": 4, "COMPANY": 5}
	for rows.Next() {
		var scope string
		var permission *string
		if err = rows.Scan(&scope, &permission); err != nil {
			return nil, err
		}
		if rank[scope] > rank[p.DataScope] {
			p.DataScope = scope
		}
		if permission != nil {
			p.perm[*permission] = true
		}
	}
	for permission := range p.perm {
		p.Permissions = append(p.Permissions, permission)
	}
	sort.Strings(p.Permissions)
	return p, rows.Err()
}

func (s *Service) Logout(ctx context.Context, token string) {
	digest := sha256.Sum256([]byte(token))
	_, _ = s.DB.Exec(ctx, `DELETE FROM sessions WHERE id_hash=$1`, digest[:])
}

func (s *Service) ChangePassword(ctx context.Context, p *Principal, current, next string) error {
	var hash string
	if err := s.DB.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 AND auth_source='LOCAL'`, p.UserID).Scan(&hash); err != nil {
		return errors.New("local password is not available")
	}
	if !VerifyPassword(hash, current) {
		return errors.New("current password is incorrect")
	}
	newHash, err := HashPassword(next)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `UPDATE users SET password_hash=$2,must_change_password=false,updated_at=now(),version=version+1 WHERE id=$1`, p.UserID, newHash)
	return err
}

func (s *Service) SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.sessionDuration(r.Context()).Seconds())})
}
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

func Require(p *Principal, permission string) error {
	if p == nil || !p.Has(permission) {
		return fmt.Errorf("permission %s is required", permission)
	}
	return nil
}
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
