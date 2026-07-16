package application

import "context"

const TenantOwnerRole = "owner"

// AddTenantMemberInput identifies the membership to add in Relation.
type AddTenantMemberInput struct {
	TenantID string
	Role     string
}

// TenantMembershipService is the application port for Relation-owned
// membership operations.
type TenantMembershipService interface {
	AddTenantMember(context.Context, AddTenantMemberInput) error
}
