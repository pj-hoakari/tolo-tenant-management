package domain

import (
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
)

// OwnershipClaim is the pending claim attached to a pending_owner tenant.
type OwnershipClaim struct {
	TokenHash onetimetoken.Hash
	ExpiresAt time.Time
}

// Expired reports whether the claim can no longer be used at now.
func (c OwnershipClaim) Expired(now time.Time) bool {
	return !now.Before(c.ExpiresAt)
}
