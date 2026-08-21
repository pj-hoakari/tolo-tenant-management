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

// TenantRepository persists tenants and their events.
//
// Lookups that serve RPC requests take public IDs, because the wire contract
// only carries public IDs. Internal primary keys stay inside the repository
// and the domain model.
type TenantRepository interface {
	CreateTenant(context.Context, domain.Tenant) error
	DeleteTenant(context.Context, string) error
	FindTenantByID(context.Context, string) (domain.Tenant, error)
	FindTenantByPublicID(context.Context, string) (domain.Tenant, error)
	CreateEvent(context.Context, domain.Event) error
	FindEventByPublicID(context.Context, string) (domain.Event, error)
	ListEventsByTenantID(context.Context, string) ([]domain.Event, error)
	UpdateEvent(context.Context, domain.Event) error
}
