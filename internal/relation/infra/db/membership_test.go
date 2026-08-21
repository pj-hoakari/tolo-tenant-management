package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
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

type fixture struct {
	tenants     *infradb.PostgresTenantRepository
	memberships *PostgresMembershipRepository
	tenantA     tenantdomain.Tenant
	tenantB     tenantdomain.Tenant
	eventA      tenantdomain.Event
	eventB      tenantdomain.Event
}

// newFixture resets the database and seeds two owned tenants with one event
// each.
func newFixture(t *testing.T) fixture {
	t.Helper()

	if _, err := testDB.Exec(`TRUNCATE events, tenants CASCADE`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}

	ctx := context.Background()
	f := fixture{
		tenants:     infradb.NewPostgresTenantRepository(testDB),
		memberships: NewPostgresMembershipRepository(testDB),
		tenantA:     tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000a1", "aaaaaaaaaaaaaaa1", "Alpha", "standard", tenantdomain.TenantOwnershipStateOwned, false),
		tenantB:     tenantdomain.NewTenant("00000000-0000-0000-0000-0000000000b1", "bbbbbbbbbbbbbbb1", "Beta", "standard", tenantdomain.TenantOwnershipStateOwned, false),
	}
	f.eventA = tenantdomain.NewEvent("00000000-0000-0000-0000-0000000000a2", "aaaaaaaaaaaaaaa2", f.tenantA.ID(), f.tenantA.PublicID(), "Alpha Festival", tenantdomain.EventTypeShortTerm, tenantdomain.EventStatusDraft)
	f.eventB = tenantdomain.NewEvent("00000000-0000-0000-0000-0000000000b2", "bbbbbbbbbbbbbbb2", f.tenantB.ID(), f.tenantB.PublicID(), "Beta Festival", tenantdomain.EventTypeShortTerm, tenantdomain.EventStatusDraft)

	for _, tenant := range []tenantdomain.Tenant{f.tenantA, f.tenantB} {
		if err := f.tenants.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%q) error = %v", tenant.Name(), err)
		}
	}

	for _, event := range []tenantdomain.Event{f.eventA, f.eventB} {
		if err := f.tenants.CreateEvent(ctx, event); err != nil {
			t.Fatalf("CreateEvent(%q) error = %v", event.Name(), err)
		}
	}

	return f
}

func TestTenantMemberships(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	membership, err := f.memberships.AddTenantMember(ctx, f.tenantA.ID(), "user-1", domain.RoleStaff)
	if err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	want := domain.NewMembership("user-1", f.tenantA.ID(), f.tenantA.PublicID(), domain.RoleStaff, nil)
	if got := membership; got.UserID() != want.UserID() || got.TenantID() != want.TenantID() || got.TenantPublicID() != want.TenantPublicID() || got.TenantRole() != want.TenantRole() || len(got.EventRoles()) != 0 {
		t.Errorf("AddTenantMember() = %#v, want %#v", got, want)
	}

	if _, err := f.memberships.AddTenantMember(ctx, f.tenantA.ID(), "user-1", domain.RoleOwner); !errors.Is(err, repository.ErrMembershipAlreadyExists) {
		t.Errorf("duplicate AddTenantMember() error = %v, want %v", err, repository.ErrMembershipAlreadyExists)
	}

	// The same user may belong to another tenant.
	if _, err := f.memberships.AddTenantMember(ctx, f.tenantB.ID(), "user-1", domain.RoleOwner); err != nil {
		t.Errorf("AddTenantMember(other tenant) error = %v, want nil", err)
	}

	if _, err := f.memberships.AddTenantMember(ctx, "00000000-0000-0000-0000-000000000099", "user-1", domain.RoleStaff); !errors.Is(err, repository.ErrTenantNotFound) {
		t.Errorf("AddTenantMember(unknown tenant) error = %v, want %v", err, repository.ErrTenantNotFound)
	}

	if _, err := f.memberships.AddTenantMember(ctx, f.tenantA.ID(), "user-2", domain.RoleAdmin); !errors.Is(err, domain.ErrRoleReserved) {
		t.Errorf("AddTenantMember(admin) error = %v, want %v", err, domain.ErrRoleReserved)
	}

	if _, err := f.memberships.AddTenantMember(ctx, f.tenantA.ID(), "user-2", domain.RoleUnspecified); !errors.Is(err, domain.ErrRoleRequired) {
		t.Errorf("AddTenantMember(unspecified) error = %v, want %v", err, domain.ErrRoleRequired)
	}

	changed, err := f.memberships.ChangeTenantRole(ctx, f.tenantA.ID(), "user-1", domain.RoleOwner)
	if err != nil {
		t.Fatalf("ChangeTenantRole() error = %v", err)
	}

	if got, want := changed.TenantRole(), domain.RoleOwner; got != want {
		t.Errorf("TenantRole() after change = %v, want %v", got, want)
	}

	if _, err := f.memberships.ChangeTenantRole(ctx, f.tenantA.ID(), "user-9", domain.RoleOwner); !errors.Is(err, repository.ErrMembershipNotFound) {
		t.Errorf("ChangeTenantRole(unknown member) error = %v, want %v", err, repository.ErrMembershipNotFound)
	}

	if err := f.memberships.RevokeTenantMembership(ctx, f.tenantA.ID(), "user-1"); err != nil {
		t.Fatalf("RevokeTenantMembership() error = %v", err)
	}

	if _, err := f.memberships.FindMembership(ctx, f.tenantA.ID(), "user-1"); !errors.Is(err, repository.ErrMembershipNotFound) {
		t.Errorf("FindMembership() after revoke error = %v, want %v", err, repository.ErrMembershipNotFound)
	}

	if err := f.memberships.RevokeTenantMembership(ctx, f.tenantA.ID(), "user-1"); !errors.Is(err, repository.ErrMembershipNotFound) {
		t.Errorf("second RevokeTenantMembership() error = %v, want %v", err, repository.ErrMembershipNotFound)
	}
}

func TestEventRoles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.memberships.AddTenantMember(ctx, f.tenantA.ID(), "user-1", domain.RoleStaff); err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	membership, err := f.memberships.GrantEventRole(ctx, f.eventA.ID(), "user-1", domain.RoleStaff)
	if err != nil {
		t.Fatalf("GrantEventRole() error = %v", err)
	}

	if got := membership.EventRoles(); len(got) != 1 || got[0].EventID() != f.eventA.ID() || got[0].EventPublicID() != f.eventA.PublicID() || got[0].Role() != domain.RoleStaff {
		t.Errorf("EventRoles() = %#v, want staff on %q", got, f.eventA.PublicID())
	}

	// Granting again replaces the role instead of failing.
	membership, err = f.memberships.GrantEventRole(ctx, f.eventA.ID(), "user-1", domain.RoleOwner)
	if err != nil {
		t.Fatalf("GrantEventRole(replace) error = %v", err)
	}

	if got := membership.EventRoles(); len(got) != 1 || got[0].Role() != domain.RoleOwner {
		t.Errorf("EventRoles() after replace = %#v, want owner", got)
	}

	// event-role ⇒ tenant-role: user-1 does not belong to tenant B, so a role
	// on tenant B's event is refused. The same check covers event ∉ tenant.
	if _, err := f.memberships.GrantEventRole(ctx, f.eventB.ID(), "user-1", domain.RoleStaff); !errors.Is(err, repository.ErrTenantMembershipRequired) {
		t.Errorf("GrantEventRole(event of another tenant) error = %v, want %v", err, repository.ErrTenantMembershipRequired)
	}

	if _, err := f.memberships.GrantEventRole(ctx, "00000000-0000-0000-0000-000000000099", "user-1", domain.RoleStaff); !errors.Is(err, repository.ErrEventNotFound) {
		t.Errorf("GrantEventRole(unknown event) error = %v, want %v", err, repository.ErrEventNotFound)
	}

	if _, err := f.memberships.GrantEventRole(ctx, f.eventA.ID(), "user-1", domain.RoleAdmin); !errors.Is(err, domain.ErrRoleReserved) {
		t.Errorf("GrantEventRole(admin) error = %v, want %v", err, domain.ErrRoleReserved)
	}

	if err := f.memberships.RevokeEventRole(ctx, f.eventA.ID(), "user-1"); err != nil {
		t.Fatalf("RevokeEventRole() error = %v", err)
	}

	if err := f.memberships.RevokeEventRole(ctx, f.eventA.ID(), "user-1"); !errors.Is(err, repository.ErrEventRoleNotFound) {
		t.Errorf("second RevokeEventRole() error = %v, want %v", err, repository.ErrEventRoleNotFound)
	}

	// Revoking the tenant membership removes the event roles with it.
	if _, err := f.memberships.GrantEventRole(ctx, f.eventA.ID(), "user-1", domain.RoleStaff); err != nil {
		t.Fatalf("GrantEventRole() error = %v", err)
	}

	if err := f.memberships.RevokeTenantMembership(ctx, f.tenantA.ID(), "user-1"); err != nil {
		t.Fatalf("RevokeTenantMembership() error = %v", err)
	}

	if err := f.memberships.RevokeEventRole(ctx, f.eventA.ID(), "user-1"); !errors.Is(err, repository.ErrEventRoleNotFound) {
		t.Errorf("RevokeEventRole() after membership revoke error = %v, want %v", err, repository.ErrEventRoleNotFound)
	}
}

func TestListMemberships(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	seed := []struct {
		tenantID string
		userID   string
		role     domain.Role
	}{
		{f.tenantA.ID(), "user-1", domain.RoleOwner},
		{f.tenantA.ID(), "user-2", domain.RoleStaff},
		{f.tenantB.ID(), "user-1", domain.RoleStaff},
	}
	for _, s := range seed {
		if _, err := f.memberships.AddTenantMember(ctx, s.tenantID, s.userID, s.role); err != nil {
			t.Fatalf("AddTenantMember(%s) error = %v", s.userID, err)
		}
	}

	if _, err := f.memberships.GrantEventRole(ctx, f.eventA.ID(), "user-2", domain.RoleStaff); err != nil {
		t.Fatalf("GrantEventRole() error = %v", err)
	}

	byTenant, err := f.memberships.ListMembershipsByTenant(ctx, f.tenantA.ID())
	if err != nil {
		t.Fatalf("ListMembershipsByTenant() error = %v", err)
	}

	if len(byTenant) != 2 || byTenant[0].UserID() != "user-1" || byTenant[1].UserID() != "user-2" {
		t.Fatalf("ListMembershipsByTenant() = %#v, want user-1 and user-2", byTenant)
	}

	if got := byTenant[1].EventRoles(); len(got) != 1 || got[0].EventPublicID() != f.eventA.PublicID() {
		t.Errorf("user-2 event roles = %#v, want staff on %q", got, f.eventA.PublicID())
	}

	if got := byTenant[0].EventRoles(); len(got) != 0 {
		t.Errorf("user-1 event roles = %#v, want none", got)
	}

	byUser, err := f.memberships.ListMembershipsByUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListMembershipsByUser() error = %v", err)
	}

	if len(byUser) != 2 || byUser[0].TenantPublicID() != f.tenantA.PublicID() || byUser[1].TenantPublicID() != f.tenantB.PublicID() {
		t.Errorf("ListMembershipsByUser() = %#v, want memberships of both tenants", byUser)
	}

	empty, err := f.memberships.ListMembershipsByUser(ctx, "nobody")
	if err != nil || len(empty) != 0 {
		t.Errorf("ListMembershipsByUser(nobody) = %#v, %v, want empty", empty, err)
	}

	// Under a tenant context, memberships of other tenants are never handed
	// back, even by a query that is not scoped by tenant.
	foreignCtx := tenantctx.WithTenantPublicID(ctx, f.tenantA.PublicID())
	if _, err := f.memberships.ListMembershipsByUser(foreignCtx, "user-1"); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("ListMembershipsByUser(foreign context) error = %v, want %v", err, tenantctx.ErrMismatch)
	}

	if _, err := f.memberships.ListMembershipsByTenant(foreignCtx, f.tenantA.ID()); err != nil {
		t.Errorf("ListMembershipsByTenant(own context) error = %v, want nil", err)
	}
}

func TestAddOwnerJoinsTheCallerTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	errAbort := errors.New("abort")

	err := f.tenants.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := f.memberships.AddOwner(ctx, f.tenantA.ID(), "user-1"); err != nil {
			return err
		}

		if _, err := f.memberships.FindMembership(ctx, f.tenantA.ID(), "user-1"); err != nil {
			return err
		}

		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("WithinTransaction() error = %v, want %v", err, errAbort)
	}

	// The membership was written inside the rolled-back transaction.
	if _, err := f.memberships.FindMembership(ctx, f.tenantA.ID(), "user-1"); !errors.Is(err, repository.ErrMembershipNotFound) {
		t.Errorf("FindMembership() after rollback error = %v, want %v", err, repository.ErrMembershipNotFound)
	}

	if err := f.memberships.AddOwner(ctx, f.tenantA.ID(), "user-1"); err != nil {
		t.Fatalf("AddOwner() error = %v", err)
	}

	membership, err := f.memberships.FindMembership(ctx, f.tenantA.ID(), "user-1")
	if err != nil {
		t.Fatalf("FindMembership() error = %v", err)
	}

	if got, want := membership.TenantRole(), domain.RoleOwner; got != want {
		t.Errorf("TenantRole() = %v, want %v", got, want)
	}
}

func TestFindTenantRoleForShare(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.memberships.AddTenantMember(ctx, f.tenantA.ID(), "user-1", domain.RoleOwner); err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	role, err := f.memberships.FindTenantRoleForShare(ctx, f.tenantA.ID(), "user-1")
	if err != nil || role != domain.RoleOwner {
		t.Fatalf("FindTenantRoleForShare() = %v, %v, want %v", role, err, domain.RoleOwner)
	}

	if _, err := f.memberships.FindTenantRoleForShare(ctx, f.tenantA.ID(), "user-9"); !errors.Is(err, repository.ErrMembershipNotFound) {
		t.Errorf("FindTenantRoleForShare(unknown member) error = %v, want %v", err, repository.ErrMembershipNotFound)
	}

	// The membership is looked up in the named tenant only.
	if _, err := f.memberships.FindTenantRoleForShare(ctx, f.tenantB.ID(), "user-1"); !errors.Is(err, repository.ErrMembershipNotFound) {
		t.Errorf("FindTenantRoleForShare(other tenant) error = %v, want %v", err, repository.ErrMembershipNotFound)
	}
}

func TestFindTenantRoleForShareJoinsTheCallerTransaction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	errAbort := errors.New("abort")

	if _, err := f.memberships.AddTenantMember(ctx, f.tenantA.ID(), "user-1", domain.RoleOwner); err != nil {
		t.Fatalf("AddTenantMember() error = %v", err)
	}

	err := f.memberships.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := f.memberships.ChangeTenantRole(ctx, f.tenantA.ID(), "user-1", domain.RoleStaff); err != nil {
			return err
		}

		// The lookup runs in the same transaction, so it sees the change.
		role, err := f.memberships.FindTenantRoleForShare(ctx, f.tenantA.ID(), "user-1")
		if err != nil {
			return err
		}

		if role != domain.RoleStaff {
			t.Errorf("FindTenantRoleForShare() inside transaction = %v, want %v", role, domain.RoleStaff)
		}

		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("WithinTransaction() error = %v, want %v", err, errAbort)
	}

	role, err := f.memberships.FindTenantRoleForShare(ctx, f.tenantA.ID(), "user-1")
	if err != nil || role != domain.RoleOwner {
		t.Errorf("FindTenantRoleForShare() after rollback = %v, %v, want %v", role, err, domain.RoleOwner)
	}
}

// lockWaitTimeout is the generous bound on how long a lock that is expected to
// be granted may take; it only guards against a test hanging forever.
const lockWaitTimeout = 10 * time.Second

// heldLock is a transaction holding a tenant's advisory lock: it closes held
// once it has the lock, keeps it until release is closed, and reports the
// outcome of its transaction on done.
type heldLock struct {
	held    chan struct{}
	release chan struct{}
	done    chan error
}

// takeLock starts a transaction that takes the tenant's advisory lock and
// holds it until the returned heldLock is released.
func takeLock(memberships *PostgresMembershipRepository, tenantID string) heldLock {
	lock := heldLock{held: make(chan struct{}), release: make(chan struct{}), done: make(chan error, 1)}

	go func() {
		lock.done <- memberships.WithinTransaction(context.Background(), func(ctx context.Context) error {
			if err := memberships.LockTenantMemberships(ctx, tenantID); err != nil {
				return err
			}

			close(lock.held)
			<-lock.release

			return nil
		})
	}()

	return lock
}

// awaitHeld fails the test unless the lock is granted within lockWaitTimeout.
func (l heldLock) awaitHeld(t *testing.T, name string) {
	t.Helper()

	select {
	case <-l.held:
	case err := <-l.done:
		t.Fatalf("%s ended before it held the lock: %v", name, err)
	case <-time.After(lockWaitTimeout):
		t.Fatalf("%s did not obtain the lock", name)
	}
}

// finish releases the lock and fails the test unless the transaction commits.
func (l heldLock) finish(t *testing.T, name string) {
	t.Helper()

	close(l.release)

	if err := <-l.done; err != nil {
		t.Fatalf("%s error = %v", name, err)
	}
}

func TestLockTenantMembershipsSerializesWritesPerTenant(t *testing.T) {
	f := newFixture(t)

	first := takeLock(f.memberships, f.tenantA.ID())
	first.awaitHeld(t, "first transaction")

	// Another tenant's writes are not held up.
	other := takeLock(f.memberships, f.tenantB.ID())
	other.awaitHeld(t, "transaction of the other tenant")
	other.finish(t, "transaction of the other tenant")

	// The same tenant's next write waits for the first transaction to end.
	second := takeLock(f.memberships, f.tenantA.ID())

	select {
	case <-second.held:
		t.Fatal("the tenant's lock was granted twice at the same time")
	case err := <-second.done:
		t.Fatalf("second transaction ended early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	first.finish(t, "first transaction")
	second.awaitHeld(t, "second transaction")
	second.finish(t, "second transaction")
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
