// Package application contains use cases for the tenant context.
package application

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var (
	ErrTenantIDRequired    = errors.New("tenant ID is required")
	ErrEventNameRequired   = errors.New("event name is required")
	ErrEventIDRequired     = errors.New("event ID is required")
	ErrEventTypeRequired   = errors.New("event type is required")
	ErrEventStatusRequired = errors.New("event status is required")
)

// CreateEventInput contains the values accepted by the CreateEvent use case.
// TenantPublicID is the target tenant taken from the request; it is
// cross-checked against the authenticated tenant carried in the context.
type CreateEventInput struct {
	TenantPublicID string
	Name           string
	Type           domain.EventType
}

// TransitionEventStatusInput contains the requested event status change.
type TransitionEventStatusInput struct {
	EventPublicID string
	To            domain.EventStatus
}

// AssignEventTypeInput contains the requested event type assignment.
type AssignEventTypeInput struct {
	EventPublicID string
	Type          domain.EventType
}

// CreateEventUseCase creates an event for a tenant.
type CreateEventUseCase interface {
	CreateEvent(context.Context, CreateEventInput) (domain.Event, error)
}

// TransitionEventStatusUseCase changes an event's lifecycle status.
type TransitionEventStatusUseCase interface {
	TransitionEventStatus(context.Context, TransitionEventStatusInput) (domain.Event, error)
}

// AssignEventTypeUseCase changes an event's type.
type AssignEventTypeUseCase interface {
	AssignEventType(context.Context, AssignEventTypeInput) (domain.Event, error)
}

// GetEventUseCase retrieves one event by its public ID.
type GetEventUseCase interface {
	GetEvent(context.Context, string) (domain.Event, error)
}

// ListEventsUseCase lists events belonging to the requested tenant.
type ListEventsUseCase interface {
	ListEvents(context.Context, string) ([]domain.Event, error)
}

// TenantUseCases groups the tenant operations exposed by the Connect
// transport.
type TenantUseCases interface {
	CreateEventUseCase
	AssignEventTypeUseCase
	TransitionEventStatusUseCase
	GetEventUseCase
	ListEventsUseCase
}

// TenantService implements tenant use cases.
type TenantService struct {
	tenantRepository repository.TenantRepository
}

func NewTenantService(tenantRepository repository.TenantRepository) *TenantService {
	return &TenantService{tenantRepository: tenantRepository}
}

// resolveTenant loads the tenant named in the request after verifying that it
// is the tenant the caller is authenticated for. The request carries the
// target explicitly; the claim is only used to confirm it.
func (s *TenantService) resolveTenant(ctx context.Context, tenantPublicID string) (domain.Tenant, error) {
	if tenantPublicID == "" {
		return domain.Tenant{}, ErrTenantIDRequired
	}

	if err := tenantctx.Ensure(ctx, tenantPublicID); err != nil {
		return domain.Tenant{}, err
	}

	return s.tenantRepository.FindTenantByPublicID(ctx, tenantPublicID)
}

// resolveEvent loads the event named in the request and verifies that it
// belongs to the tenant the caller is authenticated for. The tenant is only
// known once the event is loaded, so the check happens here.
func (s *TenantService) resolveEvent(ctx context.Context, eventPublicID string) (domain.Event, error) {
	event, err := s.tenantRepository.FindEventByPublicID(ctx, eventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	if err := tenantctx.Ensure(ctx, event.TenantPublicID()); err != nil {
		return domain.Event{}, err
	}

	return event, nil
}

func (s *TenantService) CreateEvent(ctx context.Context, input CreateEventInput) (domain.Event, error) {
	if input.Name == "" {
		return domain.Event{}, ErrEventNameRequired
	}

	tenant, err := s.resolveTenant(ctx, input.TenantPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	if tenant.Archived() {
		return domain.Event{}, repository.ErrTenantArchived
	}

	eventID, err := newUUIDv7()
	if err != nil {
		return domain.Event{}, err
	}

	publicID, err := newPublicID()
	if err != nil {
		return domain.Event{}, err
	}

	event := domain.NewEvent(eventID, publicID, tenant.ID(), tenant.PublicID(), input.Name, input.Type, domain.EventStatusDraft)
	if err := s.tenantRepository.CreateEvent(ctx, event); err != nil {
		return domain.Event{}, err
	}

	return event, nil
}

func (s *TenantService) TransitionEventStatus(ctx context.Context, input TransitionEventStatusInput) (domain.Event, error) {
	if input.EventPublicID == "" {
		return domain.Event{}, ErrEventIDRequired
	}

	if input.To == domain.EventStatusUnspecified {
		return domain.Event{}, ErrEventStatusRequired
	}

	event, err := s.resolveEvent(ctx, input.EventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	updatedEvent, err := event.TransitionTo(input.To)
	if err != nil {
		return domain.Event{}, err
	}

	if err := s.tenantRepository.UpdateEvent(ctx, updatedEvent); err != nil {
		return domain.Event{}, err
	}

	return updatedEvent, nil
}

func (s *TenantService) AssignEventType(ctx context.Context, input AssignEventTypeInput) (domain.Event, error) {
	if input.EventPublicID == "" {
		return domain.Event{}, ErrEventIDRequired
	}

	if input.Type == domain.EventTypeUnspecified {
		return domain.Event{}, ErrEventTypeRequired
	}

	event, err := s.resolveEvent(ctx, input.EventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	if event.Status() == domain.EventStatusArchived {
		return domain.Event{}, repository.ErrEventArchived
	}

	updatedEvent := event.AssignType(input.Type)
	if err := s.tenantRepository.UpdateEvent(ctx, updatedEvent); err != nil {
		return domain.Event{}, err
	}

	return updatedEvent, nil
}

// GetEvent serves the service-to-service referential-integrity read. It
// enforces the tenant boundary: the caller's tenant context (from the service
// token's tenant_id claim) must match the event's tenant.
func (s *TenantService) GetEvent(ctx context.Context, eventPublicID string) (domain.Event, error) {
	if eventPublicID == "" {
		return domain.Event{}, ErrEventIDRequired
	}

	return s.resolveEvent(ctx, eventPublicID)
}

func (s *TenantService) ListEvents(ctx context.Context, tenantPublicID string) ([]domain.Event, error) {
	tenant, err := s.resolveTenant(ctx, tenantPublicID)
	if err != nil {
		return nil, err
	}

	return s.tenantRepository.ListEventsByTenantID(ctx, tenant.ID())
}
