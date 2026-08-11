package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Manager struct{ key []byte }

// NewManager wraps raw key bytes. It is used for the data encryption key that
// is unwrapped from PostgreSQL and for keys parsed out of ENCRYPTION_KEY.
func NewManager(key []byte) (*Manager, error) {
	if len(key) != 32 {
		return nil, errors.New("Relio encryption key must be exactly 32 bytes")
	}
	owned := make([]byte, 32)
	copy(owned, key)
	return &Manager{key: owned}, nil
}

func load(path string) (*Manager, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("Relio master key must be a regular file")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid Relio master key length")
	}
	return &Manager{key: key}, nil
}

func LoadOrCreate(path string) (*Manager, error) {
	manager, err := load(path)
	if err == nil {
		return manager, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create secrets directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadOrCreate(path)
		}
		return nil, fmt.Errorf("create master key: %w", err)
	}
	if _, err = f.Write(key); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write master key: %w", err)
	}
	if err = f.Close(); err != nil {
		return nil, fmt.Errorf("close master key: %w", err)
	}
	return &Manager{key: key}, nil
}

// Fingerprint is a one-way identifier used to bind a PostgreSQL database to
// the exact master key stored in its companion relio-data volume. It is safe
// to persist and compare, but it cannot be used to recover the key.
func (m *Manager) Fingerprint() string {
	digest := sha256.New()
	digest.Write([]byte("relio:instance-master-key:v1:"))
	digest.Write(m.key)
	return hex.EncodeToString(digest.Sum(nil))
}

func (m *Manager) KeyID() string {
	fingerprint := m.Fingerprint()
	return fingerprint[:12]
}

func (m *Manager) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), []byte("relio:setting:v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (m *Manager) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("encrypted value is truncated")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte("relio:setting:v1"))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (m *Manager) Digest(secret string) []byte {
	h := hmac.New(sha256.New, m.key)
	h.Write([]byte("relio:personal-key:v1:"))
	h.Write([]byte(secret))
	return h.Sum(nil)
}
