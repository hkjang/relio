package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// wrapAAD binds a wrapped data encryption key to its purpose so a ciphertext
// taken from another column can never be replayed as a key envelope.
var wrapAAD = []byte("relio:dek-wrap:v1")

// GenerateDataKey creates the random data encryption key that protects every
// Personal Key digest and encrypted setting for the lifetime of the instance.
func GenerateDataKey() (*Manager, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate data encryption key: %w", err)
	}
	return NewManager(key)
}

// Wrap seals the data encryption key with this manager's key. The result is
// stored in PostgreSQL so the data key survives the loss of /var/lib/relio as
// long as the same wrapping key is presented again.
func (m *Manager) Wrap(data *Manager) ([]byte, error) {
	if data == nil {
		return nil, errors.New("data encryption key is missing")
	}
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, data.key, wrapAAD), nil
}

// Unwrap recovers the data encryption key. A failure means the presented
// wrapping key is not the one the instance was initialised with.
func (m *Manager) Unwrap(sealed []byte) (*Manager, error) {
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) <= gcm.NonceSize() {
		return nil, errors.New("wrapped data encryption key is truncated")
	}
	key, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], wrapAAD)
	if err != nil {
		return nil, errors.New("wrapped data encryption key cannot be opened with the presented key")
	}
	return NewManager(key)
}
