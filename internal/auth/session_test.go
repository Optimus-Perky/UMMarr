package auth_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/auth"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, auth.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

func TestSessionCipher_RoundTrip(t *testing.T) {
	c, err := auth.NewSessionCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	token, err := c.NewToken(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := c.Verify(token); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSessionCipher_RejectsExpiredToken(t *testing.T) {
	c, err := auth.NewSessionCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	token, err := c.NewToken(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := c.Verify(token); err != auth.ErrInvalidSession {
		t.Fatalf("want ErrInvalidSession for an expired token, got %v", err)
	}
}

func TestSessionCipher_RejectsTamperedToken(t *testing.T) {
	c, err := auth.NewSessionCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	token, err := c.NewToken(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	tampered := []byte(token)
	tampered[len(tampered)-1] ^= 1 // flip a bit near the end (inside the GCM tag)
	if err := c.Verify(string(tampered)); err != auth.ErrInvalidSession {
		t.Fatalf("want ErrInvalidSession for a tampered token, got %v", err)
	}
}

func TestSessionCipher_RejectsTokenFromDifferentKey(t *testing.T) {
	c1, err := auth.NewSessionCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	c2, err := auth.NewSessionCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	token, err := c1.NewToken(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := c2.Verify(token); err != auth.ErrInvalidSession {
		t.Fatalf("want ErrInvalidSession when verifying with a different key, got %v", err)
	}
}

func TestSessionCipher_RejectsGarbageToken(t *testing.T) {
	c, err := auth.NewSessionCipher(testKey(t))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	if err := c.Verify("not-a-real-token"); err != auth.ErrInvalidSession {
		t.Fatalf("want ErrInvalidSession for garbage input, got %v", err)
	}
	if err := c.Verify(""); err != auth.ErrInvalidSession {
		t.Fatalf("want ErrInvalidSession for empty input, got %v", err)
	}
}

func TestNewSessionCipher_RejectsWrongKeySize(t *testing.T) {
	if _, err := auth.NewSessionCipher(make([]byte, 16)); err == nil {
		t.Fatalf("want an error constructing a cipher with a 16-byte key")
	}
}
