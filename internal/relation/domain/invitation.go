package domain

import (
	"errors"
	"time"
)

//go:generate go tool stringer -type=InvitationState -trimprefix=Invitation -output=invitation_enum_string.go

type InvitationState uint8

const (
	InvitationPending InvitationState = iota
	InvitationAccepted
	InvitationRevoked
)

var ErrInvitationNotPending = errors.New("invitation is accepted, revoked, or expired")

type Invitation struct {
	id             string
	publicID       string
	tenantID       string
	tenantPublicID string
	role           Role
	displayName    DisplayName
	expiresAt      time.Time
	state          InvitationState
}

func NewInvitation(id, publicID, tenantID, tenantPublicID string, role Role, displayName DisplayName, expiresAt time.Time, state InvitationState) Invitation {
	return Invitation{
		id:             id,
		publicID:       publicID,
		tenantID:       tenantID,
		tenantPublicID: tenantPublicID,
		role:           role,
		displayName:    displayName,
		expiresAt:      expiresAt,
		state:          state,
	}
}

func (i Invitation) ID() string               { return i.id }
func (i Invitation) PublicID() string         { return i.publicID }
func (i Invitation) TenantID() string         { return i.tenantID }
func (i Invitation) TenantPublicID() string   { return i.tenantPublicID }
func (i Invitation) Role() Role               { return i.role }
func (i Invitation) DisplayName() DisplayName { return i.displayName }
func (i Invitation) ExpiresAt() time.Time     { return i.expiresAt }
func (i Invitation) State() InvitationState   { return i.state }

func (i Invitation) Open(now time.Time) bool {
	return i.state == InvitationPending && now.Before(i.expiresAt)
}

func (i Invitation) Accept(now time.Time) (Invitation, error) {
	return i.close(now, InvitationAccepted)
}

func (i Invitation) Revoke(now time.Time) (Invitation, error) {
	return i.close(now, InvitationRevoked)
}

func (i Invitation) close(now time.Time, state InvitationState) (Invitation, error) {
	if !i.Open(now) {
		return Invitation{}, ErrInvitationNotPending
	}

	i.state = state

	return i, nil
}
