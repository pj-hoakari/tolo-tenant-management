package application

import (
	"context"
	"errors"
)

// ErrOwnerMembershipUnavailable is returned by a MembershipWriter that has no
// membership store behind it. ClaimTenantOwnership then fails closed, so no
// tenant becomes owned without an owner membership.
var ErrOwnerMembershipUnavailable = errors.New("owner membership store is unavailable")

// MembershipWriter is the tenant side's port to the relation model, which owns
// memberships and roles. ClaimTenantOwnership calls it inside its own
// transaction (the context carries the transaction), so the owner membership
// and the ownership transition commit together. The relation side implements
// it; the tenant side never reads memberships through this port.
type MembershipWriter interface {
	// AddOwner records userID as the owner of the tenant identified by its
	// internal ID.
	AddOwner(ctx context.Context, tenantID, userID string) error
}

// UnavailableMembershipWriter rejects every write. It is the wiring used until
// the relation model is implemented.
type UnavailableMembershipWriter struct{}

func (UnavailableMembershipWriter) AddOwner(context.Context, string, string) error {
	return ErrOwnerMembershipUnavailable
}
