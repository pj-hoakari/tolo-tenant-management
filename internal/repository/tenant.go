// Package repository defines persistence contracts for the tenant context.
package repository

import (
	"context"
	"errors"
)

var (
	ErrTenantNameAlreadyExists = errors.New("tenant name already exists")
	ErrTenantNotFound          = errors.New("tenant not found")
	ErrTenantArchived          = errors.New("tenant is archived")
)

// Tenant is the persistence representation of a tenant.
type Tenant struct {
	ID           string
	PublicID     string
	Name         string
	ContractPlan string
	Archived     bool
}

// CreateTenantParams contains the data needed to create a tenant.
type CreateTenantParams struct {
	Name         string
	ContractPlan string
}

// Event is the persistence representation of an event.
type Event struct {
	ID             string
	PublicID       string
	TenantID       string
	TenantPublicID string
	Name           string
	Type           string
	Status         string
}

// CreateEventParams contains the data needed to create an event.
type CreateEventParams struct {
	TenantPublicID string
	Name           string
	Type           string
}

// TenantRepository persists tenants.
type TenantRepository interface {
	CreateTenant(context.Context, CreateTenantParams) (Tenant, error)
	DeleteTenant(context.Context, string) error
	CreateEvent(context.Context, CreateEventParams) (Event, error)
}
