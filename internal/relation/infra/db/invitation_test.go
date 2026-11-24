package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
)

func TestFindInvitationByToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	invitations := NewPostgresInvitationRepository(testDB)
	hash := onetimetoken.HashOf("invitation-token")
	expiresAt := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	invitation := domain.NewInvitation("00000000-0000-0000-0000-0000000000a3", "aaaaaaaaaaaaaaa3", f.tenantA.ID(), f.tenantA.PublicID(), domain.RoleStaff, hanako, expiresAt, domain.InvitationPending)
	if err := invitations.CreateInvitation(ctx, invitation, hash); err != nil {
		t.Fatalf("CreateInvitation() error = %v", err)
	}

	got, err := invitations.FindInvitationByToken(ctx, f.tenantA.ID(), hash)
	if err != nil {
		t.Fatalf("FindInvitationByToken() error = %v", err)
	}

	if got.ID() != invitation.ID() || got.TenantPublicID() != f.tenantA.PublicID() || got.State() != domain.InvitationPending || !got.ExpiresAt().Equal(expiresAt) {
		t.Errorf("FindInvitationByToken() = %#v, want the pending invitation %#v", got, invitation)
	}

	misses := []struct {
		name     string
		tenantID string
		hash     onetimetoken.Hash
	}{
		{name: "another tenant", tenantID: f.tenantB.ID(), hash: hash},
		{name: "unknown token", tenantID: f.tenantA.ID(), hash: onetimetoken.HashOf("other-token")},
	}

	for _, tt := range misses {
		if _, err := invitations.FindInvitationByToken(ctx, tt.tenantID, tt.hash); !errors.Is(err, repository.ErrInvitationNotFound) {
			t.Errorf("FindInvitationByToken(%s) error = %v, want %v", tt.name, err, repository.ErrInvitationNotFound)
		}
	}

	err = f.memberships.WithinTransaction(ctx, func(locked context.Context) error {
		if _, err := invitations.FindInvitationByTokenForUpdate(locked, f.tenantA.ID(), hash); err != nil {
			return err
		}

		readCtx, cancelRead := context.WithTimeout(ctx, 2*time.Second)
		defer cancelRead()

		if _, err := invitations.FindInvitationByToken(readCtx, f.tenantA.ID(), hash); err != nil {
			t.Errorf("FindInvitationByToken() while the row is locked error = %v, want nil", err)
		}

		lockCtx, cancelLock := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancelLock()

		if _, err := invitations.FindInvitationByTokenForUpdate(lockCtx, f.tenantA.ID(), hash); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("FindInvitationByTokenForUpdate() while the row is locked error = %v, want %v", err, context.DeadlineExceeded)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("locking transaction error = %v", err)
	}

	if err := invitations.MarkInvitationAccepted(ctx, invitation.ID(), "user-1", expiresAt.Add(-time.Hour)); err != nil {
		t.Fatalf("MarkInvitationAccepted() error = %v", err)
	}

	accepted, err := invitations.FindInvitationByToken(ctx, f.tenantA.ID(), hash)
	if err != nil {
		t.Fatalf("FindInvitationByToken(accepted) error = %v", err)
	}

	if accepted.State() != domain.InvitationAccepted {
		t.Errorf("State() after acceptance = %v, want %v", accepted.State(), domain.InvitationAccepted)
	}
}
