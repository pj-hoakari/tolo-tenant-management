// Package db contains PostgreSQL-backed infrastructure implementations.
package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// PostgresTenantRepository persists tenants and their events in PostgreSQL.
type PostgresTenantRepository struct {
	db *sqlx.DB
}

// NewPostgresTenantRepository creates a PostgreSQL-backed tenant repository.
func NewPostgresTenantRepository(db *sqlx.DB) *PostgresTenantRepository {
	return &PostgresTenantRepository{db: db}
}

func (r *PostgresTenantRepository) CreateTenant(ctx context.Context, tenant domain.Tenant) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tenants (id, public_id, name, contract_plan, archived)
		VALUES ($1, $2, $3, $4, $5)`,
		tenant.ID(), tenant.PublicID(), tenant.Name(), tenant.ContractPlan(), tenant.Archived())
	if err != nil {
		return mapTenantConstraintError(err)
	}

	return nil
}

func (r *PostgresTenantRepository) FindTenantByID(ctx context.Context, tenantID string) (domain.Tenant, error) {
	return r.findTenant(ctx, `SELECT id, public_id, name, contract_plan, archived FROM tenants WHERE id = $1`, tenantID)
}

func (r *PostgresTenantRepository) FindTenantByPublicID(ctx context.Context, publicID string) (domain.Tenant, error) {
	return r.findTenant(ctx, `SELECT id, public_id, name, contract_plan, archived FROM tenants WHERE public_id = $1`, publicID)
}

type tenantRow struct {
	ID           string `db:"id"`
	PublicID     string `db:"public_id"`
	Name         string `db:"name"`
	ContractPlan string `db:"contract_plan"`
	Archived     bool   `db:"archived"`
}

func (r *PostgresTenantRepository) findTenant(ctx context.Context, query, value string) (domain.Tenant, error) {
	var row tenantRow
	if err := r.db.GetContext(ctx, &row, query, value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Tenant{}, repository.ErrTenantNotFound
		}

		return domain.Tenant{}, err
	}

	// Defense in depth: never return a tenant that differs from the
	// authenticated tenant carried in the context.
	if err := tenantctx.VerifyOwnership(ctx, row.PublicID); err != nil {
		return domain.Tenant{}, err
	}

	return domain.NewTenant(row.ID, row.PublicID, row.Name, row.ContractPlan, row.Archived), nil
}

func mapTenantConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}

	switch pgErr.ConstraintName {
	case "tenants_name_key":
		return repository.ErrTenantNameAlreadyExists
	case "tenants_public_id_key":
		return repository.ErrTenantPublicIDExists
	default:
		return err
	}
}
