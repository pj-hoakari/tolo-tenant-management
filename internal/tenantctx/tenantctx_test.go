package tenantctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

func TestTenantPublicIDFromContext(t *testing.T) {
	t.Parallel()

	ctx := tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id")

	got, ok := tenantctx.TenantPublicIDFromContext(ctx)
	if !ok {
		t.Fatal("TenantPublicIDFromContext() ok = false, want true")
	}

	if want := "tenant-public-id"; got != want {
		t.Errorf("TenantPublicIDFromContext() = %q, want %q", got, want)
	}

	if _, ok := tenantctx.TenantPublicIDFromContext(context.Background()); ok {
		t.Error("TenantPublicIDFromContext() ok = true for empty context, want false")
	}
}

func TestEnsure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		ctx            context.Context
		tenantPublicID string
		wantErr        error
	}{
		{
			name:           "matches context tenant",
			ctx:            tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id"),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "differs from context tenant",
			ctx:            tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id"),
			tenantPublicID: "other-tenant-public-id",
			wantErr:        tenantctx.ErrMismatch,
		},
		{
			name:           "missing context tenant",
			ctx:            context.Background(),
			tenantPublicID: "tenant-public-id",
			wantErr:        tenantctx.ErrMissing,
		},
		{
			name:           "empty context tenant",
			ctx:            tenantctx.WithTenantPublicID(context.Background(), ""),
			tenantPublicID: "tenant-public-id",
			wantErr:        tenantctx.ErrMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tenantctx.Ensure(tt.ctx, tt.tenantPublicID)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Ensure() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		ctx            context.Context
		tenantPublicID string
		wantErr        error
	}{
		{
			name:           "matches context tenant",
			ctx:            tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id"),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "differs from context tenant",
			ctx:            tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id"),
			tenantPublicID: "other-tenant-public-id",
			wantErr:        tenantctx.ErrMismatch,
		},
		{
			name:           "missing context tenant is unrestricted",
			ctx:            context.Background(),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "empty context tenant is unrestricted",
			ctx:            tenantctx.WithTenantPublicID(context.Background(), ""),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tenantctx.VerifyOwnership(tt.ctx, tt.tenantPublicID)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("VerifyOwnership() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
