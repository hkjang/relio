package config

import "testing"

func TestLoadThreeEnvironmentValues(t *testing.T) {
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
}

func TestLoadRequiresEveryBootstrapValue(t *testing.T) {
	t.Setenv(PostgresDSNEnv, "")
	t.Setenv(BootstrapAdminEnv, "admin")
	t.Setenv(BootstrapPasswordEnv, "long-enough-password")
	if _, err := Load(); err == nil {
		t.Fatal("missing PostgreSQL DSN must fail")
	}
}
