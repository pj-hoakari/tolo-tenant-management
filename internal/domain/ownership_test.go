package domain

import (
	"testing"
	"time"
)

func TestOwnershipClaimToken(t *testing.T) {
	t.Parallel()

	token, hash, err := NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	if len(token) != 43 {
		t.Errorf("token length = %d, want 43 (32 random bytes, base64url)", len(token))
	}

	if !hash.Matches(token) {
		t.Error("Matches(token) = false, want true")
	}

	if hash.Matches(token + "x") {
		t.Error("Matches(tampered token) = true, want false")
	}

	if got := HashOwnershipClaimToken(token); got != hash {
		t.Error("HashOwnershipClaimToken(token) differs from the hash returned at creation")
	}

	other, _, err := NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	if other == token {
		t.Error("two generated tokens are equal")
	}
}

func TestOwnershipClaimExpired(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	claim := OwnershipClaim{TokenHash: OwnershipClaimTokenHash{}, ExpiresAt: expiresAt}

	if claim.Expired(expiresAt.Add(-time.Second)) {
		t.Error("Expired(before expiry) = true, want false")
	}

	if !claim.Expired(expiresAt) {
		t.Error("Expired(at expiry) = false, want true")
	}
}
