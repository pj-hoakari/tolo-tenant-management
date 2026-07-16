// Package repository defines persistence contracts for the tenant context.
package repository

import "context"

// Tenant is the persistence representation of a tenant.
type Tenant struct {
	ID           string
	Name         string
	ContractPlan string
	Archived     bool
}

// CreateTenantParams contains the data needed to create a tenant.
type CreateTenantParams struct {
	Name         string
	ContractPlan string
}

// TenantRepository persists tenants.
type TenantRepository interface {
	CreateTenant(context.Context, CreateTenantParams) (Tenant, error)
	DeleteTenant(context.Context, string) error
}
