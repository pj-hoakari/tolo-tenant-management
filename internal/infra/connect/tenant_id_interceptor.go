package connect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

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

// tenantClaimPolicy states whether a procedure needs the tenant_id claim of
// the internal JWT to establish its tenant context.
type tenantClaimPolicy int

const (
	// tenantClaimRequired: the procedure acts inside one tenant, so the claim
	// must be present and becomes the authenticated tenant for the call.
	tenantClaimRequired tenantClaimPolicy = iota
	// tenantClaimOptional: the procedure may be called without tenant context
	// (machine-origin service tokens). When the claim is present it is still
	// honoured as the authenticated tenant.
	tenantClaimOptional
	// tenantClaimNone: the procedure is served without a tenant-bearing
	// credential, so no tenant context is extracted.
	tenantClaimNone
)

// TenantPublicIDFromContext returns the tenant's 16-character hexadecimal
// public ID verified by the transport interceptor.
func TenantPublicIDFromContext(ctx context.Context) (string, bool) {
	return tenantctx.TenantPublicIDFromContext(ctx)
}

func newTenantPublicIDInterceptor(validator JWTValidator) connectrpc.Interceptor {
	return connectrpc.UnaryInterceptorFunc(func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			policy := tenantClaimPolicyFor(req.Spec().Procedure)
			if policy == tenantClaimNone {
				return next(ctx, req)
			}

			claims, err := validator.Claims(ctx, req.Header().Get("Authorization"))
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
			}

			tenantPublicID := strings.TrimSpace(claims.TenantPublicID)
			if tenantPublicID == "" {
				if policy == tenantClaimRequired {
					return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
				}

				return next(ctx, req)
			}

			return next(tenantctx.WithTenantPublicID(ctx, tenantPublicID), req)
		}
	})
}

// requiredTokenUse returns the credential class each procedure accepts.
// StartTenantRegistration is unauthenticated and never reaches this check.
func requiredTokenUse(procedure string) internalTokenUse {
	switch procedure {
	case tenantv1connect.TenantServiceClaimTenantOwnershipProcedure:
		return internalTokenUseRegistration
	case tenantv1connect.TenantServiceGetEventProcedure,
		tenantv1connect.TenantServiceGetObservationSettingsProcedure:
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

// tenantClaimPolicyFor derives the tenant-context requirement of a procedure
// from the service specification (tenant_management_spec.md「参照系 RPC と
// テナント境界」). GetEvent enforces the boundary from a user-origin service
// token; GetObservationSettings does not, because it may be reached from
// machine-origin chains without tenant context.
func tenantClaimPolicyFor(procedure string) tenantClaimPolicy {
	switch procedure {
	case tenantv1connect.TenantServiceStartTenantRegistrationProcedure,
		tenantv1connect.TenantServiceClaimTenantOwnershipProcedure:
		return tenantClaimNone
	case tenantv1connect.TenantServiceGetObservationSettingsProcedure:
		return tenantClaimOptional
	default:
		return tenantClaimRequired
	}
}
