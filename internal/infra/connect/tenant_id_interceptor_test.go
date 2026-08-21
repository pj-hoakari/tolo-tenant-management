package connect

import (
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

func TestTenantIDNotRequired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		procedure string
		want      bool
	}{
		{procedure: tenantv1connect.TenantServiceStartTenantRegistrationProcedure, want: true},
		{procedure: tenantv1connect.TenantServiceClaimTenantOwnershipProcedure, want: true},
		{procedure: tenantv1connect.TenantServiceGetEventProcedure, want: true},
		{procedure: tenantv1connect.TenantServiceGetObservationSettingsProcedure, want: true},
		{procedure: tenantv1connect.TenantServiceCreateEventProcedure, want: false},
		{procedure: tenantv1connect.TenantServiceUpdateObservationSettingsProcedure, want: false},
		{procedure: tenantv1connect.TenantServiceListEventsProcedure, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.procedure, func(t *testing.T) {
			if got := tenantIDNotRequired(tt.procedure); got != tt.want {
				t.Errorf("tenantIDNotRequired(%q) = %t, want %t", tt.procedure, got, tt.want)
			}
		})
	}
}

func TestRequiredTokenUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		procedure string
		want      internalTokenUse
	}{
		{procedure: tenantv1connect.TenantServiceClaimTenantOwnershipProcedure, want: internalTokenUseRegistration},
		{procedure: tenantv1connect.TenantServiceGetEventProcedure, want: internalTokenUseService},
		{procedure: tenantv1connect.TenantServiceGetObservationSettingsProcedure, want: internalTokenUseService},
		{procedure: tenantv1connect.TenantServiceCreateEventProcedure, want: internalTokenUseTenantAccess},
		{procedure: tenantv1connect.TenantServiceArchiveTenantProcedure, want: internalTokenUseTenantAccess},
		{procedure: tenantv1connect.TenantServiceUpdateObservationSettingsProcedure, want: internalTokenUseTenantAccess},
	}
	for _, tt := range tests {
		t.Run(tt.procedure, func(t *testing.T) {
			if got := requiredTokenUse(tt.procedure); got != tt.want {
				t.Errorf("requiredTokenUse(%q) = %q, want %q", tt.procedure, got, tt.want)
			}
		})
	}
}
