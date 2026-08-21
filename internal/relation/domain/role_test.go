package domain

import (
	"errors"
	"testing"
)

func TestRoleGrantable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role Role
		want error
	}{
		{role: RoleOwner, want: nil},
		{role: RoleStaff, want: nil},
		{role: RoleAdmin, want: ErrRoleReserved},
		{role: RoleUnspecified, want: ErrRoleRequired},
	}

	for _, tt := range tests {
		t.Run(tt.role.String(), func(t *testing.T) {
			t.Parallel()

			if err := tt.role.Grantable(); !errors.Is(err, tt.want) {
				t.Errorf("Grantable() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseRole(t *testing.T) {
	t.Parallel()

	for _, role := range []Role{RoleUnspecified, RoleOwner, RoleStaff, RoleAdmin} {
		got, err := ParseRole(role.String())
		if err != nil || got != role {
			t.Errorf("ParseRole(%q) = %v, %v, want %v", role.String(), got, err, role)
		}
	}

	if _, err := ParseRole("other"); err == nil {
		t.Error("ParseRole(other) error = nil, want error")
	}
}

func TestMembershipCopiesEventRoles(t *testing.T) {
	t.Parallel()

	roles := []EventRole{NewEventRole("event-id", "event-public-id", RoleStaff)}
	membership := NewMembership("user-1", "tenant-id", "tenant-public-id", RoleOwner, roles)

	roles[0] = NewEventRole("other", "other", RoleOwner)

	if got := membership.EventRoles(); len(got) != 1 || got[0].EventID() != "event-id" || got[0].Role() != RoleStaff {
		t.Errorf("EventRoles() = %#v, want the roles passed at construction", got)
	}

	membership.EventRoles()[0] = NewEventRole("mutated", "mutated", RoleOwner)

	if got := membership.EventRoles()[0].EventID(); got != "event-id" {
		t.Errorf("EventRoles() after external mutation = %q, want %q", got, "event-id")
	}
}
