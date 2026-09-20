// Package domain contains immutable models for the relation model: who belongs
// to which tenant and event with which role. It is owned by the relation side
// and is independent of the tenant context's domain package.
package domain

import (
	"errors"
	"fmt"
)

//go:generate go tool stringer -type=Role -trimprefix=Role -linecomment -output=role_enum_string.go

// Role is a tenant or event role. Tenant roles and event roles are
// independent, but draw from the same set.
type Role uint8

const (
	RoleUnspecified Role = iota // unspecified
	RoleOwner                   // owner
	RoleStaff                   // staff
	// RoleAdmin is reserved: it exists in the vocabulary but cannot be
	// granted until it is made a real role.
	RoleAdmin // admin
)

var (
	// ErrRoleRequired rejects an unspecified role.
	ErrRoleRequired = errors.New("role is required")
	// ErrRoleReserved rejects the reserved admin role.
	ErrRoleReserved = errors.New("role is reserved and cannot be granted")
)

// ParseRole maps a string representation (as produced by String) back to a
// Role.
func ParseRole(s string) (Role, error) {
	for r := RoleUnspecified; r <= RoleAdmin; r++ {
		if r.String() == s {
			return r, nil
		}
	}

	return RoleUnspecified, fmt.Errorf("invalid role %q", s)
}

// Grantable reports whether the role may be assigned, returning the reason
// when it may not.
func (r Role) Grantable() error {
	switch r {
	case RoleOwner, RoleStaff:
		return nil
	case RoleAdmin:
		return ErrRoleReserved
	case RoleUnspecified:
		return ErrRoleRequired
	default:
		return fmt.Errorf("invalid role %d", r)
	}
}
