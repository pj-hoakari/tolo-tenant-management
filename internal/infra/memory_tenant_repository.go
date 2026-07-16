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
	mu              sync.Mutex
	nextTenantID    uint64
	nextEventID     uint64
	tenants         map[string]repository.Tenant
	tenantIDsByName map[string]string
	events          map[string]repository.Event
}

func NewInMemoryTenantRepository() *InMemoryTenantRepository {
	return &InMemoryTenantRepository{
		mu:              sync.Mutex{},
		nextTenantID:    0,
		nextEventID:     0,
		tenants:         make(map[string]repository.Tenant),
		tenantIDsByName: make(map[string]string),
		events:          make(map[string]repository.Event),
	}
}

func (r *InMemoryTenantRepository) CreateTenant(_ context.Context, params repository.CreateTenantParams) (repository.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tenantIDsByName[params.Name]; ok {
		return repository.Tenant{}, repository.ErrTenantNameAlreadyExists
	}

	r.nextTenantID++
	tenant := repository.Tenant{
		ID:           fmt.Sprintf("tenant-%d", r.nextTenantID),
		Name:         params.Name,
		ContractPlan: params.ContractPlan,
		Archived:     false,
	}
	r.tenants[tenant.ID] = tenant
	r.tenantIDsByName[tenant.Name] = tenant.ID

	return tenant, nil
}

func (r *InMemoryTenantRepository) DeleteTenant(_ context.Context, tenantID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenant, ok := r.tenants[tenantID]
	if !ok {
		return nil
	}

	delete(r.tenants, tenantID)
	delete(r.tenantIDsByName, tenant.Name)

	return nil
}

func (r *InMemoryTenantRepository) CreateEvent(_ context.Context, params repository.CreateEventParams) (repository.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenant, ok := r.tenants[params.TenantID]
	if !ok {
		return repository.Event{}, repository.ErrTenantNotFound
	}

	if tenant.Archived {
		return repository.Event{}, repository.ErrTenantArchived
	}

	r.nextEventID++
	event := repository.Event{
		ID:       fmt.Sprintf("event-%d", r.nextEventID),
		TenantID: params.TenantID,
		Name:     params.Name,
		Type:     params.Type,
		Status:   "draft",
	}
	r.events[event.ID] = event

	return event, nil
}
