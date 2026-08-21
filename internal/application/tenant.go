// Package application contains use cases for the tenant context.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var (
	ErrTenantNameRequired         = errors.New("tenant name is required")
	ErrTenantContractPlanRequired = errors.New("tenant contract plan is required")
	ErrEventNameRequired          = errors.New("event name is required")
	ErrEventIDRequired            = errors.New("event ID is required")
	ErrEventTypeRequired          = errors.New("event type is required")
	ErrEventStatusRequired        = errors.New("event status is required")
)

// RegisterTenantInput contains the values accepted by the RegisterTenant use
// case.
type RegisterTenantInput struct {
	Name         string
	ContractPlan string
}

// CreateEventInput contains the values accepted by the CreateEvent use case.
type CreateEventInput struct {
	Name string
	Type domain.EventType
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

// RegisterTenantUseCase registers a tenant.
type RegisterTenantUseCase interface {
	RegisterTenant(context.Context, RegisterTenantInput) (domain.Tenant, error)
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

// ListEventsUseCase lists events belonging to the authenticated tenant.
type ListEventsUseCase interface {
	ListEvents(context.Context) ([]domain.Event, error)
}

// TenantUseCases groups the tenant operations exposed by the Connect
// transport.
type TenantUseCases interface {
	RegisterTenantUseCase
	CreateEventUseCase
	AssignEventTypeUseCase
	TransitionEventStatusUseCase
	GetEventUseCase
	ListEventsUseCase
}

// TenantService implements tenant use cases.
type TenantService struct {
	tenantRepository  repository.TenantRepository
	tenantMemberships TenantMembershipService
}

func NewTenantService(tenantRepository repository.TenantRepository, tenantMemberships TenantMembershipService) *TenantService {
	return &TenantService{
		tenantRepository:  tenantRepository,
		tenantMemberships: tenantMemberships,
	}
}

func (s *TenantService) RegisterTenant(ctx context.Context, input RegisterTenantInput) (domain.Tenant, error) {
	if input.Name == "" {
		return domain.Tenant{}, ErrTenantNameRequired
	}

	if input.ContractPlan == "" {
		return domain.Tenant{}, ErrTenantContractPlanRequired
	}

	tenantID, err := newUUIDv7()
	if err != nil {
		return domain.Tenant{}, err
	}

	publicID, err := newPublicID()
	if err != nil {
		return domain.Tenant{}, err
	}

	tenant := domain.NewTenant(tenantID, publicID, input.Name, input.ContractPlan, false)
	if err := s.tenantRepository.CreateTenant(ctx, tenant); err != nil {
		return domain.Tenant{}, err
	}

	if err := s.tenantMemberships.AddTenantMember(ctx, AddTenantMemberInput{
		TenantID: tenant.ID(),
		Role:     TenantOwnerRole,
	}); err != nil {
		if deleteErr := s.tenantRepository.DeleteTenant(ctx, tenant.ID()); deleteErr != nil {
			return domain.Tenant{}, fmt.Errorf("add tenant owner: %w (compensating delete tenant: %v)", err, deleteErr)
		}

		return domain.Tenant{}, fmt.Errorf("add tenant owner: %w", err)
	}

	return tenant, nil
}

func (s *TenantService) CreateEvent(ctx context.Context, input CreateEventInput) (domain.Event, error) {
	if input.Name == "" {
		return domain.Event{}, ErrEventNameRequired
	}

	tenantPublicID, ok := tenantctx.TenantPublicIDFromContext(ctx)
	if !ok || tenantPublicID == "" {
		return domain.Event{}, tenantctx.ErrMissing
	}

	tenant, err := s.tenantRepository.FindTenantByPublicID(ctx, tenantPublicID)
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

	event, err := s.tenantRepository.FindEventByPublicID(ctx, input.EventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	// The tenant is only known once the event is loaded, so authorize here.
	if err := tenantctx.Ensure(ctx, event.TenantPublicID()); err != nil {
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

	event, err := s.tenantRepository.FindEventByPublicID(ctx, input.EventPublicID)
	if err != nil {
		return domain.Event{}, err
	}

	// The tenant is only known once the event is loaded, so authorize here.
	if err := tenantctx.Ensure(ctx, event.TenantPublicID()); err != nil {
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

func (s *TenantService) GetEvent(ctx context.Context, eventPublicID string) (domain.Event, error) {
	if eventPublicID == "" {
		return domain.Event{}, ErrEventIDRequired
	}

	return s.tenantRepository.FindEventByPublicID(ctx, eventPublicID)
}

func (s *TenantService) ListEvents(ctx context.Context) ([]domain.Event, error) {
	tenantPublicID, ok := tenantctx.TenantPublicIDFromContext(ctx)
	if !ok || tenantPublicID == "" {
		return nil, tenantctx.ErrMissing
	}

	tenant, err := s.tenantRepository.FindTenantByPublicID(ctx, tenantPublicID)
	if err != nil {
		return nil, err
	}

	return s.tenantRepository.ListEventsByTenantID(ctx, tenant.ID())
}
