package connect_test

import (
	"context"
	"testing"

	connectrpc "connectrpc.com/connect"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
)

// relationFixture is a process seeded with two owned tenants, one event each
// (created through TenantService) and the caller as the owner of both, because
// the write RPCs of RelationAdminService re-check the membership of the token's
// subject.
type relationFixture struct {
	*process
	tenantA tenantdomain.Tenant
	tenantB tenantdomain.Tenant
	eventA  string
	eventB  string
	tokenA  string
	tokenB  string
}

func newRelationFixture(t *testing.T) relationFixture {
	t.Helper()

	p := newProcess(t)
	f := relationFixture{
		process: p,
		tenantA: p.createTenant(t, "aaaaaaaaaaaaaaa1", "Alpha"),
		tenantB: p.createTenant(t, "bbbbbbbbbbbbbbb1", "Beta"),
	}

	for _, tenant := range []tenantdomain.Tenant{f.tenantA, f.tenantB} {
		p.addTenantMember(t, tenant.ID(), callerSubject, relationdomain.RoleOwner)
	}

	f.tokenA = p.mintTenantAccessToken(t, f.tenantA.PublicID())
	f.tokenB = p.mintTenantAccessToken(t, f.tenantB.PublicID())
	f.eventA = p.createEvent(t, f.tokenA, f.tenantA.PublicID(), "Alpha Festival").GetEventId()
	f.eventB = p.createEvent(t, f.tokenB, f.tenantB.PublicID(), "Beta Festival").GetEventId()

	return f
}

// mintToken issues a tenant_access token for the tenant granting exactly scope.
func (f relationFixture) mintToken(t *testing.T, tenantPublicID, scope string) string {
	t.Helper()

	return f.mintTenantAccessTokenWithScope(t, tenantPublicID, scope)
}

func (f relationFixture) addMember(t *testing.T, token, tenantID, userID string, role relationv1.Role) (*relationv1.Membership, error) {
	t.Helper()

	res, err := f.relationClient.AddTenantMember(context.Background(), authorized(token, &relationv1.AddTenantMemberRequest{TenantId: tenantID, UserId: userID, TenantRole: role}))
	if err != nil {
		return nil, err
	}

	return res.Msg.GetMembership(), nil
}

// listMemberships lists the memberships of the tenant through the transport.
func (f relationFixture) listMemberships(t *testing.T, token, tenantPublicID string) []*relationv1.Membership {
	t.Helper()

	res, err := f.relationClient.ListMemberships(context.Background(), authorized(token, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: tenantPublicID}}))
	if err != nil {
		t.Fatalf("ListMemberships(%q) error = %v", tenantPublicID, err)
	}

	return res.Msg.GetMemberships()
}

// membershipOf returns the listed membership of userID, or nil.
func membershipOf(memberships []*relationv1.Membership, userID string) *relationv1.Membership {
	for _, membership := range memberships {
		if membership.GetUserId() == userID {
			return membership
		}
	}

	return nil
}

func TestAddTenantMemberOverTransport(t *testing.T) {
	f := newRelationFixture(t)

	membership, err := f.addMember(t, f.tokenA, f.tenantA.PublicID(), "user-1", relationv1.Role_ROLE_STAFF)
	if err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	if membership.GetUserId() != "user-1" || membership.GetTenantId() != f.tenantA.PublicID() || membership.GetTenantRole() != relationv1.Role_ROLE_STAFF || len(membership.GetEventRoles()) != 0 {
		t.Errorf("Membership = %v, want staff of %q without event roles", membership, f.tenantA.PublicID())
	}

	tests := []struct {
		name     string
		token    string
		tenantID string
		userID   string
		role     relationv1.Role
		want     connectrpc.Code
	}{
		{name: "duplicate membership", token: f.tokenA, tenantID: f.tenantA.PublicID(), userID: "user-1", role: relationv1.Role_ROLE_OWNER, want: connectrpc.CodeFailedPrecondition},
		{name: "reserved role", token: f.tokenA, tenantID: f.tenantA.PublicID(), userID: "user-2", role: relationv1.Role_ROLE_ADMIN, want: connectrpc.CodeInvalidArgument},
		{name: "unspecified role", token: f.tokenA, tenantID: f.tenantA.PublicID(), userID: "user-2", role: relationv1.Role_ROLE_UNSPECIFIED, want: connectrpc.CodeInvalidArgument},
		{name: "missing user", token: f.tokenA, tenantID: f.tenantA.PublicID(), userID: "", role: relationv1.Role_ROLE_STAFF, want: connectrpc.CodeInvalidArgument},
		{name: "another tenant", token: f.tokenA, tenantID: f.tenantB.PublicID(), userID: "user-2", role: relationv1.Role_ROLE_STAFF, want: connectrpc.CodePermissionDenied},
		{name: "unknown tenant", token: f.mintToken(t, "0000000000000000", "tenant.write"), tenantID: "0000000000000000", userID: "user-2", role: relationv1.Role_ROLE_STAFF, want: connectrpc.CodeNotFound},
		{name: "missing scope", token: f.mintToken(t, f.tenantA.PublicID(), "tenant.read"), tenantID: f.tenantA.PublicID(), userID: "user-2", role: relationv1.Role_ROLE_STAFF, want: connectrpc.CodePermissionDenied},
		{name: "no token", token: "", tenantID: f.tenantA.PublicID(), userID: "user-2", role: relationv1.Role_ROLE_STAFF, want: connectrpc.CodeUnauthenticated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.addMember(t, tt.token, tt.tenantID, tt.userID, tt.role)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("AddTenantMember() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFrozenTenantsRejectMembershipWrites(t *testing.T) {
	f := newRelationFixture(t)
	ctx := context.Background()

	archived := tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000c1", "ccccccccccccccc1", "Gamma", "standard", tenantdomain.TenantOwnershipStateOwned, true)
	if err := f.tenants.CreateTenant(ctx, archived); err != nil {
		t.Fatalf("CreateTenant(archived) error = %v", err)
	}

	// A registration that has not been claimed is a pending_owner tenant.
	pending := f.startRegistration(t, "Delta").GetTenant().GetTenantId()

	for name, tenantID := range map[string]string{"archived": archived.PublicID(), "pending_owner": pending} {
		token := f.mintToken(t, tenantID, "tenant.read tenant.write")

		_, err := f.addMember(t, token, tenantID, "user-1", relationv1.Role_ROLE_STAFF)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Errorf("AddTenantMember(%s tenant) error code = %v, want %v", name, got, want)
		}
	}

	// Reads keep working on an archived tenant.
	if got := f.listMemberships(t, f.mintToken(t, archived.PublicID(), "tenant.read"), archived.PublicID()); len(got) != 0 {
		t.Errorf("ListMemberships(archived) = %v, want empty list", got)
	}
}

func TestRolesOverTransport(t *testing.T) {
	f := newRelationFixture(t)
	ctx := context.Background()

	if _, err := f.addMember(t, f.tokenA, f.tenantA.PublicID(), "user-1", relationv1.Role_ROLE_STAFF); err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	changed, err := f.relationClient.ChangeTenantRole(ctx, authorized(f.tokenA, &relationv1.ChangeTenantRoleRequest{TenantId: f.tenantA.PublicID(), UserId: "user-1", TenantRole: relationv1.Role_ROLE_OWNER}))
	if err != nil {
		t.Fatalf("ChangeTenantRole() error = %v", err)
	}

	if got, want := changed.Msg.GetMembership().GetTenantRole(), relationv1.Role_ROLE_OWNER; got != want {
		t.Errorf("TenantRole after change = %v, want %v", got, want)
	}

	_, err = f.relationClient.ChangeTenantRole(ctx, authorized(f.tokenA, &relationv1.ChangeTenantRoleRequest{TenantId: f.tenantA.PublicID(), UserId: "user-9", TenantRole: relationv1.Role_ROLE_OWNER}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Errorf("ChangeTenantRole(unknown member) error code = %v, want %v", got, want)
	}

	granted, err := f.relationClient.GrantEventRole(ctx, authorized(f.tokenA, &relationv1.GrantEventRoleRequest{EventId: f.eventA, UserId: "user-1", Role: relationv1.Role_ROLE_STAFF}))
	if err != nil {
		t.Fatalf("GrantEventRole() error = %v", err)
	}

	if roles := granted.Msg.GetMembership().GetEventRoles(); len(roles) != 1 || roles[0].GetEventId() != f.eventA || roles[0].GetRole() != relationv1.Role_ROLE_STAFF {
		t.Errorf("EventRoles after grant = %v, want staff on %q", roles, f.eventA)
	}

	grantTests := []struct {
		name    string
		token   string
		eventID string
		userID  string
		want    connectrpc.Code
	}{
		{name: "user not a member of the tenant", token: f.tokenA, eventID: f.eventA, userID: "user-2", want: connectrpc.CodeFailedPrecondition},
		{name: "event of another tenant", token: f.tokenA, eventID: f.eventB, userID: "user-1", want: connectrpc.CodePermissionDenied},
		{name: "unknown event", token: f.tokenA, eventID: "0000000000000000", userID: "user-1", want: connectrpc.CodeNotFound},
	}

	for _, tt := range grantTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.relationClient.GrantEventRole(ctx, authorized(tt.token, &relationv1.GrantEventRoleRequest{EventId: tt.eventID, UserId: tt.userID, Role: relationv1.Role_ROLE_STAFF}))
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("GrantEventRole() error code = %v, want %v", got, tt.want)
			}
		})
	}

	// The caller's own owner membership is listed next to user-1's.
	if listed := f.listMemberships(t, f.tokenA, f.tenantA.PublicID()); len(listed) != 2 || len(membershipOf(listed, "user-1").GetEventRoles()) != 1 {
		t.Errorf("ListMemberships(tenant) = %v, want the caller and user-1 with one event role", listed)
	}

	byUser, err := f.relationClient.ListMemberships(ctx, authorized(f.tokenA, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_UserId{UserId: "user-1"}}))
	if err != nil || len(byUser.Msg.GetMemberships()) != 1 {
		t.Errorf("ListMemberships(user) = %v, %v, want one membership", byUser, err)
	}

	// Tenant B cannot see tenant A's memberships, even through the user filter.
	byUserB, err := f.relationClient.ListMemberships(ctx, authorized(f.tokenB, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_UserId{UserId: "user-1"}}))
	if err != nil || len(byUserB.Msg.GetMemberships()) != 0 {
		t.Errorf("ListMemberships(user, other tenant) = %v, %v, want empty", byUserB, err)
	}

	_, err = f.relationClient.ListMemberships(ctx, authorized(f.tokenB, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: f.tenantA.PublicID()}}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Errorf("ListMemberships(another tenant) error code = %v, want %v", got, want)
	}

	if _, err := f.relationClient.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1", Scope: &relationv1.RevokeRoleRequest_EventId{EventId: f.eventA}})); err != nil {
		t.Fatalf("RevokeRole(event) error = %v", err)
	}

	_, err = f.relationClient.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1", Scope: &relationv1.RevokeRoleRequest_EventId{EventId: f.eventA}}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Errorf("second RevokeRole(event) error code = %v, want %v", got, want)
	}

	if _, err := f.relationClient.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1", Scope: &relationv1.RevokeRoleRequest_TenantId{TenantId: f.tenantA.PublicID()}})); err != nil {
		t.Fatalf("RevokeRole(tenant) error = %v", err)
	}

	_, err = f.relationClient.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1"}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
		t.Errorf("RevokeRole(no scope) error code = %v, want %v", got, want)
	}

	if after := f.listMemberships(t, f.tokenA, f.tenantA.PublicID()); len(after) != 1 || membershipOf(after, callerSubject) == nil {
		t.Errorf("ListMemberships after revoke = %v, want the caller's own membership only", after)
	}
}

// TestCurrentPermissionOverTransport covers the re-check of the caller's own
// membership: the tenant.write scope of the token does not by itself
// administer a tenant.
func TestCurrentPermissionOverTransport(t *testing.T) {
	f := newRelationFixture(t)

	staffTenant := f.createTenant(t, "ccccccccccccccc1", "Gamma")
	strangerTenant := f.createTenant(t, "ddddddddddddddd1", "Delta")

	// The caller is staff of one tenant and belongs to the other not at all.
	f.addTenantMember(t, staffTenant.ID(), callerSubject, relationdomain.RoleStaff)

	writeTests := []struct {
		name   string
		tenant tenantdomain.Tenant
	}{
		{name: "staff cannot administer the tenant", tenant: staffTenant},
		{name: "a caller without membership cannot administer the tenant", tenant: strangerTenant},
	}

	for _, tt := range writeTests {
		t.Run(tt.name, func(t *testing.T) {
			token := f.mintToken(t, tt.tenant.PublicID(), "tenant.read tenant.write")

			_, err := f.addMember(t, token, tt.tenant.PublicID(), "user-1", relationv1.Role_ROLE_STAFF)
			if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
				t.Fatalf("AddTenantMember() error code = %v, want %v", got, want)
			}
		})
	}

	// Reads rely on the scope only, so even the caller who does not belong to
	// the tenant lists its memberships. The lists also show that the refused
	// writes above left nothing behind: the stranger tenant has no membership
	// at all, and the staff tenant only the caller's own.
	staffRead := f.listMemberships(t, f.mintToken(t, staffTenant.PublicID(), "tenant.read"), staffTenant.PublicID())
	if len(staffRead) != 1 || membershipOf(staffRead, callerSubject) == nil {
		t.Errorf("ListMemberships(staff tenant) = %v, want the caller's own membership only", staffRead)
	}

	strangerRead := f.listMemberships(t, f.mintToken(t, strangerTenant.PublicID(), "tenant.read"), strangerTenant.PublicID())
	if len(strangerRead) != 0 {
		t.Errorf("ListMemberships(non-member) = %v, want empty list", strangerRead)
	}
}
