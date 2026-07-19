package tenantctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

func TestFromContext(t *testing.T) {
	t.Parallel()

	ctx := tenantctx.WithTenantID(context.Background(), "tenant-public-id")

	got, ok := tenantctx.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext() ok = false, want true")
	}

	if want := "tenant-public-id"; got != want {
		t.Errorf("FromContext() = %q, want %q", got, want)
	}

	if _, ok := tenantctx.FromContext(context.Background()); ok {
		t.Error("FromContext() ok = true for empty context, want false")
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
			ctx:            tenantctx.WithTenantID(context.Background(), "tenant-public-id"),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "differs from context tenant",
			ctx:            tenantctx.WithTenantID(context.Background(), "tenant-public-id"),
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
			ctx:            tenantctx.WithTenantID(context.Background(), ""),
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
			ctx:            tenantctx.WithTenantID(context.Background(), "tenant-public-id"),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "differs from context tenant",
			ctx:            tenantctx.WithTenantID(context.Background(), "tenant-public-id"),
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
			ctx:            tenantctx.WithTenantID(context.Background(), ""),
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
