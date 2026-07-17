package connect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"

	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/jwks"
)

func newTenantAuthzVerifier(validator JWTValidator) tenantv1connect.Verifier {
	return tenantv1connect.VerifierFunc(func(ctx context.Context, policy tenantv1connect.AuthPolicy) error {
		if policy.Level == tenantv1connect.AuthLevelPublic {
			return nil
		}

		callInfo, ok := connectrpc.CallInfoForHandlerContext(ctx)
		if !ok {
			return connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
		}

		return authorizeInternalJWT(ctx, validator, callInfo.RequestHeader().Get("Authorization"), callInfo.Spec().Procedure, policy.RequiredScopes)
	})
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

func tenantIDFromClaims(claims jwks.InternalJWTClaims) (string, bool) {
	tenantID := strings.TrimSpace(claims.TenantID)

	return tenantID, claims.TokenUse == internalTokenUseTenantAccess && tenantID != ""
}
