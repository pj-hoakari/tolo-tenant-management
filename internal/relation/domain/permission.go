package domain

import "slices"

// Scopes of the internal JWT that a role can issue (docs/auth_domain.md).
// They name what the holder may do inside one tenant.
const (
	ScopeTenantRead  = "tenant.read"
	ScopeTenantWrite = "tenant.write"
	ScopeEventsRead  = "events.read"
	ScopeEventsWrite = "events.write"
)

var (
	// readScopes are the scopes every role of a tenant can issue.
	readScopes = []string{ScopeTenantRead, ScopeEventsRead}
	// writeScopes are the scopes reserved for the roles that administer the
	// tenant.
	writeScopes = []string{ScopeTenantWrite, ScopeEventsWrite}
)

// Grants reports whether the role can issue the scope. Owner administers the
// tenant and issues every scope; the reserved admin role is owner-equivalent;
// staff only reads. An unspecified role and an unknown scope grant nothing.
func (r Role) Grants(scope string) bool {
	switch r {
	case RoleOwner, RoleAdmin:
		return slices.Contains(readScopes, scope) || slices.Contains(writeScopes, scope)
	case RoleStaff:
		return slices.Contains(readScopes, scope)
	case RoleUnspecified:
		return false
	default:
		return false
	}
}
