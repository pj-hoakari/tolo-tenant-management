package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// The write reached no row even though the tenant is present and not archived,
// so the cause is unknown. The messages name no identifier: an error message
// carries no internal primary key, tenant name, or user ID
// (tenant_management_spec.md「エラー」). The transport logs the cause
// server-side and answers the client with a fixed message.
var (
	errCreateEventNoRow = errors.New("create event: no row inserted")
	errUpdateEventNoRow = errors.New("update event: no row updated")
)

func (r *PostgresTenantRepository) CreateEvent(ctx context.Context, event domain.Event) error {
	result, err := r.executor(ctx).ExecContext(ctx, `
		INSERT INTO events (id, public_id, tenant_id, tenant_public_id, name, event_type, status)
		SELECT $1, $2, $3, $4, $5, $6, $7
		FROM tenants
		WHERE id = $3 AND archived = FALSE`,
		event.ID(), event.PublicID(), event.TenantID(), event.TenantPublicID(), event.Name(), event.Type().String(), event.Status().String())
	if err != nil {
		return mapEventConstraintError(err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get created event row count: %w", err)
	}

	if rows != 0 {
		return nil
	}

	tenant, err := r.FindTenantByID(ctx, event.TenantID())
	if err != nil {
		return err
	}

	if tenant.Archived() {
		return repository.ErrTenantArchived
	}

	return errCreateEventNoRow
}

func (r *PostgresTenantRepository) FindEventByPublicID(ctx context.Context, publicID string) (domain.Event, error) {
	return r.findEvent(ctx, `
		SELECT id, public_id, tenant_id, tenant_public_id, name, event_type, status
		FROM events WHERE public_id = $1`, publicID)
}

func (r *PostgresTenantRepository) findEventByID(ctx context.Context, eventID string) (domain.Event, error) {
	return r.findEvent(ctx, `
		SELECT id, public_id, tenant_id, tenant_public_id, name, event_type, status
		FROM events WHERE id = $1`, eventID)
}

func (r *PostgresTenantRepository) findEvent(ctx context.Context, query, value string) (domain.Event, error) {
	var row eventRow
	if err := sqlx.GetContext(ctx, r.executor(ctx), &row, query, value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Event{}, repository.ErrEventNotFound
		}

		return domain.Event{}, err
	}

	return row.domain(ctx)
}

func (r *PostgresTenantRepository) ListEventsByTenantID(ctx context.Context, tenantID string, filter repository.ListEventsFilter) ([]domain.Event, error) {
	// A filter that asks for no limit of its own still gets the cap, so that
	// no caller can trigger an unbounded scan of a tenant's events.
	limit := filter.Limit
	if limit <= 0 {
		limit = repository.MaxListEvents
	}

	// Events carry a UUIDv7 primary key, so ordering by id is creation order.
	var rows []eventRow
	if err := sqlx.SelectContext(ctx, r.executor(ctx), &rows, `
		SELECT id, public_id, tenant_id, tenant_public_id, name, event_type, status
		FROM events WHERE tenant_id = $1 AND ($2 OR status <> $3) ORDER BY id LIMIT $4`,
		tenantID, filter.IncludeArchived, domain.EventStatusArchived.String(), limit); err != nil {
		return nil, err
	}

	events := make([]domain.Event, len(rows))
	for i, row := range rows {
		event, err := row.domain(ctx)
		if err != nil {
			return nil, err
		}

		events[i] = event
	}

	return events, nil
}

func (r *PostgresTenantRepository) UpdateEvent(ctx context.Context, event domain.Event) error {
	result, err := r.executor(ctx).ExecContext(ctx, `
		UPDATE events AS e
		SET name = $2, event_type = $3, status = $4
		FROM tenants AS t
		WHERE e.id = $1 AND e.tenant_id = t.id AND t.archived = FALSE`,
		event.ID(), event.Name(), event.Type().String(), event.Status().String())
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get updated event row count: %w", err)
	}

	if rows != 0 {
		return nil
	}

	storedEvent, err := r.findEventByID(ctx, event.ID())
	if err != nil {
		return err
	}

	tenant, err := r.FindTenantByID(ctx, storedEvent.TenantID())
	if err != nil {
		return err
	}

	if tenant.Archived() {
		return repository.ErrTenantArchived
	}

	return errUpdateEventNoRow
}

// FindObservationSettingsByEventPublicID reads the observation settings of one
// event. The tenant's public ID is selected alongside them so that the
// ownership check below has something to verify, but it never leaves this
// method: the caller receives the settings alone.
func (r *PostgresTenantRepository) FindObservationSettingsByEventPublicID(ctx context.Context, publicID string) (domain.ObservationSettings, error) {
	var row observationSettingsRow
	if err := sqlx.GetContext(ctx, r.executor(ctx), &row, `
		SELECT tenant_public_id, history_window_days
		FROM events WHERE public_id = $1`, publicID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ObservationSettings{}, repository.ErrEventNotFound
		}

		return domain.ObservationSettings{}, err
	}

	return row.domain(ctx)
}

func (r *PostgresTenantRepository) UpdateObservationSettings(ctx context.Context, eventID string, settings domain.ObservationSettings) error {
	result, err := r.executor(ctx).ExecContext(ctx, `
		UPDATE events AS e
		SET history_window_days = $2
		FROM tenants AS t
		WHERE e.id = $1 AND e.tenant_id = t.id AND t.archived = FALSE`,
		eventID, settings.HistoryWindowDays())
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get updated observation settings row count: %w", err)
	}

	if rows != 0 {
		return nil
	}

	// The update matched nothing: either the event is gone or its tenant is
	// archived. Distinguish the two the way UpdateEvent does.
	storedEvent, err := r.findEventByID(ctx, eventID)
	if err != nil {
		return err
	}

	tenant, err := r.FindTenantByID(ctx, storedEvent.TenantID())
	if err != nil {
		return err
	}

	if tenant.Archived() {
		return repository.ErrTenantArchived
	}

	return fmt.Errorf("update observation settings of event %q: no row updated", eventID)
}

type observationSettingsRow struct {
	TenantPublicID    string `db:"tenant_public_id"`
	HistoryWindowDays int    `db:"history_window_days"`
}

func (r observationSettingsRow) domain(ctx context.Context) (domain.ObservationSettings, error) {
	// Defense in depth: never return the settings of an event that belongs to
	// a tenant other than the authenticated tenant carried in the context.
	if err := tenantctx.VerifyOwnership(ctx, r.TenantPublicID); err != nil {
		return domain.ObservationSettings{}, err
	}

	return domain.NewObservationSettings(r.HistoryWindowDays)
}

type eventRow struct {
	ID             string `db:"id"`
	PublicID       string `db:"public_id"`
	TenantID       string `db:"tenant_id"`
	TenantPublicID string `db:"tenant_public_id"`
	Name           string `db:"name"`
	EventType      string `db:"event_type"`
	Status         string `db:"status"`
}

func (r eventRow) domain(ctx context.Context) (domain.Event, error) {
	eventType, err := domain.ParseEventType(r.EventType)
	if err != nil {
		return domain.Event{}, fmt.Errorf("parse event type: %w", err)
	}

	status, err := domain.ParseEventStatus(r.Status)
	if err != nil {
		return domain.Event{}, fmt.Errorf("parse event status: %w", err)
	}

	// Defense in depth: never return an event that belongs to a tenant other
	// than the authenticated tenant carried in the context.
	if err := tenantctx.VerifyOwnership(ctx, r.TenantPublicID); err != nil {
		return domain.Event{}, err
	}

	return domain.NewEvent(r.ID, r.PublicID, r.TenantID, r.TenantPublicID, r.Name, eventType, status), nil
}

func mapEventConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "events_public_id_key" {
		return repository.ErrEventPublicIDExists
	}

	return err
}
