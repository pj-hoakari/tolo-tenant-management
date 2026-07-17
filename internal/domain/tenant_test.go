package domain

import "testing"

func TestNewTenant(t *testing.T) {
	t.Parallel()

	tenant := NewTenant("tenant-id", "tenant-public-id", "Acme", "standard")

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

	if tenant.Archived() {
		t.Error("Archived() = true, want false")
	}
}
