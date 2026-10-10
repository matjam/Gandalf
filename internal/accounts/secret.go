package accounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// ParseSecretKey decodes the email encryption key as supplied in the
// GANDALF_SECRET_KEY environment variable: 32 random bytes, base64. Standard
// and URL-safe alphabets are accepted, padded or not, because operators
// generate these with whatever tool is to hand.
//
// Errors never contain the input.
func ParseSecretKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("%w: empty", ErrSecretKey)
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		b, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		if len(b) != secretKeyBytes {
			return nil, fmt.Errorf("%w: want %d bytes, got %d", ErrSecretKey, secretKeyBytes, len(b))
		}
		return b, nil
	}
	return nil, fmt.Errorf("%w: not valid base64", ErrSecretKey)
}

// GenerateSecretKey returns a fresh key in the form ParseSecretKey accepts,
// for an operator to put in the server's environment.
func GenerateSecretKey() (string, error) {
	b := make([]byte, secretKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// newAEAD builds the AES-256-GCM cipher for key.
func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != secretKeyBytes {
		return nil, fmt.Errorf("%w: want %d bytes, got %d", ErrSecretKey, secretKeyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSecretKey, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSecretKey, err)
	}
	return aead, nil
}

// seal encrypts plain, returning nonce||ciphertext. The nonce is random per
// value: with 96-bit random nonces the collision risk is negligible at the
// few thousand values this store will ever hold.
//
// aad is authenticated but not encrypted. It names what the value is and
// whom it belongs to ("email:u_..."), so a ciphertext copied into another row
// or another column fails to open instead of silently yielding someone
// else's data.
//
// The stored format has no version byte (the agreed design is plain nonce
// prefix). Rotating the secret key therefore means re-encrypting every value
// in one pass; there is no per-value key id to support a gradual rollover.
func (s *Store) seal(aad string, plain []byte) ([]byte, error) {
	if s.aead == nil {
		return nil, ErrNoSecretKey
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(aad)), nil
}

// open reverses seal. The error carries no detail about the value.
func (s *Store) open(aad string, blob []byte) ([]byte, error) {
	if s.aead == nil {
		return nil, ErrNoSecretKey
	}
	n := s.aead.NonceSize()
	if len(blob) < n+s.aead.Overhead() {
		return nil, ErrDecrypt
	}
	plain, err := s.aead.Open(nil, blob[:n], blob[n:], []byte(aad))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

func emailAAD(userID string) string { return "email:" + userID }
