// Package connect provides the Connect transport of RelationAdminService.
package connect

import (
	"context"
	"fmt"
	"net/http"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1/relationv1connect"
	infraconnect "github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// Mount returns the mount of RelationAdminService for the process's handler.
// The service is guarded by an interceptor built from its own generated policy
// table and runs with the process-wide interceptors (tracing) before it.
func Mount(relationService application.RelationUseCases) infraconnect.Mount {
	return func(mux *http.ServeMux, auth infraconnect.AuthInterceptor, interceptors ...connectrpc.ServerInterceptor) error {
		relationAuth, err := auth(relationv1connect.RelationAdminServicePolicies)
		if err != nil {
			return fmt.Errorf("create RelationAdminService authentication interceptor: %w", err)
		}

		server := connectrpc.NewServer(append(interceptors, relationAuth)...)
		relationv1connect.RegisterRelationAdminServiceHandler(server, NewService(relationService))
		connecthttp.Mount(mux, server)

		return nil
	}
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

func (s *Service) AddTenantMember(ctx context.Context, req *relationv1.AddTenantMemberRequest) (*relationv1.AddTenantMemberResponse, error) {
	membership, err := s.relationService.AddTenantMember(ctx, application.AddTenantMemberInput{
		TenantPublicID: req.GetTenantId(),
		UserID:         req.GetUserId(),
		Role:           roleDomain(req.GetTenantRole()),
		DisplayName:    req.GetDisplayName(),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &relationv1.AddTenantMemberResponse{Membership: membershipProto(membership)}, nil
}

func (s *Service) ChangeTenantRole(ctx context.Context, req *relationv1.ChangeTenantRoleRequest) (*relationv1.ChangeTenantRoleResponse, error) {
	membership, err := s.relationService.ChangeTenantRole(ctx, application.ChangeTenantRoleInput{
		TenantPublicID: req.GetTenantId(),
		UserID:         req.GetUserId(),
		Role:           roleDomain(req.GetTenantRole()),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &relationv1.ChangeTenantRoleResponse{Membership: membershipProto(membership)}, nil
}

func (s *Service) GrantEventRole(ctx context.Context, req *relationv1.GrantEventRoleRequest) (*relationv1.GrantEventRoleResponse, error) {
	membership, err := s.relationService.GrantEventRole(ctx, application.GrantEventRoleInput{
		EventPublicID: req.GetEventId(),
		UserID:        req.GetUserId(),
		Role:          roleDomain(req.GetRole()),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &relationv1.GrantEventRoleResponse{Membership: membershipProto(membership)}, nil
}

func (s *Service) RevokeRole(ctx context.Context, req *relationv1.RevokeRoleRequest) (*relationv1.RevokeRoleResponse, error) {
	err := s.relationService.RevokeRole(ctx, application.RevokeRoleInput{
		UserID:         req.GetUserId(),
		TenantPublicID: req.GetTenantId(),
		EventPublicID:  req.GetEventId(),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &relationv1.RevokeRoleResponse{}, nil
}

func (s *Service) ListMemberships(ctx context.Context, req *relationv1.ListMembershipsRequest) (*relationv1.ListMembershipsResponse, error) {
	memberships, err := s.relationService.ListMemberships(ctx, application.ListMembershipsInput{
		TenantPublicID: req.GetTenantId(),
		UserID:         req.GetUserId(),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	response := make([]*relationv1.Membership, 0, len(memberships))
	for _, membership := range memberships {
		response = append(response, membershipProto(membership))
	}

	return &relationv1.ListMembershipsResponse{Memberships: response}, nil
}

var clientErrors = infraconnect.ClientErrors{
	{Target: application.ErrTenantIDRequired, Code: connectrpc.CodeInvalidArgument, Message: "tenant_id is required"},
	{Target: application.ErrEventIDRequired, Code: connectrpc.CodeInvalidArgument, Message: "event_id is required"},
	{Target: application.ErrUserIDRequired, Code: connectrpc.CodeInvalidArgument, Message: "user_id is required"},
	{Target: application.ErrScopeRequired, Code: connectrpc.CodeInvalidArgument, Message: "scope is required"},
	{Target: application.ErrScopeAmbiguous, Code: connectrpc.CodeInvalidArgument, Message: "scope must name either a tenant or an event"},
	{Target: application.ErrFilterRequired, Code: connectrpc.CodeInvalidArgument, Message: "filter is required"},
	{Target: application.ErrFilterAmbiguous, Code: connectrpc.CodeInvalidArgument, Message: "filter must name either a tenant or a user"},
	{Target: domain.ErrRoleRequired, Code: connectrpc.CodeInvalidArgument, Message: "role is required"},
	{Target: domain.ErrRoleReserved, Code: connectrpc.CodeInvalidArgument, Message: "role is reserved and cannot be granted"},
	{Target: domain.ErrDisplayNameRequired, Code: connectrpc.CodeInvalidArgument, Message: "display_name is required"},
	{Target: domain.ErrDisplayNameTooLong, Code: connectrpc.CodeInvalidArgument, Message: "display_name is too long"},
	{Target: tenantrepository.ErrTenantNotFound, Code: connectrpc.CodeNotFound, Message: "tenant not found"},
	{Target: tenantrepository.ErrEventNotFound, Code: connectrpc.CodeNotFound, Message: "event not found"},
	{Target: repository.ErrTenantNotFound, Code: connectrpc.CodeNotFound, Message: "tenant not found"},
	{Target: repository.ErrEventNotFound, Code: connectrpc.CodeNotFound, Message: "event not found"},
	{Target: repository.ErrMembershipNotFound, Code: connectrpc.CodeNotFound, Message: "membership not found"},
	{Target: repository.ErrEventRoleNotFound, Code: connectrpc.CodeNotFound, Message: "event role not found"},
	{Target: tenantrepository.ErrTenantArchived, Code: connectrpc.CodeFailedPrecondition, Message: "tenant is archived"},
	{Target: tenantrepository.ErrEventArchived, Code: connectrpc.CodeFailedPrecondition, Message: "event is archived"},
	{Target: application.ErrTenantPendingOwner, Code: connectrpc.CodeFailedPrecondition, Message: "tenant is pending an owner"},
	{Target: repository.ErrMembershipAlreadyExists, Code: connectrpc.CodeFailedPrecondition, Message: "membership already exists"},
	{Target: repository.ErrTenantMembershipRequired, Code: connectrpc.CodeFailedPrecondition, Message: "event role requires membership of the event's tenant"},
	{Target: tenantctx.ErrMismatch, Code: connectrpc.CodePermissionDenied, Message: "tenant does not match the token"},
	{Target: application.ErrPermissionDenied, Code: connectrpc.CodePermissionDenied, Message: "permission denied"},
	{Target: tenantctx.ErrMissing, Code: connectrpc.CodeUnauthenticated, Message: "tenant ID is missing from the token"},
	{Target: tenantctx.ErrSubjectMissing, Code: connectrpc.CodeUnauthenticated, Message: "subject is missing from the token"},
	{Target: infradb.ErrTransactionAborted, Code: connectrpc.CodeAborted, Message: "transaction aborted; retry the request"},
}

func membershipProto(membership domain.Membership) *relationv1.Membership {
	eventRoles := membership.EventRoles()
	roles := make([]*relationv1.EventRole, 0, len(eventRoles))

	for _, role := range eventRoles {
		roles = append(roles, &relationv1.EventRole{EventId: role.EventPublicID(), Role: roleProto(role.Role())})
	}

	return &relationv1.Membership{
		UserId:      membership.UserID(),
		TenantId:    membership.TenantPublicID(),
		TenantRole:  roleProto(membership.TenantRole()),
		EventRoles:  roles,
		DisplayName: membership.DisplayName().String(),
		JoinedAt:    timestamppb.New(membership.JoinedAt()),
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
