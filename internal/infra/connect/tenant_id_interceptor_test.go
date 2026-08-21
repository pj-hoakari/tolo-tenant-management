package connect

import (
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

func TestClaimPolicyFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		procedure string
		want      claimPolicy
	}{
		{procedure: tenantv1connect.TenantServiceStartTenantRegistrationProcedure, want: claimPolicyNone},
		{procedure: tenantv1connect.TenantServiceClaimTenantOwnershipProcedure, want: claimPolicySubjectOnly},
		{procedure: tenantv1connect.TenantServiceGetEventProcedure, want: claimPolicyTenantRequired},
		{procedure: tenantv1connect.TenantServiceGetObservationSettingsProcedure, want: claimPolicyTenantOptional},
		{procedure: tenantv1connect.TenantServiceCreateEventProcedure, want: claimPolicyTenantRequired},
		{procedure: tenantv1connect.TenantServiceUpdateObservationSettingsProcedure, want: claimPolicyTenantRequired},
		{procedure: tenantv1connect.TenantServiceListEventsProcedure, want: claimPolicyTenantRequired},
	}
	for _, tt := range tests {
		t.Run(tt.procedure, func(t *testing.T) {
			if got := claimPolicyFor(tt.procedure); got != tt.want {
				t.Errorf("claimPolicyFor(%q) = %d, want %d", tt.procedure, got, tt.want)
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
