package connect

import (
	"context"
	"errors"

	connectrpc "connectrpc.com/connect"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

var errNotImplemented = errors.New("tenant service implementation is not configured")

// Service is the skeleton Connect transport implementation of TenantService.
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

func (s *Service) RegisterTenant(ctx context.Context, req *connectrpc.Request[tenantv1.RegisterTenantRequest]) (*connectrpc.Response[tenantv1.RegisterTenantResponse], error) {
	tenant, err := s.tenantService.RegisterTenant(ctx, application.RegisterTenantInput{
		Name:         req.Msg.GetName(),
		ContractPlan: req.Msg.GetContractPlan(),
	})
	if err != nil {
		if errors.Is(err, application.ErrTenantNameRequired) || errors.Is(err, application.ErrTenantContractPlanRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrTenantNameAlreadyExists) {
			return nil, connectrpc.NewError(connectrpc.CodeAlreadyExists, err)
		}

		return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
	}

	return connectrpc.NewResponse(&tenantv1.RegisterTenantResponse{
		Tenant: &tenantv1.Tenant{
			TenantId:     tenant.ID,
			Name:         tenant.Name,
			ContractPlan: tenant.ContractPlan,
			Archived:     tenant.Archived,
		},
	}), nil
}

func (s *Service) ChangeTenantContract(context.Context, *connectrpc.Request[tenantv1.ChangeTenantContractRequest]) (*connectrpc.Response[tenantv1.ChangeTenantContractResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ArchiveTenant(context.Context, *connectrpc.Request[tenantv1.ArchiveTenantRequest]) (*connectrpc.Response[tenantv1.ArchiveTenantResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) CreateEvent(ctx context.Context, req *connectrpc.Request[tenantv1.CreateEventRequest]) (*connectrpc.Response[tenantv1.CreateEventResponse], error) {
	event, err := s.tenantService.CreateEvent(ctx, application.CreateEventInput{
		TenantID: req.Msg.GetTenantId(),
		Name:     req.Msg.GetName(),
		Type:     eventTypeString(req.Msg.GetType()),
	})
	if err != nil {
		if errors.Is(err, application.ErrEventTenantIDRequired) || errors.Is(err, application.ErrEventNameRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrTenantNotFound) {
			return nil, connectrpc.NewError(connectrpc.CodeNotFound, err)
		}

		if errors.Is(err, repository.ErrTenantArchived) {
			return nil, connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
		}

		return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
	}

	return connectrpc.NewResponse(&tenantv1.CreateEventResponse{
		Event: &tenantv1.Event{
			EventId:             event.ID,
			TenantId:            event.TenantID,
			Name:                event.Name,
			Type:                eventTypeProto(event.Type),
			Status:              eventStatusProto(event.Status),
			ObservationSettings: nil,
		},
	}), nil
}

func eventTypeString(eventType tenantv1.EventType) string {
	switch eventType {
	case tenantv1.EventType_EVENT_TYPE_SHORT_TERM:
		return "short_term"
	case tenantv1.EventType_EVENT_TYPE_LONG_TERM:
		return "long_term"
	case tenantv1.EventType_EVENT_TYPE_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

func eventTypeProto(eventType string) tenantv1.EventType {
	switch eventType {
	case "short_term":
		return tenantv1.EventType_EVENT_TYPE_SHORT_TERM
	case "long_term":
		return tenantv1.EventType_EVENT_TYPE_LONG_TERM
	default:
		return tenantv1.EventType_EVENT_TYPE_UNSPECIFIED
	}
}

func eventStatusProto(eventStatus string) tenantv1.EventStatus {
	switch eventStatus {
	case "draft":
		return tenantv1.EventStatus_EVENT_STATUS_DRAFT
	case "open":
		return tenantv1.EventStatus_EVENT_STATUS_OPEN
	case "locked":
		return tenantv1.EventStatus_EVENT_STATUS_LOCKED
	case "closed":
		return tenantv1.EventStatus_EVENT_STATUS_CLOSED
	case "archived":
		return tenantv1.EventStatus_EVENT_STATUS_ARCHIVED
	default:
		return tenantv1.EventStatus_EVENT_STATUS_UNSPECIFIED
	}
}

func (s *Service) AssignEventType(context.Context, *connectrpc.Request[tenantv1.AssignEventTypeRequest]) (*connectrpc.Response[tenantv1.AssignEventTypeResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) TransitionEventStatus(context.Context, *connectrpc.Request[tenantv1.TransitionEventStatusRequest]) (*connectrpc.Response[tenantv1.TransitionEventStatusResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) GetEvent(context.Context, *connectrpc.Request[tenantv1.GetEventRequest]) (*connectrpc.Response[tenantv1.GetEventResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ListEvents(context.Context, *connectrpc.Request[tenantv1.ListEventsRequest]) (*connectrpc.Response[tenantv1.ListEventsResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}
