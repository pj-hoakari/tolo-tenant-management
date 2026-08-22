package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	repositorypkg "github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
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
		// Open is the same instrumented entry point the server uses, so the
		// repository tests also cover the OpenTelemetry driver wrapper.
		testDB, err = Open(ctx, databaseURL)
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

func TestPostgresTenantRepositoryTenants(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()
	tenant := newTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Acme", false)

	if err := repository.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}

	for _, find := range []struct {
		name string
		call func() (domain.Tenant, error)
	}{
		{"by ID", func() (domain.Tenant, error) { return repository.FindTenantByID(ctx, tenant.ID()) }},
		{"by public ID", func() (domain.Tenant, error) { return repository.FindTenantByPublicID(ctx, tenant.PublicID()) }},
	} {
		t.Run(find.name, func(t *testing.T) {
			got, err := find.call()
			if err != nil {
				t.Fatalf("find tenant: %v", err)
			}

			if got != tenant {
				t.Errorf("found tenant = %#v, want %#v", got, tenant)
			}
		})
	}

	if _, err := repository.FindTenantByPublicID(ctx, "tenant-999"); !errors.Is(err, repositorypkg.ErrTenantNotFound) {
		t.Errorf("FindTenantByPublicID() error = %v, want %v", err, repositorypkg.ErrTenantNotFound)
	}
}

func TestPostgresTenantRepositoryTenantErrors(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()

	tenant := newTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Acme", false)
	if err := repository.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}

	if err := repository.CreateTenant(ctx, newTenant("00000000-0000-0000-0000-000000000002", "tenant-002", tenant.Name(), false)); !errors.Is(err, repositorypkg.ErrTenantNameAlreadyExists) {
		t.Errorf("duplicate name error = %v, want %v", err, repositorypkg.ErrTenantNameAlreadyExists)
	}

	if err := repository.CreateTenant(ctx, newTenant("00000000-0000-0000-0000-000000000003", tenant.PublicID(), "Globex", false)); !errors.Is(err, repositorypkg.ErrTenantPublicIDExists) {
		t.Errorf("duplicate public ID error = %v, want %v", err, repositorypkg.ErrTenantPublicIDExists)
	}

	if _, err := repository.FindTenantByID(ctx, "00000000-0000-0000-0000-000000000099"); !errors.Is(err, repositorypkg.ErrTenantNotFound) {
		t.Errorf("FindTenantByID() error = %v, want %v", err, repositorypkg.ErrTenantNotFound)
	}
}

func TestPostgresTenantRepositoryEvents(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()

	tenant := newTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Acme", false)
	if err := repository.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}

	// The archived event sits between the other two by key, so a limited
	// listing that includes archived events differs from the default one.
	archivedEvent := newEvent("00000000-0000-0000-0000-000000000012", "event-002", tenant, "Festival 2", domain.EventTypeLongTerm, domain.EventStatusArchived)
	openEvent := newEvent("00000000-0000-0000-0000-000000000013", "event-003", tenant, "Festival 3", domain.EventTypeLongTerm, domain.EventStatusOpen)

	draftEvent := newEvent("00000000-0000-0000-0000-000000000011", "event-001", tenant, "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft)
	for _, event := range []domain.Event{openEvent, archivedEvent, draftEvent} {
		if err := repository.CreateEvent(ctx, event); err != nil {
			t.Fatalf("CreateEvent(%q) error = %v", event.ID(), err)
		}
	}

	got, err := repository.FindEventByPublicID(ctx, draftEvent.PublicID())
	if err != nil {
		t.Fatalf("FindEventByPublicID() error = %v", err)
	}

	if got != draftEvent {
		t.Errorf("found event = %#v, want %#v", got, draftEvent)
	}

	// The listing is ordered by primary key byte order, not by the order the
	// rows were inserted in. These fixtures use hand-written UUIDs; production
	// keys are UUIDv7, which makes that order the creation order.
	listTests := []struct {
		name   string
		filter repositorypkg.ListEventsFilter
		want   []domain.Event
	}{
		{name: "default", filter: repositorypkg.ListEventsFilter{}, want: []domain.Event{draftEvent, openEvent}},
		{name: "include archived", filter: repositorypkg.ListEventsFilter{IncludeArchived: true}, want: []domain.Event{draftEvent, archivedEvent, openEvent}},
		{name: "limit", filter: repositorypkg.ListEventsFilter{Limit: 1}, want: []domain.Event{draftEvent}},
		{name: "limit within an archived listing", filter: repositorypkg.ListEventsFilter{IncludeArchived: true, Limit: 2}, want: []domain.Event{draftEvent, archivedEvent}},
		{name: "non-positive limit falls back to the cap", filter: repositorypkg.ListEventsFilter{Limit: -1}, want: []domain.Event{draftEvent, openEvent}},
	}

	for _, tt := range listTests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := repository.ListEventsByTenantID(ctx, tenant.ID(), tt.filter)
			if err != nil {
				t.Fatalf("ListEventsByTenantID() error = %v", err)
			}

			if !slices.Equal(events, tt.want) {
				t.Errorf("ListEventsByTenantID() = %#v, want %#v", events, tt.want)
			}
		})
	}

	updated := draftEvent.AssignType(domain.EventTypeLongTerm)

	updated, err = updated.TransitionTo(domain.EventStatusOpen)
	if err != nil {
		t.Fatalf("TransitionTo() error = %v", err)
	}

	if err := repository.UpdateEvent(ctx, updated); err != nil {
		t.Fatalf("UpdateEvent() error = %v", err)
	}

	got, err = repository.FindEventByPublicID(ctx, updated.PublicID())
	if err != nil {
		t.Fatalf("FindEventByPublicID() after update error = %v", err)
	}

	if got != updated {
		t.Errorf("updated event = %#v, want %#v", got, updated)
	}
}

func TestPostgresTenantRepositoryEventErrors(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()
	activeTenant := newTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Acme", false)

	archivedTenant := newTenant("00000000-0000-0000-0000-000000000002", "tenant-002", "Initech", true)
	for _, tenant := range []domain.Tenant{activeTenant, archivedTenant} {
		if err := repository.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%q) error = %v", tenant.ID(), err)
		}
	}

	event := newEvent("00000000-0000-0000-0000-000000000011", "event-001", activeTenant, "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	if err := repository.CreateEvent(ctx, event); err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	if err := repository.CreateEvent(ctx, newEvent("00000000-0000-0000-0000-000000000012", event.PublicID(), activeTenant, "Other festival", domain.EventTypeShortTerm, domain.EventStatusDraft)); !errors.Is(err, repositorypkg.ErrEventPublicIDExists) {
		t.Errorf("duplicate event public ID error = %v, want %v", err, repositorypkg.ErrEventPublicIDExists)
	}

	if err := repository.CreateEvent(ctx, newEvent("00000000-0000-0000-0000-000000000013", "event-003", archivedTenant, "Archived festival", domain.EventTypeShortTerm, domain.EventStatusDraft)); !errors.Is(err, repositorypkg.ErrTenantArchived) {
		t.Errorf("archived tenant create error = %v, want %v", err, repositorypkg.ErrTenantArchived)
	}

	missingTenant := domain.NewTenant("00000000-0000-0000-0000-000000000099", "tenant-099", "", "", domain.TenantOwnershipStateOwned, false)
	if err := repository.CreateEvent(ctx, newEvent("00000000-0000-0000-0000-000000000014", "event-004", missingTenant, "Missing tenant festival", domain.EventTypeShortTerm, domain.EventStatusDraft)); !errors.Is(err, repositorypkg.ErrTenantNotFound) {
		t.Errorf("missing tenant create error = %v, want %v", err, repositorypkg.ErrTenantNotFound)
	}

	if _, err := repository.FindEventByPublicID(ctx, "event-099"); !errors.Is(err, repositorypkg.ErrEventNotFound) {
		t.Errorf("FindEventByPublicID() error = %v, want %v", err, repositorypkg.ErrEventNotFound)
	}

	missingEvent := newEvent("00000000-0000-0000-0000-000000000099", "event-099", activeTenant, "Missing festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	if err := repository.UpdateEvent(ctx, missingEvent); !errors.Is(err, repositorypkg.ErrEventNotFound) {
		t.Errorf("missing event update error = %v, want %v", err, repositorypkg.ErrEventNotFound)
	}

	archivedEvent := newEvent("00000000-0000-0000-0000-000000000015", "event-005", archivedTenant, "Archived event", domain.EventTypeShortTerm, domain.EventStatusDraft)
	if _, err := testDB.ExecContext(ctx, `INSERT INTO events (id, public_id, tenant_id, tenant_public_id, name, event_type, status) VALUES ($1, $2, $3, $4, $5, $6, $7)`, archivedEvent.ID(), archivedEvent.PublicID(), archivedEvent.TenantID(), archivedEvent.TenantPublicID(), archivedEvent.Name(), archivedEvent.Type().String(), archivedEvent.Status().String()); err != nil {
		t.Fatalf("insert archived event fixture: %v", err)
	}

	if err := repository.UpdateEvent(ctx, archivedEvent.AssignType(domain.EventTypeLongTerm)); !errors.Is(err, repositorypkg.ErrTenantArchived) {
		t.Errorf("archived tenant update error = %v, want %v", err, repositorypkg.ErrTenantArchived)
	}

	// A row the repository cannot parse is reported without naming the event.
	brokenEvent := newEvent("00000000-0000-0000-0000-000000000016", "event-006", activeTenant, "Broken festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	if _, err := testDB.ExecContext(ctx, `INSERT INTO events (id, public_id, tenant_id, tenant_public_id, name, event_type, status) VALUES ($1, $2, $3, $4, $5, $6, $7)`, brokenEvent.ID(), brokenEvent.PublicID(), brokenEvent.TenantID(), brokenEvent.TenantPublicID(), brokenEvent.Name(), "not-an-event-type", brokenEvent.Status().String()); err != nil {
		t.Fatalf("insert broken event fixture: %v", err)
	}

	_, parseErr := repository.FindEventByPublicID(ctx, brokenEvent.PublicID())
	if parseErr == nil {
		t.Fatalf("FindEventByPublicID(unparsable row) error = nil, want a parse error")
	}

	assertNoInternalIdentifier(t, parseErr, brokenEvent.ID(), activeTenant.ID(), activeTenant.Name())
}

// assertNoInternalIdentifier fails when the error message carries one of the
// given internal identifiers. Repository errors travel to the transport, so
// they must not name internal primary keys, tenant names, or user IDs
// (tenant_management_spec.md「エラー」).
func assertNoInternalIdentifier(t *testing.T, err error, identifiers ...string) {
	t.Helper()

	if err == nil {
		return
	}

	for _, identifier := range identifiers {
		if identifier == "" {
			continue
		}

		if strings.Contains(err.Error(), identifier) {
			t.Errorf("error = %q, want it to omit %q", err, identifier)
		}
	}
}

func TestPostgresTenantRepositoryRejectsForeignTenantOnReconstitution(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()

	tenantA := newTenant("00000000-0000-0000-0000-0000000000a1", "tenant-aaa", "Acme", false)
	tenantB := newTenant("00000000-0000-0000-0000-0000000000b1", "tenant-bbb", "Beta", false)

	for _, tenant := range []domain.Tenant{tenantA, tenantB} {
		if err := repository.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%q) error = %v", tenant.ID(), err)
		}
	}

	eventB := newEvent("00000000-0000-0000-0000-0000000000b2", "event-bbb", tenantB, "Beta Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	if err := repository.CreateEvent(ctx, eventB); err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	// The caller is authenticated as tenant A, so loading any of tenant B's
	// records must be rejected even though the queries themselves succeed. This
	// is the safety net for a repository query that fails to scope by tenant.
	foreignCtx := tenantctx.WithTenantPublicID(ctx, tenantA.PublicID())

	if _, err := repository.FindTenantByID(foreignCtx, tenantB.ID()); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("FindTenantByID(foreign) error = %v, want %v", err, tenantctx.ErrMismatch)
	}

	if _, err := repository.FindTenantByPublicID(foreignCtx, tenantB.PublicID()); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("FindTenantByPublicID(foreign) error = %v, want %v", err, tenantctx.ErrMismatch)
	}

	if _, err := repository.FindEventByPublicID(foreignCtx, eventB.PublicID()); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("FindEventByPublicID(foreign) error = %v, want %v", err, tenantctx.ErrMismatch)
	}

	if _, err := repository.ListEventsByTenantID(foreignCtx, tenantB.ID(), repositorypkg.ListEventsFilter{}); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("ListEventsByTenantID(foreign) error = %v, want %v", err, tenantctx.ErrMismatch)
	}

	// The caller may still load its own tenant's records.
	ownCtx := tenantctx.WithTenantPublicID(ctx, tenantB.PublicID())
	if _, err := repository.FindEventByPublicID(ownCtx, eventB.PublicID()); err != nil {
		t.Errorf("FindEventByPublicID(own) error = %v, want nil", err)
	}

	// A context without an authenticated tenant (e.g. service-token reads) is
	// left unrestricted.
	if _, err := repository.FindTenantByID(ctx, tenantB.ID()); err != nil {
		t.Errorf("FindTenantByID(no tenant context) error = %v, want nil", err)
	}
}

func newTestRepository(t *testing.T) *PostgresTenantRepository {
	t.Helper()

	if _, err := testDB.Exec(`TRUNCATE events, tenants CASCADE`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}

	return NewPostgresTenantRepository(testDB)
}

func newTenant(id, publicID, name string, archived bool) domain.Tenant {
	return domain.NewTenant(id, publicID, name, "standard", domain.TenantOwnershipStateOwned, archived)
}

func newEvent(id, publicID string, tenant domain.Tenant, name string, eventType domain.EventType, status domain.EventStatus) domain.Event {
	return domain.NewEvent(id, publicID, tenant.ID(), tenant.PublicID(), name, eventType, status)
}

func migrationPaths() []string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("locate test source file")
	}

	paths, err := filepath.Glob(filepath.Join(filepath.Dir(filename), "..", "..", "..", "migrations", "*.up.sql"))
	if err != nil || len(paths) == 0 {
		panic("locate migration files")
	}

	sort.Strings(paths)

	return paths
}

func TestPostgresTenantRepositoryWithinTransaction(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()
	errAbort := errors.New("abort")

	rolledBack := newTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Rolled Back", false)

	err := repository.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := repository.CreateTenant(ctx, rolledBack); err != nil {
			return err
		}

		// Inside the transaction the row is visible to the same connection.
		if _, err := repository.FindTenantByID(ctx, rolledBack.ID()); err != nil {
			return err
		}

		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("WithinTransaction() error = %v, want %v", err, errAbort)
	}

	if _, err := repository.FindTenantByID(ctx, rolledBack.ID()); !errors.Is(err, repositorypkg.ErrTenantNotFound) {
		t.Errorf("FindTenantByID() after rollback error = %v, want %v", err, repositorypkg.ErrTenantNotFound)
	}

	committed := newTenant("00000000-0000-0000-0000-000000000002", "tenant-002", "Committed", false)

	err = repository.WithinTransaction(ctx, func(ctx context.Context) error {
		// A nested call joins the surrounding transaction instead of opening
		// a second one.
		return repository.WithinTransaction(ctx, func(ctx context.Context) error {
			return repository.CreateTenant(ctx, committed)
		})
	})
	if err != nil {
		t.Fatalf("WithinTransaction() error = %v", err)
	}

	if _, err := repository.FindTenantByID(ctx, committed.ID()); err != nil {
		t.Errorf("FindTenantByID() after commit error = %v, want nil", err)
	}
}

func TestIsTransactionAbort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadlock", err: fmt.Errorf("create tenant: %w", &pgconn.PgError{Code: sqlStateDeadlockDetected}), want: true},
		{name: "serialization failure", err: &pgconn.PgError{Code: sqlStateSerializationFailure}, want: true},
		{name: "other PostgreSQL error", err: fmt.Errorf("create tenant: %w", &pgconn.PgError{Code: "23505"}), want: false},
		{name: "plain error", err: errors.New("abort"), want: false},
		{name: "no error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isTransactionAbort(tt.err); got != tt.want {
				t.Errorf("isTransactionAbort(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestRunInTransactionReportsAbort covers the retriable answer of a real
// transaction whose work failed with a deadlock.
func TestRunInTransactionReportsAbort(t *testing.T) {
	ctx := context.Background()
	deadlock := fmt.Errorf("revoke tenant membership: %w", &pgconn.PgError{Code: sqlStateDeadlockDetected})

	err := RunInTransaction(ctx, testDB, func(context.Context) error {
		return deadlock
	})
	if !errors.Is(err, ErrTransactionAborted) || !errors.Is(err, deadlock) {
		t.Errorf("RunInTransaction() error = %v, want it to join %v and the original error", err, ErrTransactionAborted)
	}

	// An ordinary failure is not reported as retriable.
	errAbort := errors.New("abort")

	err = RunInTransaction(ctx, testDB, func(context.Context) error {
		return errAbort
	})
	if !errors.Is(err, errAbort) || errors.Is(err, ErrTransactionAborted) {
		t.Errorf("RunInTransaction() error = %v, want %v alone", err, errAbort)
	}
}

func TestPostgresTenantRepositoryPendingTenants(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	_, hash, err := domain.NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	expired := domain.NewTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Expired", "standard", domain.TenantOwnershipStatePendingOwner, false)
	live := domain.NewTenant("00000000-0000-0000-0000-000000000002", "tenant-002", "Live", "standard", domain.TenantOwnershipStatePendingOwner, false)
	owned := newTenant("00000000-0000-0000-0000-000000000003", "tenant-003", "Owned", false)

	if err := repository.CreatePendingTenant(ctx, expired, domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now.Add(-time.Minute)}); err != nil {
		t.Fatalf("CreatePendingTenant(expired) error = %v", err)
	}

	if err := repository.CreatePendingTenant(ctx, live, domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("CreatePendingTenant(live) error = %v", err)
	}

	if err := repository.CreateTenant(ctx, owned); err != nil {
		t.Fatalf("CreateTenant(owned) error = %v", err)
	}

	got, err := repository.FindTenantByPublicID(ctx, live.PublicID())
	if err != nil {
		t.Fatalf("FindTenantByPublicID(live) error = %v", err)
	}

	if got != live {
		t.Errorf("found tenant = %#v, want %#v", got, live)
	}

	if err := repository.CreatePendingTenant(ctx, owned, domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now}); !errors.Is(err, domain.ErrTenantNotPendingOwner) {
		t.Errorf("CreatePendingTenant(owned tenant) error = %v, want %v", err, domain.ErrTenantNotPendingOwner)
	}

	deleted, err := repository.DeleteExpiredPendingTenants(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpiredPendingTenants() error = %v", err)
	}

	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	if _, err := repository.FindTenantByID(ctx, expired.ID()); !errors.Is(err, repositorypkg.ErrTenantNotFound) {
		t.Errorf("FindTenantByID(expired) error = %v, want %v", err, repositorypkg.ErrTenantNotFound)
	}

	for _, tenant := range []domain.Tenant{live, owned} {
		if _, err := repository.FindTenantByID(ctx, tenant.ID()); err != nil {
			t.Errorf("FindTenantByID(%q) error = %v, want nil", tenant.Name(), err)
		}
	}
}

func TestPostgresTenantRepositoryOwnershipClaim(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	token, hash, err := domain.NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	pending := domain.NewTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Acme", "standard", domain.TenantOwnershipStatePendingOwner, false)
	if err := repository.CreatePendingTenant(ctx, pending, domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now}); err != nil {
		t.Fatalf("CreatePendingTenant() error = %v", err)
	}

	err = repository.WithinTransaction(ctx, func(ctx context.Context) error {
		tenant, claim, err := repository.FindTenantByPublicIDForUpdate(ctx, pending.PublicID())
		if err != nil {
			return err
		}

		if tenant != pending {
			t.Errorf("locked tenant = %#v, want %#v", tenant, pending)
		}

		if !claim.TokenHash.Matches(token) || !claim.ExpiresAt.Equal(now) {
			t.Errorf("locked claim = %#v, want hash of token expiring at %v", claim, now)
		}

		owned, err := tenant.ClaimOwnership()
		if err != nil {
			return err
		}

		return repository.MarkTenantOwned(ctx, owned)
	})
	if err != nil {
		t.Fatalf("claim transaction error = %v", err)
	}

	tenant, claim, err := repository.FindTenantByPublicIDForUpdate(ctx, pending.PublicID())
	if err != nil {
		t.Fatalf("FindTenantByPublicIDForUpdate() after claim error = %v", err)
	}

	if got, want := tenant.OwnershipState(), domain.TenantOwnershipStateOwned; got != want {
		t.Errorf("OwnershipState() after claim = %v, want %v", got, want)
	}

	// The claim token is consumed: the stored claim is cleared.
	if claim != (domain.OwnershipClaim{}) {
		t.Errorf("claim after ownership = %#v, want zero", claim)
	}

	if err := repository.MarkTenantOwned(ctx, tenant); !errors.Is(err, domain.ErrTenantNotPendingOwner) {
		t.Errorf("second MarkTenantOwned() error = %v, want %v", err, domain.ErrTenantNotPendingOwner)
	}

	if _, _, err := repository.FindTenantByPublicIDForUpdate(ctx, "tenant-999"); !errors.Is(err, repositorypkg.ErrTenantNotFound) {
		t.Errorf("FindTenantByPublicIDForUpdate(unknown) error = %v, want %v", err, repositorypkg.ErrTenantNotFound)
	}
}

func TestPostgresTenantRepositoryUpdateTenant(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()

	tenant := newTenant("00000000-0000-0000-0000-000000000001", "tenant-001", "Acme", false)
	if err := repository.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}

	updated, err := tenant.ChangeContractPlan("enterprise").Archive()
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}

	if err := repository.UpdateTenant(ctx, updated); err != nil {
		t.Fatalf("UpdateTenant() error = %v", err)
	}

	got, err := repository.FindTenantByID(ctx, tenant.ID())
	if err != nil {
		t.Fatalf("FindTenantByID() after update error = %v", err)
	}

	if got != updated {
		t.Errorf("updated tenant = %#v, want %#v", got, updated)
	}

	missing := newTenant("00000000-0000-0000-0000-000000000099", "tenant-099", "Missing", false)
	if err := repository.UpdateTenant(ctx, missing); !errors.Is(err, repositorypkg.ErrTenantNotFound) {
		t.Errorf("UpdateTenant(unknown) error = %v, want %v", err, repositorypkg.ErrTenantNotFound)
	}

	// The administrative write never touches the ownership columns, so a
	// pending_owner tenant keeps its state and its unconsumed claim.
	expiresAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	token, hash, err := domain.NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	pending := domain.NewTenant("00000000-0000-0000-0000-000000000002", "tenant-002", "Pending", "standard", domain.TenantOwnershipStatePendingOwner, false)
	if err := repository.CreatePendingTenant(ctx, pending, domain.OwnershipClaim{TokenHash: hash, ExpiresAt: expiresAt}); err != nil {
		t.Fatalf("CreatePendingTenant() error = %v", err)
	}

	if err := repository.UpdateTenant(ctx, pending.ChangeContractPlan("enterprise")); err != nil {
		t.Fatalf("UpdateTenant(pending) error = %v", err)
	}

	storedPending, claim, err := repository.FindTenantByPublicIDForUpdate(ctx, pending.PublicID())
	if err != nil {
		t.Fatalf("FindTenantByPublicIDForUpdate() after update error = %v", err)
	}

	if got, want := storedPending.OwnershipState(), domain.TenantOwnershipStatePendingOwner; got != want {
		t.Errorf("OwnershipState() after update = %v, want %v", got, want)
	}

	if !claim.TokenHash.Matches(token) || !claim.ExpiresAt.Equal(expiresAt) {
		t.Errorf("claim after update = %#v, want hash of the token expiring at %v", claim, expiresAt)
	}
}
