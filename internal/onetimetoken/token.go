package onetimetoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// tokenBytes is the entropy of a one-time token. 256 bits make the token
// infeasible to guess within its lifetime.
const tokenBytes = 32

// Hash is the SHA-256 digest of a one-time token. Only the digest is
// persisted; the plaintext is returned to the caller once.
type Hash [sha256.Size]byte

// New creates a cryptographically random one-time token and returns its
// plaintext together with the hash to persist.
func New() (string, Hash, error) {
	var raw [tokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Hash{}, fmt.Errorf("generate one-time token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(raw[:])

	return token, HashOf(token), nil
}

// HashOf derives the persisted digest of a plaintext token.
func HashOf(token string) Hash {
	return sha256.Sum256([]byte(token))
}

// Matches reports whether token hashes to h, comparing in constant time.
func (h Hash) Matches(token string) bool {
	candidate := HashOf(token)

	return subtle.ConstantTimeCompare(h[:], candidate[:]) == 1
}
