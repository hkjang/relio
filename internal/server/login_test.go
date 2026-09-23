package server

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hkjang/relio/internal/auth"
)

// With local login off the handler used to answer 403 for every username but
// the bootstrap administrator's and 401 for that one, so the account exempted
// from the policy could be found without a password. Only the error auth
// hands out for a verified password may leave the generic 401.
func TestLoginFailureCollapsesEverythingButThePolicyRefusal(t *testing.T) {
	for _, err := range []error{
		auth.ErrInvalidCredentials,
		errors.New("invalid credentials"),
		fmt.Errorf("session insert: %w", errors.New("connection reset")),
	} {
		status, code, _ := loginFailure(err)
		if status != 401 || code != "invalid_credentials" {
			t.Fatalf("%v: got %d %s, want 401 invalid_credentials", err, status, code)
		}
	}
	status, code, _ := loginFailure(fmt.Errorf("login: %w", auth.ErrLocalLoginDisabled))
	if status != 403 || code != "local_login_disabled" {
		t.Fatalf("policy refusal: got %d %s, want 403 local_login_disabled", status, code)
	}
}
