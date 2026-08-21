package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

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
		postgres.WithInitScripts(migrationPath()),
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

	event2 := newEvent("00000000-0000-0000-0000-000000000012", "event-002", tenant, "Festival 2", domain.EventTypeLongTerm, domain.EventStatusOpen)

	event1 := newEvent("00000000-0000-0000-0000-000000000011", "event-001", tenant, "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft)
	for _, event := range []domain.Event{event2, event1} {
		if err := repository.CreateEvent(ctx, event); err != nil {
			t.Fatalf("CreateEvent(%q) error = %v", event.ID(), err)
		}
	}

	got, err := repository.FindEventByPublicID(ctx, event1.PublicID())
	if err != nil {
		t.Fatalf("FindEventByPublicID() error = %v", err)
	}

	if got != event1 {
		t.Errorf("found event = %#v, want %#v", got, event1)
	}

	events, err := repository.ListEventsByTenantID(ctx, tenant.ID())
	if err != nil {
		t.Fatalf("ListEventsByTenantID() error = %v", err)
	}

	if want := []domain.Event{event1, event2}; !slices.Equal(events, want) {
		t.Errorf("ListEventsByTenantID() = %#v, want %#v", events, want)
	}

	updated := event1.AssignType(domain.EventTypeLongTerm)

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

	missingTenant := domain.NewTenant("00000000-0000-0000-0000-000000000099", "tenant-099", "", "", false)
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

	if _, err := repository.ListEventsByTenantID(foreignCtx, tenantB.ID()); !errors.Is(err, tenantctx.ErrMismatch) {
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

	if _, err := testDB.Exec(`TRUNCATE events, tenants`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}

	return NewPostgresTenantRepository(testDB)
}

func newTenant(id, publicID, name string, archived bool) domain.Tenant {
	return domain.NewTenant(id, publicID, name, "standard", archived)
}

func newEvent(id, publicID string, tenant domain.Tenant, name string, eventType domain.EventType, status domain.EventStatus) domain.Event {
	return domain.NewEvent(id, publicID, tenant.ID(), tenant.PublicID(), name, eventType, status)
}

func migrationPath() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("locate test source file")
	}

	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "migrations", "000001_init.up.sql")
}
