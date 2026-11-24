package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
)

func TestNewTenant(t *testing.T) {
	t.Parallel()

	tenant := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStateOwned, Subdomain{}, false)

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

	pending := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStatePendingOwner, Subdomain{}, false)
	if pending.Owned() {
		t.Error("Owned() = true for pending_owner, want false")
	}

	if got := pending.Subdomain().String(); got != "" {
		t.Errorf("Subdomain() of pending_owner = %q, want none", got)
	}

	subdomain, err := ParseSubdomain("acme")
	if err != nil {
		t.Fatalf("ParseSubdomain() error = %v", err)
	}

	owned, err := pending.ClaimOwnership(subdomain)
	if err != nil {
		t.Fatalf("ClaimOwnership() error = %v", err)
	}

	if got, want := owned.OwnershipState(), TenantOwnershipStateOwned; got != want {
		t.Errorf("OwnershipState() after claim = %v, want %v", got, want)
	}

	if got, want := owned.Subdomain(), subdomain; got != want {
		t.Errorf("Subdomain() after claim = %q, want %q", got, want)
	}

	if _, err := owned.ClaimOwnership(subdomain); !errors.Is(err, ErrTenantNotPendingOwner) {
		t.Errorf("second ClaimOwnership() error = %v, want %v", err, ErrTenantNotPendingOwner)
	}
}

func TestTenantAdmitsOwnershipClaim(t *testing.T) {
	t.Parallel()

	const token = "ownership-claim-token"

	expiresAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	pending := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStatePendingOwner, Subdomain{}, false)
	owned := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStateOwned, Subdomain{}, false)
	live := OwnershipClaim{TokenHash: onetimetoken.HashOf(token), ExpiresAt: expiresAt}

	tests := []struct {
		name   string
		tenant Tenant
		claim  OwnershipClaim
		token  string
		now    time.Time
		want   bool
	}{
		{name: "pending tenant, matching token before expiry", tenant: pending, claim: live, token: token, now: expiresAt.Add(-time.Second), want: true},
		{name: "owned tenant with no claim", tenant: owned, claim: OwnershipClaim{}, token: token, now: expiresAt.Add(-time.Second), want: false},
		{name: "at expiry", tenant: pending, claim: live, token: token, now: expiresAt, want: false},
		{name: "after expiry", tenant: pending, claim: live, token: token, now: expiresAt.Add(time.Second), want: false},
		{name: "wrong token", tenant: pending, claim: live, token: token + "x", now: expiresAt.Add(-time.Second), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.tenant.AdmitsOwnershipClaim(tt.claim, tt.token, tt.now); got != tt.want {
				t.Errorf("AdmitsOwnershipClaim() = %v, want %v", got, tt.want)
			}
		})
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

	tenant := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStateOwned, Subdomain{}, false)

	changed := tenant.ChangeContractPlan("enterprise")
	if got, want := changed.ContractPlan(), "enterprise"; got != want {
		t.Errorf("ContractPlan() after change = %q, want %q", got, want)
	}

	// The tenant is immutable: the original keeps its plan and everything else
	// carries over to the copy.
	if got, want := tenant.ContractPlan(), "standard"; got != want {
		t.Errorf("ContractPlan() of the original = %q, want %q", got, want)
	}

	if got, want := changed, NewTenant(tenant.ID(), tenant.PublicID(), tenant.Name(), "enterprise", tenant.OwnershipState(), tenant.Subdomain(), tenant.Archived()); got != want {
		t.Errorf("changed tenant = %#v, want %#v", got, want)
	}
}

func TestTenantArchive(t *testing.T) {
	t.Parallel()

	tenant := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", TenantOwnershipStateOwned, Subdomain{}, false)

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
	if got, want := archived, NewTenant(tenant.ID(), tenant.PublicID(), tenant.Name(), tenant.ContractPlan(), tenant.OwnershipState(), tenant.Subdomain(), true); got != want {
		t.Errorf("archived tenant = %#v, want %#v", got, want)
	}

	if _, err := archived.Archive(); !errors.Is(err, ErrTenantAlreadyArchived) {
		t.Errorf("second Archive() error = %v, want %v", err, ErrTenantAlreadyArchived)
	}
}
