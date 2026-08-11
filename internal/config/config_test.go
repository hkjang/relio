package config

import "testing"

func TestLoadBootstrapEnvironmentValues(t *testing.T) {
	t.Setenv(PostgresDSNEnv, "postgres://db/relio")
	t.Setenv(BootstrapAdminEnv, "admin")
	t.Setenv(BootstrapPasswordEnv, "long-enough-password")
	t.Setenv("KEYCLOAK_URL", "must-not-be-read")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PostgresDSN != "postgres://db/relio" || cfg.BootstrapAdmin != "admin" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.EncryptionKey != "" {
		t.Fatal("ENCRYPTION_KEY must stay optional so existing volumes keep working")
	}
}

func TestLoadReadsEncryptionKey(t *testing.T) {
	t.Setenv(PostgresDSNEnv, "postgres://db/relio")
	t.Setenv(BootstrapAdminEnv, "admin")
	t.Setenv(BootstrapPasswordEnv, "long-enough-password")
	t.Setenv(EncryptionKeyEnv, "  relio-instance-encryption-key-2026-value  ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EncryptionKey != "relio-instance-encryption-key-2026-value" {
		t.Fatalf("ENCRYPTION_KEY was not trimmed: %q", cfg.EncryptionKey)
	}
}

func TestLoadRequiresEveryBootstrapValue(t *testing.T) {
	t.Setenv(PostgresDSNEnv, "")
	t.Setenv(BootstrapAdminEnv, "admin")
	t.Setenv(BootstrapPasswordEnv, "long-enough-password")
	if _, err := Load(); err == nil {
		t.Fatal("missing PostgreSQL DSN must fail")
	}
}
