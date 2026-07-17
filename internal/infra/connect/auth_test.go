package connect

import (
	"context"
	"testing"

	connectrpc "connectrpc.com/connect"
	"go.uber.org/mock/gomock"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
)

func TestAuthorizeInternalJWT(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	validator := NewMockJWTValidator(controller)
	validator.EXPECT().Claims(gomock.Any(), "Bearer test-token").Return(jwks.InternalJWTClaims{
		TokenUse: internalTokenUseTenantAccess,
		Scope:    "tenant_access events.write",
	}, nil)

	err := authorizeInternalJWT(context.Background(), validator, "Bearer test-token", tenantv1connect.TenantServiceCreateEventProcedure, []string{"events.write"})
	if err != nil {
		t.Fatalf("authorizeInternalJWT() error = %v", err)
	}
}

func TestAuthorizeInternalJWTRejectsTokenUse(t *testing.T) {
	t.Parallel()
	controller := gomock.NewController(t)
	validator := NewMockJWTValidator(controller)
	validator.EXPECT().Claims(gomock.Any(), "Bearer service-token").Return(jwks.InternalJWTClaims{TokenUse: internalTokenUseService}, nil)

	err := authorizeInternalJWT(context.Background(), validator, "Bearer service-token", tenantv1connect.TenantServiceCreateEventProcedure, nil)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
		t.Errorf("error code = %v, want %v", got, want)
	}
}
