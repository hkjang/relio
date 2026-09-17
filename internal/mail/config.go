package mail

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Defaults aim at the common case: an internal relay on port 25 that accepts
// mail from the network without credentials.
const (
	defaultPort     = 25
	defaultSecurity = "auto"
	defaultTimeout  = 10 * time.Second
	defaultFromName = "Relio"
)

// eventSettings maps each event to the mail.notify_* key that switches it off.
var eventSettings = map[string]string{
	EventApprovalRequested: "notify_approval_requested",
	EventApprovalDecided:   "notify_approval_decided",
	EventVoiceAssigned:     "notify_voice_assigned",
	EventContractRenewal:   "notify_contract_renewal",
}

// EventSettingKeys lists the switch for every event, for tests and the guide.
func EventSettingKeys() map[string]string {
	out := make(map[string]string, len(eventSettings))
	for event, key := range eventSettings {
		out[event] = "mail." + key
	}
	return out
}

// settingsProvider returns the stored values of one settings namespace, keyed
// without the namespace prefix ("smtp_host", not "mail.smtp_host"). Secrets
// come back decrypted; the mail package is the only reader of mail.password.
type settingsProvider interface {
	Values(ctx context.Context, namespace string) (map[string]any, error)
}

// ReadConfig builds the configuration from stored settings. The system
// namespace is read too, because mail.base_url falls back to
// system.service_url so links in mail point where users already sign in.
func ReadConfig(ctx context.Context, provider settingsProvider) (Config, error) {
	config := defaults()
	if provider == nil {
		return config, nil
	}
	values, err := provider.Values(ctx, "mail")
	if err != nil {
		return Config{}, err
	}
	config = FromValues(values)
	if strings.TrimSpace(config.BaseURL) == "" {
		if system, err := provider.Values(ctx, "system"); err == nil {
			config.BaseURL = stringValue(system, "service_url", "")
		}
	}
	return config, nil
}

func defaults() Config {
	return Config{Port: defaultPort, Security: defaultSecurity, Timeout: defaultTimeout, FromName: defaultFromName, Events: map[string]bool{}}
}

// FromValues reads the mail namespace as the settings service stores it. Every
// value is a JSON value, so numbers may arrive as float64, json.Number or int
// and booleans may have been saved as the strings a form posts.
func FromValues(values map[string]any) Config {
	config := defaults()
	config.Enabled = boolValue(values, "enabled")
	config.Host = stringValue(values, "smtp_host", "")
	config.Username = stringValue(values, "username", "")
	config.Password = stringValue(values, "password", "")
	config.FromAddress = stringValue(values, "from_address", "")
	config.FromName = stringValue(values, "from_name", defaultFromName)
	config.Security = strings.ToLower(stringValue(values, "security", defaultSecurity))
	config.BaseURL = stringValue(values, "base_url", "")
	config.SkipVerify = boolValue(values, "skip_tls_verify")
	if port, ok := numberValue(values, "smtp_port"); ok && port > 0 {
		config.Port = port
	}
	if seconds, ok := numberValue(values, "timeout_seconds"); ok && seconds > 0 {
		config.Timeout = time.Duration(seconds) * time.Second
	}
	// A relay on the implicit TLS port needs no extra configuration.
	if config.Security == defaultSecurity && config.Port == 465 {
		config.Security = "tls"
	}
	for event, key := range eventSettings {
		if _, present := values[key]; present {
			config.Events[event] = boolValue(values, key)
		}
	}
	if strings.TrimSpace(config.FromAddress) == "" && strings.TrimSpace(config.Host) != "" {
		config.FromAddress = "relio@" + config.Host
	}
	return config
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func boolValue(values map[string]any, key string) bool {
	switch typed := values[key].(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	}
	return false
}

func numberValue(values map[string]any, key string) (int, bool) {
	switch typed := values[key].(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return int(n), true
		}
	case string:
		var n int
		for _, r := range strings.TrimSpace(typed) {
			if r < '0' || r > '9' {
				return 0, false
			}
			n = n*10 + int(r-'0')
		}
		return n, strings.TrimSpace(typed) != ""
	}
	return 0, false
}
