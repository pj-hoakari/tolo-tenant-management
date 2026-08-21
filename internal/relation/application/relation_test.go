//go:generate go tool mockgen -source=../repository/membership.go -destination=mock_membership_repository_test.go -package=application_test
//go:generate go tool mockgen -source=../../repository/tenant.go -destination=mock_tenant_repository_test.go -package=application_test

package application_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var (
	ownedTenant    = tenantdomain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", tenantdomain.TenantOwnershipStateOwned, false)
	pendingTenant  = tenantdomain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", tenantdomain.TenantOwnershipStatePendingOwner, false)
	archivedTenant = tenantdomain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", tenantdomain.TenantOwnershipStateOwned, true)
	draftEvent     = tenantdomain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", tenantdomain.EventTypeShortTerm, tenantdomain.EventStatusDraft)
	archivedEvent  = tenantdomain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", tenantdomain.EventTypeShortTerm, tenantdomain.EventStatusArchived)
)

func ownCtx() context.Context {
	return tenantctx.WithTenantPublicID(context.Background(), ownedTenant.PublicID())
}

type mocks struct {
	tenants     *MockTenantRepository
	memberships *MockMembershipRepository
	service     *application.RelationService
}

func newMocks(t *testing.T) mocks {
	t.Helper()

	ctrl := gomock.NewController(t)
	tenants := NewMockTenantRepository(ctrl)
	memberships := NewMockMembershipRepository(ctrl)

	return mocks{tenants: tenants, memberships: memberships, service: application.NewRelationService(tenants, memberships)}
}

func TestAddTenantMember(t *testing.T) {
	t.Parallel()

	m := newMocks(t)
	want := domain.NewMembership("user-1", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, nil)
	m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
	m.memberships.EXPECT().AddTenantMember(gomock.Any(), ownedTenant.ID(), "user-1", domain.RoleStaff).Return(want, nil)

	got, err := m.service.AddTenantMember(ownCtx(), application.AddTenantMemberInput{TenantPublicID: ownedTenant.PublicID(), UserID: "user-1", Role: domain.RoleStaff})
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
		{name: "pending tenant", ctx: ownCtx(), tenant: pendingTenant, lookup: true, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleStaff}, want: application.ErrTenantPendingOwner},
		{name: "archived tenant", ctx: ownCtx(), tenant: archivedTenant, lookup: true, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleStaff}, want: tenantrepository.ErrTenantArchived},
		{name: "other tenant", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "other-tenant", UserID: "user-1", Role: domain.RoleStaff}, want: tenantctx.ErrMismatch},
		{name: "no tenant context", ctx: context.Background(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleStaff}, want: tenantctx.ErrMissing},
		{name: "reserved role", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleAdmin}, want: domain.ErrRoleReserved},
		{name: "unspecified role", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", UserID: "user-1", Role: domain.RoleUnspecified}, want: domain.ErrRoleRequired},
		{name: "missing user", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{TenantPublicID: "tenant-public-id", Role: domain.RoleStaff}, want: application.ErrUserIDRequired},
		{name: "missing tenant", ctx: ownCtx(), lookup: false, input: application.AddTenantMemberInput{UserID: "user-1", Role: domain.RoleStaff}, want: application.ErrTenantIDRequired},
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
	want := domain.NewMembership("user-1", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleStaff, []domain.EventRole{domain.NewEventRole(draftEvent.ID(), draftEvent.PublicID(), domain.RoleStaff)})
	m.tenants.EXPECT().FindEventByPublicID(gomock.Any(), draftEvent.PublicID()).Return(draftEvent, nil)
	m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
	m.memberships.EXPECT().GrantEventRole(gomock.Any(), draftEvent.ID(), "user-1", domain.RoleStaff).Return(want, nil)

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
		{name: "event of another tenant", ctx: tenantctx.WithTenantPublicID(context.Background(), "other-tenant"), event: draftEvent, want: tenantctx.ErrMismatch},
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
		m.memberships.EXPECT().RevokeTenantMembership(gomock.Any(), ownedTenant.ID(), "user-1").Return(nil)

		if err := m.service.RevokeRole(ownCtx(), application.RevokeRoleInput{UserID: "user-1", TenantPublicID: ownedTenant.PublicID()}); err != nil {
			t.Fatalf("RevokeRole() error = %v", err)
		}
	})

	t.Run("event scope removes the event role", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)
		m.tenants.EXPECT().FindEventByPublicID(gomock.Any(), draftEvent.PublicID()).Return(draftEvent, nil)
		m.tenants.EXPECT().FindTenantByPublicID(gomock.Any(), ownedTenant.PublicID()).Return(ownedTenant, nil)
		m.memberships.EXPECT().RevokeEventRole(gomock.Any(), draftEvent.ID(), "user-1").Return(nil)

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

func TestListMemberships(t *testing.T) {
	t.Parallel()

	t.Run("by tenant keeps answering for archived tenants", func(t *testing.T) {
		t.Parallel()

		m := newMocks(t)
		want := []domain.Membership{domain.NewMembership("user-1", ownedTenant.ID(), ownedTenant.PublicID(), domain.RoleOwner, nil)}
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
