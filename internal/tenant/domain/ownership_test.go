package domain

import (
	"testing"
	"time"
)

func TestOwnershipClaimExpired(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	claim := OwnershipClaim{ExpiresAt: expiresAt}

	if claim.Expired(expiresAt.Add(-time.Second)) {
		t.Error("Expired(before expiry) = true, want false")
	}

	if !claim.Expired(expiresAt) {
		t.Error("Expired(at expiry) = false, want true")
	}
}
