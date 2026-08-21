package application

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// ErrPermissionDenied means the caller's current role in the tenant cannot
// issue the scope the operation requires.
var ErrPermissionDenied = errors.New("current permission denied")

// Authorizer answers whether the authenticated subject currently holds, in a
// tenant, a role that can issue a scope. The scope claim of the internal JWT
// states what the caller was granted when the token was minted; administrative
// writes additionally re-read the membership from the database, so that a
// member who has since been revoked or downgraded stops administering the
// tenant before the token expires
// (tenant_management_spec.md「管理系書き込みの現在権限確認」).
//
// The caller must invoke it inside the transaction of the write it guards: the
// lookup locks the membership row for the rest of that transaction, so the
// role cannot change between the check and the write.
type Authorizer struct {
	memberships repository.MembershipRepository
}

func NewAuthorizer(memberships repository.MembershipRepository) *Authorizer {
	return &Authorizer{memberships: memberships}
}

// Allowed reports whether the authenticated subject's current role in the
// tenant can issue the scope. tenantID is the internal tenant ID, as the
// repositories use it. A caller who no longer belongs to the tenant holds no
// permission, which is an answer rather than an error.
func (a *Authorizer) Allowed(ctx context.Context, tenantID, scope string) (bool, error) {
	subject, ok := tenantctx.SubjectFromContext(ctx)
	if !ok {
		return false, tenantctx.ErrSubjectMissing
	}

	role, err := a.memberships.FindTenantRoleForShare(ctx, tenantID, subject)
	if errors.Is(err, repository.ErrMembershipNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return role.Grants(scope), nil
}

// Require reports ErrPermissionDenied unless the authenticated subject's
// current role in the tenant can issue the scope.
func (a *Authorizer) Require(ctx context.Context, tenantID, scope string) error {
	allowed, err := a.Allowed(ctx, tenantID, scope)
	if err != nil {
		return err
	}

	if !allowed {
		return ErrPermissionDenied
	}

	return nil
}
