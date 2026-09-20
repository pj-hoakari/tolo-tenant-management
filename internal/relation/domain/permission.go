package domain

import "slices"

// Scopes of the internal JWT that a role can issue (docs/auth_domain.md).
// They name what the holder may do inside one tenant.
const (
	ScopeTenantRead    = "tenant.read"
	ScopeTenantWrite   = "tenant.write"
	ScopeEventsRead    = "events.read"
	ScopeEventsManage  = "events.manage"
	ScopeEventsOperate = "events.operate"
	ScopeEventsReport  = "events.report"
)

var (
	// memberScopes are the scopes every role of a tenant can issue.
	memberScopes = []string{ScopeTenantRead, ScopeEventsRead, ScopeEventsManage, ScopeEventsOperate, ScopeEventsReport}
	// adminScopes are the scopes reserved for the roles that administer the
	// tenant.
	adminScopes = []string{ScopeTenantWrite}
)

// Grants reports whether the role can issue the scope. Owner administers the
// tenant and issues every scope; the reserved admin role is owner-equivalent;
// staff issues every scope but the write of the tenant. An unspecified role and
// an unknown scope grant nothing.
func (r Role) Grants(scope string) bool {
	switch r {
	case RoleOwner, RoleAdmin:
		return slices.Contains(memberScopes, scope) || slices.Contains(adminScopes, scope)
	case RoleStaff:
		return slices.Contains(memberScopes, scope)
	case RoleUnspecified:
		return false
	default:
		return false
	}
}
