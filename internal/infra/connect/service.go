package connect

import (
	"context"
	"errors"

	connectrpc "connectrpc.com/connect"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var errNotImplemented = errors.New("tenant service method is not implemented")

// tenantContextErrorCode maps the tenant-context guard errors to Connect codes.
// A mismatch is a cross-tenant access attempt (permission denied); a missing
// context tenant on a tenant-scoped call means the caller is not authenticated
// as any tenant.
func tenantContextErrorCode(err error) (connectrpc.Code, bool) {
	switch {
	case errors.Is(err, tenantctx.ErrMismatch):
		return connectrpc.CodePermissionDenied, true
	case errors.Is(err, tenantctx.ErrMissing):
		return connectrpc.CodeUnauthenticated, true
	default:
		return 0, false
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
			TenantId:       tenant.ID(),
			Name:           tenant.Name(),
			ContractPlan:   tenant.ContractPlan(),
			Archived:       tenant.Archived(),
			TenantPublicId: tenant.PublicID(),
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
		TenantID: req.Msg.GetTenantPublicId(),
		Name:     req.Msg.GetName(),
		Type:     eventTypeDomain(req.Msg.GetType()),
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

		if code, ok := tenantContextErrorCode(err); ok {
			return nil, connectrpc.NewError(code, err)
		}

		return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
	}

	return connectrpc.NewResponse(&tenantv1.CreateEventResponse{
		Event: &tenantv1.Event{
			EventId:             event.ID(),
			TenantId:            event.TenantID(),
			Name:                event.Name(),
			Type:                eventTypeProto(event.Type()),
			Status:              eventStatusProto(event.Status()),
			ObservationSettings: nil,
			EventPublicId:       event.PublicID(),
			TenantPublicId:      event.TenantPublicID(),
		},
	}), nil
}

func eventTypeDomain(eventType tenantv1.EventType) domain.EventType {
	switch eventType {
	case tenantv1.EventType_EVENT_TYPE_UNSPECIFIED:
		return domain.EventTypeUnspecified
	case tenantv1.EventType_EVENT_TYPE_SHORT_TERM:
		return domain.EventTypeShortTerm
	case tenantv1.EventType_EVENT_TYPE_LONG_TERM:
		return domain.EventTypeLongTerm
	default:
		return domain.EventTypeUnspecified
	}
}

func eventTypeProto(eventType domain.EventType) tenantv1.EventType {
	switch eventType {
	case domain.EventTypeUnspecified:
		return tenantv1.EventType_EVENT_TYPE_UNSPECIFIED
	case domain.EventTypeShortTerm:
		return tenantv1.EventType_EVENT_TYPE_SHORT_TERM
	case domain.EventTypeLongTerm:
		return tenantv1.EventType_EVENT_TYPE_LONG_TERM
	default:
		return tenantv1.EventType_EVENT_TYPE_UNSPECIFIED
	}
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

func (s *Service) AssignEventType(ctx context.Context, req *connectrpc.Request[tenantv1.AssignEventTypeRequest]) (*connectrpc.Response[tenantv1.AssignEventTypeResponse], error) {
	event, err := s.tenantService.AssignEventType(ctx, application.AssignEventTypeInput{
		EventID: req.Msg.GetEventId(),
		Type:    eventTypeDomain(req.Msg.GetType()),
	})
	if err != nil {
		if errors.Is(err, application.ErrEventIDRequired) || errors.Is(err, application.ErrEventTypeRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrEventNotFound) {
			return nil, connectrpc.NewError(connectrpc.CodeNotFound, err)
		}

		if errors.Is(err, repository.ErrEventArchived) || errors.Is(err, repository.ErrTenantArchived) {
			return nil, connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
		}

		if code, ok := tenantContextErrorCode(err); ok {
			return nil, connectrpc.NewError(code, err)
		}

		return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
	}

	return connectrpc.NewResponse(&tenantv1.AssignEventTypeResponse{Event: eventProto(event)}), nil
}

func (s *Service) TransitionEventStatus(ctx context.Context, req *connectrpc.Request[tenantv1.TransitionEventStatusRequest]) (*connectrpc.Response[tenantv1.TransitionEventStatusResponse], error) {
	event, err := s.tenantService.TransitionEventStatus(ctx, application.TransitionEventStatusInput{
		EventID: req.Msg.GetEventId(),
		To:      eventStatusDomain(req.Msg.GetTo()),
	})
	if err != nil {
		if errors.Is(err, application.ErrEventIDRequired) || errors.Is(err, application.ErrEventStatusRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrEventNotFound) {
			return nil, connectrpc.NewError(connectrpc.CodeNotFound, err)
		}

		if errors.Is(err, domain.ErrInvalidEventStatusTransition) || errors.Is(err, repository.ErrTenantArchived) {
			return nil, connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
		}

		if code, ok := tenantContextErrorCode(err); ok {
			return nil, connectrpc.NewError(code, err)
		}

		return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
	}

	return connectrpc.NewResponse(&tenantv1.TransitionEventStatusResponse{
		Event: eventProto(event),
	}), nil
}

func eventProto(event domain.Event) *tenantv1.Event {
	return &tenantv1.Event{
		EventId:             event.ID(),
		TenantId:            event.TenantID(),
		Name:                event.Name(),
		Type:                eventTypeProto(event.Type()),
		Status:              eventStatusProto(event.Status()),
		ObservationSettings: nil,
		EventPublicId:       event.PublicID(),
		TenantPublicId:      event.TenantPublicID(),
	}
}

func (s *Service) GetEvent(ctx context.Context, req *connectrpc.Request[tenantv1.GetEventRequest]) (*connectrpc.Response[tenantv1.GetEventResponse], error) {
	event, err := s.tenantService.GetEvent(ctx, req.Msg.GetEventId())
	if err != nil {
		if errors.Is(err, application.ErrEventIDRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrEventNotFound) {
			return nil, connectrpc.NewError(connectrpc.CodeNotFound, err)
		}

		return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
	}

	return connectrpc.NewResponse(&tenantv1.GetEventResponse{Event: eventProto(event)}), nil
}

func (s *Service) ListEvents(ctx context.Context, req *connectrpc.Request[tenantv1.ListEventsRequest]) (*connectrpc.Response[tenantv1.ListEventsResponse], error) {
	events, err := s.tenantService.ListEvents(ctx, req.Msg.GetTenantId())
	if err != nil {
		if errors.Is(err, application.ErrTenantIDRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrTenantNotFound) {
			return nil, connectrpc.NewError(connectrpc.CodeNotFound, err)
		}

		if code, ok := tenantContextErrorCode(err); ok {
			return nil, connectrpc.NewError(code, err)
		}

		return nil, connectrpc.NewError(connectrpc.CodeInternal, err)
	}

	responseEvents := make([]*tenantv1.Event, 0, len(events))
	for _, event := range events {
		responseEvents = append(responseEvents, eventProto(event))
	}

	return connectrpc.NewResponse(&tenantv1.ListEventsResponse{Events: responseEvents}), nil
}
