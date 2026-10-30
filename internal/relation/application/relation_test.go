//go:generate go tool mockgen -source=../repository/membership.go -destination=mock_membership_repository_test.go -package=application_test
//go:generate go tool mockgen -source=../repository/invitation.go -destination=mock_invitation_repository_test.go -package=application_test
//go:generate go tool mockgen -source=../../tenant/repository/tenant.go -destination=mock_tenant_repository_test.go -package=application_test

package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/mock/gomock"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var (
	ownedTenant    = tenantdomain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", tenantdomain.TenantOwnershipStateOwned, tenantdomain.Subdomain{}, false)
	pendingTenant  = tenantdomain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", tenantdomain.TenantOwnershipStatePendingOwner, tenantdomain.Subdomain{}, false)
	archivedTenant = tenantdomain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", tenantdomain.TenantOwnershipStateOwned, tenantdomain.Subdomain{}, true)
	draftEvent     = tenantdomain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", tenantdomain.EventStatusDraft)
	archivedEvent  = tenantdomain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", tenantdomain.EventStatusArchived)
)

var (
	hanako  = mustDisplayName("Hanako")
	testNow = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
)

func mustDisplayName(s string) domain.DisplayName {
	name, err := domain.ParseDisplayName(s)
	if err != nil {
		panic(err)
	}

	return name
}

// callerID is the authenticated subject of the tests, an owner of the tenant
// unless a test says otherwise.
const callerID = "user-0"

// withTenant returns a context carrying verified claims that authenticate the
// given tenant but no subject, as the transport interceptor would.
func withTenant(ctx context.Context, tenantPublicID string) context.Context {
	return internaljwt.ContextWithClaims(ctx, internaljwt.Claims{TenantPublicID: tenantPublicID})
}

// withPrincipal returns a context carrying verified claims that authenticate
// both the subject and the tenant.
func withPrincipal(ctx context.Context, subject, tenantPublicID string) context.Context {
	return internaljwt.ContextWithClaims(ctx, internaljwt.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
		TenantPublicID:   tenantPublicID,
	})
}

func ownCtx() context.Context {
	return withPrincipal(context.Background(), callerID, ownedTenant.PublicID())
}

// passthroughTransactor runs the unit of work on the caller's context: the use
// cases' transaction boundary is exercised without a database.
type passthroughTransactor struct{}

func (passthroughTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type mocks struct {
	tenants     *MockTenantRepository
	memberships *MockMembershipRepository
	invitations *MockInvitationRepository
	service     *application.RelationService
}

func newMocks(t *testing.T) mocks {
	t.Helper()

	ctrl := gomock.NewController(t)
	tenants := NewMockTenantRepository(ctrl)
	memberships := NewMockMembershipRepository(ctrl)
	invitations := NewMockInvitationRepository(ctrl)
	service := application.NewRelationService(tenants, memberships, invitations, passthroughTransactor{}, application.WithClock(func() time.Time { return testNow }))

	return mocks{tenants: tenants, memberships: memberships, invitations: invitations, service: service}
}

// expectLock expects the tenant's advisory lock, which the write RPCs take
// before anything else in their transaction.
func (m mocks) expectLock(tenantID string) *gomock.Call {
	return m.memberships.EXPECT().LockTenantMemberships(gomock.Any(), tenantID).Return(nil)
}

// expectOwner expects the current-permission check the write RPCs run against
// the caller's membership before the write itself.
func (m mocks) expectOwner(tenantID string) *gomock.Call {
	return m.memberships.EXPECT().FindTenantRoleForShare(gomock.Any(), tenantID, callerID).Return(domain.RoleOwner, nil)
}

func TestAddTenantMember(t *testing.T) {
	t.Parallel()

	m := newMocks(t)
	want := domain.NewMembership("user-1", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, hanako, time.Time{}, nil)
	m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
	m.memberships.EXPECT().AddTenantMember(gomock.Any(), ownedTenant.ID(), "user-1", domain.RoleStaff, hanako).Return(want, nil)

	got, err := m.service.AddTenantMember(ownCtx(), application.AddTenantMemberInput{TenantPublicID: ownedTenant.PublicID(), UserID: "user-1", Role: domain.RoleStaff, DisplayName: "Hanako"})
	if err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	if got.UserID() != want.UserID() || got.TenantRole() != want.TenantRole() {
		t.Errorf("AddTenantMember() = %#v, want %#v", got, want)
	}
}

func TestTenantWriteGates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ctx    context.Context
		tenant tenantdomain.Tenant
		lookup bool
		input  application.AddTenantMemberInput
		want   error
	}{
		{name: "pending tenant", ctx: ownCtx(), tenant: pendingTenant, lookup: true, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleStaff, DisplayName: "Hanako"}, want: application.ErrTenantPendingOwner},
		{name: "archived tenant", ctx: ownCtx(), tenant: archivedTenant, lookup: true, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleStaff, DisplayName: "Hanako"}, want: tenantrepository.ErrTenantArchived},
		{name: "other tenant", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "other-tenant", UserID: "user-1", Role: domain.RoleStaff, DisplayName: "Hanako"}, want: tenantctx.ErrMismatch},
		{name: "no tenant context", ctx: context.Background(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleStaff, DisplayName: "Hanako"}, want: tenantctx.ErrMissing},
		{name: "reserved role", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleAdmin, DisplayName: "Hanako"}, want: domain.ErrRoleReserved},
		{name: "unspecified role", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleUnspecified, DisplayName: "Hanako"}, want: domain.ErrRoleRequired},
		{name: "missing user", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", Role: domain.RoleStaff, DisplayName: "Hanako"}, want: application.ErrUserIDRequired},
		{name: "missing tenant", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{UserID: "user-1", Role: domain.RoleStaff, DisplayName: "Hanako"}, want: application.ErrTenantIDRequired},
		{name: "missing display name", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleStaff, DisplayName: " "}, want: domain.ErrDisplayNameRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			if tt.lookup {
				m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), tt.input.TenantPublicID).Return(tt.tenant, nil)
			}

			// The membership repository is never reached when a gate closes.
			if _, err := m.service.AddTenantMember(tt.ctx, tt.input); !errors.Is(err, tt.want) {
				t.Fatalf("AddTenantMember() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestGrantEventRole(t *testing.T) {
	t.Parallel()

	m := newMocks(t)
	want := domain.NewMembership("user-1", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, hanako, time.Time{}, []domain.EventRole{domain.NewEventRole(draftEvent.ID(), draftEvent.PublicID(), domain.RoleStaff)})
	m.tenants.EXPECT().FindEventByPublicID(gomock.Any(), draftEvent.PublicID()).Return(draftEvent, nil)
	m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
	gomock.InOrder(
		m.expectLock(draftEvent.TenantID()),
		m.expectOwner(draftEvent.TenantID()),
		m.memberships.EXPECT().GrantEventRole(gomock.Any(), draftEvent.ID(), "user-1", domain.RoleStaff).Return(want, nil),
	)

	got, err := m.service.GrantEventRole(ownCtx(), application.GrantEventRoleInput{EventPublicID: draftEvent.PublicID(), UserID: "user-1", Role: domain.RoleStaff})
	if err != nil {
		t.Fatalf("GrantEventRole() error = %v", err)
	}

	if len(got.EventRoles()) != 1 {
		t.Errorf("GrantEventRole() = %#v, want one event role", got)
	}
}

func TestGrantEventRoleGates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ctx    context.Context
		event  tenantdomain.Event
		tenant tenantdomain.Tenant
		want   error
	}{
		{name: "archived event", ctx: ownCtx(), event: archivedEvent, want: tenantrepository.ErrEventArchived},
		{name: "archived tenant", ctx: ownCtx(), event: draftEvent, tenant: archivedTenant, want: tenantrepository.ErrTenantArchived},
		{name: "event of another tenant", ctx: withTenant(context.Background(), "other-tenant"), event: draftEvent, want: tenantctx.ErrMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			m.tenants.EXPECT().FindEventByPublicID(gomock.Any(), draftEvent.PublicID()).Return(tt.event, nil)

			if tt.tenant != (tenantdomain.Tenant{}) {
				m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(tt.tenant, nil)
			}

			if _, err := m.service.GrantEventRole(tt.ctx, application.GrantEventRoleInput{EventPublicID: draftEvent.PublicID(), UserID: "user-1", Role: domain.RoleStaff}); !errors.Is(err, tt.want) {
				t.Fatalf("GrantEventRole() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRevokeRole(t *testing.T) {
	t.Parallel()

	t.Run("tenant scope removes the membership", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)
		m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
		gomock.InOrder(
			m.expectLock(ownedTenant.ID()),
			m.expectOwner(ownedTenant.ID()),
			m.memberships.EXPECT().FindTenantRoleForShare(gomock.Any(), ownedTenant.ID(), "user-1").Return(domain.RoleStaff, nil),
			m.memberships.EXPECT().RevokeTenantMembership(gomock.Any(), ownedTenant.ID(), "user-1").Return(nil),
		)

		if err := m.service.RevokeRole(ownCtx(), application.RevokeRoleInput{UserID: "user-1", TenantPublicID: ownedTenant.PublicID()}); err != nil {
			t.Fatalf("RevokeRole() error = %v", err)
		}
	})

	t.Run("event scope removes the event role", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)
		m.tenants.EXPECT().FindEventByPublicID(gomock.Any(), draftEvent.PublicID()).Return(draftEvent, nil)
		m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
		gomock.InOrder(
			m.expectLock(draftEvent.TenantID()),
			m.expectOwner(draftEvent.TenantID()),
			m.memberships.EXPECT().RevokeEventRole(gomock.Any(), draftEvent.ID(), "user-1").Return(nil),
		)

		if err := m.service.RevokeRole(ownCtx(), application.RevokeRoleInput{UserID: "user-1", EventPublicID: draftEvent.PublicID()}); err != nil {
			t.Fatalf("RevokeRole() error = %v", err)
		}
	})

	t.Run("scope validation", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)

		if err := m.service.RevokeRole(ownCtx(), application.RevokeRoleInput{UserID: "user-1"}); !errors.Is(err, application.ErrScopeRequired) {
			t.Errorf("RevokeRole(no scope) error = %v, want %v", err, application.ErrScopeRequired)
		}

		if err := m.service.RevokeRole(ownCtx(), application.RevokeRoleInput{UserID: "user-1", TenantPublicID: "t", EventPublicID: "e"}); !errors.Is(err, application.ErrScopeAmbiguous) {
			t.Errorf("RevokeRole(both scopes) error = %v, want %v", err, application.ErrScopeAmbiguous)
		}
	})
}

func TestLastOwnerRule(t *testing.T) {
	t.Parallel()

	demote := func(ctx context.Context, s *application.RelationService) error {
		_, err := s.ChangeTenantRole(ctx, application.ChangeTenantRoleInput{TenantPublicID: ownedTenant.PublicID(), UserID: "user-1", Role: domain.RoleStaff})

		return err
	}
	revoke := func(ctx context.Context, s *application.RelationService) error {
		return s.RevokeRole(ctx, application.RevokeRoleInput{UserID: "user-1", TenantPublicID: ownedTenant.PublicID()})
	}
	demoted := domain.NewMembership("user-1", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, hanako, time.Time{}, nil)

	tests := []struct {
		name   string
		call   func(context.Context, *application.RelationService) error
		owners int
		write  func(m mocks) *gomock.Call
		want   error
	}{
		{name: "demoting the only owner", call: demote, owners: 1, want: application.ErrLastOwner},
		{name: "revoking the only owner", call: revoke, owners: 1, want: application.ErrLastOwner},
		{name: "demoting one of two owners", call: demote, owners: 2, write: func(m mocks) *gomock.Call {
			return m.memberships.EXPECT().ChangeTenantRole(gomock.Any(), ownedTenant.ID(), "user-1", domain.RoleStaff).Return(demoted, nil)
		}},
		{name: "revoking one of two owners", call: revoke, owners: 2, write: func(m mocks) *gomock.Call {
			return m.memberships.EXPECT().RevokeTenantMembership(gomock.Any(), ownedTenant.ID(), "user-1").Return(nil)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)

			calls := []any{
				m.expectLock(ownedTenant.ID()),
				m.expectOwner(ownedTenant.ID()),
				m.memberships.EXPECT().FindTenantRoleForShare(gomock.Any(), ownedTenant.ID(), "user-1").Return(domain.RoleOwner, nil),
				m.memberships.EXPECT().CountTenantOwners(gomock.Any(), ownedTenant.ID()).Return(tt.owners, nil),
			}
			if tt.write != nil {
				calls = append(calls, tt.write(m))
			}

			gomock.InOrder(calls...)

			if err := tt.call(ownCtx(), m.service); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestCurrentPermissionGates covers the re-check of the caller's own
// membership: the JWT scope alone is not enough for an administrative write.
func TestCurrentPermissionGates(t *testing.T) {
	t.Parallel()

	noSubjectCtx := withTenant(context.Background(), ownedTenant.PublicID())

	tests := []struct {
		name   string
		ctx    context.Context
		lookup bool
		role   domain.Role
		err    error
		want   error
	}{
		{name: "staff cannot administer the tenant", ctx: ownCtx(), lookup: true, role: domain.RoleStaff, want: application.ErrPermissionDenied},
		{name: "membership was revoked", ctx: ownCtx(), lookup: true, err: repository.ErrMembershipNotFound, want: application.ErrPermissionDenied},
		{name: "no subject in context", ctx: noSubjectCtx, lookup: false, want: tenantctx.ErrSubjectMissing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
			// The tenant is locked even when the check then refuses the write.
			m.expectLock(ownedTenant.ID())

			if tt.lookup {
				m.memberships.EXPECT().FindTenantRoleForShare(gomock.Any(), ownedTenant.ID(), callerID).Return(tt.role, tt.err)
			}

			// The write is never reached: it has no expectation, so a call
			// would fail the test.
			input := application.ChangeTenantRoleInput{TenantPublicID: ownedTenant.PublicID(), UserID: "user-1", Role: domain.RoleOwner}
			if _, err := m.service.ChangeTenantRole(tt.ctx, input); !errors.Is(err, tt.want) {
				t.Fatalf("ChangeTenantRole() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestCurrentPermissionGatesEveryWrite checks that no administrative write
// escapes the re-check.
func TestCurrentPermissionGatesEveryWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event bool
		call  func(context.Context, *application.RelationService) error
	}{
		{name: "ChangeTenantRole", call: func(ctx context.Context, s *application.RelationService) error {
			_, err := s.ChangeTenantRole(ctx, application.ChangeTenantRoleInput{TenantPublicID: ownedTenant.PublicID(), UserID: "user-1", Role: domain.RoleOwner})

			return err
		}},
		{name: "GrantEventRole", event: true, call: func(ctx context.Context, s *application.RelationService) error {
			_, err := s.GrantEventRole(ctx, application.GrantEventRoleInput{EventPublicID: draftEvent.PublicID(), UserID: "user-1", Role: domain.RoleStaff})

			return err
		}},
		{name: "RevokeRole by tenant", call: func(ctx context.Context, s *application.RelationService) error {
			return s.RevokeRole(ctx, application.RevokeRoleInput{UserID: "user-1", TenantPublicID: ownedTenant.PublicID()})
		}},
		{name: "RevokeRole by event", event: true, call: func(ctx context.Context, s *application.RelationService) error {
			return s.RevokeRole(ctx, application.RevokeRoleInput{UserID: "user-1", EventPublicID: draftEvent.PublicID()})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			if tt.event {
				m.tenants.EXPECT().FindEventByPublicID(gomock.Any(), draftEvent.PublicID()).Return(draftEvent, nil)
			}

			m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
			gomock.InOrder(
				m.expectLock(ownedTenant.ID()),
				m.memberships.EXPECT().FindTenantRoleForShare(gomock.Any(), ownedTenant.ID(), callerID).Return(domain.RoleStaff, nil),
			)

			if err := tt.call(ownCtx(), m.service); !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatalf("%s error = %v, want %v", tt.name, err, application.ErrPermissionDenied)
			}
		})
	}
}

// TestListMemberships also covers that a read is not re-checked: no
// FindTenantRoleForShare is expected, so a call would fail the test.
func TestListMemberships(t *testing.T) {
	t.Parallel()

	t.Run("by tenant keeps answering for archived tenants", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)
		want := []domain.Membership{domain.NewMembership("user-1", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleOwner, hanako, time.Time{}, nil)}
		m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(archivedTenant, nil)
		m.memberships.EXPECT().ListMembershipsByTenant(gomock.Any(), ownedTenant.ID()).Return(want, nil)

		got, err := m.service.ListMemberships(ownCtx(), application.ListMembershipsInput{TenantPublicID: ownedTenant.PublicID()})
		if err != nil || len(got) != 1 {
			t.Fatalf("ListMemberships() = %#v, %v, want one membership", got, err)
		}
	})

	t.Run("by user answers within the authenticated tenant", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)
		m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
		m.memberships.EXPECT().FindMembership(gomock.Any(), ownedTenant.ID(), "user-1").Return(domain.Membership{}, repository.ErrMembershipNotFound)

		got, err := m.service.ListMemberships(ownCtx(), application.ListMembershipsInput{UserID: "user-1"})
		if err != nil || len(got) != 0 {
			t.Fatalf("ListMemberships(user not a member) = %#v, %v, want empty", got, err)
		}
	})

	t.Run("filter validation", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)

		if _, err := m.service.ListMemberships(ownCtx(), application.ListMembershipsInput{}); !errors.Is(err, application.ErrFilterRequired) {
			t.Errorf("ListMemberships(no filter) error = %v, want %v", err, application.ErrFilterRequired)
		}

		if _, err := m.service.ListMemberships(ownCtx(), application.ListMembershipsInput{TenantPublicID: "t", UserID: "u"}); !errors.Is(err, application.ErrFilterAmbiguous) {
			t.Errorf("ListMemberships(both filters) error = %v, want %v", err, application.ErrFilterAmbiguous)
		}
	})
}

func registrationCtx(subject string) context.Context {
	return internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}})
}

func pendingInvitation(expiresAt time.Time) domain.Invitation {
	return domain.NewInvitation("invitation-id", "invitation-public-id", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, hanako, expiresAt, domain.InvitationPending)
}

func TestCreateTenantInvitation(t *testing.T) {
	t.Parallel()

	m := newMocks(t)
	m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)

	var (
		stored     domain.Invitation
		storedHash onetimetoken.Hash
	)

	gomock.InOrder(
		m.expectLock(ownedTenant.ID()),
		m.expectOwner(ownedTenant.ID()),
		m.invitations.EXPECT().CreateInvitation(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, invitation domain.Invitation, hash onetimetoken.Hash) error {
			stored, storedHash = invitation, hash

			return nil
		}),
	)

	got, err := m.service.CreateTenantInvitation(ownCtx(), application.CreateTenantInvitationInput{TenantPublicID: ownedTenant.PublicID(), Role: domain.RoleStaff, DisplayName: " Hanako "})
	if err != nil {
		t.Fatalf("CreateTenantInvitation() error = %v", err)
	}

	if got.Invitation != stored {
		t.Errorf("Invitation = %#v, want the stored %#v", got.Invitation, stored)
	}

	if !storedHash.Matches(got.Token) {
		t.Error("stored hash does not match the returned token")
	}

	invitation := got.Invitation
	if invitation.Role() != domain.RoleStaff || invitation.DisplayName() != hanako || invitation.TenantID() != ownedTenant.ID() || invitation.State() != domain.InvitationPending {
		t.Errorf("Invitation = %#v, want a pending staff invitation for Hanako in the tenant", invitation)
	}

	if want := testNow.Add(application.DefaultInvitationTTL); !invitation.ExpiresAt().Equal(want) {
		t.Errorf("ExpiresAt() = %v, want %v", invitation.ExpiresAt(), want)
	}
}

func TestCreateTenantInvitationRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		role        domain.Role
		displayName string
		tenant      *tenantdomain.Tenant
		callerRole  domain.Role
		want        error
	}{
		{name: "owner role", role: domain.RoleOwner, displayName: "Hanako", want: domain.ErrRoleNotInvitable},
		{name: "admin role", role: domain.RoleAdmin, displayName: "Hanako", want: domain.ErrRoleNotInvitable},
		{name: "unspecified role", role: domain.RoleUnspecified, displayName: "Hanako", want: domain.ErrRoleNotInvitable},
		{name: "blank display name", role: domain.RoleStaff, displayName: " ", want: domain.ErrDisplayNameRequired},
		{name: "pending tenant", role: domain.RoleStaff, displayName: "Hanako", tenant: &pendingTenant, want: application.ErrTenantPendingOwner},
		{name: "archived tenant", role: domain.RoleStaff, displayName: "Hanako", tenant: &archivedTenant, want: tenantrepository.ErrTenantArchived},
		{name: "staff caller", role: domain.RoleStaff, displayName: "Hanako", tenant: &ownedTenant, callerRole: domain.RoleStaff, want: application.ErrPermissionDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			if tt.tenant != nil {
				m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(*tt.tenant, nil)
			}

			if tt.callerRole != domain.RoleUnspecified {
				m.expectLock(ownedTenant.ID())
				m.memberships.EXPECT().FindTenantRoleForShare(gomock.Any(), ownedTenant.ID(), callerID).Return(tt.callerRole, nil)
			}

			input := application.CreateTenantInvitationInput{TenantPublicID: ownedTenant.PublicID(), Role: tt.role, DisplayName: tt.displayName}
			if _, err := m.service.CreateTenantInvitation(ownCtx(), input); !errors.Is(err, tt.want) {
				t.Fatalf("CreateTenantInvitation() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestListTenantInvitations(t *testing.T) {
	t.Parallel()

	m := newMocks(t)
	want := []domain.Invitation{pendingInvitation(testNow.Add(time.Hour))}

	m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), archivedTenant.PublicID()).Return(archivedTenant, nil)
	m.invitations.EXPECT().ListOpenInvitations(gomock.Any(), archivedTenant.ID(), testNow).Return(want, nil)

	got, err := m.service.ListTenantInvitations(ownCtx(), archivedTenant.PublicID())
	if err != nil {
		t.Fatalf("ListTenantInvitations() error = %v", err)
	}

	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("ListTenantInvitations() = %#v, want %#v", got, want)
	}
}

func TestRevokeTenantInvitation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		invitation domain.Invitation
		findErr    error
		want       error
	}{
		{name: "open invitation", invitation: pendingInvitation(testNow.Add(time.Hour))},
		{name: "expired invitation", invitation: pendingInvitation(testNow), want: domain.ErrInvitationNotPending},
		{name: "unknown invitation", findErr: repository.ErrInvitationNotFound, want: repository.ErrInvitationNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
			m.invitations.EXPECT().FindInvitationForUpdate(gomock.Any(), ownedTenant.ID(), "invitation-public-id").Return(tt.invitation, tt.findErr)

			if tt.want == nil {
				m.invitations.EXPECT().MarkInvitationRevoked(gomock.Any(), "invitation-id", testNow).Return(nil)
			}

			got, err := m.service.RevokeTenantInvitation(ownCtx(), application.RevokeTenantInvitationInput{TenantPublicID: ownedTenant.PublicID(), InvitationID: "invitation-public-id"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("RevokeTenantInvitation() error = %v, want %v", err, tt.want)
			}

			if tt.want == nil && got.State() != domain.InvitationRevoked {
				t.Errorf("State() = %v, want %v", got.State(), domain.InvitationRevoked)
			}
		})
	}
}

func TestAcceptTenantInvitation(t *testing.T) {
	t.Parallel()

	const token = "invitation-token"

	joined := domain.NewMembership("user-9", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, hanako, testNow, nil)

	m := newMocks(t)
	m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
	gomock.InOrder(
		m.expectLock(ownedTenant.ID()),
		m.invitations.EXPECT().FindInvitationByTokenForUpdate(gomock.Any(), ownedTenant.ID(), onetimetoken.HashOf(token)).Return(pendingInvitation(testNow.Add(time.Hour)), nil),
		m.memberships.EXPECT().AddTenantMember(gomock.Any(), ownedTenant.ID(), "user-9", domain.RoleStaff, hanako).Return(joined, nil),
		m.invitations.EXPECT().MarkInvitationAccepted(gomock.Any(), "invitation-id", "user-9", testNow).Return(nil),
	)

	got, err := m.service.AcceptTenantInvitation(registrationCtx("user-9"), application.AcceptTenantInvitationInput{TenantPublicID: ownedTenant.PublicID(), Token: token})
	if err != nil {
		t.Fatalf("AcceptTenantInvitation() error = %v", err)
	}

	if got.UserID() != "user-9" || got.TenantRole() != domain.RoleStaff || got.DisplayName() != hanako {
		t.Errorf("AcceptTenantInvitation() = %#v, want Hanako as staff", got)
	}
}

func TestAcceptTenantInvitationRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tenant     tenantdomain.Tenant
		invitation domain.Invitation
		findErr    error
		addErr     error
		want       error
	}{
		{name: "unknown token", tenant: ownedTenant, findErr: repository.ErrInvitationNotFound, want: application.ErrInvitationRejected},
		{name: "expired invitation", tenant: ownedTenant, invitation: pendingInvitation(testNow), want: application.ErrInvitationRejected},
		{name: "accepted invitation", tenant: ownedTenant, invitation: domain.NewInvitation("invitation-id", "invitation-public-id", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, hanako, testNow.Add(time.Hour), domain.InvitationAccepted), want: application.ErrInvitationRejected},
		{name: "revoked invitation", tenant: ownedTenant, invitation: domain.NewInvitation("invitation-id", "invitation-public-id", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, hanako, testNow.Add(time.Hour), domain.InvitationRevoked), want: application.ErrInvitationRejected},
		{name: "archived tenant", tenant: archivedTenant, invitation: pendingInvitation(testNow.Add(time.Hour)), want: tenantrepository.ErrTenantArchived},
		{name: "already a member", tenant: ownedTenant, invitation: pendingInvitation(testNow.Add(time.Hour)), addErr: repository.ErrMembershipAlreadyExists, want: repository.ErrMembershipAlreadyExists},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMocks(t)
			m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(tt.tenant, nil)
			m.expectLock(ownedTenant.ID())
			m.invitations.EXPECT().FindInvitationByTokenForUpdate(gomock.Any(), ownedTenant.ID(), gomock.Any()).Return(tt.invitation, tt.findErr)

			if tt.addErr != nil {
				m.memberships.EXPECT().AddTenantMember(gomock.Any(), ownedTenant.ID(), "user-9", domain.RoleStaff, hanako).Return(domain.Membership{}, tt.addErr)
			}

			_, err := m.service.AcceptTenantInvitation(registrationCtx("user-9"), application.AcceptTenantInvitationInput{TenantPublicID: ownedTenant.PublicID(), Token: "invitation-token"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("AcceptTenantInvitation() error = %v, want %v", err, tt.want)
			}
		})
	}

	t.Run("input validation", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)

		if _, err := m.service.AcceptTenantInvitation(registrationCtx("user-9"), application.AcceptTenantInvitationInput{TenantPublicID: ownedTenant.PublicID()}); !errors.Is(err, application.ErrInvitationTokenRequired) {
			t.Errorf("AcceptTenantInvitation(no token) error = %v, want %v", err, application.ErrInvitationTokenRequired)
		}

		if _, err := m.service.AcceptTenantInvitation(context.Background(), application.AcceptTenantInvitationInput{TenantPublicID: ownedTenant.PublicID(), Token: "invitation-token"}); !errors.Is(err, tenantctx.ErrSubjectMissing) {
			t.Errorf("AcceptTenantInvitation(no subject) error = %v, want %v", err, tenantctx.ErrSubjectMissing)
		}
	})
}
