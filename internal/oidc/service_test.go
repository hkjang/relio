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

// The browser asking for prompt=none is never enough on its own: the redirect
// surface stays tied to the administrator's auto login setting.
func TestSilentAttemptRequiresAutoLogin(t *testing.T) {
	cases := []struct {
		name      string
		enabled   bool
		autoLogin bool
		requested bool
		want      bool
	}{
		{"default installation ignores ?prompt=none", true, false, true, false},
		{"auto login on and requested", true, true, true, true},
		{"auto login on but an interactive login was asked for", true, true, false, false},
		{"auto login on while SSO itself is off", false, true, true, false},
		{"nothing on, nothing asked", false, false, false, false},
	}
	for _, tc := range cases {
		c := Config{Enabled: tc.enabled, AutoLogin: tc.autoLogin}
		if got := silentAllowed(c, LoginRequest{Silent: tc.requested}); got != tc.want {
			t.Errorf("%s: silentAllowed=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAuthorizeQueryOnlyCarriesPromptNoneForASilentAttempt(t *testing.T) {
	c := Config{ClientID: "relio", Scopes: []string{"openid", "profile"}}
	interactive := authorizeQuery(c, "https://relio.example/api/v1/auth/oidc/callback", "st", "nc", "ch", false)
	if _, present := interactive["prompt"]; present {
		t.Fatalf("an interactive login must not send prompt: %v", interactive)
	}
	silent := authorizeQuery(c, "https://relio.example/api/v1/auth/oidc/callback", "st", "nc", "ch", true)
	if silent.Get("prompt") != "none" {
		t.Fatalf("a silent login must send prompt=none: %v", silent)
	}
	// Everything else is identical, so a silent attempt that succeeds finishes
	// through the same code exchange as an interactive one.
	silent.Del("prompt")
	if silent.Encode() != interactive.Encode() {
		t.Fatalf("silent and interactive requests differ beyond prompt:\n%s\n%s", silent.Encode(), interactive.Encode())
	}
	for _, key := range []string{"client_id", "response_type", "scope", "redirect_uri", "state", "nonce", "code_challenge", "code_challenge_method"} {
		if interactive.Get(key) == "" {
			t.Errorf("authorization request is missing %s", key)
		}
	}
}

// A refused silent attempt lands on the login screen with the marker that
// stops the browser from retrying; a refused interactive one keeps its error.
func TestRefusalRedirectMarksASilentRefusalInTheAddress(t *testing.T) {
	if got := RefusalRedirect(Refusal{Silent: true, ReturnTo: "/app/customers/1"}, "login_required"); got != "/login?sso=none" {
		t.Fatalf("silent refusal redirected to %q", got)
	}
	// Any error on a silent attempt must stop the retry, not only login_required.
	if got := RefusalRedirect(Refusal{Silent: true}, "invalid_request"); got != "/login?sso=none" {
		t.Fatalf("silent refusal with another error redirected to %q", got)
	}
	if got := RefusalRedirect(Refusal{Silent: false}, "access_denied"); got != "/login?sso_error=access_denied" {
		t.Fatalf("interactive refusal redirected to %q", got)
	}
	// The provider's error code goes straight into a query string.
	if got := RefusalRedirect(Refusal{}, "a b&c=d"); got != "/login?sso_error=a+b%26c%3Dd" {
		t.Fatalf("provider error was not escaped: %q", got)
	}
	for _, code := range []string{"login_required", "interaction_required", "consent_required"} {
		if !SilentRefusalCodes[code] {
			t.Errorf("%s is an ordinary prompt=none answer and must not be treated as a failure", code)
		}
	}
	if SilentRefusalCodes["invalid_client"] {
		t.Error("invalid_client is a real failure")
	}
}

// return_to is a springboard out of the site unless it is pinned to a
// same-origin application path, and a springboard into a loop if it may point
// at the login or API paths.
func TestSafeReturnToOnlyAcceptsApplicationPaths(t *testing.T) {
	accepted := []string{"/", "/app", "/app/dashboard", "/app/customers/42?tab=plan", "/admin/oidc", "/me/keys", "/app/search?q=%2F%2Fevil"}
	for _, value := range accepted {
		if !SafeReturnTo(value) {
			t.Errorf("%q must be accepted", value)
		}
		if got := returnToOrDefault(value); got != value {
			t.Errorf("%q was replaced by %q", value, got)
		}
	}
	rejected := map[string]string{
		"":                                    "empty",
		"app/dashboard":                       "relative without leading slash",
		"//evil.example/app":                  "scheme-relative URL",
		"/\\evil.example":                     "backslash the browser reads as a slash",
		"https://evil.example/app":            "absolute URL",
		"javascript:alert(1)":                 "javascript URL",
		"/app\r\nSet-Cookie: x=y":             "header injection",
		"/api/v1/auth/oidc/start?prompt=none": "restarting the login from the login",
		"/api":                                "API root",
		"/mcp":                                "MCP endpoint",
		"/mcp/":                               "MCP endpoint with slash",
		"/.well-known/openid-configuration":   "discovery probe",
		"/login":                              "the login screen itself",
		"/login?sso=none":                     "the login screen with a marker",
	}
	for value, why := range rejected {
		if SafeReturnTo(value) {
			t.Errorf("%q must be rejected (%s)", value, why)
		}
		if got := returnToOrDefault(value); got != DefaultReturnTo {
			t.Errorf("%q fell back to %q, want %q", value, got, DefaultReturnTo)
		}
	}
	// Prefix checks must not swallow legitimate application paths that merely
	// start with the same letters.
	for _, value := range []string{"/apis", "/loginhistory", "/mcpx"} {
		if !SafeReturnTo(value) {
			t.Errorf("%q is not a machine or login path and must be accepted", value)
		}
	}
}
