package auth

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	encoded, err := HashPassword("a-strong-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if encoded == "a-strong-test-password" {
		t.Fatal("password was not hashed")
	}
	if !VerifyPassword(encoded, "a-strong-test-password") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(encoded, "wrong-password-value") {
		t.Fatal("invalid password accepted")
	}
}

func TestPasswordMinimumLength(t *testing.T) {
	if _, err := HashPassword("too-short"); err == nil {
		t.Fatal("short password must be rejected")
	}
}
