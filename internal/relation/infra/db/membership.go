// Package db contains the PostgreSQL implementation of the relation model's
// persistence contracts. It shares the connection pool and the context-bound
// transaction with the tenant context's repository, so an owner membership and
// the ownership transition commit together.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// advisoryLockClassTenantMemberships namespaces this use of PostgreSQL
// advisory locks. Advisory locks share one process-wide space, so the first of
// the two keys names the use (serializing the membership writes of a tenant)
// and the second the tenant; another use of advisory locks picks another
// class and can never collide with this one.
const advisoryLockClassTenantMemberships int32 = 1

// PostgresMembershipRepository persists memberships and roles in PostgreSQL.
type PostgresMembershipRepository struct {
	db *sqlx.DB
}

// NewPostgresMembershipRepository creates a PostgreSQL-backed membership
// repository on the pool shared with the tenant repository.
func NewPostgresMembershipRepository(db *sqlx.DB) *PostgresMembershipRepository {
	return &PostgresMembershipRepository{db: db}
}

func (r *PostgresMembershipRepository) executor(ctx context.Context) sqlx.ExtContext {
	return infradb.Executor(ctx, r.db)
}

// WithinTransaction runs fn inside one database transaction of the pool the
// repository shares with the tenant repository, so that a permission check and
// the membership write it guards commit together. See
// infradb.RunInTransaction.
func (r *PostgresMembershipRepository) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return infradb.RunInTransaction(ctx, r.db, fn)
}

// AddOwner implements the tenant side's MembershipWriter port: the claiming
// user becomes the owner of the tenant inside the caller's transaction.
func (r *PostgresMembershipRepository) AddOwner(ctx context.Context, tenantID, userID string) error {
	_, err := r.AddTenantMember(ctx, tenantID, userID, domain.RoleOwner)

	return err
}

func (r *PostgresMembershipRepository) AddTenantMember(ctx context.Context, tenantID, userID string, role domain.Role) (domain.Membership, error) {
	if err := role.Grantable(); err != nil {
		return domain.Membership{}, err
	}

	_, err := r.executor(ctx).ExecContext(ctx, `
		INSERT INTO tenant_memberships (tenant_id, user_id, tenant_role)
		VALUES ($1, $2, $3)`,
		tenantID, userID, role.String())
	if err != nil {
		return domain.Membership{}, mapMembershipConstraintError(err)
	}

	return r.FindMembership(ctx, tenantID, userID)
}

func (r *PostgresMembershipRepository) ChangeTenantRole(ctx context.Context, tenantID, userID string, role domain.Role) (domain.Membership, error) {
	if err := role.Grantable(); err != nil {
		return domain.Membership{}, err
	}

	result, err := r.executor(ctx).ExecContext(ctx, `
		UPDATE tenant_memberships SET tenant_role = $3
		WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID, role.String())
	if err != nil {
		return domain.Membership{}, fmt.Errorf("change tenant role: %w", err)
	}

	if err := expectOneRow(result, repository.ErrMembershipNotFound); err != nil {
		return domain.Membership{}, err
	}

	return r.FindMembership(ctx, tenantID, userID)
}

func (r *PostgresMembershipRepository) GrantEventRole(ctx context.Context, eventID, userID string, role domain.Role) (domain.Membership, error) {
	if err := role.Grantable(); err != nil {
		return domain.Membership{}, err
	}

	// The event's tenant is taken from the event itself (event ∈ tenant) and
	// the membership key must exist for that tenant (event-role ⇒ tenant-role);
	// both are enforced by the foreign keys of event_roles.
	var tenantID string

	err := r.executor(ctx).QueryRowxContext(ctx, `
		INSERT INTO event_roles (event_id, tenant_id, user_id, role)
		SELECT e.id, e.tenant_id, $2, $3 FROM events AS e WHERE e.id = $1
		ON CONFLICT (event_id, user_id) DO UPDATE SET role = EXCLUDED.role
		RETURNING tenant_id`,
		eventID, userID, role.String()).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Membership{}, repository.ErrEventNotFound
		}

		return domain.Membership{}, mapMembershipConstraintError(err)
	}

	return r.FindMembership(ctx, tenantID, userID)
}

func (r *PostgresMembershipRepository) RevokeTenantMembership(ctx context.Context, tenantID, userID string) error {
	// Event roles in the tenant are removed by the cascade of the membership
	// foreign key.
	result, err := r.executor(ctx).ExecContext(ctx, `
		DELETE FROM tenant_memberships WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID)
	if err != nil {
		return fmt.Errorf("revoke tenant membership: %w", err)
	}

	return expectOneRow(result, repository.ErrMembershipNotFound)
}

func (r *PostgresMembershipRepository) RevokeEventRole(ctx context.Context, eventID, userID string) error {
	result, err := r.executor(ctx).ExecContext(ctx, `
		DELETE FROM event_roles WHERE event_id = $1 AND user_id = $2`,
		eventID, userID)
	if err != nil {
		return fmt.Errorf("revoke event role: %w", err)
	}

	return expectOneRow(result, repository.ErrEventRoleNotFound)
}

func (r *PostgresMembershipRepository) FindMembership(ctx context.Context, tenantID, userID string) (domain.Membership, error) {
	memberships, err := r.listMemberships(ctx, `WHERE m.tenant_id = $1 AND m.user_id = $2`, tenantID, userID)
	if err != nil {
		return domain.Membership{}, err
	}

	if len(memberships) == 0 {
		return domain.Membership{}, repository.ErrMembershipNotFound
	}

	return memberships[0], nil
}

// LockTenantMemberships serializes the membership writes of one tenant. The
// advisory lock is held until the surrounding transaction ends, so two
// administrators of the same tenant queue instead of locking each other's
// membership rows in opposite orders and deadlocking. The tenant ID is hashed
// into the lock's second key; two tenants whose hashes collide are only
// serialized against each other, which is harmless.
func (r *PostgresMembershipRepository) LockTenantMemberships(ctx context.Context, tenantID string) error {
	_, err := r.executor(ctx).ExecContext(ctx, `
		SELECT pg_advisory_xact_lock($1, hashtext($2))`,
		advisoryLockClassTenantMemberships, tenantID)
	if err != nil {
		return fmt.Errorf("lock tenant memberships: %w", err)
	}

	return nil
}

// FindTenantRoleForShare loads the tenant role of userID and holds a share
// lock on the membership row until the surrounding transaction ends, so a
// concurrent revoke or role change cannot slip in between the check and the
// write it guards.
func (r *PostgresMembershipRepository) FindTenantRoleForShare(ctx context.Context, tenantID, userID string) (domain.Role, error) {
	var role string

	err := r.executor(ctx).QueryRowxContext(ctx, `
		SELECT tenant_role FROM tenant_memberships
		WHERE tenant_id = $1 AND user_id = $2
		FOR SHARE`,
		tenantID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.RoleUnspecified, repository.ErrMembershipNotFound
		}

		return domain.RoleUnspecified, fmt.Errorf("find tenant role: %w", err)
	}

	parsed, err := domain.ParseRole(role)
	if err != nil {
		return domain.RoleUnspecified, fmt.Errorf("parse tenant role: %w", err)
	}

	return parsed, nil
}

func (r *PostgresMembershipRepository) ListMembershipsByTenant(ctx context.Context, tenantID string) ([]domain.Membership, error) {
	return r.listMemberships(ctx, `WHERE m.tenant_id = $1`, tenantID)
}

func (r *PostgresMembershipRepository) ListMembershipsByUser(ctx context.Context, userID string) ([]domain.Membership, error) {
	return r.listMemberships(ctx, `WHERE m.user_id = $1`, userID)
}

type membershipRow struct {
	TenantID       string `db:"tenant_id"`
	TenantPublicID string `db:"tenant_public_id"`
	UserID         string `db:"user_id"`
	TenantRole     string `db:"tenant_role"`
}

type eventRoleRow struct {
	TenantID      string `db:"tenant_id"`
	UserID        string `db:"user_id"`
	EventID       string `db:"event_id"`
	EventPublicID string `db:"event_public_id"`
	Role          string `db:"role"`
}

type membershipKey struct {
	tenantID string
	userID   string
}

// listMemberships loads memberships matching the filter together with their
// event roles. The filter is applied to both queries through the alias m.
func (r *PostgresMembershipRepository) listMemberships(ctx context.Context, filter string, args ...any) ([]domain.Membership, error) {
	var rows []membershipRow
	if err := sqlx.SelectContext(ctx, r.executor(ctx), &rows, `
		SELECT m.tenant_id, t.public_id AS tenant_public_id, m.user_id, m.tenant_role
		FROM tenant_memberships AS m
		JOIN tenants AS t ON t.id = m.tenant_id
		`+filter+`
		ORDER BY m.tenant_id, m.created_at, m.user_id`, args...); err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}

	if len(rows) == 0 {
		return []domain.Membership{}, nil
	}

	var roleRows []eventRoleRow
	if err := sqlx.SelectContext(ctx, r.executor(ctx), &roleRows, `
		SELECT m.tenant_id, m.user_id, r.event_id, e.public_id AS event_public_id, r.role
		FROM event_roles AS r
		JOIN tenant_memberships AS m ON m.tenant_id = r.tenant_id AND m.user_id = r.user_id
		JOIN events AS e ON e.id = r.event_id
		`+filter+`
		ORDER BY r.event_id`, args...); err != nil {
		return nil, fmt.Errorf("list event roles: %w", err)
	}

	eventRoles := make(map[membershipKey][]domain.EventRole, len(rows))

	for _, row := range roleRows {
		role, err := domain.ParseRole(row.Role)
		if err != nil {
			return nil, fmt.Errorf("parse event role: %w", err)
		}

		key := membershipKey{tenantID: row.TenantID, userID: row.UserID}
		eventRoles[key] = append(eventRoles[key], domain.NewEventRole(row.EventID, row.EventPublicID, role))
	}

	memberships := make([]domain.Membership, 0, len(rows))

	for _, row := range rows {
		// Defense in depth: never return a membership of a tenant other than
		// the authenticated tenant carried in the context.
		if err := tenantctx.VerifyOwnership(ctx, row.TenantPublicID); err != nil {
			return nil, err
		}

		role, err := domain.ParseRole(row.TenantRole)
		if err != nil {
			return nil, fmt.Errorf("parse tenant role: %w", err)
		}

		key := membershipKey{tenantID: row.TenantID, userID: row.UserID}
		memberships = append(memberships, domain.NewMembership(row.UserID, row.TenantID, row.TenantPublicID, role, eventRoles[key]))
	}

	return memberships, nil
}

func expectOneRow(result sql.Result, notFound error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get affected row count: %w", err)
	}

	if rows == 0 {
		return notFound
	}

	return nil
}

func mapMembershipConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.ConstraintName {
	case "tenant_memberships_pkey":
		return repository.ErrMembershipAlreadyExists
	case "tenant_memberships_tenant_id_fkey":
		return repository.ErrTenantNotFound
	case "event_roles_membership_fkey":
		return repository.ErrTenantMembershipRequired
	case "event_roles_event_fkey":
		return repository.ErrEventNotFound
	default:
		return err
	}
}
