// Package application contains use cases for the tenant context.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

var (
	ErrTenantNameRequired         = errors.New("tenant name is required")
	ErrTenantContractPlanRequired = errors.New("tenant contract plan is required")
	ErrEventTenantIDRequired      = errors.New("event tenant ID is required")
	ErrEventNameRequired          = errors.New("event name is required")
)

// RegisterTenantInput contains the values accepted by the RegisterTenant use
// case.
type RegisterTenantInput struct {
	Name         string
	ContractPlan string
}

// CreateEventInput contains the values accepted by the CreateEvent use case.
type CreateEventInput struct {
	TenantID string
	Name     string
	Type     string
}

// RegisterTenantUseCase registers a tenant.
type RegisterTenantUseCase interface {
	RegisterTenant(context.Context, RegisterTenantInput) (domain.Tenant, error)
}

// CreateEventUseCase creates an event for a tenant.
type CreateEventUseCase interface {
	CreateEvent(context.Context, CreateEventInput) (domain.Event, error)
}

// TenantUseCases groups the tenant operations exposed by the Connect
// transport.
type TenantUseCases interface {
	RegisterTenantUseCase
	CreateEventUseCase
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

	tenant := domain.NewTenant(tenantID, publicID, input.Name, input.ContractPlan)
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
	if input.TenantID == "" {
		return domain.Event{}, ErrEventTenantIDRequired
	}

	if input.Name == "" {
		return domain.Event{}, ErrEventNameRequired
	}

	tenant, err := s.tenantRepository.FindTenantByPublicID(ctx, input.TenantID)
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

	event := domain.NewEvent(eventID, publicID, tenant.ID(), tenant.PublicID(), input.Name, input.Type)
	if err := s.tenantRepository.CreateEvent(ctx, event); err != nil {
		return domain.Event{}, err
	}

	return event, nil
}
