package secrets

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// MinPassphraseLength is the shortest ENCRYPTION_KEY accepted when the value is
// not an encoded 32 byte key. It is deliberately long because a passphrase is
// stretched deterministically rather than salted per installation.
const MinPassphraseLength = 32

// KeyFormat records how an operator supplied ENCRYPTION_KEY so the diagnostic
// screen can explain what is expected without ever echoing the value.
type KeyFormat string

const (
	FormatHex        KeyFormat = "hex"
	FormatBase64     KeyFormat = "base64"
	FormatPassphrase KeyFormat = "passphrase"
)

// ParseKeyMaterial turns an operator supplied ENCRYPTION_KEY into exactly 32
// key bytes. 64 hexadecimal characters and Base64 encodings of 32 bytes are
// used verbatim; any other value of at least MinPassphraseLength characters is
// stretched with HKDF-SHA256 so a human readable passphrase is still usable in
// an air-gapped deployment that has no key management system.
func ParseKeyMaterial(raw string) ([]byte, KeyFormat, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, "", errors.New("ENCRYPTION_KEY is empty")
	}
	if len(value) == 64 {
		if decoded, err := hex.DecodeString(value); err == nil {
			return decoded, FormatHex, nil
		}
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) == 32 {
			return decoded, FormatBase64, nil
		}
	}
	if len(value) < MinPassphraseLength {
		return nil, "", fmt.Errorf("ENCRYPTION_KEY must be 64 hexadecimal characters, a Base64 encoded 32 byte key, or a passphrase of at least %d characters", MinPassphraseLength)
	}
	derived, err := hkdf.Key(sha256.New, []byte(value), nil, "relio:encryption-key:v1", 32)
	if err != nil {
		return nil, "", fmt.Errorf("derive ENCRYPTION_KEY: %w", err)
	}
	return derived, FormatPassphrase, nil
}
