// Package db contains PostgreSQL-backed infrastructure implementations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
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
	_, err := r.executor(ctx).ExecContext(ctx, `
		INSERT INTO tenants (id, public_id, name, contract_plan, ownership_state, archived)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		tenant.ID(), tenant.PublicID(), tenant.Name(), tenant.ContractPlan(), tenant.OwnershipState().String(), tenant.Archived())
	if err != nil {
		return mapTenantConstraintError(err)
	}

	return nil
}

func (r *PostgresTenantRepository) CreatePendingTenant(ctx context.Context, tenant domain.Tenant, claim domain.OwnershipClaim) error {
	if tenant.OwnershipState() != domain.TenantOwnershipStatePendingOwner {
		return fmt.Errorf("create pending tenant: %w", domain.ErrTenantNotPendingOwner)
	}

	_, err := r.executor(ctx).ExecContext(ctx, `
		INSERT INTO tenants (id, public_id, name, contract_plan, ownership_state, ownership_claim_token_hash, ownership_claim_expires_at, archived)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		tenant.ID(), tenant.PublicID(), tenant.Name(), tenant.ContractPlan(), tenant.OwnershipState().String(), claim.TokenHash[:], claim.ExpiresAt, tenant.Archived())
	if err != nil {
		return mapTenantConstraintError(err)
	}

	return nil
}

func (r *PostgresTenantRepository) DeleteExpiredPendingTenants(ctx context.Context, now time.Time) (int64, error) {
	result, err := r.executor(ctx).ExecContext(ctx, `
		DELETE FROM tenants
		WHERE ownership_state = $1 AND ownership_claim_expires_at <= $2`,
		domain.TenantOwnershipStatePendingOwner.String(), now)
	if err != nil {
		return 0, fmt.Errorf("delete expired pending tenants: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get deleted pending tenant row count: %w", err)
	}

	return deleted, nil
}

func (r *PostgresTenantRepository) FindTenantByPublicIDForUpdate(ctx context.Context, publicID string) (domain.Tenant, domain.OwnershipClaim, error) {
	var row tenantClaimRow
	if err := sqlx.GetContext(ctx, r.executor(ctx), &row, `
		SELECT id, public_id, name, contract_plan, ownership_state, archived, ownership_claim_token_hash, ownership_claim_expires_at
		FROM tenants WHERE public_id = $1
		FOR UPDATE`, publicID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Tenant{}, domain.OwnershipClaim{}, repository.ErrTenantNotFound
		}

		return domain.Tenant{}, domain.OwnershipClaim{}, err
	}

	tenant, err := row.domain(ctx)
	if err != nil {
		return domain.Tenant{}, domain.OwnershipClaim{}, err
	}

	return tenant, row.claim(), nil
}

func (r *PostgresTenantRepository) MarkTenantOwned(ctx context.Context, tenant domain.Tenant) error {
	if tenant.OwnershipState() != domain.TenantOwnershipStateOwned {
		return fmt.Errorf("mark tenant owned: %w", domain.ErrTenantNotPendingOwner)
	}

	result, err := r.executor(ctx).ExecContext(ctx, `
		UPDATE tenants
		SET ownership_state = $2, ownership_claim_token_hash = NULL, ownership_claim_expires_at = NULL
		WHERE id = $1 AND ownership_state = $3`,
		tenant.ID(), domain.TenantOwnershipStateOwned.String(), domain.TenantOwnershipStatePendingOwner.String())
	if err != nil {
		return fmt.Errorf("mark tenant owned: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get marked tenant row count: %w", err)
	}

	if rows == 0 {
		return domain.ErrTenantNotPendingOwner
	}

	return nil
}

// UpdateTenant persists the administrative attributes of a tenant: its
// contract plan and whether it is archived. The ownership columns stay
// untouched, so an administrative write can never undo an ownership claim.
func (r *PostgresTenantRepository) UpdateTenant(ctx context.Context, tenant domain.Tenant) error {
	result, err := r.executor(ctx).ExecContext(ctx, `
		UPDATE tenants
		SET contract_plan = $2, archived = $3
		WHERE id = $1`,
		tenant.ID(), tenant.ContractPlan(), tenant.Archived())
	if err != nil {
		return fmt.Errorf("update tenant: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get updated tenant row count: %w", err)
	}

	if rows == 0 {
		return repository.ErrTenantNotFound
	}

	return nil
}

func (r *PostgresTenantRepository) FindTenantByID(ctx context.Context, tenantID string) (domain.Tenant, error) {
	return r.findTenant(ctx, `SELECT id, public_id, name, contract_plan, ownership_state, archived FROM tenants WHERE id = $1`, tenantID)
}

func (r *PostgresTenantRepository) FindTenantByPublicID(ctx context.Context, publicID string) (domain.Tenant, error) {
	return r.findTenant(ctx, `SELECT id, public_id, name, contract_plan, ownership_state, archived FROM tenants WHERE public_id = $1`, publicID)
}

type tenantRow struct {
	ID             string `db:"id"`
	PublicID       string `db:"public_id"`
	Name           string `db:"name"`
	ContractPlan   string `db:"contract_plan"`
	OwnershipState string `db:"ownership_state"`
	Archived       bool   `db:"archived"`
}

type tenantClaimRow struct {
	tenantRow
	ClaimTokenHash []byte       `db:"ownership_claim_token_hash"`
	ClaimExpiresAt sql.NullTime `db:"ownership_claim_expires_at"`
}

// claim reconstitutes the pending ownership claim; it is zero for an owned
// tenant, whose claim columns are NULL.
func (r tenantClaimRow) claim() domain.OwnershipClaim {
	var claim domain.OwnershipClaim
	copy(claim.TokenHash[:], r.ClaimTokenHash)

	if r.ClaimExpiresAt.Valid {
		claim.ExpiresAt = r.ClaimExpiresAt.Time
	}

	return claim
}

func (r tenantRow) domain(ctx context.Context) (domain.Tenant, error) {
	ownershipState, err := domain.ParseTenantOwnershipState(r.OwnershipState)
	if err != nil {
		return domain.Tenant{}, fmt.Errorf("parse tenant ownership state: %w", err)
	}

	// Defense in depth: never return a tenant that differs from the
	// authenticated tenant carried in the context.
	if err := tenantctx.VerifyOwnership(ctx, r.PublicID); err != nil {
		return domain.Tenant{}, err
	}

	return domain.NewTenant(r.ID, r.PublicID, r.Name, r.ContractPlan, ownershipState, r.Archived), nil
}

func (r *PostgresTenantRepository) findTenant(ctx context.Context, query, value string) (domain.Tenant, error) {
	var row tenantRow
	if err := sqlx.GetContext(ctx, r.executor(ctx), &row, query, value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Tenant{}, repository.ErrTenantNotFound
		}

		return domain.Tenant{}, err
	}

	return row.domain(ctx)
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
