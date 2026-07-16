package connect

import (
	"context"

	connectrpc "connectrpc.com/connect"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

const exampleTenantBearerToken = "example-tenant-token"

// newExampleTenantAuthzVerifier demonstrates where an application would adapt
// validated identity claims to the authorization policies generated from proto.
func newExampleTenantAuthzVerifier() tenantv1connect.Verifier {
	return tenantv1connect.VerifierFunc(func(ctx context.Context, policy tenantv1connect.AuthPolicy) error {
		if policy.Level == tenantv1connect.AuthLevelPublic {
			return nil
		}

		callInfo, ok := connectrpc.CallInfoForHandlerContext(ctx)
		if !ok || callInfo.RequestHeader().Get("Authorization") != "Bearer "+exampleTenantBearerToken {
			return connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
		}

		if policy.Level == tenantv1connect.AuthLevelInternal {
			return connectrpc.NewError(connectrpc.CodePermissionDenied, nil)
		}

		grantedScopes := map[string]bool{
			"tenant.register": true,
			"tenant_access":   true,
			"tenant.write":    true,
			"events.read":     true,
			"events.write":    true,
		}
		for _, requiredScope := range policy.RequiredScopes {
			if !grantedScopes[requiredScope] {
				return connectrpc.NewError(connectrpc.CodePermissionDenied, nil)
			}
		}

		return nil
	})
}

func exampleTenantAuthorizationHeader() string {
	return "Bearer " + exampleTenantBearerToken
}
