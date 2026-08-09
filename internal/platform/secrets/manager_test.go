package secrets

import (
	"bytes"
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
}
