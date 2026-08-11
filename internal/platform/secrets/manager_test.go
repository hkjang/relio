package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerPersistsAndEncrypts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "master.key")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := first.Encrypt("confidential-client-secret")
	if err != nil {
		t.Fatal(err)
	}
	if ciphertext == "confidential-client-secret" {
		t.Fatal("plaintext was returned")
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := second.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "confidential-client-secret" {
		t.Fatalf("unexpected plaintext %q", plain)
	}
	if !bytes.Equal(first.Digest("key"), second.Digest("key")) {
		t.Fatal("digest changed after reloading master key")
	}
	if bytes.Equal(first.Digest("key"), first.Digest("other")) {
		t.Fatal("different keys produced the same digest")
	}
	if first.Fingerprint() != second.Fingerprint() || first.KeyID() != second.KeyID() {
		t.Fatal("master key identity changed after reloading the same volume")
	}
	if len(first.Fingerprint()) != 64 || len(first.KeyID()) != 12 {
		t.Fatal("unexpected master key identity format")
	}
}

func TestManagerIdentityChangesWithAnotherVolume(t *testing.T) {
	first, err := LoadOrCreate(filepath.Join(t.TempDir(), "secrets", "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(filepath.Join(t.TempDir(), "secrets", "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if sameFingerprint(first.Fingerprint(), second.Fingerprint()) {
		t.Fatal("different data volumes must not have the same master key identity")
	}
}

func TestManagerRejectsSymlinkedMasterKey(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.key")
	if err := os.WriteFile(target, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "master.key")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("symlinked master key must be rejected")
	}
}
