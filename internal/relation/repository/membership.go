// Package repository defines persistence contracts for the relation model.
package repository

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
)

var (
	// ErrMembershipAlreadyExists rejects a second membership of the same user
	// in the same tenant.
	ErrMembershipAlreadyExists = errors.New("membership already exists")
	// ErrMembershipNotFound means the user does not belong to the tenant.
	ErrMembershipNotFound = errors.New("membership not found")
	// ErrEventRoleNotFound means the user holds no role on the event.
	ErrEventRoleNotFound = errors.New("event role not found")
	// ErrTenantMembershipRequired rejects an event role for a user who does
	// not belong to the event's tenant (event-role ⇒ tenant-role). It also
	// covers an event that belongs to a tenant other than the user's, since
	// the membership looked up is the one of the event's tenant.
	ErrTenantMembershipRequired = errors.New("event role requires membership of the event's tenant")
	// ErrTenantNotFound means the referenced tenant does not exist.
	ErrTenantNotFound = errors.New("tenant not found")
	// ErrEventNotFound means the referenced event does not exist.
	ErrEventNotFound = errors.New("event not found")
)

// MembershipRepository persists memberships and roles. Tenants and events are
// identified by their internal IDs; callers resolve public IDs beforehand
// through the tenant context, which is the source of truth for identifiers.
// The repository participates in the transaction carried by the context.
type MembershipRepository interface {
	// AddTenantMember creates the membership of userID in the tenant.
	AddTenantMember(ctx context.Context, tenantID, userID string, role domain.Role) (domain.Membership, error)
	// ChangeTenantRole replaces the tenant role of an existing membership.
	ChangeTenantRole(ctx context.Context, tenantID, userID string, role domain.Role) (domain.Membership, error)
	// GrantEventRole assigns (or replaces) the user's role on the event.
	GrantEventRole(ctx context.Context, eventID, userID string, role domain.Role) (domain.Membership, error)
	// RevokeTenantMembership removes the membership and, with it, every event
	// role the user held in the tenant.
	RevokeTenantMembership(ctx context.Context, tenantID, userID string) error
	// RevokeEventRole removes the user's role on the event only.
	RevokeEventRole(ctx context.Context, eventID, userID string) error
	// FindMembership loads the membership of userID in the tenant.
	FindMembership(ctx context.Context, tenantID, userID string) (domain.Membership, error)
	// FindTenantRoleForShare loads the user's tenant role and locks the
	// membership row for the rest of the surrounding transaction, so that a
	// concurrent revoke or role change waits until the caller's write commits.
	// It returns ErrMembershipNotFound when the user does not belong to the
	// tenant.
	FindTenantRoleForShare(ctx context.Context, tenantID, userID string) (domain.Role, error)
	// ListMembershipsByTenant lists every membership of the tenant.
	ListMembershipsByTenant(ctx context.Context, tenantID string) ([]domain.Membership, error)
	// ListMembershipsByUser lists the user's memberships across tenants.
	ListMembershipsByUser(ctx context.Context, userID string) ([]domain.Membership, error)
}
