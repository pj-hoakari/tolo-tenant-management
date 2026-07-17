// Package domain contains immutable models for the tenant context.
package domain

// Tenant is an immutable tenant model.
type Tenant struct {
	id           string
	publicID     string
	name         string
	contractPlan string
	archived     bool
}

func NewTenant(id, publicID, name, contractPlan string, archived bool) Tenant {
	return Tenant{
		id:           id,
		publicID:     publicID,
		name:         name,
		contractPlan: contractPlan,
		archived:     archived,
	}
}

func (t Tenant) ID() string           { return t.id }
func (t Tenant) PublicID() string     { return t.publicID }
func (t Tenant) Name() string         { return t.name }
func (t Tenant) ContractPlan() string { return t.contractPlan }
func (t Tenant) Archived() bool       { return t.archived }
