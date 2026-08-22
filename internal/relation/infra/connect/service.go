// Package connect provides the Connect transport of RelationAdminService.
package connect

import (
	"context"
	"errors"
	"net/http"

	connectrpc "connectrpc.com/connect"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1/relationv1connect"
	tenantconnect "github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// Mount returns the mount of RelationAdminService for the process's handler.
// The service runs with the same validator and interceptors as TenantService.
func Mount(relationService application.RelationUseCases) tenantconnect.Mount {
	return func(mux *http.ServeMux, validator tenantconnect.JWTValidator, interceptors ...connectrpc.Interceptor) {
		path, handler := relationv1connect.NewRelationAdminServiceHandlerWithAuthz(
			NewService(relationService),
			newAuthzVerifier(validator),
			connectrpc.WithInterceptors(interceptors...),
		)
		mux.Handle(path, handler)
	}
}

func newAuthzVerifier(validator tenantconnect.JWTValidator) relationv1connect.Verifier {
	return relationv1connect.VerifierFunc(func(ctx context.Context, policy relationv1connect.AuthPolicy) error {
		if policy.Level == relationv1connect.AuthLevelPublic {
			return nil
		}

		return tenantconnect.AuthorizeCall(ctx, validator, policy.RequiredScopes)
	})
}

// Service is the Connect transport implementation of RelationAdminService.
type Service struct {
	relationv1connect.UnimplementedRelationAdminServiceHandler
	relationService application.RelationUseCases
}

func NewService(relationService application.RelationUseCases) *Service {
	return &Service{
		UnimplementedRelationAdminServiceHandler: relationv1connect.UnimplementedRelationAdminServiceHandler{},
		relationService:                          relationService,
	}
}

func (s *Service) AddTenantMember(ctx context.Context, req *connectrpc.Request[relationv1.AddTenantMemberRequest]) (*connectrpc.Response[relationv1.AddTenantMemberResponse], error) {
	membership, err := s.relationService.AddTenantMember(ctx, application.AddTenantMemberInput{
		TenantPublicID: req.Msg.GetTenantId(),
		UserID:         req.Msg.GetUserId(),
		Role:           roleDomain(req.Msg.GetTenantRole()),
	})
	if err != nil {
		return nil, connectError(ctx, err)
	}

	return connectrpc.NewResponse(&relationv1.AddTenantMemberResponse{Membership: membershipProto(membership)}), nil
}

func (s *Service) ChangeTenantRole(ctx context.Context, req *connectrpc.Request[relationv1.ChangeTenantRoleRequest]) (*connectrpc.Response[relationv1.ChangeTenantRoleResponse], error) {
	membership, err := s.relationService.ChangeTenantRole(ctx, application.ChangeTenantRoleInput{
		TenantPublicID: req.Msg.GetTenantId(),
		UserID:         req.Msg.GetUserId(),
		Role:           roleDomain(req.Msg.GetTenantRole()),
	})
	if err != nil {
		return nil, connectError(ctx, err)
	}

	return connectrpc.NewResponse(&relationv1.ChangeTenantRoleResponse{Membership: membershipProto(membership)}), nil
}

func (s *Service) GrantEventRole(ctx context.Context, req *connectrpc.Request[relationv1.GrantEventRoleRequest]) (*connectrpc.Response[relationv1.GrantEventRoleResponse], error) {
	membership, err := s.relationService.GrantEventRole(ctx, application.GrantEventRoleInput{
		EventPublicID: req.Msg.GetEventId(),
		UserID:        req.Msg.GetUserId(),
		Role:          roleDomain(req.Msg.GetRole()),
	})
	if err != nil {
		return nil, connectError(ctx, err)
	}

	return connectrpc.NewResponse(&relationv1.GrantEventRoleResponse{Membership: membershipProto(membership)}), nil
}

func (s *Service) RevokeRole(ctx context.Context, req *connectrpc.Request[relationv1.RevokeRoleRequest]) (*connectrpc.Response[relationv1.RevokeRoleResponse], error) {
	err := s.relationService.RevokeRole(ctx, application.RevokeRoleInput{
		UserID:         req.Msg.GetUserId(),
		TenantPublicID: req.Msg.GetTenantId(),
		EventPublicID:  req.Msg.GetEventId(),
	})
	if err != nil {
		return nil, connectError(ctx, err)
	}

	return connectrpc.NewResponse(&relationv1.RevokeRoleResponse{}), nil
}

func (s *Service) ListMemberships(ctx context.Context, req *connectrpc.Request[relationv1.ListMembershipsRequest]) (*connectrpc.Response[relationv1.ListMembershipsResponse], error) {
	memberships, err := s.relationService.ListMemberships(ctx, application.ListMembershipsInput{
		TenantPublicID: req.Msg.GetTenantId(),
		UserID:         req.Msg.GetUserId(),
	})
	if err != nil {
		return nil, connectError(ctx, err)
	}

	response := make([]*relationv1.Membership, 0, len(memberships))
	for _, membership := range memberships {
		response = append(response, membershipProto(membership))
	}

	return connectrpc.NewResponse(&relationv1.ListMembershipsResponse{Memberships: response}), nil
}

// connectError maps use case and repository errors to Connect codes
// (tenant_management_spec.md「エラー」). Missing identifiers and the reserved
// role are invalid arguments; unknown tenants, events, and memberships are not
// found; relation model violations and frozen (archived / pending) targets are
// failed preconditions. A caller whose current membership no longer permits
// the write is denied, and one without a subject is unauthenticated. A
// transaction PostgreSQL aborted is reported as aborted, which tells the
// client the call can be retried as it stands. Anything else is an internal
// failure, reported without its detail (tenantconnect.InternalError).
func connectError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, application.ErrTenantIDRequired),
		errors.Is(err, application.ErrEventIDRequired),
		errors.Is(err, application.ErrUserIDRequired),
		errors.Is(err, application.ErrScopeRequired),
		errors.Is(err, application.ErrScopeAmbiguous),
		errors.Is(err, application.ErrFilterRequired),
		errors.Is(err, application.ErrFilterAmbiguous),
		errors.Is(err, domain.ErrRoleRequired),
		errors.Is(err, domain.ErrRoleReserved):
		return connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
	case errors.Is(err, tenantrepository.ErrTenantNotFound),
		errors.Is(err, tenantrepository.ErrEventNotFound),
		errors.Is(err, repository.ErrTenantNotFound),
		errors.Is(err, repository.ErrEventNotFound),
		errors.Is(err, repository.ErrMembershipNotFound),
		errors.Is(err, repository.ErrEventRoleNotFound):
		return connectrpc.NewError(connectrpc.CodeNotFound, err)
	case errors.Is(err, tenantrepository.ErrTenantArchived),
		errors.Is(err, tenantrepository.ErrEventArchived),
		errors.Is(err, application.ErrTenantPendingOwner),
		errors.Is(err, repository.ErrMembershipAlreadyExists),
		errors.Is(err, repository.ErrTenantMembershipRequired):
		return connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
	case errors.Is(err, tenantctx.ErrMismatch),
		errors.Is(err, application.ErrPermissionDenied):
		return connectrpc.NewError(connectrpc.CodePermissionDenied, err)
	case errors.Is(err, tenantctx.ErrMissing),
		errors.Is(err, tenantctx.ErrSubjectMissing):
		return connectrpc.NewError(connectrpc.CodeUnauthenticated, err)
	case errors.Is(err, infradb.ErrTransactionAborted):
		return connectrpc.NewError(connectrpc.CodeAborted, err)
	default:
		return tenantconnect.InternalError(ctx, err)
	}
}

func membershipProto(membership domain.Membership) *relationv1.Membership {
	eventRoles := membership.EventRoles()
	roles := make([]*relationv1.EventRole, 0, len(eventRoles))

	for _, role := range eventRoles {
		roles = append(roles, &relationv1.EventRole{EventId: role.EventPublicID(), Role: roleProto(role.Role())})
	}

	return &relationv1.Membership{
		UserId:     membership.UserID(),
		TenantId:   membership.TenantPublicID(),
		TenantRole: roleProto(membership.TenantRole()),
		EventRoles: roles,
	}
}

func roleDomain(role relationv1.Role) domain.Role {
	switch role {
	case relationv1.Role_ROLE_UNSPECIFIED:
		return domain.RoleUnspecified
	case relationv1.Role_ROLE_OWNER:
		return domain.RoleOwner
	case relationv1.Role_ROLE_STAFF:
		return domain.RoleStaff
	case relationv1.Role_ROLE_ADMIN:
		return domain.RoleAdmin
	default:
		return domain.RoleUnspecified
	}
}

func roleProto(role domain.Role) relationv1.Role {
	switch role {
	case domain.RoleUnspecified:
		return relationv1.Role_ROLE_UNSPECIFIED
	case domain.RoleOwner:
		return relationv1.Role_ROLE_OWNER
	case domain.RoleStaff:
		return relationv1.Role_ROLE_STAFF
	case domain.RoleAdmin:
		return relationv1.Role_ROLE_ADMIN
	default:
		return relationv1.Role_ROLE_UNSPECIFIED
	}
}
