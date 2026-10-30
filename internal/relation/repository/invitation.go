package repository

import (
	"context"
	"errors"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
)

var (
	ErrInvitationNotFound       = errors.New("invitation not found")
	ErrInvitationPublicIDExists = errors.New("invitation public ID already exists")
)

type InvitationRepository interface {
	CreateInvitation(ctx context.Context, invitation domain.Invitation, tokenHash onetimetoken.Hash) error
	ListOpenInvitations(ctx context.Context, tenantID string, now time.Time) ([]domain.Invitation, error)
	FindInvitationForUpdate(ctx context.Context, tenantID, publicID string) (domain.Invitation, error)
	FindInvitationByTokenForUpdate(ctx context.Context, tenantID string, tokenHash onetimetoken.Hash) (domain.Invitation, error)
	MarkInvitationAccepted(ctx context.Context, invitationID, userID string, at time.Time) error
	MarkInvitationRevoked(ctx context.Context, invitationID string, at time.Time) error
}
