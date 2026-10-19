// Package connect provides the Connect transport of TenantService.
package connect

import (
	"context"
	"fmt"
	"net/http"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	infraconnect "github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var clientErrors = infraconnect.ClientErrors{
	{Target: application.ErrTenantNameRequired, Code: connectrpc.CodeInvalidArgument, Message: "name is required"},
	{Target: application.ErrTenantContractPlanRequired, Code: connectrpc.CodeInvalidArgument, Message: "contract_plan is required"},
	{Target: application.ErrTenantIDRequired, Code: connectrpc.CodeInvalidArgument, Message: "tenant_id is required"},
	{Target: application.ErrOwnershipClaimTokenRequired, Code: connectrpc.CodeInvalidArgument, Message: "ownership_claim_token is required"},
	{Target: application.ErrEventNameRequired, Code: connectrpc.CodeInvalidArgument, Message: "name is required"},
	{Target: application.ErrEventIDRequired, Code: connectrpc.CodeInvalidArgument, Message: "event_id is required"},
	{Target: application.ErrEventStatusRequired, Code: connectrpc.CodeInvalidArgument, Message: "to is required"},
	{Target: application.ErrSubdomainRequired, Code: connectrpc.CodeInvalidArgument, Message: "subdomain is required"},
	{Target: domain.ErrInvalidSubdomain, Code: connectrpc.CodeInvalidArgument, Message: "subdomain is invalid"},
	{Target: repository.ErrTenantSubdomainExists, Code: connectrpc.CodeAlreadyExists, Message: "subdomain already exists"},
	{Target: repository.ErrTenantPublicIDExists, Code: connectrpc.CodeAlreadyExists, Message: "generated tenant ID already exists; retry the request"},
	{Target: repository.ErrEventPublicIDExists, Code: connectrpc.CodeAlreadyExists, Message: "generated event ID already exists; retry the request"},
	{Target: repository.ErrTenantNotFound, Code: connectrpc.CodeNotFound, Message: "tenant not found"},
	{Target: repository.ErrEventNotFound, Code: connectrpc.CodeNotFound, Message: "event not found"},
	{Target: application.ErrTenantPendingOwner, Code: connectrpc.CodeFailedPrecondition, Message: "tenant is pending an owner"},
	{Target: repository.ErrTenantArchived, Code: connectrpc.CodeFailedPrecondition, Message: "tenant is archived"},
	{Target: domain.ErrTenantAlreadyArchived, Code: connectrpc.CodeFailedPrecondition, Message: "tenant is already archived"},
	{Target: domain.ErrInvalidEventStatusTransition, Code: connectrpc.CodeFailedPrecondition, Message: "event status transition is not allowed"},
	{Target: application.ErrPermissionDenied, Code: connectrpc.CodePermissionDenied, Message: "permission denied"},
	{Target: tenantctx.ErrMismatch, Code: connectrpc.CodePermissionDenied, Message: "tenant does not match the token"},
	{Target: application.ErrOwnershipClaimRejected, Code: connectrpc.CodeUnauthenticated, Message: "ownership claim rejected"},
	{Target: tenantctx.ErrSubjectMissing, Code: connectrpc.CodeUnauthenticated, Message: "subject is missing from the token"},
	{Target: tenantctx.ErrMissing, Code: connectrpc.CodeUnauthenticated, Message: "tenant ID is missing from the token"},
	{Target: infradb.ErrTransactionAborted, Code: connectrpc.CodeAborted, Message: "transaction aborted; retry the request"},
}

// Mount returns the mount of TenantService for the process's handler. The
// service is guarded by an interceptor built from its own generated policy
// table and runs with the process-wide interceptors (tracing) before it.
func Mount(tenantService application.TenantUseCases) infraconnect.Mount {
	return func(mux *http.ServeMux, auth infraconnect.AuthInterceptor, interceptors ...connectrpc.ServerInterceptor) error {
		tenantAuth, err := auth(tenantv1connect.TenantServicePolicies)
		if err != nil {
			return fmt.Errorf("create TenantService authentication interceptor: %w", err)
		}

		server := connectrpc.NewServer(append(interceptors, tenantAuth)...)
		tenantv1connect.RegisterTenantServiceHandler(server, NewService(tenantService))
		connecthttp.Mount(mux, server)

		return nil
	}
}

// Service is the Connect transport implementation of TenantService.
type Service struct {
	tenantv1connect.UnimplementedTenantServiceHandler
	tenantService application.TenantUseCases
}

func NewService(tenantService application.TenantUseCases) *Service {
	return &Service{
		UnimplementedTenantServiceHandler: tenantv1connect.UnimplementedTenantServiceHandler{},
		tenantService:                     tenantService,
	}
}

func (s *Service) StartTenantRegistration(ctx context.Context, req *tenantv1.StartTenantRegistrationRequest) (*tenantv1.StartTenantRegistrationResponse, error) {
	registration, err := s.tenantService.StartTenantRegistration(ctx, application.StartTenantRegistrationInput{
		Name:         req.GetName(),
		ContractPlan: req.GetContractPlan(),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	// The plaintext claim token appears in this response only; it is never
	// persisted, logged, or echoed in errors.
	return &tenantv1.StartTenantRegistrationResponse{
		Tenant:              tenantProto(registration.Tenant),
		OwnershipClaimToken: registration.ClaimToken,
		ExpiresAt:           timestamppb.New(registration.ExpiresAt),
	}, nil
}

func (s *Service) ClaimTenantOwnership(ctx context.Context, req *tenantv1.ClaimTenantOwnershipRequest) (*tenantv1.ClaimTenantOwnershipResponse, error) {
	tenant, err := s.tenantService.ClaimTenantOwnership(ctx, application.ClaimTenantOwnershipInput{
		TenantPublicID: req.GetTenantId(),
		ClaimToken:     req.GetOwnershipClaimToken(),
		Subdomain:      req.GetSubdomain(),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &tenantv1.ClaimTenantOwnershipResponse{Tenant: tenantProto(tenant)}, nil
}

func (s *Service) ResolveTenantSubdomain(ctx context.Context, req *tenantv1.ResolveTenantSubdomainRequest) (*tenantv1.ResolveTenantSubdomainResponse, error) {
	tenantID, err := s.tenantService.ResolveTenantSubdomain(ctx, req.GetSubdomain())
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &tenantv1.ResolveTenantSubdomainResponse{TenantId: tenantID}, nil
}

func (s *Service) ChangeTenantContract(ctx context.Context, req *tenantv1.ChangeTenantContractRequest) (*tenantv1.ChangeTenantContractResponse, error) {
	tenant, err := s.tenantService.ChangeTenantContract(ctx, application.ChangeTenantContractInput{
		TenantPublicID: req.GetTenantId(),
		ContractPlan:   req.GetContractPlan(),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &tenantv1.ChangeTenantContractResponse{Tenant: tenantProto(tenant)}, nil
}

func (s *Service) ArchiveTenant(ctx context.Context, req *tenantv1.ArchiveTenantRequest) (*tenantv1.ArchiveTenantResponse, error) {
	tenant, err := s.tenantService.ArchiveTenant(ctx, application.ArchiveTenantInput{TenantPublicID: req.GetTenantId()})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &tenantv1.ArchiveTenantResponse{Tenant: tenantProto(tenant)}, nil
}

func (s *Service) CreateEvent(ctx context.Context, req *tenantv1.CreateEventRequest) (*tenantv1.CreateEventResponse, error) {
	event, err := s.tenantService.CreateEvent(ctx, application.CreateEventInput{
		TenantPublicID: req.GetTenantId(),
		Name:           req.GetName(),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &tenantv1.CreateEventResponse{Event: eventProto(event)}, nil
}

func eventStatusProto(eventStatus domain.EventStatus) tenantv1.EventStatus {
	switch eventStatus {
	case domain.EventStatusUnspecified:
		return tenantv1.EventStatus_EVENT_STATUS_UNSPECIFIED
	case domain.EventStatusDraft:
		return tenantv1.EventStatus_EVENT_STATUS_DRAFT
	case domain.EventStatusOpen:
		return tenantv1.EventStatus_EVENT_STATUS_OPEN
	case domain.EventStatusLocked:
		return tenantv1.EventStatus_EVENT_STATUS_LOCKED
	case domain.EventStatusClosed:
		return tenantv1.EventStatus_EVENT_STATUS_CLOSED
	case domain.EventStatusArchived:
		return tenantv1.EventStatus_EVENT_STATUS_ARCHIVED
	default:
		return tenantv1.EventStatus_EVENT_STATUS_UNSPECIFIED
	}
}

func eventStatusDomain(eventStatus tenantv1.EventStatus) domain.EventStatus {
	switch eventStatus {
	case tenantv1.EventStatus_EVENT_STATUS_UNSPECIFIED:
		return domain.EventStatusUnspecified
	case tenantv1.EventStatus_EVENT_STATUS_DRAFT:
		return domain.EventStatusDraft
	case tenantv1.EventStatus_EVENT_STATUS_OPEN:
		return domain.EventStatusOpen
	case tenantv1.EventStatus_EVENT_STATUS_LOCKED:
		return domain.EventStatusLocked
	case tenantv1.EventStatus_EVENT_STATUS_CLOSED:
		return domain.EventStatusClosed
	case tenantv1.EventStatus_EVENT_STATUS_ARCHIVED:
		return domain.EventStatusArchived
	default:
		return domain.EventStatusUnspecified
	}
}

func (s *Service) TransitionEventStatus(ctx context.Context, req *tenantv1.TransitionEventStatusRequest) (*tenantv1.TransitionEventStatusResponse, error) {
	event, err := s.tenantService.TransitionEventStatus(ctx, application.TransitionEventStatusInput{
		EventPublicID: req.GetEventId(),
		To:            eventStatusDomain(req.GetTo()),
	})
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &tenantv1.TransitionEventStatusResponse{Event: eventProto(event)}, nil
}

func tenantProto(tenant domain.Tenant) *tenantv1.Tenant {
	return &tenantv1.Tenant{
		TenantId:       tenant.PublicID(),
		Name:           tenant.Name(),
		ContractPlan:   tenant.ContractPlan(),
		Archived:       tenant.Archived(),
		OwnershipState: tenantOwnershipStateProto(tenant.OwnershipState()),
		Subdomain:      tenant.Subdomain().String(),
	}
}

func tenantOwnershipStateProto(state domain.TenantOwnershipState) tenantv1.TenantOwnershipState {
	switch state {
	case domain.TenantOwnershipStateUnspecified:
		return tenantv1.TenantOwnershipState_TENANT_OWNERSHIP_STATE_UNSPECIFIED
	case domain.TenantOwnershipStatePendingOwner:
		return tenantv1.TenantOwnershipState_TENANT_OWNERSHIP_STATE_PENDING_OWNER
	case domain.TenantOwnershipStateOwned:
		return tenantv1.TenantOwnershipState_TENANT_OWNERSHIP_STATE_OWNED
	default:
		return tenantv1.TenantOwnershipState_TENANT_OWNERSHIP_STATE_UNSPECIFIED
	}
}

// eventProto maps an event to its wire representation. Only public IDs are
// exposed; the internal primary keys never leave the service.
func eventProto(event domain.Event) *tenantv1.Event {
	return &tenantv1.Event{
		EventId:  event.PublicID(),
		TenantId: event.TenantPublicID(),
		Name:     event.Name(),
		Status:   eventStatusProto(event.Status()),
	}
}

func (s *Service) GetEvent(ctx context.Context, req *tenantv1.GetEventRequest) (*tenantv1.GetEventResponse, error) {
	event, err := s.tenantService.GetEvent(ctx, req.GetEventId())
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	return &tenantv1.GetEventResponse{Event: eventProto(event)}, nil
}

func (s *Service) ListEvents(ctx context.Context, req *tenantv1.ListEventsRequest) (*tenantv1.ListEventsResponse, error) {
	events, err := s.tenantService.ListEvents(ctx, req.GetTenantId(), req.GetIncludeArchived())
	if err != nil {
		return nil, clientErrors.For(ctx, err)
	}

	responseEvents := make([]*tenantv1.Event, 0, len(events))
	for _, event := range events {
		responseEvents = append(responseEvents, eventProto(event))
	}

	return &tenantv1.ListEventsResponse{Events: responseEvents}, nil
}
