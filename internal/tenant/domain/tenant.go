// Package domain contains immutable models for the tenant context.
package domain

import (
	"errors"
	"fmt"
)

//go:generate go tool stringer -type=TenantOwnershipState -trimprefix=TenantOwnershipState -linecomment -output=tenant_enum_string.go

// TenantOwnershipState tells whether a tenant has a confirmed owner.
type TenantOwnershipState uint8

const (
	TenantOwnershipStateUnspecified  TenantOwnershipState = iota // unspecified
	TenantOwnershipStatePendingOwner                             // pending_owner
	TenantOwnershipStateOwned                                    // owned
)

var (
	ErrTenantNotPendingOwner = errors.New("tenant is not pending an owner")
	// ErrTenantAlreadyArchived rejects archiving a tenant that is already
	// archived: archiving is a one-way transition and never repeated.
	ErrTenantAlreadyArchived = errors.New("tenant is already archived")
)

// ParseTenantOwnershipState maps a string representation (as produced by
// String) back to a TenantOwnershipState.
func ParseTenantOwnershipState(s string) (TenantOwnershipState, error) {
	for st := TenantOwnershipStateUnspecified; st <= TenantOwnershipStateOwned; st++ {
		if st.String() == s {
			return st, nil
		}
	}

	return TenantOwnershipStateUnspecified, fmt.Errorf("invalid tenant ownership state %q", s)
}

// Tenant is an immutable tenant model.
type Tenant struct {
	id             string
	publicID       string
	name           string
	contractPlan   string
	ownershipState TenantOwnershipState
	archived       bool
}

func NewTenant(id, publicID, name, contractPlan string, ownershipState TenantOwnershipState, archived bool) Tenant {
	return Tenant{
		id:             id,
		publicID:       publicID,
		name:           name,
		contractPlan:   contractPlan,
		ownershipState: ownershipState,
		archived:       archived,
	}
}

func (t Tenant) ID() string                           { return t.id }
func (t Tenant) PublicID() string                     { return t.publicID }
func (t Tenant) Name() string                         { return t.name }
func (t Tenant) ContractPlan() string                 { return t.contractPlan }
func (t Tenant) OwnershipState() TenantOwnershipState { return t.ownershipState }
func (t Tenant) Archived() bool                       { return t.archived }

// Owned reports whether the tenant has a confirmed owner and may be used for
// business operations.
func (t Tenant) Owned() bool { return t.ownershipState == TenantOwnershipStateOwned }

// ClaimOwnership returns a copy of the tenant with its ownership confirmed.
// Only a pending_owner tenant can be claimed; the transition never reverses.
func (t Tenant) ClaimOwnership() (Tenant, error) {
	if t.ownershipState != TenantOwnershipStatePendingOwner {
		return Tenant{}, ErrTenantNotPendingOwner
	}

	t.ownershipState = TenantOwnershipStateOwned

	return t, nil
}

// ChangeContractPlan returns a copy of the tenant on the given contract plan.
func (t Tenant) ChangeContractPlan(plan string) Tenant {
	t.contractPlan = plan

	return t
}

// Archive returns a copy of the tenant marked as archived. Archiving is a soft
// delete: the tenant keeps its identifier, its name (which is never released),
// its events and its memberships, and only refuses further writes. A tenant
// that is already archived cannot be archived again.
func (t Tenant) Archive() (Tenant, error) {
	if t.archived {
		return Tenant{}, ErrTenantAlreadyArchived
	}

	t.archived = true

	return t, nil
}
