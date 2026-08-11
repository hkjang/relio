package oidc

import (
	"errors"
	"testing"
)

func TestCallbackReasonSeparatesProvisioningFromConnectivity(t *testing.T) {
	cases := map[string]string{
		"user is not provisioned and auto provisioning is disabled":                     "not_provisioned",
		"no default sign-in Role is configured; set one in the administrator console":   "no_default_role",
		"OIDC state is invalid or expired":                                              "state_expired",
		"token exchange failed: invalid_client":                                         "token_exchange_failed",
		"ID token signature verification failed":                                        "token_invalid",
		"configured username claim is missing":                                          "claim_missing",
		"discovery issuer does not match configured issuer":                             "discovery_failed",
		"OIDC provider is unavailable":                                                  "discovery_failed",
		"something entirely unexpected happened while talking to the identity provider": "callback_failed",
	}
	for message, want := range cases {
		if got := CallbackReason(errors.New(message)); got != want {
			t.Fatalf("%q produced %q, expected %q", message, got, want)
		}
	}
	if CallbackReason(nil) != "" {
		t.Fatal("a successful callback has no reason code")
	}
}

func TestCallbackReasonCodesAreURLSafe(t *testing.T) {
	// The code is placed straight into a redirect query string, so it must not
	// need escaping.
	for _, message := range []string{"user is not provisioned and auto provisioning is disabled", "boom"} {
		for _, char := range CallbackReason(errors.New(message)) {
			if !(char >= 'a' && char <= 'z' || char == '_') {
				t.Fatalf("reason code contains %q which is not safe for a redirect", char)
			}
		}
	}
}

func TestDefaultsFillEveryClaimName(t *testing.T) {
	c := Config{}
	defaults(&c)
	if c.UsernameClaim != "preferred_username" || c.EmailClaim != "email" || c.NameClaim != "name" {
		t.Fatalf("unexpected claim defaults: %#v", c)
	}
	if c.GroupClaim != "groups" || c.RoleClaim != "realm_access.roles" {
		t.Fatalf("unexpected mapping defaults: %#v", c)
	}
	if len(c.Scopes) != 3 || c.Scopes[0] != "openid" {
		t.Fatalf("openid scope must be present by default: %#v", c.Scopes)
	}
}

func TestValidateRequiresOpenIDScope(t *testing.T) {
	base := Config{IssuerURL: "https://keycloak.example/realms/relio", ClientID: "relio", ClientSecret: "secret"}
	base.Scopes = []string{"profile"}
	if err := validate(base); err == nil {
		t.Fatal("a configuration without the openid scope must be rejected")
	}
	base.Scopes = []string{"openid", "email"}
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	// A stored secret must not have to be re-entered on every save.
	masked := base
	masked.ClientSecret = ""
	masked.ClientSecretConfigured = true
	if err := validate(masked); err != nil {
		t.Fatalf("a masked Client Secret must stay valid: %v", err)
	}
	masked.ClientSecretConfigured = false
	if err := validate(masked); err == nil {
		t.Fatal("a brand new provider must require a Client Secret")
	}
}
