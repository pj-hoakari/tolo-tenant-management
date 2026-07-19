// Package tenantctx carries the authenticated tenant's public ID through the
// request context and verifies that tenant-scoped work targets that tenant.
//
// The authenticated tenant ID is a request-scoped authorization fact set by the
// transport interceptor from the verified internal JWT. It is deliberately kept
// out of the domain layer: whether a caller may act on a given tenant is
// contextual authorization, not an intrinsic domain invariant, so domain value
// objects stay free of request context.
package tenantctx

import (
	"context"
	"errors"
)

var (
	// ErrMissing indicates the authenticated tenant ID is absent from the
	// context on an operation that requires it.
	ErrMissing = errors.New("tenant ID is missing from context")
	// ErrMismatch indicates a tenant-scoped operation targets a tenant other
	// than the authenticated one carried in the context.
	ErrMismatch = errors.New("tenant ID does not match context")
)

type contextKey struct{}

// WithTenantID stores the authenticated tenant's public ID on the context.
func WithTenantID(ctx context.Context, tenantPublicID string) context.Context {
	return context.WithValue(ctx, contextKey{}, tenantPublicID)
}

// FromContext returns the authenticated tenant's public ID stored by
// WithTenantID.
func FromContext(ctx context.Context) (string, bool) {
	tenantPublicID, ok := ctx.Value(contextKey{}).(string)

	return tenantPublicID, ok
}

// Ensure verifies that tenantPublicID matches the authenticated tenant carried
// in the context. It fails closed: a missing context tenant ID is an error, so
// tenant-scoped work never proceeds without a verified owner. Callers pass the
// public ID of the tenant they are about to act on to guard against tenant
// mix-ups. Use this at the use-case boundary to authorize an operation.
func Ensure(ctx context.Context, tenantPublicID string) error {
	contextTenantID, ok := FromContext(ctx)
	if !ok || contextTenantID == "" {
		return ErrMissing
	}

	if contextTenantID != tenantPublicID {
		return ErrMismatch
	}

	return nil
}

// VerifyOwnership guards a tenant-scoped model reconstituted from persistence
// against the authenticated tenant. Unlike Ensure it fails open on a missing
// context tenant: operations that legitimately run without an authenticated
// tenant (e.g. service-token reads) stay unrestricted. It exists as defense in
// depth, so that a repository query which forgets to scope by tenant cannot
// hand back — and thereby leak — another tenant's data.
func VerifyOwnership(ctx context.Context, tenantPublicID string) error {
	contextTenantID, ok := FromContext(ctx)
	if !ok || contextTenantID == "" {
		return nil
	}

	if contextTenantID != tenantPublicID {
		return ErrMismatch
	}

	return nil
}
