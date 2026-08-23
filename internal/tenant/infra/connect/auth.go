package connect

import (
	"context"

	connectrpc "connectrpc.com/connect"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

func newTenantAuthzVerifier(validator JWTValidator) tenantv1connect.Verifier {
	return tenantv1connect.VerifierFunc(func(ctx context.Context, policy tenantv1connect.AuthPolicy) error {
		if policy.Level == tenantv1connect.AuthLevelPublic {
			return nil
		}

		return AuthorizeCall(ctx, validator, policy.RequiredScopes)
	})
}

// AuthorizeCall verifies the internal JWT of the call in progress against the
// token_use the procedure accepts and the scopes its policy requires. Every
// service served by this process builds its generated authz Verifier on it,
// so the credential rules stay in one place.
func AuthorizeCall(ctx context.Context, validator JWTValidator, requiredScopes []string) error {
	callInfo, ok := connectrpc.CallInfoForHandlerContext(ctx)
	if !ok {
		return connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
	}

	return authorizeInternalJWT(ctx, validator, callInfo.RequestHeader().Get("Authorization"), callInfo.Spec().Procedure, requiredScopes)
}

func authorizeInternalJWT(ctx context.Context, validator JWTValidator, authorization, procedure string, requiredScopes []string) error {
	claims, err := validator.Claims(ctx, authorization)
	if err != nil || claims.TokenUse != requiredTokenUse(procedure) {
		return connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
	}

	for _, requiredScope := range requiredScopes {
		if !hasScope(claims.Scope, requiredScope) {
			return connectrpc.NewError(connectrpc.CodePermissionDenied, nil)
		}
	}

	return nil
}
