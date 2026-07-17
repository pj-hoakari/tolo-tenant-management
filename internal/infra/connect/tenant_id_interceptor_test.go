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
		{procedure: tenantv1connect.TenantServiceRegisterTenantProcedure, want: true},
		{procedure: tenantv1connect.TenantServiceGetEventProcedure, want: true},
		{procedure: tenantv1connect.TenantServiceCreateEventProcedure, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.procedure, func(t *testing.T) {
			if got := tenantIDNotRequired(tt.procedure); got != tt.want {
				t.Errorf("tenantIDNotRequired(%q) = %t, want %t", tt.procedure, got, tt.want)
			}
		})
	}
}
