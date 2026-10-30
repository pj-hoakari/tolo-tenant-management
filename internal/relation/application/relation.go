// Package application contains the use cases of the relation model: the
// administration of memberships and roles exposed as RelationAdminService.
//
// Identifiers arrive as public IDs. The tenant context is the source of truth
// for them, so the use cases resolve public IDs, and the archived / pending
// state that gates writes, through the tenant repository (read-only). The
// memberships themselves are read and written through the relation repository.
package application

import (
	"context"
	"errors"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/identifier"
	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var (
	ErrTenantIDRequired        = errors.New("tenant ID is required")
	ErrEventIDRequired         = errors.New("event ID is required")
	ErrUserIDRequired          = errors.New("user ID is required")
	ErrScopeRequired           = errors.New("revoke scope is required")
	ErrScopeAmbiguous          = errors.New("revoke scope must name either a tenant or an event")
	ErrFilterRequired          = errors.New("list filter is required")
	ErrFilterAmbiguous         = errors.New("list filter must name either a tenant or a user")
	ErrTenantPendingOwner      = errors.New("tenant is pending an owner")
	ErrLastOwner               = errors.New("the tenant must keep at least one owner")
	ErrInvitationIDRequired    = errors.New("invitation ID is required")
	ErrInvitationTokenRequired = errors.New("invitation token is required")
	ErrInvitationRejected      = errors.New("invitation rejected")
)

const DefaultInvitationTTL = 7 * 24 * time.Hour

type CreateTenantInvitationInput struct {
	TenantPublicID string
	Role           domain.Role
	DisplayName    string
}

type TenantInvitationCreation struct {
	Invitation domain.Invitation
	Token      string
}

type RevokeTenantInvitationInput struct {
	TenantPublicID string
	InvitationID   string
}

type AcceptTenantInvitationInput struct {
	TenantPublicID string
	Token          string
}

// AddTenantMemberInput identifies the membership to create.
type AddTenantMemberInput struct {
	TenantPublicID string
	UserID         string
	Role           domain.Role
	DisplayName    string
}

// ChangeTenantRoleInput identifies the membership whose tenant role changes.
type ChangeTenantRoleInput struct {
	TenantPublicID string
	UserID         string
	Role           domain.Role
}

// GrantEventRoleInput identifies the event role to assign.
type GrantEventRoleInput struct {
	EventPublicID string
	UserID        string
	Role          domain.Role
}

// RevokeRoleInput names either the tenant whose membership is removed or the
// event whose role is removed; exactly one of the two is set.
type RevokeRoleInput struct {
	UserID         string
	TenantPublicID string
	EventPublicID  string
}

// ListMembershipsInput names either a tenant (every membership of the tenant)
// or a user (the user's membership of the authenticated tenant); exactly one
// of the two is set.
type ListMembershipsInput struct {
	TenantPublicID string
	UserID         string
}

// RelationUseCases groups the operations exposed by RelationAdminService.
type RelationUseCases interface {
	AddTenantMember(context.Context, AddTenantMemberInput) (domain.Membership, error)
	ChangeTenantRole(context.Context, ChangeTenantRoleInput) (domain.Membership, error)
	GrantEventRole(context.Context, GrantEventRoleInput) (domain.Membership, error)
	RevokeRole(context.Context, RevokeRoleInput) error
	ListMemberships(context.Context, ListMembershipsInput) ([]domain.Membership, error)
	CreateTenantInvitation(context.Context, CreateTenantInvitationInput) (TenantInvitationCreation, error)
	ListTenantInvitations(context.Context, string) ([]domain.Invitation, error)
	RevokeTenantInvitation(context.Context, RevokeTenantInvitationInput) (domain.Invitation, error)
	AcceptTenantInvitation(context.Context, AcceptTenantInvitationInput) (domain.Membership, error)
}

// RelationService implements the relation use cases.
type RelationService struct {
	tenants       tenantrepository.TenantRepository
	memberships   repository.MembershipRepository
	invitations   repository.InvitationRepository
	transactor    tenantrepository.Transactor
	authorizer    *Authorizer
	now           func() time.Time
	invitationTTL time.Duration
}

type Option func(*RelationService)

func WithClock(now func() time.Time) Option {
	return func(s *RelationService) { s.now = now }
}

func WithInvitationTTL(ttl time.Duration) Option {
	return func(s *RelationService) { s.invitationTTL = ttl }
}

func NewRelationService(tenants tenantrepository.TenantRepository, memberships repository.MembershipRepository, invitations repository.InvitationRepository, transactor tenantrepository.Transactor, options ...Option) *RelationService {
	service := &RelationService{
		tenants:       tenants,
		memberships:   memberships,
		invitations:   invitations,
		transactor:    transactor,
		authorizer:    NewAuthorizer(memberships),
		now:           time.Now,
		invitationTTL: DefaultInvitationTTL,
	}

	for _, option := range options {
		option(service)
	}

	return service
}

func (s *RelationService) AddTenantMember(ctx context.Context, input AddTenantMemberInput) (domain.Membership, error) {
	if input.UserID == "" {
		return domain.Membership{}, ErrUserIDRequired
	}

	if err := input.Role.Grantable(); err != nil {
		return domain.Membership{}, err
	}

	displayName, err := domain.ParseDisplayName(input.DisplayName)
	if err != nil {
		return domain.Membership{}, err
	}

	tenant, err := s.writableTenant(ctx, input.TenantPublicID)
	if err != nil {
		return domain.Membership{}, err
	}

	return s.memberships.AddTenantMember(ctx, tenant.ID(), input.UserID, input.Role, displayName)
}

func (s *RelationService) ChangeTenantRole(ctx context.Context, input ChangeTenantRoleInput) (domain.Membership, error) {
	if input.UserID == "" {
		return domain.Membership{}, ErrUserIDRequired
	}

	if err := input.Role.Grantable(); err != nil {
		return domain.Membership{}, err
	}

	tenant, err := s.writableTenant(ctx, input.TenantPublicID)
	if err != nil {
		return domain.Membership{}, err
	}

	return s.writeMembership(ctx, tenant.ID(), func(ctx context.Context) (domain.Membership, error) {
		if input.Role != domain.RoleOwner {
			if err := s.keepAnotherOwner(ctx, tenant.ID(), input.UserID); err != nil {
				return domain.Membership{}, err
			}
		}

		return s.memberships.ChangeTenantRole(ctx, tenant.ID(), input.UserID, input.Role)
	})
}

func (s *RelationService) GrantEventRole(ctx context.Context, input GrantEventRoleInput) (domain.Membership, error) {
	if input.UserID == "" {
		return domain.Membership{}, ErrUserIDRequired
	}

	if err := input.Role.Grantable(); err != nil {
		return domain.Membership{}, err
	}

	event, err := s.writableEvent(ctx, input.EventPublicID)
	if err != nil {
		return domain.Membership{}, err
	}

	// The event's tenant is the tenant the caller must be permitted to write.
	return s.writeMembership(ctx, event.TenantID(), func(ctx context.Context) (domain.Membership, error) {
		return s.memberships.GrantEventRole(ctx, event.ID(), input.UserID, input.Role)
	})
}

func (s *RelationService) RevokeRole(ctx context.Context, input RevokeRoleInput) error {
	if input.UserID == "" {
		return ErrUserIDRequired
	}

	switch {
	case input.TenantPublicID != "" && input.EventPublicID != "":
		return ErrScopeAmbiguous
	case input.TenantPublicID != "":
		tenant, err := s.writableTenant(ctx, input.TenantPublicID)
		if err != nil {
			return err
		}

		return s.withCurrentPermission(ctx, tenant.ID(), func(ctx context.Context) error {
			if err := s.keepAnotherOwner(ctx, tenant.ID(), input.UserID); err != nil {
				return err
			}

			return s.memberships.RevokeTenantMembership(ctx, tenant.ID(), input.UserID)
		})
	case input.EventPublicID != "":
		event, err := s.writableEvent(ctx, input.EventPublicID)
		if err != nil {
			return err
		}

		return s.withCurrentPermission(ctx, event.TenantID(), func(ctx context.Context) error {
			return s.memberships.RevokeEventRole(ctx, event.ID(), input.UserID)
		})
	default:
		return ErrScopeRequired
	}
}

// ListMemberships is a read: archived tenants keep answering, so that
// existing memberships stay visible.
func (s *RelationService) ListMemberships(ctx context.Context, input ListMembershipsInput) ([]domain.Membership, error) {
	switch {
	case input.TenantPublicID != "" && input.UserID != "":
		return nil, ErrFilterAmbiguous
	case input.TenantPublicID != "":
		tenant, err := s.resolveTenant(ctx, input.TenantPublicID)
		if err != nil {
			return nil, err
		}

		return s.memberships.ListMembershipsByTenant(ctx, tenant.ID())
	case input.UserID != "":
		// The user filter is answered within the authenticated tenant only;
		// memberships of other tenants are not this tenant's to see.
		tenantPublicID, ok := tenantctx.TenantPublicIDFromContext(ctx)
		if !ok {
			return nil, tenantctx.ErrMissing
		}

		tenant, err := s.resolveTenant(ctx, tenantPublicID)
		if err != nil {
			return nil, err
		}

		membership, err := s.memberships.FindMembership(ctx, tenant.ID(), input.UserID)
		if errors.Is(err, repository.ErrMembershipNotFound) {
			return []domain.Membership{}, nil
		}

		if err != nil {
			return nil, err
		}

		return []domain.Membership{membership}, nil
	default:
		return nil, ErrFilterRequired
	}
}

func (s *RelationService) CreateTenantInvitation(ctx context.Context, input CreateTenantInvitationInput) (TenantInvitationCreation, error) {
	if err := input.Role.Invitable(); err != nil {
		return TenantInvitationCreation{}, err
	}

	displayName, err := domain.ParseDisplayName(input.DisplayName)
	if err != nil {
		return TenantInvitationCreation{}, err
	}

	tenant, err := s.writableTenant(ctx, input.TenantPublicID)
	if err != nil {
		return TenantInvitationCreation{}, err
	}

	id, err := identifier.NewUUIDv7()
	if err != nil {
		return TenantInvitationCreation{}, err
	}

	publicID, err := identifier.NewPublicID()
	if err != nil {
		return TenantInvitationCreation{}, err
	}

	token, tokenHash, err := onetimetoken.New()
	if err != nil {
		return TenantInvitationCreation{}, err
	}

	invitation := domain.NewInvitation(id, publicID, tenant.ID(), tenant.PublicID(), input.Role, displayName, s.now().Add(s.invitationTTL), domain.InvitationPending)

	err = s.withCurrentPermission(ctx, tenant.ID(), func(ctx context.Context) error {
		return s.invitations.CreateInvitation(ctx, invitation, tokenHash)
	})
	if err != nil {
		return TenantInvitationCreation{}, err
	}

	return TenantInvitationCreation{Invitation: invitation, Token: token}, nil
}

func (s *RelationService) ListTenantInvitations(ctx context.Context, tenantPublicID string) ([]domain.Invitation, error) {
	tenant, err := s.resolveTenant(ctx, tenantPublicID)
	if err != nil {
		return nil, err
	}

	return s.invitations.ListOpenInvitations(ctx, tenant.ID(), s.now())
}

func (s *RelationService) RevokeTenantInvitation(ctx context.Context, input RevokeTenantInvitationInput) (domain.Invitation, error) {
	if input.InvitationID == "" {
		return domain.Invitation{}, ErrInvitationIDRequired
	}

	tenant, err := s.writableTenant(ctx, input.TenantPublicID)
	if err != nil {
		return domain.Invitation{}, err
	}

	var revoked domain.Invitation

	err = s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		invitation, err := s.invitations.FindInvitationForUpdate(ctx, tenant.ID(), input.InvitationID)
		if err != nil {
			return err
		}

		now := s.now()

		revoked, err = invitation.Revoke(now)
		if err != nil {
			return err
		}

		return s.invitations.MarkInvitationRevoked(ctx, invitation.ID(), now)
	})
	if err != nil {
		return domain.Invitation{}, err
	}

	return revoked, nil
}

func (s *RelationService) AcceptTenantInvitation(ctx context.Context, input AcceptTenantInvitationInput) (domain.Membership, error) {
	if input.TenantPublicID == "" {
		return domain.Membership{}, ErrTenantIDRequired
	}

	if input.Token == "" {
		return domain.Membership{}, ErrInvitationTokenRequired
	}

	subject, ok := tenantctx.SubjectFromContext(ctx)
	if !ok {
		return domain.Membership{}, tenantctx.ErrSubjectMissing
	}

	tenant, err := s.tenants.FindTenantByPublicID(ctx, input.TenantPublicID)
	if err != nil {
		return domain.Membership{}, err
	}

	var membership domain.Membership

	err = s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := s.memberships.LockTenantMemberships(ctx, tenant.ID()); err != nil {
			return err
		}

		invitation, err := s.invitations.FindInvitationByTokenForUpdate(ctx, tenant.ID(), onetimetoken.HashOf(input.Token))
		if errors.Is(err, repository.ErrInvitationNotFound) {
			return ErrInvitationRejected
		}

		if err != nil {
			return err
		}

		now := s.now()

		if _, err := invitation.Accept(now); err != nil {
			return ErrInvitationRejected
		}

		if !tenant.Owned() {
			return ErrTenantPendingOwner
		}

		if tenant.Archived() {
			return tenantrepository.ErrTenantArchived
		}

		membership, err = s.memberships.AddTenantMember(ctx, tenant.ID(), subject, invitation.Role(), invitation.DisplayName())
		if err != nil {
			return err
		}

		return s.invitations.MarkInvitationAccepted(ctx, invitation.ID(), subject, now)
	})
	if err != nil {
		return domain.Membership{}, err
	}

	return membership, nil
}

// withCurrentPermission runs the caller's current-permission check and the
// write it guards in one transaction: the check locks the caller's membership
// row, so a concurrent revoke or downgrade either lands before the check or
// waits for the write to commit. The scope of the internal JWT is verified by
// the transport beforehand; this re-reads what the caller may do right now.
//
// The transaction opens by taking the tenant's advisory lock, so the
// membership writes of one tenant run one at a time: two administrators
// revoking or downgrading each other queue instead of holding each other's
// membership rows and deadlocking. The row lock of the check itself stays as
// defense in depth against a write that does not pass through here.
func (s *RelationService) withCurrentPermission(ctx context.Context, tenantID string, write func(context.Context) error) error {
	return s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := s.memberships.LockTenantMemberships(ctx, tenantID); err != nil {
			return err
		}

		if err := s.authorizer.Require(ctx, tenantID, domain.ScopeTenantWrite); err != nil {
			return err
		}

		return write(ctx)
	})
}

func (s *RelationService) keepAnotherOwner(ctx context.Context, tenantID, userID string) error {
	role, err := s.memberships.FindTenantRoleForShare(ctx, tenantID, userID)
	if errors.Is(err, repository.ErrMembershipNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	if role != domain.RoleOwner {
		return nil
	}

	owners, err := s.memberships.CountTenantOwners(ctx, tenantID)
	if err != nil {
		return err
	}

	if owners <= 1 {
		return ErrLastOwner
	}

	return nil
}

// writeMembership is withCurrentPermission for a write that answers with the
// membership it produced.
func (s *RelationService) writeMembership(ctx context.Context, tenantID string, write func(context.Context) (domain.Membership, error)) (domain.Membership, error) {
	var membership domain.Membership

	err := s.withCurrentPermission(ctx, tenantID, func(ctx context.Context) error {
		written, err := write(ctx)
		if err != nil {
			return err
		}

		membership = written

		return nil
	})
	if err != nil {
		return domain.Membership{}, err
	}

	return membership, nil
}

// resolveTenant loads the tenant named by its public ID after confirming it
// is the tenant the caller is authenticated for.
func (s *RelationService) resolveTenant(ctx context.Context, tenantPublicID string) (tenantdomain.Tenant, error) {
	if tenantPublicID == "" {
		return tenantdomain.Tenant{}, ErrTenantIDRequired
	}

	if err := tenantctx.Ensure(ctx, tenantPublicID); err != nil {
		return tenantdomain.Tenant{}, err
	}

	return s.tenants.FindTenantByPublicID(ctx, tenantPublicID)
}

// writableTenant is resolveTenant plus the write gates: a pending_owner tenant
// accepts no membership administration, and an archived tenant keeps its
// memberships frozen.
func (s *RelationService) writableTenant(ctx context.Context, tenantPublicID string) (tenantdomain.Tenant, error) {
	tenant, err := s.resolveTenant(ctx, tenantPublicID)
	if err != nil {
		return tenantdomain.Tenant{}, err
	}

	if !tenant.Owned() {
		return tenantdomain.Tenant{}, ErrTenantPendingOwner
	}

	if tenant.Archived() {
		return tenantdomain.Tenant{}, tenantrepository.ErrTenantArchived
	}

	return tenant, nil
}

// writableEvent loads the event named by its public ID, confirms it belongs
// to the authenticated tenant, and applies the write gates of the event and
// of its tenant.
func (s *RelationService) writableEvent(ctx context.Context, eventPublicID string) (tenantdomain.Event, error) {
	if eventPublicID == "" {
		return tenantdomain.Event{}, ErrEventIDRequired
	}

	event, err := s.tenants.FindEventByPublicID(ctx, eventPublicID)
	if err != nil {
		return tenantdomain.Event{}, err
	}

	if err := tenantctx.Ensure(ctx, event.TenantPublicID()); err != nil {
		return tenantdomain.Event{}, err
	}

	if event.Status() == tenantdomain.EventStatusArchived {
		return tenantdomain.Event{}, tenantrepository.ErrEventArchived
	}

	if _, err := s.writableTenant(ctx, event.TenantPublicID()); err != nil {
		return tenantdomain.Event{}, err
	}

	return event, nil
}
