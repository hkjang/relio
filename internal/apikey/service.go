package apikey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var AllowedScopes = []string{"customer:read", "customer:write", "customer:delete", "contact:read", "contact:write", "lead:read", "lead:write", "opportunity:read", "opportunity:write", "activity:read", "activity:write", "product:read", "product:write", "quotation:read", "quotation:write", "contract:read", "contract:write", "sales:read", "sales:write", "target:read", "target:write", "forecast:read", "notification:read", "notification:write", "report:read", "approval:request", "approval:approve", "mcp:use"}

type Key struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	KeyID          string     `json:"keyId"`
	Scopes         []string   `json:"scopes"`
	Channels       []string   `json:"channels"`
	Status         string     `json:"status"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	GraceExpiresAt *time.Time `json:"graceExpiresAt,omitempty"`
	LastUsedAt     *time.Time `json:"lastUsedAt,omitempty"`
	LastUsedIP     string     `json:"lastUsedIp,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}
type AdminKey struct {
	Key
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}
type CreateInput struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	Channels  []string   `json:"channels"`
	ExpiresAt *time.Time `json:"expiresAt"`
}
type Created struct {
	Key     Key    `json:"key"`
	Secret  string `json:"secret"`
	Warning string `json:"warning"`
}
type Service struct {
	DB      *pgxpool.Pool
	Secrets *secrets.Manager
	Audit   *audit.Service
}

func (s *Service) policyInt(ctx context.Context, key string, fallback int) int {
	var v int
	if err := s.DB.QueryRow(ctx, `SELECT (value #>> '{}')::int FROM system_settings WHERE namespace='keys' AND key=$1`, key).Scan(&v); err != nil {
		return fallback
	}
	return v
}
func (s *Service) policyBool(ctx context.Context, key string, fallback bool) bool {
	var v bool
	if err := s.DB.QueryRow(ctx, `SELECT (value #>> '{}')::boolean FROM system_settings WHERE namespace='keys' AND key=$1`, key).Scan(&v); err != nil {
		return fallback
	}
	return v
}
func allowedScope(v string) bool {
	for _, a := range AllowedScopes {
		if v == a {
			return true
		}
	}
	return false
}
func validChannels(channels []string) bool {
	if len(channels) == 0 {
		return false
	}
	for _, c := range channels {
		if c != "REST" && c != "MCP" {
			return false
		}
	}
	return true
}

func (s *Service) List(ctx context.Context, p *auth.Principal, userID string, admin bool) ([]Key, error) {
	if userID == "" {
		userID = p.UserID
	}
	if userID != p.UserID && !admin {
		return nil, errors.New("access denied")
	}
	rows, err := s.DB.Query(ctx, `SELECT id,key_name,key_id,scopes,channels,status,expires_at,grace_expires_at,last_used_at,COALESCE(last_used_ip::text,''),created_at FROM personal_keys WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err = rows.Scan(&k.ID, &k.Name, &k.KeyID, &k.Scopes, &k.Channels, &k.Status, &k.ExpiresAt, &k.GraceExpiresAt, &k.LastUsedAt, &k.LastUsedIP, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Service) ListAll(ctx context.Context, p *auth.Principal, userID string) ([]AdminKey, error) {
	if err := auth.Require(p, "admin:read"); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT k.id,k.key_name,k.key_id,k.scopes,k.channels,k.status,k.expires_at,k.grace_expires_at,k.last_used_at,COALESCE(k.last_used_ip::text,''),k.created_at,u.id,u.username,u.display_name FROM personal_keys k JOIN users u ON u.id=k.user_id WHERE ($1='' OR u.id::text=$1) ORDER BY k.created_at DESC LIMIT 1000`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminKey{}
	for rows.Next() {
		var item AdminKey
		if err = rows.Scan(&item.ID, &item.Name, &item.KeyID, &item.Scopes, &item.Channels, &item.Status, &item.ExpiresAt, &item.GraceExpiresAt, &item.LastUsedAt, &item.LastUsedIP, &item.CreatedAt, &item.UserID, &item.Username, &item.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in CreateInput, ip, requestID, ua string) (Created, error) {
	return s.create(ctx, p, in, ip, requestID, ua, false)
}

func (s *Service) create(ctx context.Context, p *auth.Principal, in CreateInput, ip, requestID, ua string, allowRotationReplacement bool) (Created, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Created{}, errors.New("key name is required")
	}
	max := s.policyInt(ctx, "max_per_user", 10)
	var count int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM personal_keys WHERE user_id=$1 AND status IN ('ACTIVE','ROTATING')`, p.UserID).Scan(&count)
	if count >= max && !allowRotationReplacement {
		return Created{}, fmt.Errorf("maximum of %d active keys reached", max)
	}
	if !validChannels(in.Channels) {
		return Created{}, errors.New("channels must contain REST and/or MCP")
	}
	for _, c := range in.Channels {
		if c == "REST" && !s.policyBool(ctx, "api_enabled", true) {
			return Created{}, errors.New("personal REST keys are disabled")
		}
		if c == "MCP" && !s.policyBool(ctx, "mcp_enabled", true) {
			return Created{}, errors.New("personal MCP keys are disabled")
		}
	}
	if len(in.Scopes) == 0 {
		return Created{}, errors.New("at least one scope is required")
	}
	for _, scope := range in.Scopes {
		if !allowedScope(scope) {
			return Created{}, fmt.Errorf("scope %s is not allowed", scope)
		}
		if !p.IsBootstrap && !hasUserPermission(p, scope) {
			return Created{}, fmt.Errorf("scope %s exceeds user permission", scope)
		}
	}
	now := time.Now()
	defaultDays, maxDays := s.policyInt(ctx, "default_lifetime_days", 365), s.policyInt(ctx, "max_lifetime_days", 730)
	expires := in.ExpiresAt
	if expires == nil {
		v := now.AddDate(0, 0, defaultDays)
		expires = &v
	}
	if expires.Before(now) || expires.After(now.AddDate(0, 0, maxDays)) {
		return Created{}, fmt.Errorf("expiry must be within %d days", maxDays)
	}
	dbID, keyID := ids.New(), ids.HexToken(6)
	raw := "relio_" + keyID + "_" + ids.Token(32)
	digest := s.Secrets.Digest(raw)
	_, err := s.DB.Exec(ctx, `INSERT INTO personal_keys(id,user_id,key_name,key_id,secret_digest,scopes,channels,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, dbID, p.UserID, strings.TrimSpace(in.Name), keyID, digest, in.Scopes, in.Channels, expires)
	if err != nil {
		return Created{}, err
	}
	_, _ = s.DB.Exec(ctx, `INSERT INTO personal_key_history(id,key_id,action,actor_id,details) VALUES($1,$2,'CREATE',$3,jsonb_build_object('scopes',$4::text[],'channels',$5::text[]))`, ids.New(), dbID, p.UserID, in.Scopes, in.Channels)
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "WEB", Action: "KEY_CREATE", Resource: "personal_key", ResourceID: dbID, After: map[string]any{"keyId": keyID, "scopes": in.Scopes, "channels": in.Channels}, IP: ip, RequestID: requestID, UserAgent: ua})
	items, _ := s.List(ctx, p, p.UserID, false)
	var key Key
	for _, v := range items {
		if v.ID == dbID {
			key = v
			break
		}
	}
	return Created{Key: key, Secret: raw, Warning: "이 Secret은 지금 한 번만 표시됩니다. 안전한 곳에 보관하세요."}, nil
}
func hasUserPermission(p *auth.Principal, scope string) bool { return p.Has(scope) }

func (s *Service) Rotate(ctx context.Context, p *auth.Principal, id, ip, requestID, ua string) (Created, error) {
	var in CreateInput
	var owner, status string
	err := s.DB.QueryRow(ctx, `SELECT user_id,key_name,scopes,channels,status,expires_at FROM personal_keys WHERE id=$1`, id).Scan(&owner, &in.Name, &in.Scopes, &in.Channels, &status, &in.ExpiresAt)
	if err != nil {
		return Created{}, err
	}
	if owner != p.UserID {
		return Created{}, errors.New("access denied")
	}
	if status != "ACTIVE" {
		return Created{}, errors.New("only an active key can be rotated")
	}
	created, err := s.create(ctx, p, in, ip, requestID, ua, true)
	if err != nil {
		return Created{}, err
	}
	grace := s.policyInt(ctx, "rotation_grace_hours", 24)
	graceAt := time.Now().Add(time.Duration(grace) * time.Hour)
	_, err = s.DB.Exec(ctx, `UPDATE personal_keys SET status='ROTATING',grace_expires_at=$2 WHERE id=$1 AND status='ACTIVE'`, id, graceAt)
	if err != nil {
		return Created{}, err
	}
	_, _ = s.DB.Exec(ctx, `UPDATE personal_keys SET rotation_parent_id=$2 WHERE id=$1`, created.Key.ID, id)
	_, _ = s.DB.Exec(ctx, `INSERT INTO personal_key_history(id,key_id,action,actor_id,details) VALUES($1,$2,'ROTATE',$3,jsonb_build_object('replacementId',$4,'graceExpiresAt',$5))`, ids.New(), id, p.UserID, created.Key.ID, graceAt)
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "WEB", Action: "KEY_ROTATE", Resource: "personal_key", ResourceID: id, After: map[string]any{"replacementId": created.Key.ID, "graceExpiresAt": graceAt}, IP: ip, RequestID: requestID, UserAgent: ua})
	return created, nil
}

func (s *Service) Revoke(ctx context.Context, p *auth.Principal, id, ip, requestID, ua string, admin bool) error {
	var owner, keyID string
	if err := s.DB.QueryRow(ctx, `SELECT user_id,key_id FROM personal_keys WHERE id=$1`, id).Scan(&owner, &keyID); err != nil {
		return err
	}
	if owner != p.UserID && !admin {
		return errors.New("access denied")
	}
	cmd, err := s.DB.Exec(ctx, `UPDATE personal_keys SET status='REVOKED',revoked_at=now() WHERE id=$1 AND status<>'REVOKED'`, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return errors.New("key is already revoked")
	}
	_, _ = s.DB.Exec(ctx, `INSERT INTO personal_key_history(id,key_id,action,actor_id) VALUES($1,$2,'REVOKE',$3)`, ids.New(), id, p.UserID)
	channel := "WEB"
	if admin {
		channel = "ADMIN"
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: channel, Action: "KEY_REVOKE", Resource: "personal_key", ResourceID: id, After: map[string]any{"keyId": keyID}, IP: ip, RequestID: requestID, UserAgent: ua})
	return nil
}

func (s *Service) RevokeAll(ctx context.Context, p *auth.Principal, userID, ip, requestID, ua string) (int64, error) {
	if err := auth.Require(p, "admin:write"); err != nil {
		return 0, err
	}
	var username string
	if err := s.DB.QueryRow(ctx, `SELECT username FROM users WHERE id=$1`, userID).Scan(&username); err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `UPDATE personal_keys SET status='REVOKED',revoked_at=now() WHERE user_id=$1 AND status IN ('ACTIVE','ROTATING') RETURNING id`, userID)
	if err != nil {
		return 0, err
	}
	idsToRecord := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		idsToRecord = append(idsToRecord, id)
	}
	rows.Close()
	for _, id := range idsToRecord {
		if _, err = tx.Exec(ctx, `INSERT INTO personal_key_history(id,key_id,action,actor_id,details) VALUES($1,$2,'ADMIN_REVOKE',$3,jsonb_build_object('all',true))`, ids.New(), id, p.UserID); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	count := int64(len(idsToRecord))
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "USER_KEYS_REVOKE_ALL", Resource: "user", ResourceID: userID, After: map[string]any{"username": username, "revokedCount": count}, IP: ip, RequestID: requestID, UserAgent: ua})
	return count, nil
}

func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
