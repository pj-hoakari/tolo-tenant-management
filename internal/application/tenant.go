// Package application contains use cases for the tenant context.
package application

import (
	"context"
	"errors"
	"fmt"

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

// Tenant is the application representation of a tenant.
type Tenant struct {
	ID           string
	PublicID     string
	Name         string
	ContractPlan string
	Archived     bool
}

// CreateEventInput contains the values accepted by the CreateEvent use case.
type CreateEventInput struct {
	TenantID string
	Name     string
	Type     string
}

// Event is the application representation of an event.
type Event struct {
	ID             string
	PublicID       string
	TenantID       string
	TenantPublicID string
	Name           string
	Type           string
	Status         string
}

// RegisterTenantUseCase registers a tenant.
type RegisterTenantUseCase interface {
	RegisterTenant(context.Context, RegisterTenantInput) (Tenant, error)
}

// CreateEventUseCase creates an event for a tenant.
type CreateEventUseCase interface {
	CreateEvent(context.Context, CreateEventInput) (Event, error)
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

func (s *TenantService) RegisterTenant(ctx context.Context, input RegisterTenantInput) (Tenant, error) {
	if input.Name == "" {
		return Tenant{}, ErrTenantNameRequired
	}

	if input.ContractPlan == "" {
		return Tenant{}, ErrTenantContractPlanRequired
	}

	tenant, err := s.tenantRepository.CreateTenant(ctx, repository.CreateTenantParams{
		Name:         input.Name,
		ContractPlan: input.ContractPlan,
	})
	if err != nil {
		return Tenant{}, err
	}

	if err := s.tenantMemberships.AddTenantMember(ctx, AddTenantMemberInput{
		TenantID: tenant.ID,
		Role:     TenantOwnerRole,
	}); err != nil {
		if deleteErr := s.tenantRepository.DeleteTenant(ctx, tenant.ID); deleteErr != nil {
			return Tenant{}, fmt.Errorf("add tenant owner: %w (compensating delete tenant: %v)", err, deleteErr)
		}

		return Tenant{}, fmt.Errorf("add tenant owner: %w", err)
	}

	return Tenant{
		ID:           tenant.ID,
		PublicID:     tenant.PublicID,
		Name:         tenant.Name,
		ContractPlan: tenant.ContractPlan,
		Archived:     tenant.Archived,
	}, nil
}

func (s *TenantService) CreateEvent(ctx context.Context, input CreateEventInput) (Event, error) {
	if input.TenantID == "" {
		return Event{}, ErrEventTenantIDRequired
	}

	if input.Name == "" {
		return Event{}, ErrEventNameRequired
	}

	event, err := s.tenantRepository.CreateEvent(ctx, repository.CreateEventParams{
		TenantPublicID: input.TenantID,
		Name:           input.Name,
		Type:           input.Type,
	})
	if err != nil {
		return Event{}, err
	}

	return Event{
		ID:             event.ID,
		PublicID:       event.PublicID,
		TenantID:       event.TenantID,
		TenantPublicID: event.TenantPublicID,
		Name:           event.Name,
		Type:           event.Type,
		Status:         event.Status,
	}, nil
}
