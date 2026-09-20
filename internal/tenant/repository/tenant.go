// Package repository defines persistence contracts for the tenant context.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
)

var (
	ErrTenantNameAlreadyExists = errors.New("tenant name already exists")
	ErrTenantNotFound          = errors.New("tenant not found")
	ErrTenantArchived          = errors.New("tenant is archived")
	ErrTenantPublicIDExists    = errors.New("tenant public ID already exists")
	ErrEventNotFound           = errors.New("event not found")
	ErrEventArchived           = errors.New("event is archived")
	ErrEventPublicIDExists     = errors.New("event public ID already exists")
)

// Transactor runs a unit of work inside one database transaction. The
// transaction is carried by the context handed to fn, so repository calls made
// with that context join it.
type Transactor interface {
	WithinTransaction(ctx context.Context, fn func(context.Context) error) error
}

// MaxListEvents is the number of events a listing returns at most. The spec
// caps ListEvents at 1000 events and defines no paging; callers pass it as
// ListEventsFilter.Limit.
const MaxListEvents = 1000

// ListEventsFilter narrows a tenant's event listing.
//
// Archived events are omitted unless IncludeArchived is set. A Limit greater
// than zero caps the number of events returned; zero or less leaves the cap to
// the implementation.
type ListEventsFilter struct {
	IncludeArchived bool
	Limit           int
}

// TenantRepository persists tenants and their events.
//
// Lookups that serve RPC requests take public IDs, because the wire contract
// only carries public IDs. Internal primary keys stay inside the repository
// and the domain model.
type TenantRepository interface {
	// CreateTenant stores an owned tenant.
	CreateTenant(context.Context, domain.Tenant) error
	// CreatePendingTenant stores a pending_owner tenant together with the hash
	// and expiry of its one-time ownership claim token.
	CreatePendingTenant(context.Context, domain.Tenant, domain.OwnershipClaim) error
	// DeleteExpiredPendingTenants physically removes pending_owner tenants
	// whose claim expired before now, releasing their names.
	DeleteExpiredPendingTenants(context.Context, time.Time) (int64, error)
	// FindTenantByPublicIDForUpdate loads a tenant and its pending ownership
	// claim (zero for an owned tenant), locking the row for the rest of the
	// surrounding transaction.
	FindTenantByPublicIDForUpdate(context.Context, string) (domain.Tenant, domain.OwnershipClaim, error)
	// MarkTenantOwned records the ownership transition of a pending_owner
	// tenant and consumes its claim token.
	MarkTenantOwned(context.Context, domain.Tenant) error
	// UpdateTenant persists the contract plan and the archived flag of an
	// existing tenant. The ownership columns are left untouched, because they
	// are owned by the onboarding writes. It returns ErrTenantNotFound when no
	// tenant carries the given internal ID.
	UpdateTenant(context.Context, domain.Tenant) error
	FindTenantByID(context.Context, string) (domain.Tenant, error)
	FindTenantByPublicID(context.Context, string) (domain.Tenant, error)
	CreateEvent(context.Context, domain.Event) error
	FindEventByPublicID(context.Context, string) (domain.Event, error)
	// ListEventsByTenantID lists a tenant's events in creation order,
	// narrowed by filter.
	ListEventsByTenantID(ctx context.Context, tenantID string, filter ListEventsFilter) ([]domain.Event, error)
	UpdateEvent(context.Context, domain.Event) error
	// FindObservationSettingsByEventPublicID loads the observation settings of
	// the event with the given public ID, and nothing else about the event.
	FindObservationSettingsByEventPublicID(context.Context, string) (domain.ObservationSettings, error)
	// UpdateObservationSettings stores the observation settings of the event
	// with the given internal ID.
	UpdateObservationSettings(context.Context, string, domain.ObservationSettings) error
}
