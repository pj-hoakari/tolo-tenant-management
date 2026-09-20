package domain

import "testing"

func TestRoleGrants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		role  Role
		scope string
		want  bool
	}{
		{name: "owner reads the tenant", role: RoleOwner, scope: ScopeTenantRead, want: true},
		{name: "owner writes the tenant", role: RoleOwner, scope: ScopeTenantWrite, want: true},
		{name: "owner reads events", role: RoleOwner, scope: ScopeEventsRead, want: true},
		{name: "owner manages events", role: RoleOwner, scope: ScopeEventsManage, want: true},
		{name: "owner operates events", role: RoleOwner, scope: ScopeEventsOperate, want: true},
		{name: "owner reports events", role: RoleOwner, scope: ScopeEventsReport, want: true},
		{name: "admin is owner-equivalent", role: RoleAdmin, scope: ScopeTenantWrite, want: true},
		{name: "staff reads the tenant", role: RoleStaff, scope: ScopeTenantRead, want: true},
		{name: "staff reads events", role: RoleStaff, scope: ScopeEventsRead, want: true},
		{name: "staff does not write the tenant", role: RoleStaff, scope: ScopeTenantWrite, want: false},
		{name: "staff does not manage events", role: RoleStaff, scope: ScopeEventsManage, want: false},
		{name: "staff operates events", role: RoleStaff, scope: ScopeEventsOperate, want: true},
		{name: "staff reports events", role: RoleStaff, scope: ScopeEventsReport, want: true},
		{name: "unspecified grants nothing", role: RoleUnspecified, scope: ScopeTenantRead, want: false},
		{name: "unknown scope", role: RoleOwner, scope: "tenant.admin", want: false},
		{name: "empty scope", role: RoleOwner, scope: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.role.Grants(tt.scope); got != tt.want {
				t.Errorf("Role(%v).Grants(%q) = %v, want %v", tt.role, tt.scope, got, tt.want)
			}
		})
	}
}
