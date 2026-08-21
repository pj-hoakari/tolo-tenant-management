package connect

import (
	"context"
	"strings"

	connectrpc "connectrpc.com/connect"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

type internalTokenUse = string

const (
	internalTokenUseTenantAccess internalTokenUse = "tenant_access"
	internalTokenUseService      internalTokenUse = "service"
	internalTokenUseRegistration internalTokenUse = "registration"
)

// claimPolicy states which facts a procedure takes from the internal JWT to
// establish its request context.
type claimPolicy int

const (
	// claimPolicyNone: the procedure is unauthenticated; nothing is extracted.
	claimPolicyNone claimPolicy = iota
	// claimPolicySubjectOnly: the credential carries no tenant context by
	// design (registration); only the subject is extracted.
	claimPolicySubjectOnly
	// claimPolicyTenantOptional: the procedure may be called without tenant
	// context (machine-origin service tokens). When the tenant_id claim is
	// present it is still honoured as the authenticated tenant.
	claimPolicyTenantOptional
	// claimPolicyTenantRequired: the procedure acts inside one tenant, so the
	// tenant_id claim must be present and becomes the authenticated tenant.
	claimPolicyTenantRequired
)

// TenantPublicIDFromContext returns the tenant's 16-character hexadecimal
// public ID verified by the transport interceptor.
func TenantPublicIDFromContext(ctx context.Context) (string, bool) {
	return tenantctx.TenantPublicIDFromContext(ctx)
}

// NewClaimInterceptor extracts the authenticated subject and tenant from the
// internal JWT into the request context according to the procedure's claim
// policy. It is shared by every service served by this process.
func NewClaimInterceptor(validator JWTValidator) connectrpc.Interceptor {
	return connectrpc.UnaryInterceptorFunc(func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			policy := claimPolicyFor(req.Spec().Procedure)
			if policy == claimPolicyNone {
				return next(ctx, req)
			}

			claims, err := validator.Claims(ctx, req.Header().Get("Authorization"))
			if err != nil {
				return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated, nil)
			}

			ctx = tenantctx.WithSubject(ctx, strings.TrimSpace(claims.Subject))

			if policy == claimPolicySubjectOnly {
				return next(ctx, req)
			}

			tenantPublicID := strings.TrimSpace(claims.TenantPublicID)
			if tenantPublicID == "" {
				if policy == claimPolicyTenantRequired {
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
// Procedures of other services (RelationAdminService) take the default,
// tenant_access.
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

// claimPolicyFor derives the request-context requirement of a procedure from
// the service specification (tenant_management_spec.md「参照系 RPC と
// テナント境界」「オンボーディング」). GetEvent enforces the boundary from a
// user-origin service token; GetObservationSettings does not, because it may
// be reached from machine-origin chains without tenant context. Procedures of
// other services (RelationAdminService) take the default: the tenant claim is
// required.
func claimPolicyFor(procedure string) claimPolicy {
	switch procedure {
	case tenantv1connect.TenantServiceStartTenantRegistrationProcedure:
		return claimPolicyNone
	case tenantv1connect.TenantServiceClaimTenantOwnershipProcedure:
		return claimPolicySubjectOnly
	case tenantv1connect.TenantServiceGetObservationSettingsProcedure:
		return claimPolicyTenantOptional
	default:
		return claimPolicyTenantRequired
	}
}
