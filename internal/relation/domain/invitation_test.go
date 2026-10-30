package domain

import (
	"errors"
	"testing"
	"time"
)

func TestInvitationTransitions(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	invitation := func(state InvitationState) Invitation {
		return NewInvitation("id", "public-id", "tenant-id", "tenant-public-id", RoleStaff, DisplayName{value: "Hanako"}, expiresAt, state)
	}
	transitions := map[InvitationState]func(Invitation, time.Time) (Invitation, error){
		InvitationAccepted: Invitation.Accept,
		InvitationRevoked:  Invitation.Revoke,
	}

	for want, transition := range transitions {
		t.Run("pending before expiry closes as "+want.String(), func(t *testing.T) {
			t.Parallel()

			got, err := transition(invitation(InvitationPending), expiresAt.Add(-time.Second))
			if err != nil {
				t.Fatalf("error = %v", err)
			}

			if got.State() != want {
				t.Errorf("State() = %v, want %v", got.State(), want)
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
			t.Run(tt.name+" refuses to close as "+want.String(), func(t *testing.T) {
				t.Parallel()

				if _, err := transition(invitation(tt.state), tt.now); !errors.Is(err, ErrInvitationNotPending) {
					t.Errorf("error = %v, want %v", err, ErrInvitationNotPending)
				}
			})
		}
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
