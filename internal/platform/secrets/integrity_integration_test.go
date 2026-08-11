package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/platform/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the real startup path against PostgreSQL, because the
// whole point of the envelope is what happens across restarts and volume loss.
// Run them with:
//
//	RELIO_TEST_POSTGRES_DSN=postgres://relio:pw@127.0.0.1:5432/relio go test ./internal/platform/secrets/
const dsnEnv = "RELIO_TEST_POSTGRES_DSN"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(dsnEnv))
	if dsn == "" {
		t.Skipf("set %s to run the credential continuity integration tests", dsnEnv)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	// Every test starts from an empty schema so one run cannot bias the next.
	if _, err = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// seedProtectedMaterial stores one encrypted OIDC Client Secret and one Personal
// Key digest so the assertions below cover both encryption and HMAC.
func seedProtectedMaterial(t *testing.T, pool *pgxpool.Pool, manager *Manager) (encrypted string, digest []byte) {
	t.Helper()
	ctx := context.Background()
	var adminID string
	err := pool.QueryRow(ctx, `INSERT INTO organizations(id,name,code,org_type) VALUES(gen_random_uuid(),'T','T','COMPANY') RETURNING id`).Scan(&adminID)
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	err = pool.QueryRow(ctx, `INSERT INTO users(id,username,display_name,auth_source,organization_id,active) VALUES(gen_random_uuid(),'continuity','Continuity','LOCAL',$1,true) RETURNING id`, adminID).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err = manager.Encrypt("confidential-client-secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO oidc_providers(id,enabled,issuer_url,client_id,client_secret_encrypted,scopes,updated_by) VALUES(gen_random_uuid(),true,'https://keycloak.invalid/realms/t','relio',$1,ARRAY['openid'],$2)`, encrypted, userID)
	if err != nil {
		t.Fatal(err)
	}
	digest = manager.Digest("relio_abc_secret")
	_, err = pool.Exec(ctx, `INSERT INTO personal_keys(id,user_id,key_name,key_id,secret_digest,scopes,channels,status) VALUES(gen_random_uuid(),$1,'k','abc',$2,ARRAY['customer:read'],ARRAY['REST'],'ACTIVE')`, userID, digest)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted, digest
}

func TestResolveKeepsCredentialsWhenTheVolumeIsLost(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const encryptionKey = "relio-integration-encryption-key-value"
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets", "master.key")

	// First boot with only the volume, exactly like an existing deployment.
	first, status, err := Resolve(ctx, pool, "", path)
	if err != nil {
		t.Fatal(err)
	}
	if status.WrapOrigin != OriginFile || status.Portable || !status.Created {
		t.Fatalf("a fresh file-wrapped install looks wrong: %+v", status)
	}
	encrypted, digest := seedProtectedMaterial(t, pool, first)

	// Adopting ENCRYPTION_KEY must re-wrap the same data key, not mint a new one.
	adopted, status, err := Resolve(ctx, pool, encryptionKey, path)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Adopted || status.WrapOrigin != OriginEnv || !status.Portable {
		t.Fatalf("ENCRYPTION_KEY was not adopted: %+v", status)
	}
	if adopted.KeyID() != first.KeyID() {
		t.Fatalf("the data key changed during adoption: %s -> %s", first.KeyID(), adopted.KeyID())
	}
	if status.ProtectedCredentials != 2 {
		t.Fatalf("expected two protected credentials, got %d", status.ProtectedCredentials)
	}

	// Lose the whole data volume and boot with nothing but the environment value.
	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	recovered, status, err := Resolve(ctx, pool, encryptionKey, path)
	if err != nil {
		t.Fatalf("ENCRYPTION_KEY alone must be enough to start: %v", err)
	}
	if recovered.KeyID() != first.KeyID() || status.Created {
		t.Fatalf("a replacement key was generated: %+v", status)
	}
	plain, err := recovered.Decrypt(encrypted)
	if err != nil || plain != "confidential-client-secret" {
		t.Fatalf("the SSO Client Secret did not survive volume loss: %q %v", plain, err)
	}
	if got := recovered.Digest("relio_abc_secret"); string(got) != string(digest) {
		t.Fatal("the Personal Key digest did not survive volume loss")
	}
}

func TestResolveRefusesAWrongOrMissingEncryptionKey(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const encryptionKey = "relio-integration-encryption-key-value"
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets", "master.key")

	manager, _, err := Resolve(ctx, pool, encryptionKey, path)
	if err != nil {
		t.Fatal(err)
	}
	seedProtectedMaterial(t, pool, manager)
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ENCRYPTION_KEY must not create a key file on the volume")
	}

	// A different value must fail closed instead of re-keying the instance.
	if _, _, err = Resolve(ctx, pool, "relio-integration-encryption-key-other", path); err == nil {
		t.Fatal("a wrong ENCRYPTION_KEY must be rejected")
	} else if !errors.Is(err, ErrRecoveryRequired) || !strings.Contains(err.Error(), "presented key cannot open it") {
		t.Fatalf("unexpected error for a wrong key: %v", err)
	}

	// Dropping the variable is equally unsafe: nothing on disk can open the key.
	if _, _, err = Resolve(ctx, pool, "", path); err == nil {
		t.Fatal("starting without the wrapping key must be rejected")
	} else if !strings.Contains(err.Error(), "wrapped by "+OriginEnv) {
		t.Fatalf("the error must name the missing wrapping key: %v", err)
	}

	// The original value still works, proving nothing was mutated by the failures.
	again, status, err := Resolve(ctx, pool, encryptionKey, path)
	if err != nil {
		t.Fatal(err)
	}
	if again.KeyID() != manager.KeyID() || !status.Matches {
		t.Fatalf("a failed boot changed the stored key: %+v", status)
	}
}

func TestResolveRejectsAReplacementVolumeWithoutEncryptionKey(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	original := filepath.Join(t.TempDir(), "secrets", "master.key")
	manager, _, err := Resolve(ctx, pool, "", original)
	if err != nil {
		t.Fatal(err)
	}
	seedProtectedMaterial(t, pool, manager)

	// An empty replacement volume must not silently produce a new key.
	empty := filepath.Join(t.TempDir(), "secrets", "master.key")
	if _, _, err = Resolve(ctx, pool, "", empty); err == nil {
		t.Fatal("an empty replacement volume must be rejected")
	} else if !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("unexpected error: %v", err)
	}

	// Neither must a volume that happens to hold a different valid key.
	other := filepath.Join(t.TempDir(), "secrets", "master.key")
	if _, err = LoadOrCreate(other); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Resolve(ctx, pool, "", other); err == nil {
		t.Fatal("a different master key must be rejected")
	} else if !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("unexpected error: %v", err)
	}

	// The original volume keeps working.
	again, status, err := Resolve(ctx, pool, "", original)
	if err != nil {
		t.Fatal(err)
	}
	if again.KeyID() != manager.KeyID() || !status.Matches || status.Portable {
		t.Fatalf("the original volume no longer resolves cleanly: %+v", status)
	}
}
