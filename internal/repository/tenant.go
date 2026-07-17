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
	ErrEventPublicIDExists     = errors.New("event public ID already exists")
)

// TenantRepository persists tenants.
type TenantRepository interface {
	CreateTenant(context.Context, domain.Tenant) error
	DeleteTenant(context.Context, string) error
	FindTenantByID(context.Context, string) (domain.Tenant, error)
	FindTenantByPublicID(context.Context, string) (domain.Tenant, error)
	CreateEvent(context.Context, domain.Event) error
	FindEventByID(context.Context, string) (domain.Event, error)
	ListEventsByTenantID(context.Context, string) ([]domain.Event, error)
	UpdateEvent(context.Context, domain.Event) error
}
