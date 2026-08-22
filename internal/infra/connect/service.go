package connect

import (
	"context"
	"errors"
	"log"

	connectrpc "connectrpc.com/connect"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var errNotImplemented = errors.New("tenant service method is not implemented")

// errInternal is the only detail a client learns about an internal failure.
var errInternal = errors.New("internal error")

// InternalError reports a failure the client can do nothing about. The cause is
// written to the server log and replaced by a fixed message, so that no
// internal detail leaves the service (service_gateway.md「エラー方針」). When
// the request carries a span, the log line names its trace ID, so an operator
// can find the failure in the trace it belongs to.
//
// A cancelled or timed-out request is the client going away rather than a
// server fault, so it keeps its own code and is not logged.
//
// It is exported for the other transports of this process, so that every
// service answers an internal failure the same way.
func InternalError(ctx context.Context, err error) *connectrpc.Error {
	if errors.Is(err, context.Canceled) {
		return connectrpc.NewError(connectrpc.CodeCanceled, err)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return connectrpc.NewError(connectrpc.CodeDeadlineExceeded, err)
	}

	if spanContext := trace.SpanFromContext(ctx).SpanContext(); spanContext.IsValid() {
		log.Printf("tenant-management: internal error: %v trace_id=%s", err, spanContext.TraceID())
	} else {
		log.Printf("tenant-management: internal error: %v", err)
	}

	return connectrpc.NewError(connectrpc.CodeInternal, errInternal)
}

// tenantContextErrorCode maps the tenant-context guard errors to Connect codes.
// A mismatch between the requested tenant and the authenticated tenant is a
// cross-tenant access attempt (permission denied); a missing context tenant on
// a tenant-scoped call means the caller is not authenticated as any tenant.
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

func (s *Service) StartTenantRegistration(ctx context.Context, req *connectrpc.Request[tenantv1.StartTenantRegistrationRequest]) (*connectrpc.Response[tenantv1.StartTenantRegistrationResponse], error) {
	registration, err := s.tenantService.StartTenantRegistration(ctx, application.StartTenantRegistrationInput{
		Name:         req.Msg.GetName(),
		ContractPlan: req.Msg.GetContractPlan(),
	})
	if err != nil {
		if errors.Is(err, application.ErrTenantNameRequired) || errors.Is(err, application.ErrTenantContractPlanRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrTenantNameAlreadyExists) || errors.Is(err, repository.ErrTenantPublicIDExists) {
			return nil, connectrpc.NewError(connectrpc.CodeAlreadyExists, err)
		}

		return nil, InternalError(ctx, err)
	}

	// The plaintext claim token appears in this response only; it is never
	// persisted, logged, or echoed in errors.
	return connectrpc.NewResponse(&tenantv1.StartTenantRegistrationResponse{
		Tenant:              tenantProto(registration.Tenant),
		OwnershipClaimToken: registration.ClaimToken,
		ExpiresAt:           timestamppb.New(registration.ExpiresAt),
	}), nil
}

func (s *Service) ClaimTenantOwnership(ctx context.Context, req *connectrpc.Request[tenantv1.ClaimTenantOwnershipRequest]) (*connectrpc.Response[tenantv1.ClaimTenantOwnershipResponse], error) {
	tenant, err := s.tenantService.ClaimTenantOwnership(ctx, application.ClaimTenantOwnershipInput{
		TenantPublicID: req.Msg.GetTenantId(),
		ClaimToken:     req.Msg.GetOwnershipClaimToken(),
	})
	if err != nil {
		if errors.Is(err, application.ErrTenantIDRequired) || errors.Is(err, application.ErrOwnershipClaimTokenRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		if errors.Is(err, repository.ErrTenantNotFound) {
			return nil, connectrpc.NewError(connectrpc.CodeNotFound, err)
		}

		if errors.Is(err, application.ErrOwnershipClaimRejected) || errors.Is(err, tenantctx.ErrSubjectMissing) {
			return nil, connectrpc.NewError(connectrpc.CodeUnauthenticated, err)
		}

		// The claim runs in a transaction; an aborted one can be retried.
		if errors.Is(err, db.ErrTransactionAborted) {
			return nil, connectrpc.NewError(connectrpc.CodeAborted, err)
		}

		return nil, InternalError(ctx, err)
	}

	return connectrpc.NewResponse(&tenantv1.ClaimTenantOwnershipResponse{Tenant: tenantProto(tenant)}), nil
}

// administrativeTenantWriteError maps the failures of the administrative
// tenant writes (ChangeTenantContract, ArchiveTenant) to Connect codes. Both
// re-check the caller's current membership, so a caller whose permission has
// been revoked or downgraded is answered with permission_denied even though
// the scope of its token still says otherwise.
func administrativeTenantWriteError(err error) error {
	switch {
	case errors.Is(err, application.ErrTenantIDRequired), errors.Is(err, application.ErrTenantContractPlanRequired):
		return connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
	case errors.Is(err, repository.ErrTenantNotFound):
		return connectrpc.NewError(connectrpc.CodeNotFound, err)
	case errors.Is(err, application.ErrTenantPendingOwner),
		errors.Is(err, repository.ErrTenantArchived),
		errors.Is(err, domain.ErrTenantAlreadyArchived):
		return connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
	case errors.Is(err, application.ErrPermissionDenied):
		return connectrpc.NewError(connectrpc.CodePermissionDenied, err)
	case errors.Is(err, tenantctx.ErrSubjectMissing):
		return connectrpc.NewError(connectrpc.CodeUnauthenticated, err)
	// The write runs in a transaction; an aborted one can be retried.
	case errors.Is(err, db.ErrTransactionAborted):
		return connectrpc.NewError(connectrpc.CodeAborted, err)
	default:
		if code, ok := tenantContextErrorCode(err); ok {
			return connectrpc.NewError(code, err)
		}

		return connectrpc.NewError(connectrpc.CodeInternal, err)
	}
}

func (s *Service) ChangeTenantContract(ctx context.Context, req *connectrpc.Request[tenantv1.ChangeTenantContractRequest]) (*connectrpc.Response[tenantv1.ChangeTenantContractResponse], error) {
	tenant, err := s.tenantService.ChangeTenantContract(ctx, application.ChangeTenantContractInput{
		TenantPublicID: req.Msg.GetTenantId(),
		ContractPlan:   req.Msg.GetContractPlan(),
	})
	if err != nil {
		return nil, administrativeTenantWriteError(err)
	}

	return connectrpc.NewResponse(&tenantv1.ChangeTenantContractResponse{Tenant: tenantProto(tenant)}), nil
}

func (s *Service) ArchiveTenant(ctx context.Context, req *connectrpc.Request[tenantv1.ArchiveTenantRequest]) (*connectrpc.Response[tenantv1.ArchiveTenantResponse], error) {
	tenant, err := s.tenantService.ArchiveTenant(ctx, application.ArchiveTenantInput{TenantPublicID: req.Msg.GetTenantId()})
	if err != nil {
		return nil, administrativeTenantWriteError(err)
	}

	return connectrpc.NewResponse(&tenantv1.ArchiveTenantResponse{Tenant: tenantProto(tenant)}), nil
}

func (s *Service) CreateEvent(ctx context.Context, req *connectrpc.Request[tenantv1.CreateEventRequest]) (*connectrpc.Response[tenantv1.CreateEventResponse], error) {
	event, err := s.tenantService.CreateEvent(ctx, application.CreateEventInput{
		TenantPublicID: req.Msg.GetTenantId(),
		Name:           req.Msg.GetName(),
		Type:           eventTypeDomain(req.Msg.GetType()),
	})
	if err != nil {
		if errors.Is(err, application.ErrEventNameRequired) || errors.Is(err, application.ErrTenantIDRequired) {
			return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
		}

		// A public ID collision is a conflict, not an internal failure.
		if errors.Is(err, repository.ErrEventPublicIDExists) {
			return nil, connectrpc.NewError(connectrpc.CodeAlreadyExists, err)
		}

		if errors.Is(err, repository.ErrTenantNotFound) {
			return nil, connectrpc.NewError(connectrpc.CodeNotFound, err)
		}

		if errors.Is(err, repository.ErrTenantArchived) || errors.Is(err, application.ErrTenantPendingOwner) {
			return nil, connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
		}

		if code, ok := tenantContextErrorCode(err); ok {
			return nil, connectrpc.NewError(code, err)
		}

		return nil, InternalError(ctx, err)
	}

	return connectrpc.NewResponse(&tenantv1.CreateEventResponse{Event: eventProto(event)}), nil
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
		EventPublicID: req.Msg.GetEventId(),
		Type:          eventTypeDomain(req.Msg.GetType()),
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

		return nil, InternalError(ctx, err)
	}

	return connectrpc.NewResponse(&tenantv1.AssignEventTypeResponse{Event: eventProto(event)}), nil
}

func (s *Service) TransitionEventStatus(ctx context.Context, req *connectrpc.Request[tenantv1.TransitionEventStatusRequest]) (*connectrpc.Response[tenantv1.TransitionEventStatusResponse], error) {
	event, err := s.tenantService.TransitionEventStatus(ctx, application.TransitionEventStatusInput{
		EventPublicID: req.Msg.GetEventId(),
		To:            eventStatusDomain(req.Msg.GetTo()),
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

		return nil, InternalError(ctx, err)
	}

	return connectrpc.NewResponse(&tenantv1.TransitionEventStatusResponse{Event: eventProto(event)}), nil
}

func tenantProto(tenant domain.Tenant) *tenantv1.Tenant {
	return &tenantv1.Tenant{
		TenantId:       tenant.PublicID(),
		Name:           tenant.Name(),
		ContractPlan:   tenant.ContractPlan(),
		Archived:       tenant.Archived(),
		OwnershipState: tenantOwnershipStateProto(tenant.OwnershipState()),
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
		Type:     eventTypeProto(event.Type()),
		Status:   eventStatusProto(event.Status()),
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

		if code, ok := tenantContextErrorCode(err); ok {
			return nil, connectrpc.NewError(code, err)
		}

		return nil, InternalError(ctx, err)
	}

	return connectrpc.NewResponse(&tenantv1.GetEventResponse{Event: eventProto(event)}), nil
}

func (s *Service) GetObservationSettings(context.Context, *connectrpc.Request[tenantv1.GetObservationSettingsRequest]) (*connectrpc.Response[tenantv1.GetObservationSettingsResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) UpdateObservationSettings(context.Context, *connectrpc.Request[tenantv1.UpdateObservationSettingsRequest]) (*connectrpc.Response[tenantv1.UpdateObservationSettingsResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
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

		if errors.Is(err, application.ErrTenantPendingOwner) {
			return nil, connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
		}

		if code, ok := tenantContextErrorCode(err); ok {
			return nil, connectrpc.NewError(code, err)
		}

		return nil, InternalError(ctx, err)
	}

	responseEvents := make([]*tenantv1.Event, 0, len(events))
	for _, event := range events {
		responseEvents = append(responseEvents, eventProto(event))
	}

	return connectrpc.NewResponse(&tenantv1.ListEventsResponse{Events: responseEvents}), nil
}
