// Package infra provides infrastructure implementations for the service.
package infra

import (
	"context"
	"sync"

	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

// InMemoryTenantRepository is a process-local tenant repository for
// development. Its contents are lost when the process stops.
type InMemoryTenantRepository struct {
	mu                  sync.Mutex
	tenants             map[string]repository.Tenant
	tenantIDsByName     map[string]string
	tenantIDsByPublicID map[string]string
	events              map[string]repository.Event
	eventIDsByPublicID  map[string]string
}

func NewInMemoryTenantRepository() *InMemoryTenantRepository {
	return &InMemoryTenantRepository{
		mu:                  sync.Mutex{},
		tenants:             make(map[string]repository.Tenant),
		tenantIDsByName:     make(map[string]string),
		tenantIDsByPublicID: make(map[string]string),
		events:              make(map[string]repository.Event),
		eventIDsByPublicID:  make(map[string]string),
	}
}

func (r *InMemoryTenantRepository) CreateTenant(_ context.Context, params repository.CreateTenantParams) (repository.Tenant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tenantIDsByName[params.Name]; ok {
		return repository.Tenant{}, repository.ErrTenantNameAlreadyExists
	}

	tenantID, err := newUUIDv7()
	if err != nil {
		return repository.Tenant{}, err
	}

	publicID, err := r.newTenantPublicID()
	if err != nil {
		return repository.Tenant{}, err
	}

	tenant := repository.Tenant{
		ID:           tenantID,
		PublicID:     publicID,
		Name:         params.Name,
		ContractPlan: params.ContractPlan,
		Archived:     false,
	}
	r.tenants[tenant.ID] = tenant
	r.tenantIDsByName[tenant.Name] = tenant.ID
	r.tenantIDsByPublicID[tenant.PublicID] = tenant.ID

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
	delete(r.tenantIDsByPublicID, tenant.PublicID)

	return nil
}

func (r *InMemoryTenantRepository) CreateEvent(_ context.Context, params repository.CreateEventParams) (repository.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tenantID, ok := r.tenantIDsByPublicID[params.TenantPublicID]
	if !ok {
		return repository.Event{}, repository.ErrTenantNotFound
	}

	tenant, ok := r.tenants[tenantID]
	if !ok {
		return repository.Event{}, repository.ErrTenantNotFound
	}

	if tenant.Archived {
		return repository.Event{}, repository.ErrTenantArchived
	}

	eventID, err := newUUIDv7()
	if err != nil {
		return repository.Event{}, err
	}

	publicID, err := r.newEventPublicID()
	if err != nil {
		return repository.Event{}, err
	}

	event := repository.Event{
		ID:             eventID,
		PublicID:       publicID,
		TenantID:       tenant.ID,
		TenantPublicID: tenant.PublicID,
		Name:           params.Name,
		Type:           params.Type,
		Status:         "draft",
	}
	r.events[event.ID] = event
	r.eventIDsByPublicID[event.PublicID] = event.ID

	return event, nil
}

func (r *InMemoryTenantRepository) newTenantPublicID() (string, error) {
	for {
		publicID, err := newPublicID()
		if err != nil {
			return "", err
		}

		if _, exists := r.tenantIDsByPublicID[publicID]; !exists {
			return publicID, nil
		}
	}
}

func (r *InMemoryTenantRepository) newEventPublicID() (string, error) {
	for {
		publicID, err := newPublicID()
		if err != nil {
			return "", err
		}

		if _, exists := r.eventIDsByPublicID[publicID]; !exists {
			return publicID, nil
		}
	}
}
