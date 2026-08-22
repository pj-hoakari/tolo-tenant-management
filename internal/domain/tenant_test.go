package domain

import (
	"errors"
	"testing"
)

func TestNewTenant(t *testing.T) {
	t.Parallel()

	tenant := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStateOwned, false)

	if got, want := tenant.ID(), "tenant-id"; got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}

	if got, want := tenant.PublicID(), "tenant-public-id"; got != want {
		t.Errorf("PublicID() = %q, want %q", got, want)
	}

	if got, want := tenant.Name(), "Acme"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}

	if got, want := tenant.ContractPlan(), "standard"; got != want {
		t.Errorf("ContractPlan() = %q, want %q", got, want)
	}

	if got, want := tenant.OwnershipState(), TenantOwnershipStateOwned; got != want {
		t.Errorf("OwnershipState() = %v, want %v", got, want)
	}

	if !tenant.Owned() {
		t.Error("Owned() = false, want true")
	}

	if tenant.Archived() {
		t.Error("Archived() = true, want false")
	}
}

func TestTenantClaimOwnership(t *testing.T) {
	t.Parallel()

	pending := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStatePendingOwner, false)
	if pending.Owned() {
		t.Error("Owned() = true for pending_owner, want false")
	}

	owned, err := pending.ClaimOwnership()
	if err != nil {
		t.Fatalf("ClaimOwnership() error = %v", err)
	}

	if got, want := owned.OwnershipState(), TenantOwnershipStateOwned; got != want {
		t.Errorf("OwnershipState() after claim = %v, want %v", got, want)
	}

	if _, err := owned.ClaimOwnership(); !errors.Is(err, ErrTenantNotPendingOwner) {
		t.Errorf("second ClaimOwnership() error = %v, want %v", err, ErrTenantNotPendingOwner)
	}
}

func TestParseTenantOwnershipState(t *testing.T) {
	t.Parallel()

	for _, state := range []TenantOwnershipState{TenantOwnershipStateUnspecified, TenantOwnershipStatePendingOwner, TenantOwnershipStateOwned} {
		got, err := ParseTenantOwnershipState(state.String())
		if err != nil || got != state {
			t.Errorf("ParseTenantOwnershipState(%q) = %v, %v, want %v", state.String(), got, err, state)
		}
	}

	if _, err := ParseTenantOwnershipState("other"); err == nil {
		t.Error("ParseTenantOwnershipState(other) error = nil, want error")
	}
}

func TestTenantChangeContractPlan(t *testing.T) {
	t.Parallel()

	tenant := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStateOwned, false)

	changed := tenant.ChangeContractPlan("enterprise")
	if got, want := changed.ContractPlan(), "enterprise"; got != want {
		t.Errorf("ContractPlan() after change = %q, want %q", got, want)
	}

	// The tenant is immutable: the original keeps its plan and everything else
	// carries over to the copy.
	if got, want := tenant.ContractPlan(), "standard"; got != want {
		t.Errorf("ContractPlan() of the original = %q, want %q", got, want)
	}

	if got, want := changed, NewTenant(tenant.ID(), tenant.PublicID(), tenant.Name(), "enterprise", tenant.OwnershipState(), tenant.Archived()); got != want {
		t.Errorf("changed tenant = %#v, want %#v", got, want)
	}
}

func TestTenantArchive(t *testing.T) {
	t.Parallel()

	tenant := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStateOwned, false)

	archived, err := tenant.Archive()
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}

	if !archived.Archived() {
		t.Error("Archived() after Archive() = false, want true")
	}

	if tenant.Archived() {
		t.Error("Archived() of the original = true, want false")
	}

	// Archiving keeps the identifier, the name, the plan and the ownership.
	if got, want := archived, NewTenant(tenant.ID(), tenant.PublicID(), tenant.Name(), tenant.ContractPlan(), tenant.OwnershipState(), true); got != want {
		t.Errorf("archived tenant = %#v, want %#v", got, want)
	}

	if _, err := archived.Archive(); !errors.Is(err, ErrTenantAlreadyArchived) {
		t.Errorf("second Archive() error = %v, want %v", err, ErrTenantAlreadyArchived)
	}
}
