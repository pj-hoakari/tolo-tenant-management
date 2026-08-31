package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/internal-jwt-handling/jwks"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"
	"github.com/pj-hoakari/internal-jwt-handling/verifier"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1/relationv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/logging"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	relationdb "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/db"
	relationrepository "github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantapplication "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	tenantconnect "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/infra/connect"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/infra/db"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

var testDB *sqlx.DB

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("tenant_management"),
		postgres.WithUsername("tenant_management"),
		postgres.WithPassword("tenant_management"),
		postgres.WithInitScripts(migrationPaths()...),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start PostgreSQL test container: %v\n", err)
		os.Exit(1)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err == nil {
		testDB, err = infradb.Open(ctx, databaseURL)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to PostgreSQL test container: %v\n", err)

		_ = container.Terminate(context.Background())

		os.Exit(1)
	}

	code := m.Run()
	_ = testDB.Close()
	_ = container.Terminate(context.Background())

	os.Exit(code)
}

// testRefreshCooldown lets a key a test registers after the first request be
// picked up at once. jwks.Config reads a non-positive duration as "use the
// default", so the shortest cooldown a test can ask for is one nanosecond.
// These tests are about the transport, not about the cache; the jwks package
// covers the cache itself.
const testRefreshCooldown = time.Nanosecond

// jwksRegistry serves the keys of every token minted by a test. Every key it
// hands out has its own kid, because a JWKS naming one kid twice is refused as
// a whole.
type jwksRegistry struct {
	mu     sync.Mutex
	keys   []internaljwt.JWK
	minted int
}

func (r *jwksRegistry) add(keys ...internaljwt.JWK) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.keys = append(r.keys, keys...)
}

func (r *jwksRegistry) document() internaljwt.JWKS {
	r.mu.Lock()
	defer r.mu.Unlock()

	return internaljwt.JWKS{Keys: append([]internaljwt.JWK(nil), r.keys...)}
}

// nextKeyID names the signing key of the next token minted for the registry.
func (r *jwksRegistry) nextKeyID(prefix string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.minted++

	return fmt.Sprintf("%s-%d", prefix, r.minted)
}

// callerSubject is the subject of every token minted by jwtgen, and therefore
// the caller whose current membership the write RPCs re-check.
const callerSubject = jwtgen.DefaultSubject

type fixture struct {
	tenants     *infradb.PostgresTenantRepository
	memberships *relationdb.PostgresMembershipRepository
	jwks        *jwksRegistry
	client      relationv1connect.RelationAdminServiceClient
	tenantA     tenantdomain.Tenant
	tenantB     tenantdomain.Tenant
	eventA      tenantdomain.Event
	eventB      tenantdomain.Event
	tokenA      string
	tokenB      string
}

// newFixture resets the database, seeds two owned tenants with one event
// each and the caller as their owner, and serves TenantService and
// RelationAdminService together as the server does.
func newFixture(t *testing.T) fixture {
	t.Helper()

	if _, err := testDB.Exec(`TRUNCATE events, tenants CASCADE`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}

	ctx := context.Background()
	tenants := infradb.NewPostgresTenantRepository(testDB)
	memberships := relationdb.NewPostgresMembershipRepository(testDB)
	registry := &jwksRegistry{}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(registry.document()); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	t.Cleanup(jwksServer.Close)

	cache, err := jwks.New(jwks.Config{
		URL:             jwksServer.URL,
		RefreshCooldown: testRefreshCooldown,
		FailureCooldown: testRefreshCooldown,
	})
	if err != nil {
		t.Fatalf("jwks.New() error = %v", err)
	}

	tokenVerifier, err := verifier.New(tenantconnect.DefaultInternalJWTIssuer, tenantconnect.DefaultInternalJWTAudience, cache)
	if err != nil {
		t.Fatalf("verifier.New() error = %v", err)
	}

	handler, err := tenantconnect.NewHandlerWithVerifier(
		tenantapplication.NewTenantService(tenants, tenants, memberships, application.NewAuthorizer(memberships)),
		tokenVerifier,
		Mount(application.NewRelationService(tenants, memberships, memberships)),
	)
	if err != nil {
		t.Fatalf("NewHandlerWithVerifier() error = %v", err)
	}

	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	f := fixture{
		tenants:     tenants,
		memberships: memberships,
		jwks:        registry,
		client:      relationv1connect.NewRelationAdminServiceClient(httpServer.Client(), httpServer.URL),
		tenantA:     tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000a1", "aaaaaaaaaaaaaaa1", "Alpha", "standard", tenantdomain.TenantOwnershipStateOwned, false),
		tenantB:     tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000b1", "bbbbbbbbbbbbbbb1", "Beta", "standard", tenantdomain.TenantOwnershipStateOwned, false),
	}
	f.eventA = tenantdomain.NewEvent("00000000-0000-0000-0000-0000000000a2", "aaaaaaaaaaaaaaa2", f.tenantA.ID(), f.tenantA.PublicID(), "Alpha Festival", tenantdomain.EventTypeShortTerm, tenantdomain.EventStatusDraft)
	f.eventB = tenantdomain.NewEvent("00000000-0000-0000-0000-0000000000b2", "bbbbbbbbbbbbbbb2", f.tenantB.ID(), f.tenantB.PublicID(), "Beta Festival", tenantdomain.EventTypeShortTerm, tenantdomain.EventStatusDraft)

	for _, tenant := range []tenantdomain.Tenant{f.tenantA, f.tenantB} {
		if err := tenants.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%q) error = %v", tenant.Name(), err)
		}
	}

	for _, event := range []tenantdomain.Event{f.eventA, f.eventB} {
		if err := tenants.CreateEvent(ctx, event); err != nil {
			t.Fatalf("CreateEvent(%q) error = %v", event.Name(), err)
		}
	}

	// The caller of the tests administers both tenants, because the write RPCs
	// re-check the membership of the token's subject.
	for _, tenant := range []tenantdomain.Tenant{f.tenantA, f.tenantB} {
		if _, err := memberships.AddTenantMember(ctx, tenant.ID(), callerSubject, relationdomain.RoleOwner); err != nil {
			t.Fatalf("AddTenantMember(owner of %q) error = %v", tenant.Name(), err)
		}
	}

	f.tokenA = f.mintToken(t, f.tenantA.PublicID(), "tenant.read tenant.write")
	f.tokenB = f.mintToken(t, f.tenantB.PublicID(), "tenant.read tenant.write")

	return f
}

func (f fixture) mintToken(t *testing.T, tenantPublicID, scope string) string {
	t.Helper()

	output, err := jwtgen.Generate(jwtgen.Config{
		Issuer:         tenantconnect.DefaultInternalJWTIssuer,
		Audience:       tenantconnect.DefaultInternalJWTAudience,
		TokenUse:       internaljwt.TokenUseTenantAccess,
		TenantPublicID: tenantPublicID,
		Scope:          scope,
		KeyID:          f.jwks.nextKeyID("key-" + tenantPublicID),
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	f.jwks.add(output.JWKS.Keys...)

	return "Bearer " + output.Token
}

func authorized[T any](token string, msg *T) *connectrpc.Request[T] {
	req := connectrpc.NewRequest(msg)
	if token != "" {
		req.Header().Set("Authorization", token)
	}

	return req
}

func (f fixture) addMember(t *testing.T, token, tenantID, userID string, role relationv1.Role) (*relationv1.Membership, error) {
	t.Helper()

	res, err := f.client.AddTenantMember(context.Background(), authorized(token, &relationv1.AddTenantMemberRequest{TenantId: tenantID, UserId: userID, TenantRole: role}))
	if err != nil {
		return nil, err
	}

	return res.Msg.GetMembership(), nil
}

func TestAddTenantMemberOverTransport(t *testing.T) {
	f := newFixture(t)

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
	f := newFixture(t)
	ctx := context.Background()

	archived := tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000c1", "ccccccccccccccc1", "Gamma", "standard", tenantdomain.TenantOwnershipStateOwned, true)
	if err := f.tenants.CreateTenant(ctx, archived); err != nil {
		t.Fatalf("CreateTenant(archived) error = %v", err)
	}

	pending := tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000d1", "ddddddddddddddd1", "Delta", "standard", tenantdomain.TenantOwnershipStatePendingOwner, false)
	if err := f.tenants.CreatePendingTenant(ctx, pending, tenantdomain.OwnershipClaim{TokenHash: tenantdomain.OwnershipClaimTokenHash{}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("CreatePendingTenant() error = %v", err)
	}

	for _, tenant := range []tenantdomain.Tenant{archived, pending} {
		token := f.mintToken(t, tenant.PublicID(), "tenant.read tenant.write")

		_, err := f.addMember(t, token, tenant.PublicID(), "user-1", relationv1.Role_ROLE_STAFF)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Errorf("AddTenantMember(%s) error code = %v, want %v", tenant.Name(), got, want)
		}
	}

	// Reads keep working on an archived tenant.
	token := f.mintToken(t, archived.PublicID(), "tenant.read")

	res, err := f.client.ListMemberships(ctx, authorized(token, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: archived.PublicID()}}))
	if err != nil || len(res.Msg.GetMemberships()) != 0 {
		t.Errorf("ListMemberships(archived) = %v, %v, want empty list", res, err)
	}
}

func TestRolesOverTransport(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.addMember(t, f.tokenA, f.tenantA.PublicID(), "user-1", relationv1.Role_ROLE_STAFF); err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	changed, err := f.client.ChangeTenantRole(ctx, authorized(f.tokenA, &relationv1.ChangeTenantRoleRequest{TenantId: f.tenantA.PublicID(), UserId: "user-1", TenantRole: relationv1.Role_ROLE_OWNER}))
	if err != nil {
		t.Fatalf("ChangeTenantRole() error = %v", err)
	}

	if got, want := changed.Msg.GetMembership().GetTenantRole(), relationv1.Role_ROLE_OWNER; got != want {
		t.Errorf("TenantRole after change = %v, want %v", got, want)
	}

	_, err = f.client.ChangeTenantRole(ctx, authorized(f.tokenA, &relationv1.ChangeTenantRoleRequest{TenantId: f.tenantA.PublicID(), UserId: "user-9", TenantRole: relationv1.Role_ROLE_OWNER}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Errorf("ChangeTenantRole(unknown member) error code = %v, want %v", got, want)
	}

	granted, err := f.client.GrantEventRole(ctx, authorized(f.tokenA, &relationv1.GrantEventRoleRequest{EventId: f.eventA.PublicID(), UserId: "user-1", Role: relationv1.Role_ROLE_STAFF}))
	if err != nil {
		t.Fatalf("GrantEventRole() error = %v", err)
	}

	if roles := granted.Msg.GetMembership().GetEventRoles(); len(roles) != 1 || roles[0].GetEventId() != f.eventA.PublicID() || roles[0].GetRole() != relationv1.Role_ROLE_STAFF {
		t.Errorf("EventRoles after grant = %v, want staff on %q", roles, f.eventA.PublicID())
	}

	grantTests := []struct {
		name    string
		token   string
		eventID string
		userID  string
		want    connectrpc.Code
	}{
		{name: "user not a member of the tenant", token: f.tokenA, eventID: f.eventA.PublicID(), userID: "user-2", want: connectrpc.CodeFailedPrecondition},
		{name: "event of another tenant", token: f.tokenA, eventID: f.eventB.PublicID(), userID: "user-1", want: connectrpc.CodePermissionDenied},
		{name: "unknown event", token: f.tokenA, eventID: "0000000000000000", userID: "user-1", want: connectrpc.CodeNotFound},
	}

	for _, tt := range grantTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.client.GrantEventRole(ctx, authorized(tt.token, &relationv1.GrantEventRoleRequest{EventId: tt.eventID, UserId: tt.userID, Role: relationv1.Role_ROLE_STAFF}))
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("GrantEventRole() error code = %v, want %v", got, tt.want)
			}
		})
	}

	listed, err := f.client.ListMemberships(ctx, authorized(f.tokenA, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: f.tenantA.PublicID()}}))
	if err != nil {
		t.Fatalf("ListMemberships(tenant) error = %v", err)
	}

	// The caller's own owner membership is listed next to user-1's.
	if listedMemberships := listed.Msg.GetMemberships(); len(listedMemberships) != 2 || len(membershipOf(listedMemberships, "user-1").GetEventRoles()) != 1 {
		t.Errorf("ListMemberships(tenant) = %v, want the caller and user-1 with one event role", listedMemberships)
	}

	byUser, err := f.client.ListMemberships(ctx, authorized(f.tokenA, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_UserId{UserId: "user-1"}}))
	if err != nil || len(byUser.Msg.GetMemberships()) != 1 {
		t.Errorf("ListMemberships(user) = %v, %v, want one membership", byUser, err)
	}

	// Tenant B cannot see tenant A's memberships, even through the user filter.
	byUserB, err := f.client.ListMemberships(ctx, authorized(f.tokenB, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_UserId{UserId: "user-1"}}))
	if err != nil || len(byUserB.Msg.GetMemberships()) != 0 {
		t.Errorf("ListMemberships(user, other tenant) = %v, %v, want empty", byUserB, err)
	}

	_, err = f.client.ListMemberships(ctx, authorized(f.tokenB, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: f.tenantA.PublicID()}}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Errorf("ListMemberships(another tenant) error code = %v, want %v", got, want)
	}

	if _, err := f.client.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1", Scope: &relationv1.RevokeRoleRequest_EventId{EventId: f.eventA.PublicID()}})); err != nil {
		t.Fatalf("RevokeRole(event) error = %v", err)
	}

	_, err = f.client.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1", Scope: &relationv1.RevokeRoleRequest_EventId{EventId: f.eventA.PublicID()}}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Errorf("second RevokeRole(event) error code = %v, want %v", got, want)
	}

	if _, err := f.client.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1", Scope: &relationv1.RevokeRoleRequest_TenantId{TenantId: f.tenantA.PublicID()}})); err != nil {
		t.Fatalf("RevokeRole(tenant) error = %v", err)
	}

	_, err = f.client.RevokeRole(ctx, authorized(f.tokenA, &relationv1.RevokeRoleRequest{UserId: "user-1"}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
		t.Errorf("RevokeRole(no scope) error code = %v, want %v", got, want)
	}

	after, err := f.client.ListMemberships(ctx, authorized(f.tokenA, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: f.tenantA.PublicID()}}))
	if err != nil || len(after.Msg.GetMemberships()) != 1 || membershipOf(after.Msg.GetMemberships(), callerSubject) == nil {
		t.Errorf("ListMemberships after revoke = %v, %v, want the caller's own membership only", after, err)
	}
}

// TestCurrentPermissionOverTransport covers the re-check of the caller's own
// membership: the tenant.write scope of the token does not by itself
// administer a tenant.
func TestCurrentPermissionOverTransport(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	staffTenant := tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000c1", "ccccccccccccccc1", "Gamma", "standard", tenantdomain.TenantOwnershipStateOwned, false)
	strangerTenant := tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000d1", "ddddddddddddddd1", "Delta", "standard", tenantdomain.TenantOwnershipStateOwned, false)

	for _, tenant := range []tenantdomain.Tenant{staffTenant, strangerTenant} {
		if err := f.tenants.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%q) error = %v", tenant.Name(), err)
		}
	}

	// The caller is staff of one tenant and belongs to the other not at all.
	if _, err := f.memberships.AddTenantMember(ctx, staffTenant.ID(), callerSubject, relationdomain.RoleStaff); err != nil {
		t.Fatalf("AddTenantMember(staff) error = %v", err)
	}

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

// listMemberships lists the memberships of the tenant through the transport.
func (f fixture) listMemberships(t *testing.T, token, tenantPublicID string) []*relationv1.Membership {
	t.Helper()

	res, err := f.client.ListMemberships(context.Background(), authorized(token, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: tenantPublicID}}))
	if err != nil {
		t.Fatalf("ListMemberships(%q) error = %v", tenantPublicID, err)
	}

	return res.Msg.GetMemberships()
}

// TestConnectError pins the code every sentinel error is answered with,
// including the ones the transport cannot reach from a test.
func TestConnectError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want connectrpc.Code
	}{
		{err: application.ErrTenantIDRequired, want: connectrpc.CodeInvalidArgument},
		{err: relationdomain.ErrRoleReserved, want: connectrpc.CodeInvalidArgument},
		{err: relationrepository.ErrMembershipNotFound, want: connectrpc.CodeNotFound},
		{err: relationrepository.ErrMembershipAlreadyExists, want: connectrpc.CodeFailedPrecondition},
		{err: tenantrepository.ErrTenantArchived, want: connectrpc.CodeFailedPrecondition},
		{err: application.ErrPermissionDenied, want: connectrpc.CodePermissionDenied},
		{err: tenantctx.ErrMismatch, want: connectrpc.CodePermissionDenied},
		{err: tenantctx.ErrSubjectMissing, want: connectrpc.CodeUnauthenticated},
		{err: tenantctx.ErrMissing, want: connectrpc.CodeUnauthenticated},
		{err: fmt.Errorf("revoke: %w", infradb.ErrTransactionAborted), want: connectrpc.CodeAborted},
		{err: errors.New("something else"), want: connectrpc.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			t.Parallel()

			if got := connectrpc.CodeOf(connectError(context.Background(), tt.err)); got != tt.want {
				t.Errorf("connectError(%v) code = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestConnectErrorHidesInternalDetail keeps the cause of an internal failure
// out of the response and puts it in the server log instead, as the tenant
// transport does (service_gateway.md「エラー方針」). It cannot run in parallel:
// it reads the log back through the process-wide default logger.
func TestConnectErrorHidesInternalDetail(t *testing.T) {
	logs := captureLog(t)

	err := connectError(context.Background(), errors.New("secret detail"))

	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("connectError() = %v, want a Connect error", err)
	}

	if got, want := connectErr.Message(), "internal error"; got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}

	if strings.Contains(err.Error(), "secret detail") {
		t.Errorf("error = %q, want it to omit the underlying failure", err)
	}

	entry := decodeLogEntry(t, logs)

	if got, want := entry["severity"], "ERROR"; got != want {
		t.Errorf("severity = %v, want %q", got, want)
	}

	if got, want := entry["message"], "internal error"; got != want {
		t.Errorf("message = %v, want %q", got, want)
	}

	if got, want := entry["error"], "secret detail"; got != want {
		t.Errorf("error = %v, want %q", got, want)
	}
}

// TestConnectErrorReportsCanceledWithoutLogging pins a client that goes away to
// canceled: it is not a server fault, so nothing is logged. It shares the
// process-wide logger with TestConnectErrorHidesInternalDetail and so cannot
// run in parallel either.
func TestConnectErrorReportsCanceledWithoutLogging(t *testing.T) {
	logs := captureLog(t)

	err := connectError(context.Background(), context.Canceled)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeCanceled; got != want {
		t.Errorf("connectError(context.Canceled) code = %v, want %v", got, want)
	}

	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing logged", logs.String())
	}
}

// captureLog installs the service's own handler over a buffer as the default
// logger for the duration of the test, so the assertions read the records in
// the shape production writes them. The default logger is process-wide, so a
// test that calls this one must not call t.Parallel().
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var logs bytes.Buffer

	previous := slog.Default()

	slog.SetDefault(logging.NewLogger(&logs, logging.Options{Level: slog.LevelDebug, AddSource: false, ProjectID: ""}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return &logs
}

// decodeLogEntry parses the single JSON record the captured log holds.
func decodeLogEntry(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()

	line := strings.TrimSpace(logs.String())
	if line == "" {
		t.Fatal("nothing was logged")
	}

	if strings.Contains(line, "\n") {
		t.Fatalf("log = %q, want a single record", line)
	}

	var entry map[string]any

	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}

	return entry
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

func migrationPaths() []string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("locate test source file")
	}

	paths, err := filepath.Glob(filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "migrations", "*.up.sql"))
	if err != nil || len(paths) == 0 {
		panic("locate migration files")
	}

	sort.Strings(paths)

	return paths
}
