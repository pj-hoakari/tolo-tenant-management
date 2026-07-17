package connect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

type tenantIDContextKey struct{}

const (
	internalJWTIssuer   = "api-gateway"
	internalJWTAudience = "tolo-tenant-management"
)

type internalTokenUse = string

const (
	internalTokenUseTenantAccess internalTokenUse = "tenant_access"
	internalTokenUseService      internalTokenUse = "service"
	internalTokenUseRegistration internalTokenUse = "registration"
)

// TenantIDFromContext returns the tenant ID verified by the transport
// interceptor.
func TenantIDFromContext(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(tenantIDContextKey{}).(string)

	return tenantID, ok
}

func newTenantIDInterceptor(validator JWTValidator) connectrpc.Interceptor {
	return connectrpc.UnaryInterceptorFunc(func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			if tenantIDNotRequired(req.Spec().Procedure) {
				return next(ctx, req)
			}

			claims, err := validator.Claims(ctx, req.Header().Get("Authorization"))

			tenantID, ok := tenantIDFromClaims(claims)
			if err != nil || !ok {
				return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
			}

			return next(context.WithValue(ctx, tenantIDContextKey{}, tenantID), req)
		}
	})
}

func requiredTokenUse(procedure string) internalTokenUse {
	switch procedure {
	case tenantv1connect.TenantServiceRegisterTenantProcedure:
		return internalTokenUseRegistration
	case tenantv1connect.TenantServiceGetEventProcedure:
		return internalTokenUseService
	default:
		return internalTokenUseTenantAccess
	}
}

func hasScope(scope, requiredScope string) bool {
	for _, grantedScope := range strings.Fields(scope) {
		if grantedScope == requiredScope {
			return true
		}
	}

	return false
}

func tenantIDNotRequired(procedure string) bool {
	switch procedure {
	case tenantv1connect.TenantServiceRegisterTenantProcedure,
		tenantv1connect.TenantServiceGetEventProcedure:
		return true
	default:
		return false
	}
}
