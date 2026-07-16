// Package infra provides infrastructure implementations for the service.
package infra

import (
	"context"
	"fmt"
	"sync"

	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

// InMemoryTenantRepository is a process-local tenant repository for
// development. Its contents are lost when the process stops.
type InMemoryTenantRepository struct {
	mu           sync.Mutex
	nextTenantID uint64
	tenants      map[string]repository.Tenant
}

func NewInMemoryTenantRepository() *InMemoryTenantRepository {
	return &InMemoryTenantRepository{
		mu:           sync.Mutex{},
		nextTenantID: 0,
		tenants:      make(map[string]repository.Tenant),
	}
}

func (r *InMemoryTenantRepository) CreateTenant(_ context.Context, params repository.CreateTenantParams) (repository.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextTenantID++
	tenant := repository.Tenant{
		ID:           fmt.Sprintf("tenant-%d", r.nextTenantID),
		Name:         params.Name,
		ContractPlan: params.ContractPlan,
		Archived:     false,
	}
	r.tenants[tenant.ID] = tenant

	return tenant, nil
}
