// Package application contains use cases for the tenant context.
package application

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

var (
	ErrTenantNameRequired         = errors.New("tenant name is required")
	ErrTenantContractPlanRequired = errors.New("tenant contract plan is required")
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
	Name         string
	ContractPlan string
	Archived     bool
}

// RegisterTenantUseCase registers a tenant.
type RegisterTenantUseCase interface {
	RegisterTenant(context.Context, RegisterTenantInput) (Tenant, error)
}

// TenantService implements tenant use cases.
type TenantService struct {
	tenantRepository repository.TenantRepository
}

func NewTenantService(tenantRepository repository.TenantRepository) *TenantService {
	return &TenantService{tenantRepository: tenantRepository}
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

	return Tenant{
		ID:           tenant.ID,
		Name:         tenant.Name,
		ContractPlan: tenant.ContractPlan,
		Archived:     tenant.Archived,
	}, nil
}
