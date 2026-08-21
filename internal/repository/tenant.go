// Package repository defines persistence contracts for the tenant context.
package repository

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
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

// TenantRepository persists tenants and their events.
//
// Lookups that serve RPC requests take public IDs, because the wire contract
// only carries public IDs. Internal primary keys stay inside the repository
// and the domain model.
type TenantRepository interface {
	CreateTenant(context.Context, domain.Tenant) error
	FindTenantByID(context.Context, string) (domain.Tenant, error)
	FindTenantByPublicID(context.Context, string) (domain.Tenant, error)
	CreateEvent(context.Context, domain.Event) error
	FindEventByPublicID(context.Context, string) (domain.Event, error)
	ListEventsByTenantID(context.Context, string) ([]domain.Event, error)
	UpdateEvent(context.Context, domain.Event) error
}
