package domain

import (
	"errors"
	"testing"
	"time"
)

func TestInvitationRevoke(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	invitation := func(state InvitationState) Invitation {
		return NewInvitation("id", "public-id", "tenant-id", "tenant-public-id", RoleStaff, DisplayName{value: "Hanako"}, expiresAt, state)
	}

	t.Run("pending before expiry is revoked", func(t *testing.T) {
		t.Parallel()

		got, err := invitation(InvitationPending).Revoke(expiresAt.Add(-time.Second))
		if err != nil {
			t.Fatalf("error = %v", err)
		}

		if got.State() != InvitationRevoked {
			t.Errorf("State() = %v, want %v", got.State(), InvitationRevoked)
		}
	})

	rejected := []struct {
		name  string
		state InvitationState
		now   time.Time
	}{
		{name: "expired", state: InvitationPending, now: expiresAt},
		{name: "accepted", state: InvitationAccepted, now: expiresAt.Add(-time.Second)},
		{name: "revoked", state: InvitationRevoked, now: expiresAt.Add(-time.Second)},
	}

	for _, tt := range rejected {
		t.Run(tt.name+" refuses to be revoked", func(t *testing.T) {
			t.Parallel()

			if _, err := invitation(tt.state).Revoke(tt.now); !errors.Is(err, ErrInvitationNotPending) {
				t.Errorf("error = %v, want %v", err, ErrInvitationNotPending)
			}
		})
	}
}

func TestRoleInvitable(t *testing.T) {
	t.Parallel()

	for _, role := range []Role{RoleUnspecified, RoleOwner, RoleStaff, RoleAdmin} {
		want := ErrRoleNotInvitable
		if role == RoleStaff {
			want = nil
		}

		if err := role.Invitable(); !errors.Is(err, want) {
			t.Errorf("Role(%v).Invitable() = %v, want %v", role, err, want)
		}
	}
}
