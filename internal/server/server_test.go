package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

func TestTenantServiceAuthorizationAndSkeleton(t *testing.T) {
	t.Parallel()

	httpServer := httptest.NewServer(NewHandler())
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	t.Run("rejects missing bearer token", func(t *testing.T) {
		t.Parallel()

		_, err := client.RegisterTenant(context.Background(), connect.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme"}))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("RegisterTenant() error code = %v, want %v", connect.CodeOf(err), connect.CodeUnauthenticated)
		}
	})

	t.Run("authorizes request before skeleton returns unimplemented", func(t *testing.T) {
		t.Parallel()

		req := connect.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme"})
		req.Header().Set("Authorization", exampleTenantAuthorizationHeader())

		_, err := client.RegisterTenant(context.Background(), req)
		if connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatalf("RegisterTenant() error code = %v, want %v", connect.CodeOf(err), connect.CodeUnimplemented)
		}
	})
}
