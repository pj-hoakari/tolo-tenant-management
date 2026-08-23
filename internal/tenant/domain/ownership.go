package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"time"
)

// ownershipClaimTokenBytes is the entropy of an ownership claim token. 256 bits
// make the token infeasible to guess within its lifetime.
const ownershipClaimTokenBytes = 32

// OwnershipClaimTokenHash is the SHA-256 digest of an ownership claim token.
// Only the digest is persisted; the plaintext is returned to the caller once.
type OwnershipClaimTokenHash [sha256.Size]byte

// NewOwnershipClaimToken creates a cryptographically random claim token and
// returns its plaintext together with the hash to persist.
func NewOwnershipClaimToken() (string, OwnershipClaimTokenHash, error) {
	var raw [ownershipClaimTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", OwnershipClaimTokenHash{}, fmt.Errorf("generate ownership claim token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(raw[:])

	return token, HashOwnershipClaimToken(token), nil
}

// HashOwnershipClaimToken derives the persisted digest of a plaintext token.
func HashOwnershipClaimToken(token string) OwnershipClaimTokenHash {
	return sha256.Sum256([]byte(token))
}

// Matches reports whether token hashes to h, comparing in constant time.
func (h OwnershipClaimTokenHash) Matches(token string) bool {
	candidate := HashOwnershipClaimToken(token)

	return subtle.ConstantTimeCompare(h[:], candidate[:]) == 1
}

// OwnershipClaim is the pending claim attached to a pending_owner tenant.
type OwnershipClaim struct {
	TokenHash OwnershipClaimTokenHash
	ExpiresAt time.Time
}

// Expired reports whether the claim can no longer be used at now.
func (c OwnershipClaim) Expired(now time.Time) bool {
	return !now.Before(c.ExpiresAt)
}
