package application

import (
	"context"
	"errors"
)

// ScopeTenantWrite is the scope the administrative tenant writes require. It
// mirrors the relation model's scope of the same name; the tenant side states
// it separately so that it never has to import the relation packages.
const ScopeTenantWrite = "tenant.write"

// ErrPermissionDenied means the caller's current membership does not permit
// the write: the membership is gone, or the role can no longer issue the
// required scope.
var ErrPermissionDenied = errors.New("current permission denied")

// CurrentPermissionChecker is the tenant side's port to the relation model for
// the administrative writes that re-check the caller's current permission
// (tenant_management_spec.md「管理系書き込みの現在権限確認」). The scope claim
// of the internal JWT only states what the caller was granted when the token
// was minted, so these writes read the caller's membership from the database
// again. It must be called inside the transaction of the write it guards, so
// that the permission cannot change between the check and the write.
type CurrentPermissionChecker interface {
	// Allowed reports whether the authenticated subject carried in the context
	// currently holds, in the tenant identified by its internal ID, a role that
	// can issue the scope.
	Allowed(ctx context.Context, tenantID, scope string) (bool, error)
}
