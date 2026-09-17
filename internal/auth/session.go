// Package auth implements UMMarr's session mechanism: a stateless,
// AES-256-GCM encrypted+authenticated token carried in a cookie, so the
// server needs no session store (matching this project's minimal-state,
// single-binary ethos) - the cookie itself just proves "this browser had
// the key at some point" and is unforgeable/untamperable without it.
// Password verification (internal/api's Login handler) is a separate,
// simpler constant-time comparison against the configured admin
// password - this package only owns the session token.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrInvalidSession covers every way a token can fail to authenticate -
// malformed, tampered with (GCM tag mismatch), or expired. Deliberately
// one error for all three: callers only ever need to know "not logged
// in", never why, and collapsing the cases avoids leaking any detail an
// attacker could use to distinguish "close but wrong" from "garbage".
var ErrInvalidSession = errors.New("auth: invalid or expired session")

// KeySize is the required session key length - 32 bytes for AES-256.
const KeySize = 32

// SessionCipher issues and verifies session tokens. Safe for concurrent
// use (crypto/cipher.AEAD implementations are).
type SessionCipher struct {
	aead cipher.AEAD
}

// NewSessionCipher builds a SessionCipher from a 32-byte AES-256 key.
func NewSessionCipher(key []byte) (*SessionCipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("auth: session key must be %d bytes (AES-256), got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("auth: build AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth: build GCM: %w", err)
	}
	return &SessionCipher{aead: aead}, nil
}

// NewToken issues a session token that verifies successfully until
// expiresAt. The only payload is the expiry timestamp - this is a
// single-admin-user app, so there's no user id or role to carry.
func (c *SessionCipher) NewToken(expiresAt time.Time) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("auth: generate nonce: %w", err)
	}
	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], uint64(expiresAt.Unix()))
	ciphertext := c.aead.Seal(nonce, nonce, payload[:], nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// Verify decrypts and validates token, returning nil only if it was
// issued by this SessionCipher (same key) and hasn't expired.
func (c *SessionCipher) Verify(token string) error {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ErrInvalidSession
	}
	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return ErrInvalidSession
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	payload, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return ErrInvalidSession
	}
	if len(payload) != 8 {
		return ErrInvalidSession
	}
	expiresAt := time.Unix(int64(binary.BigEndian.Uint64(payload)), 0)
	if time.Now().After(expiresAt) {
		return ErrInvalidSession
	}
	return nil
}
