package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

func (r *PostgresTenantRepository) CreateEvent(ctx context.Context, event domain.Event) error {
	result, err := r.db.ExecContext(ctx, `
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

	return fmt.Errorf("create event for tenant %q: no row inserted", event.TenantID())
}

func (r *PostgresTenantRepository) FindEventByID(ctx context.Context, eventID string) (domain.Event, error) {
	var row eventRow
	if err := r.db.GetContext(ctx, &row, `
		SELECT id, public_id, tenant_id, tenant_public_id, name, event_type, status
		FROM events WHERE id = $1`, eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Event{}, repository.ErrEventNotFound
		}

		return domain.Event{}, err
	}

	return row.domain()
}

func (r *PostgresTenantRepository) ListEventsByTenantID(ctx context.Context, tenantID string) ([]domain.Event, error) {
	var rows []eventRow
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT id, public_id, tenant_id, tenant_public_id, name, event_type, status
		FROM events WHERE tenant_id = $1 ORDER BY id`, tenantID); err != nil {
		return nil, err
	}

	events := make([]domain.Event, len(rows))
	for i, row := range rows {
		event, err := row.domain()
		if err != nil {
			return nil, err
		}

		events[i] = event
	}

	return events, nil
}

func (r *PostgresTenantRepository) UpdateEvent(ctx context.Context, event domain.Event) error {
	result, err := r.db.ExecContext(ctx, `
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

	storedEvent, err := r.FindEventByID(ctx, event.ID())
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

	return fmt.Errorf("update event %q: no row updated", event.ID())
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

func (r eventRow) domain() (domain.Event, error) {
	eventType, err := domain.ParseEventType(r.EventType)
	if err != nil {
		return domain.Event{}, fmt.Errorf("parse event %q type: %w", r.ID, err)
	}

	status, err := domain.ParseEventStatus(r.Status)
	if err != nil {
		return domain.Event{}, fmt.Errorf("parse event %q status: %w", r.ID, err)
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
