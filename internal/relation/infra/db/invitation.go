package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"

	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
)

type PostgresInvitationRepository struct {
	db *sqlx.DB
}

func NewPostgresInvitationRepository(db *sqlx.DB) *PostgresInvitationRepository {
	return &PostgresInvitationRepository{db: db}
}

func (r *PostgresInvitationRepository) executor(ctx context.Context) sqlx.ExtContext {
	return infradb.Executor(ctx, r.db)
}

func (r *PostgresInvitationRepository) CreateInvitation(ctx context.Context, invitation domain.Invitation, tokenHash onetimetoken.Hash) error {
	_, err := r.executor(ctx).ExecContext(ctx, `
		INSERT INTO tenant_invitations (id, public_id, tenant_id, tenant_role, display_name, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		invitation.ID(), invitation.PublicID(), invitation.TenantID(), invitation.Role().String(),
		invitation.DisplayName().String(), tokenHash[:], invitation.ExpiresAt())
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "tenant_invitations_public_id_key" {
			return repository.ErrInvitationPublicIDExists
		}

		return fmt.Errorf("create invitation: %w", err)
	}

	return nil
}

func (r *PostgresInvitationRepository) ListOpenInvitations(ctx context.Context, tenantID string, now time.Time) ([]domain.Invitation, error) {
	var rows []invitationRow
	if err := sqlx.SelectContext(ctx, r.executor(ctx), &rows, invitationSelect+`
		WHERE i.tenant_id = $1 AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > $2
		ORDER BY i.created_at, i.id`, tenantID, now); err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}

	invitations := make([]domain.Invitation, 0, len(rows))

	for _, row := range rows {
		invitation, err := row.domain()
		if err != nil {
			return nil, err
		}

		invitations = append(invitations, invitation)
	}

	return invitations, nil
}

func (r *PostgresInvitationRepository) FindInvitationForUpdate(ctx context.Context, tenantID, publicID string) (domain.Invitation, error) {
	return r.findInvitation(ctx, `WHERE i.tenant_id = $1 AND i.public_id = $2`, tenantID, publicID)
}

func (r *PostgresInvitationRepository) FindInvitationByTokenForUpdate(ctx context.Context, tenantID string, tokenHash onetimetoken.Hash) (domain.Invitation, error) {
	return r.findInvitation(ctx, `WHERE i.tenant_id = $1 AND i.token_hash = $2`, tenantID, tokenHash[:])
}

func (r *PostgresInvitationRepository) MarkInvitationAccepted(ctx context.Context, invitationID, userID string, at time.Time) error {
	result, err := r.executor(ctx).ExecContext(ctx, `
		UPDATE tenant_invitations SET accepted_at = $2, accepted_by = $3
		WHERE id = $1`,
		invitationID, at, userID)
	if err != nil {
		return fmt.Errorf("mark invitation accepted: %w", err)
	}

	return expectOneRow(result, repository.ErrInvitationNotFound)
}

func (r *PostgresInvitationRepository) MarkInvitationRevoked(ctx context.Context, invitationID string, at time.Time) error {
	result, err := r.executor(ctx).ExecContext(ctx, `
		UPDATE tenant_invitations SET revoked_at = $2
		WHERE id = $1`,
		invitationID, at)
	if err != nil {
		return fmt.Errorf("mark invitation revoked: %w", err)
	}

	return expectOneRow(result, repository.ErrInvitationNotFound)
}

const invitationSelect = `
	SELECT i.id, i.public_id, i.tenant_id, t.public_id AS tenant_public_id, i.tenant_role, i.display_name,
		i.expires_at, i.accepted_at IS NOT NULL AS accepted, i.revoked_at IS NOT NULL AS revoked
	FROM tenant_invitations AS i
	JOIN tenants AS t ON t.id = i.tenant_id
	`

func (r *PostgresInvitationRepository) findInvitation(ctx context.Context, filter string, args ...any) (domain.Invitation, error) {
	var row invitationRow
	if err := sqlx.GetContext(ctx, r.executor(ctx), &row, invitationSelect+filter+` FOR UPDATE OF i`, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Invitation{}, repository.ErrInvitationNotFound
		}

		return domain.Invitation{}, fmt.Errorf("find invitation: %w", err)
	}

	return row.domain()
}

type invitationRow struct {
	ID             string    `db:"id"`
	PublicID       string    `db:"public_id"`
	TenantID       string    `db:"tenant_id"`
	TenantPublicID string    `db:"tenant_public_id"`
	TenantRole     string    `db:"tenant_role"`
	DisplayName    string    `db:"display_name"`
	ExpiresAt      time.Time `db:"expires_at"`
	Accepted       bool      `db:"accepted"`
	Revoked        bool      `db:"revoked"`
}

func (r invitationRow) domain() (domain.Invitation, error) {
	role, err := domain.ParseRole(r.TenantRole)
	if err != nil {
		return domain.Invitation{}, fmt.Errorf("parse invitation role: %w", err)
	}

	displayName, err := domain.ParseDisplayName(r.DisplayName)
	if err != nil {
		return domain.Invitation{}, fmt.Errorf("parse invitation display name: %w", err)
	}

	state := domain.InvitationPending

	switch {
	case r.Accepted:
		state = domain.InvitationAccepted
	case r.Revoked:
		state = domain.InvitationRevoked
	}

	return domain.NewInvitation(r.ID, r.PublicID, r.TenantID, r.TenantPublicID, role, displayName, r.ExpiresAt, state), nil
}
