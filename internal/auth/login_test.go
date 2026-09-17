package auth

import (
	"errors"
	"testing"
)

// The point of admitLocal is the order of its two checks: with local login
// switched off, nothing a caller can send without the right password may
// answer differently for the bootstrap account than for anyone else.
func TestAdmitLocalRefusesEveryUnprovenLoginTheSameWay(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	unproven := map[string]localAccount{
		"unknown username": {},
		"inactive user":    {ID: "u", Hash: hash, Active: false, Found: true},
		"wrong password":   {ID: "u", Hash: hash, Active: true, Found: true},
	}
	for name, acct := range unproven {
		for _, bootstrap := range []bool{false, true} {
			for _, bootstrapOnly := range []bool{false, true} {
				acct.Bootstrap = bootstrap
				password := "not-the-password"
				if name == "inactive user" {
					password = "correct-horse-battery" // even the right password does not revive it
				}
				if got := admitLocal(acct, password, bootstrapOnly); !errors.Is(got, ErrInvalidCredentials) {
					t.Fatalf("%s (bootstrap=%v, bootstrapOnly=%v): got %v, want ErrInvalidCredentials", name, bootstrap, bootstrapOnly, got)
				}
			}
		}
	}
}

func TestAdmitLocalTellsOnlyAVerifiedUserAboutThePolicy(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	user := localAccount{ID: "u", Hash: hash, Active: true, Found: true}
	admin := localAccount{ID: "a", Hash: hash, Active: true, Bootstrap: true, Found: true}

	if got := admitLocal(user, "correct-horse-battery", false); got != nil {
		t.Fatalf("local login enabled must admit a regular user: %v", got)
	}
	if got := admitLocal(user, "correct-horse-battery", true); !errors.Is(got, ErrLocalLoginDisabled) {
		t.Fatalf("local login disabled must refuse a verified regular user with the policy error: %v", got)
	}
	if got := admitLocal(admin, "correct-horse-battery", true); got != nil {
		t.Fatalf("the bootstrap account must stay admitted with local login disabled: %v", got)
	}
	if got := admitLocal(admin, "not-the-password", true); !errors.Is(got, ErrInvalidCredentials) {
		t.Fatalf("a wrong bootstrap password must not be distinguishable from any other refusal: %v", got)
	}
}
