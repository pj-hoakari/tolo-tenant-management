package db

import (
	"context"

	"github.com/jmoiron/sqlx"

	infradb "github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
)

// WithinTransaction runs fn inside one database transaction of the pool the
// repository holds. See infradb.RunInTransaction.
func (r *PostgresTenantRepository) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return infradb.RunInTransaction(ctx, r.db, fn)
}

func (r *PostgresTenantRepository) executor(ctx context.Context) sqlx.ExtContext {
	return infradb.Executor(ctx, r.db)
}
