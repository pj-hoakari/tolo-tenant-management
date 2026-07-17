// Package infra provides infrastructure implementations for the service.
package infra

import (
	"context"
	"sync"

	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

// InMemoryTenantRepository is a process-local tenant repository for
// development. Its contents are lost when the process stops.
type InMemoryTenantRepository struct {
	mu                  sync.Mutex
	tenants             map[string]domain.Tenant
	tenantIDsByName     map[string]string
	tenantIDsByPublicID map[string]string
	events              map[string]domain.Event
	eventIDsByPublicID  map[string]string
}

func NewInMemoryTenantRepository() *InMemoryTenantRepository {
	return &InMemoryTenantRepository{
		mu:                  sync.Mutex{},
		tenants:             make(map[string]domain.Tenant),
		tenantIDsByName:     make(map[string]string),
		tenantIDsByPublicID: make(map[string]string),
		events:              make(map[string]domain.Event),
		eventIDsByPublicID:  make(map[string]string),
	}
}

func (r *InMemoryTenantRepository) CreateTenant(_ context.Context, tenant domain.Tenant) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tenantIDsByName[tenant.Name()]; ok {
		return repository.ErrTenantNameAlreadyExists
	}

	if _, ok := r.tenantIDsByPublicID[tenant.PublicID()]; ok {
		return repository.ErrTenantPublicIDExists
	}

	r.tenants[tenant.ID()] = tenant
	r.tenantIDsByName[tenant.Name()] = tenant.ID()
	r.tenantIDsByPublicID[tenant.PublicID()] = tenant.ID()

	return nil
}

func (r *InMemoryTenantRepository) DeleteTenant(_ context.Context, tenantID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenant, ok := r.tenants[tenantID]
	if !ok {
		return nil
	}

	delete(r.tenants, tenantID)
	delete(r.tenantIDsByName, tenant.Name())
	delete(r.tenantIDsByPublicID, tenant.PublicID())

	return nil
}

func (r *InMemoryTenantRepository) FindTenantByPublicID(_ context.Context, publicID string) (domain.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID, ok := r.tenantIDsByPublicID[publicID]
	if !ok {
		return domain.Tenant{}, repository.ErrTenantNotFound
	}

	tenant, ok := r.tenants[tenantID]
	if !ok {
		return domain.Tenant{}, repository.ErrTenantNotFound
	}

	return tenant, nil
}

func (r *InMemoryTenantRepository) CreateEvent(_ context.Context, event domain.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenant, ok := r.tenants[event.TenantID()]
	if !ok {
		return repository.ErrTenantNotFound
	}

	if tenant.Archived() {
		return repository.ErrTenantArchived
	}

	if _, ok := r.eventIDsByPublicID[event.PublicID()]; ok {
		return repository.ErrEventPublicIDExists
	}

	r.events[event.ID()] = event
	r.eventIDsByPublicID[event.PublicID()] = event.ID()

	return nil
}

func (r *InMemoryTenantRepository) FindEventByID(_ context.Context, eventID string) (domain.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	event, ok := r.events[eventID]
	if !ok {
		return domain.Event{}, repository.ErrEventNotFound
	}

	return event, nil
}

func (r *InMemoryTenantRepository) UpdateEvent(_ context.Context, event domain.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.events[event.ID()]; !ok {
		return repository.ErrEventNotFound
	}

	tenant, ok := r.tenants[event.TenantID()]
	if !ok {
		return repository.ErrTenantNotFound
	}

	if tenant.Archived() {
		return repository.ErrTenantArchived
	}

	r.events[event.ID()] = event

	return nil
}
