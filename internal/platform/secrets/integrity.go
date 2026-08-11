package secrets

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const registryLockID int64 = 733_541_122_020_269

// OriginEnv and OriginFile name the two places the wrapping key can come from.
// ENCRYPTION_KEY is portable across volumes; the file lives in /var/lib/relio
// and is lost whenever that volume is recreated.
const (
	OriginEnv  = "ENCRYPTION_KEY"
	OriginFile = "FILE"
)

var ErrRecoveryRequired = errors.New("instance master key recovery required")

type IntegrityStatus struct {
	// KeyID identifies the data encryption key that actually protects
	// credentials. It stays stable for the lifetime of the installation.
	KeyID string `json:"keyId"`
	// WrapOrigin, WrapKeyID and WrapFormat describe the key that currently
	// wraps the data key. Rotating ENCRYPTION_KEY changes these without
	// touching a single stored credential.
	WrapOrigin string `json:"wrapOrigin"`
	WrapKeyID  string `json:"wrapKeyId"`
	WrapFormat string `json:"wrapFormat,omitempty"`
	// Portable is true when a restart no longer depends on /var/lib/relio.
	Portable             bool `json:"portable"`
	EnvConfigured        bool `json:"envConfigured"`
	FilePresent          bool `json:"filePresent"`
	Registered           bool `json:"registered"`
	Matches              bool `json:"matches"`
	ProtectedCredentials int  `json:"protectedCredentials"`
	Created              bool `json:"created,omitempty"`
	Adopted              bool `json:"adopted,omitempty"`
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func protectedMaterialCount(ctx context.Context, db rowQuerier) (int, error) {
	var count int
	err := db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM oidc_providers WHERE client_secret_encrypted<>'') +
		(SELECT count(*) FROM system_settings WHERE secret_yn=true AND COALESCE(value #>> '{}','')<>'') +
		(SELECT count(*) FROM personal_keys WHERE status IN ('ACTIVE','ROTATING'))`).Scan(&count)
	return count, err
}

func registeredFingerprint(ctx context.Context, db rowQuerier) (string, error) {
	var fingerprint string
	err := db.QueryRow(ctx, `SELECT fingerprint FROM instance_key_registry WHERE singleton=true`).Scan(&fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return fingerprint, err
}

type envelope struct {
	wrapped         []byte
	origin          string
	wrapFingerprint string
	dekFingerprint  string
}

func readEnvelope(ctx context.Context, db rowQuerier) (envelope, bool, error) {
	var e envelope
	err := db.QueryRow(ctx, `SELECT wrapped_dek,wrap_origin,wrap_fingerprint,dek_fingerprint FROM instance_data_key WHERE singleton=true`).Scan(&e.wrapped, &e.origin, &e.wrapFingerprint, &e.dekFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return envelope{}, false, nil
	}
	if err != nil {
		return envelope{}, false, err
	}
	return e, true, nil
}

func sameFingerprint(left, right string) bool {
	if len(left) != len(right) || left == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func verifyEncryptedValues(ctx context.Context, tx pgx.Tx, manager *Manager) error {
	rows, err := tx.Query(ctx, `SELECT id::text,client_secret_encrypted FROM oidc_providers WHERE client_secret_encrypted<>''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, encrypted string
		if err = rows.Scan(&id, &encrypted); err != nil {
			rows.Close()
			return err
		}
		if _, err = manager.Decrypt(encrypted); err != nil {
			rows.Close()
			return fmt.Errorf("OIDC provider %s cannot be decrypted: %w", id, ErrRecoveryRequired)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	rows, err = tx.Query(ctx, `SELECT namespace,key,value FROM system_settings WHERE secret_yn=true AND COALESCE(value #>> '{}','')<>''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var namespace, key string
		var raw []byte
		if err = rows.Scan(&namespace, &key, &raw); err != nil {
			return err
		}
		var encrypted string
		if err = json.Unmarshal(raw, &encrypted); err != nil {
			return fmt.Errorf("secret setting %s.%s is malformed: %w", namespace, key, ErrRecoveryRequired)
		}
		if _, err = manager.Decrypt(encrypted); err != nil {
			return fmt.Errorf("secret setting %s.%s cannot be decrypted: %w", namespace, key, ErrRecoveryRequired)
		}
	}
	return rows.Err()
}

// wrappingKey resolves the key that protects the data encryption key.
// ENCRYPTION_KEY always wins so a deployment can stop depending on the
// /var/lib/relio volume without any migration step.
func wrappingKey(encryptionKey, path string) (wrap *Manager, origin string, format KeyFormat, file *Manager, err error) {
	file, fileErr := load(path)
	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		return nil, "", "", nil, fmt.Errorf("read master key: %w", fileErr)
	}
	if encryptionKey != "" {
		material, keyFormat, parseErr := ParseKeyMaterial(encryptionKey)
		if parseErr != nil {
			return nil, "", "", nil, parseErr
		}
		wrap, parseErr = NewManager(material)
		if parseErr != nil {
			return nil, "", "", nil, parseErr
		}
		return wrap, OriginEnv, keyFormat, file, nil
	}
	// Without ENCRYPTION_KEY the file remains the only wrapping key, so it is
	// created on a fresh installation exactly as earlier releases did.
	wrap, err = LoadOrCreate(path)
	if err != nil {
		return nil, "", "", nil, err
	}
	return wrap, OriginFile, "", wrap, nil
}

// candidates lists the keys that may already protect stored credentials, most
// likely first. The file key is a candidate even when ENCRYPTION_KEY is set
// because that is exactly the state of an existing deployment on the boot where
// the operator introduces the environment variable.
func candidates(wrap, file *Manager) []*Manager {
	out := []*Manager{wrap}
	if file != nil && !sameFingerprint(file.Fingerprint(), wrap.Fingerprint()) {
		out = append(out, file)
	}
	return out
}

func recordKeyEvent(ctx context.Context, tx pgx.Tx, event, from, to, fingerprint string) error {
	_, err := tx.Exec(ctx, `INSERT INTO instance_data_key_events(id,event,from_origin,to_origin,wrap_fingerprint) VALUES($1,$2,NULLIF($3,''),$4,$5)`, ids.New(), event, from, to, fingerprint)
	return err
}

// Resolve returns the data encryption key for this instance and refuses to start
// when the presented wrapping key cannot open it. Startup never silently
// generates a replacement, because that would invalidate every Personal Key and
// SSO Client Secret while still looking like a successful restart.
func Resolve(ctx context.Context, db *pgxpool.Pool, encryptionKey, path string) (*Manager, IntegrityStatus, error) {
	status := IntegrityStatus{EnvConfigured: encryptionKey != ""}
	if _, err := os.Lstat(path); err == nil {
		status.FilePresent = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, status, fmt.Errorf("inspect master key: %w", err)
	}

	protected, err := protectedMaterialCount(ctx, db)
	if err != nil {
		return nil, status, fmt.Errorf("inspect protected credentials: %w", err)
	}
	status.ProtectedCredentials = protected

	if !status.EnvConfigured && !status.FilePresent {
		registered, err := registeredFingerprint(ctx, db)
		if err != nil {
			return nil, status, fmt.Errorf("read master key registry: %w", err)
		}
		stored, found, err := readEnvelope(ctx, db)
		if err != nil {
			return nil, status, fmt.Errorf("read instance data key: %w", err)
		}
		// Name the key that actually protects the data so the operator knows
		// whether to restore a volume or re-supply an environment variable.
		if found {
			status.WrapOrigin = stored.origin
			return nil, status, fmt.Errorf("%w: the instance data key is wrapped by %s and neither that value nor %s is available; restore that exact key value", ErrRecoveryRequired, stored.origin, path)
		}
		if registered != "" || protected > 0 {
			return nil, status, fmt.Errorf("%w: %s is missing while PostgreSQL contains a registered key or %d protected credential(s); set %s to the original value or restore the matching relio-data volume", ErrRecoveryRequired, path, protected, OriginEnv)
		}
	}

	wrap, origin, format, file, err := wrappingKey(encryptionKey, path)
	if err != nil {
		return nil, status, err
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		status.FilePresent = true
	}
	status.WrapOrigin = origin
	status.WrapKeyID = wrap.KeyID()
	status.WrapFormat = string(format)
	status.Portable = origin == OriginEnv

	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, status, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, registryLockID); err != nil {
		return nil, status, err
	}

	resolved, err := resolveDataKey(ctx, tx, wrap, origin, file, protected)
	if err != nil {
		return nil, status, err
	}
	data := resolved.key
	status.KeyID = data.KeyID()
	status.Created = resolved.created
	status.Adopted = resolved.event == "ADOPTED"

	if resolved.event != "" {
		wrapped, err := wrap.Wrap(data)
		if err != nil {
			return nil, status, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO instance_data_key(singleton,wrapped_dek,wrap_origin,wrap_fingerprint,dek_fingerprint) VALUES(true,$1,$2,$3,$4)
		ON CONFLICT(singleton) DO UPDATE SET wrapped_dek=excluded.wrapped_dek,wrap_origin=excluded.wrap_origin,wrap_fingerprint=excluded.wrap_fingerprint,dek_fingerprint=excluded.dek_fingerprint,updated_at=now()`, wrapped, origin, wrap.Fingerprint(), data.Fingerprint())
		if err != nil {
			return nil, status, err
		}
		if err = recordKeyEvent(ctx, tx, resolved.event, resolved.previousOrigin, origin, wrap.Fingerprint()); err != nil {
			return nil, status, err
		}
	}

	registered, err := registeredFingerprint(ctx, tx)
	if err != nil {
		return nil, status, err
	}
	if registered == "" {
		if _, err = tx.Exec(ctx, `INSERT INTO instance_key_registry(singleton,fingerprint) VALUES(true,$1)`, data.Fingerprint()); err != nil {
			return nil, status, err
		}
	} else if !sameFingerprint(registered, data.Fingerprint()) {
		return nil, status, fmt.Errorf("%w: PostgreSQL is registered to a different data key; restore the original %s value or the matching relio-data volume instead of generating a new key", ErrRecoveryRequired, OriginEnv)
	}
	status.Registered = true

	if err = verifyEncryptedValues(ctx, tx, data); err != nil {
		return nil, status, fmt.Errorf("verify protected credentials: %w; restore the original %s value or the matching relio-data volume and database from the same recovery point", err, OriginEnv)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, status, err
	}
	status.Matches = true
	return data, status, nil
}

type resolvedKey struct {
	key *Manager
	// event is empty when the stored envelope is already correct and no write
	// is needed. Otherwise it is the audit event to record.
	event          string
	previousOrigin string
	created        bool
}

// resolveDataKey finds the data encryption key that already protects stored
// credentials, or creates one for a fresh installation. It never substitutes a
// new key for credentials that are already encrypted.
func resolveDataKey(ctx context.Context, tx pgx.Tx, wrap *Manager, origin string, file *Manager, protected int) (resolvedKey, error) {
	stored, found, err := readEnvelope(ctx, tx)
	if err != nil {
		return resolvedKey{}, fmt.Errorf("read instance data key: %w", err)
	}
	if found {
		if data, err := wrap.Unwrap(stored.wrapped); err == nil {
			if stored.origin == origin && sameFingerprint(stored.wrapFingerprint, wrap.Fingerprint()) {
				return resolvedKey{key: data}, nil
			}
			// Same key value presented through a different origin: re-record
			// where it came from so diagnostics stay truthful.
			return resolvedKey{key: data, event: "REWRAPPED", previousOrigin: stored.origin}, nil
		}
		// The presented key is new. Adopting it is only safe when a key that
		// does open the envelope is also available on this boot.
		for _, candidate := range candidates(wrap, file) {
			if candidate == wrap {
				continue
			}
			if data, err := candidate.Unwrap(stored.wrapped); err == nil {
				return resolvedKey{key: data, event: "ADOPTED", previousOrigin: stored.origin}, nil
			}
		}
		return resolvedKey{}, fmt.Errorf("%w: the instance data key is wrapped by %s and the presented key cannot open it; restore that exact key value", ErrRecoveryRequired, stored.origin)
	}

	// No envelope yet. Releases up to 1.5 encrypted credentials directly with
	// the master key, so that key becomes the data key and is wrapped in place.
	registered, err := registeredFingerprint(ctx, tx)
	if err != nil {
		return resolvedKey{}, err
	}
	if registered != "" {
		for _, candidate := range candidates(wrap, file) {
			if sameFingerprint(candidate.Fingerprint(), registered) {
				return resolvedKey{key: candidate, event: adoptionEvent(candidate, wrap)}, nil
			}
		}
		return resolvedKey{}, fmt.Errorf("%w: PostgreSQL is registered to a different master key; restore the original %s value or the matching relio-data volume", ErrRecoveryRequired, OriginEnv)
	}
	if protected > 0 {
		// Upgraded from a release that never registered a fingerprint. Use the
		// stored ciphertext itself to decide which key is the real one.
		for _, candidate := range candidates(wrap, file) {
			if verifyEncryptedValues(ctx, tx, candidate) == nil {
				return resolvedKey{key: candidate, event: adoptionEvent(candidate, wrap)}, nil
			}
		}
		return resolvedKey{}, fmt.Errorf("%w: %d protected credential(s) cannot be decrypted by any available key; restore the original %s value or the matching relio-data volume", ErrRecoveryRequired, protected, OriginEnv)
	}
	data, err := GenerateDataKey()
	if err != nil {
		return resolvedKey{}, err
	}
	return resolvedKey{key: data, event: "INITIALIZED", created: true}, nil
}

func adoptionEvent(data, wrap *Manager) string {
	if data == wrap {
		return "INITIALIZED"
	}
	return "ADOPTED"
}

// Inspect reports live key protection for the administrator diagnostics screen.
// It never returns key material, only one-way identifiers.
func Inspect(ctx context.Context, db *pgxpool.Pool, manager *Manager, path string, envConfigured bool) IntegrityStatus {
	status := IntegrityStatus{KeyID: manager.KeyID(), EnvConfigured: envConfigured}
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Size() == 32 {
		status.FilePresent = true
	}
	if stored, found, err := readEnvelope(ctx, db); err == nil && found {
		status.WrapOrigin = stored.origin
		status.WrapKeyID = stored.wrapFingerprint[:12]
		status.Portable = stored.origin == OriginEnv
		status.Matches = sameFingerprint(stored.dekFingerprint, manager.Fingerprint())
	}
	registered, err := registeredFingerprint(ctx, db)
	if err == nil && registered != "" {
		status.Registered = true
		status.Matches = status.Matches && sameFingerprint(registered, manager.Fingerprint())
	}
	status.ProtectedCredentials, _ = protectedMaterialCount(ctx, db)
	return status
}
