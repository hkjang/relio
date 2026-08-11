package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKeyMaterialAcceptsEncodedKeys(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	cases := map[string]struct {
		value  string
		format KeyFormat
	}{
		"hex":       {hex.EncodeToString(raw), FormatHex},
		"base64":    {base64.StdEncoding.EncodeToString(raw), FormatBase64},
		"rawBase64": {base64.RawURLEncoding.EncodeToString(raw), FormatBase64},
	}
	for name, c := range cases {
		key, format, err := ParseKeyMaterial(c.value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if format != c.format {
			t.Fatalf("%s: unexpected format %q", name, format)
		}
		if !bytes.Equal(key, raw) {
			t.Fatalf("%s: encoded key was not used verbatim", name)
		}
	}
}

func TestParseKeyMaterialDerivesPassphraseDeterministically(t *testing.T) {
	phrase := strings.Repeat("relio-passphrase-", 3)
	first, format, err := ParseKeyMaterial(phrase)
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatPassphrase {
		t.Fatalf("unexpected format %q", format)
	}
	if len(first) != 32 {
		t.Fatalf("derived key length %d", len(first))
	}
	second, _, err := ParseKeyMaterial("  " + phrase + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the same passphrase must derive the same key across restarts")
	}
	other, _, err := ParseKeyMaterial(phrase + "x")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, other) {
		t.Fatal("different passphrases must derive different keys")
	}
}

func TestParseKeyMaterialRejectsWeakValues(t *testing.T) {
	for _, value := range []string{"", "   ", "short", "relio-encryption-key-2026"} {
		if _, _, err := ParseKeyMaterial(value); err == nil {
			t.Fatalf("%q must be rejected", value)
		}
	}
}

func TestWrapKeepsDataKeyStableAcrossWrappingKeys(t *testing.T) {
	data, err := GenerateDataKey()
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := data.Encrypt("confidential-client-secret")
	if err != nil {
		t.Fatal(err)
	}
	digest := data.Digest("relio_abc_secret")

	volumeKey, err := LoadOrCreate(filepath.Join(t.TempDir(), "secrets", "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := volumeKey.Wrap(data)
	if err != nil {
		t.Fatal(err)
	}
	// Adopting ENCRYPTION_KEY re-wraps the same data key, so every credential
	// that was already encrypted keeps working.
	envMaterial, _, err := ParseKeyMaterial(strings.Repeat("adopted-encryption-key-", 2))
	if err != nil {
		t.Fatal(err)
	}
	envKey, err := NewManager(envMaterial)
	if err != nil {
		t.Fatal(err)
	}
	unwrapped, err := volumeKey.Unwrap(sealed)
	if err != nil {
		t.Fatal(err)
	}
	resealed, err := envKey.Wrap(unwrapped)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := envKey.Unwrap(resealed)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Fingerprint() != data.Fingerprint() {
		t.Fatal("re-wrapping must not change the data key identity")
	}
	plain, err := recovered.Decrypt(ciphertext)
	if err != nil || plain != "confidential-client-secret" {
		t.Fatalf("secret did not survive re-wrapping: %q %v", plain, err)
	}
	if !bytes.Equal(recovered.Digest("relio_abc_secret"), digest) {
		t.Fatal("Personal Key digests must stay valid after re-wrapping")
	}
}

func TestUnwrapRejectsTheWrongKey(t *testing.T) {
	data, err := GenerateDataKey()
	if err != nil {
		t.Fatal(err)
	}
	right, err := GenerateDataKey()
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := GenerateDataKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := right.Wrap(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrong.Unwrap(sealed); err == nil {
		t.Fatal("a different ENCRYPTION_KEY must not open the envelope")
	}
	if _, err = right.Unwrap(sealed[:12]); err == nil {
		t.Fatal("a truncated envelope must be rejected")
	}
	// The envelope must not be interchangeable with a settings ciphertext.
	if _, err = right.Decrypt(string(sealed)); err == nil {
		t.Fatal("a wrapped key must not decrypt as a setting value")
	}
}

func TestCandidatesPrefersTheWrappingKeyAndDeduplicates(t *testing.T) {
	wrap, err := GenerateDataKey()
	if err != nil {
		t.Fatal(err)
	}
	if got := candidates(wrap, wrap); len(got) != 1 || got[0] != wrap {
		t.Fatalf("an identical file key must not be retried: %d candidates", len(got))
	}
	if got := candidates(wrap, nil); len(got) != 1 {
		t.Fatalf("a missing file key must not add a candidate: %d", len(got))
	}
	file, err := GenerateDataKey()
	if err != nil {
		t.Fatal(err)
	}
	got := candidates(wrap, file)
	if len(got) != 2 || got[0] != wrap || got[1] != file {
		t.Fatal("the presented wrapping key must be tried before the volume key")
	}
	if adoptionEvent(wrap, wrap) != "INITIALIZED" || adoptionEvent(file, wrap) != "ADOPTED" {
		t.Fatal("unexpected key lifecycle event")
	}
}

func TestNewManagerRejectsShortKeys(t *testing.T) {
	if _, err := NewManager(make([]byte, 16)); err == nil {
		t.Fatal("a 16 byte key must be rejected")
	}
	key := make([]byte, 32)
	manager, err := NewManager(key)
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 0xff
	if manager.Fingerprint() == mustFingerprint(t, key) {
		t.Fatal("NewManager must copy the key instead of aliasing the caller's slice")
	}
}

func mustFingerprint(t *testing.T, key []byte) string {
	t.Helper()
	manager, err := NewManager(key)
	if err != nil {
		t.Fatal(err)
	}
	return manager.Fingerprint()
}
