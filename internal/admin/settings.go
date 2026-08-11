package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Setting struct {
	Namespace       string `json:"namespace"`
	Key             string `json:"key"`
	Value           any    `json:"value"`
	ValueType       string `json:"valueType"`
	Secret          bool   `json:"secret"`
	Configured      bool   `json:"configured"`
	RestartRequired bool   `json:"restartRequired"`
	Version         int    `json:"version"`
}

type SettingsService struct {
	DB      *pgxpool.Pool
	Secrets *secrets.Manager
	Audit   *audit.Service
}

func (s *SettingsService) List(ctx context.Context, namespace string) ([]Setting, error) {
	query := `SELECT namespace,key,value,value_type,secret_yn,restart_required,version FROM system_settings`
	args := []any{}
	if namespace != "" {
		query += ` WHERE namespace=$1`
		args = append(args, namespace)
	}
	query += ` ORDER BY namespace,key`
	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var item Setting
		var raw []byte
		if err = rows.Scan(&item.Namespace, &item.Key, &raw, &item.ValueType, &item.Secret, &item.RestartRequired, &item.Version); err != nil {
			return nil, err
		}
		if item.Secret {
			var encrypted string
			_ = json.Unmarshal(raw, &encrypted)
			item.Configured = encrypted != ""
			item.Value = ""
		} else {
			_ = json.Unmarshal(raw, &item.Value)
			item.Configured = true
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SettingsService) Get(ctx context.Context, namespace, key string, target any) error {
	var raw []byte
	var secret bool
	if err := s.DB.QueryRow(ctx, `SELECT value,secret_yn FROM system_settings WHERE namespace=$1 AND key=$2`, namespace, key).Scan(&raw, &secret); err != nil {
		return err
	}
	if secret {
		var encrypted string
		if err := json.Unmarshal(raw, &encrypted); err != nil {
			return err
		}
		plain, err := s.Secrets.Decrypt(encrypted)
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(plain), target)
	}
	return json.Unmarshal(raw, target)
}

func validSettingName(v string) bool {
	if v == "" || len(v) > 80 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func emptySecretValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func (s *SettingsService) Put(ctx context.Context, p *auth.Principal, item Setting, ip, requestID, ua string) error {
	if !p.Has("admin:write") && !p.IsBootstrap {
		return errors.New("admin:write permission is required")
	}
	item.Namespace = strings.ToLower(strings.TrimSpace(item.Namespace))
	item.Key = strings.ToLower(strings.TrimSpace(item.Key))
	if !validSettingName(item.Namespace) || !validSettingName(item.Key) {
		return errors.New("invalid setting namespace or key")
	}
	var before []byte
	var oldSecret bool
	var oldVersion int
	existingErr := s.DB.QueryRow(ctx, `SELECT value,secret_yn,version FROM system_settings WHERE namespace=$1 AND key=$2`, item.Namespace, item.Key).Scan(&before, &oldSecret, &oldVersion)
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return existingErr
	}
	existing := existingErr == nil
	if existing && oldSecret && !item.Secret {
		return errors.New("an existing secret setting cannot be converted to plaintext")
	}
	if existing && item.Version != 0 && item.Version != oldVersion {
		return errors.New("setting was changed by another user")
	}
	raw, err := json.Marshal(item.Value)
	if err != nil {
		return err
	}
	if item.Secret {
		if existing && oldSecret && emptySecretValue(item.Value) {
			raw = before
		} else {
			if emptySecretValue(item.Value) {
				return errors.New("a new secret setting requires a value")
			}
			encrypted, err := s.Secrets.Encrypt(string(raw))
			if err != nil {
				return err
			}
			raw, _ = json.Marshal(encrypted)
		}
	}
	if item.ValueType == "" {
		item.ValueType = "json"
	}
	command, err := s.DB.Exec(ctx, `INSERT INTO system_settings(namespace,key,value,value_type,secret_yn,restart_required,updated_by,version) VALUES($1,$2,$3,$4,$5,$6,$7,1)
	ON CONFLICT(namespace,key) DO UPDATE SET value=excluded.value,value_type=excluded.value_type,secret_yn=excluded.secret_yn,restart_required=excluded.restart_required,updated_by=excluded.updated_by,updated_at=now(),version=system_settings.version+1 WHERE system_settings.version=$8`, item.Namespace, item.Key, raw, item.ValueType, item.Secret, item.RestartRequired, p.UserID, oldVersion)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("setting was changed by another user")
	}
	beforeAudit := any(json.RawMessage(before))
	if oldSecret {
		beforeAudit = map[string]any{"value": "***", "secret": true}
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "SETTING_UPDATE", Resource: item.Namespace + "." + item.Key, Before: beforeAudit, After: map[string]any{"value": func() any {
		if item.Secret {
			return "***"
		}
		return item.Value
	}(), "secret": item.Secret}, IP: ip, RequestID: requestID, UserAgent: ua})
	return nil
}

// Delete removes a stored setting so the compiled-in default applies again. It
// is the only way to undo a one-off override without guessing what the original
// value was.
func (s *SettingsService) Delete(ctx context.Context, p *auth.Principal, namespace, key, ip, requestID, ua string) error {
	if !p.Has("admin:write") && !p.IsBootstrap {
		return errors.New("admin:write permission is required")
	}
	if !validSettingName(namespace) || !validSettingName(key) {
		return errors.New("invalid setting namespace or key")
	}
	var before []byte
	var secret bool
	err := s.DB.QueryRow(ctx, `SELECT value,secret_yn FROM system_settings WHERE namespace=$1 AND key=$2`, namespace, key).Scan(&before, &secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("setting not found")
	}
	if err != nil {
		return err
	}
	if _, err = s.DB.Exec(ctx, `DELETE FROM system_settings WHERE namespace=$1 AND key=$2`, namespace, key); err != nil {
		return err
	}
	beforeAudit := any(json.RawMessage(before))
	if secret {
		beforeAudit = map[string]any{"value": "***", "secret": true}
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "SETTING_DELETE", Resource: namespace + "." + key, Before: beforeAudit, After: map[string]any{"deleted": true, "secret": secret}, IP: ip, RequestID: requestID, UserAgent: ua})
	return nil
}
