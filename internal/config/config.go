package config

import (
	"errors"
	"os"
	"strings"
)

const (
	PostgresDSNEnv       = "POSTGRES_DSN"
	BootstrapAdminEnv    = "BOOTSTRAP_ADMIN"
	BootstrapPasswordEnv = "BOOTSTRAP_ADMIN_PASSWORD"
	EncryptionKeyEnv     = "ENCRYPTION_KEY"
	ListenAddress        = ":8080"
	DataDirectory        = "/var/lib/relio"
	MasterKeyPath        = DataDirectory + "/secrets/master.key"
)

// Config contains the only environment-sourced values accepted by Relio. Three
// are required for bootstrap and ENCRYPTION_KEY is the optional wrapping key
// that keeps Personal Keys and SSO Client Secrets valid across restarts even
// when the /var/lib/relio volume is recreated. Every other setting is stored in
// PostgreSQL and managed through the administrator console.
type Config struct {
	PostgresDSN       string
	BootstrapAdmin    string
	BootstrapPassword string
	EncryptionKey     string
}

func Load() (Config, error) {
	cfg := Config{
		PostgresDSN:       strings.TrimSpace(os.Getenv(PostgresDSNEnv)),
		BootstrapAdmin:    strings.TrimSpace(os.Getenv(BootstrapAdminEnv)),
		BootstrapPassword: os.Getenv(BootstrapPasswordEnv),
		EncryptionKey:     strings.TrimSpace(os.Getenv(EncryptionKeyEnv)),
	}
	if cfg.PostgresDSN == "" || cfg.BootstrapAdmin == "" || cfg.BootstrapPassword == "" {
		return Config{}, errors.New("POSTGRES_DSN, BOOTSTRAP_ADMIN and BOOTSTRAP_ADMIN_PASSWORD are required")
	}
	if len(cfg.BootstrapPassword) < 12 {
		return Config{}, errors.New("BOOTSTRAP_ADMIN_PASSWORD must contain at least 12 characters")
	}
	return cfg, nil
}
