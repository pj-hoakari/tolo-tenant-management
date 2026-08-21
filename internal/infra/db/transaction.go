package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

// SQLSTATEs with which PostgreSQL reports that it aborted the transaction
// itself rather than the statement failing on its own terms.
const (
	sqlStateSerializationFailure = "40001"
	sqlStateDeadlockDetected     = "40P01"
)

// ErrTransactionAborted means PostgreSQL aborted the transaction because of a
// deadlock or a serialization failure. No work of the transaction was kept and
// nothing is wrong with the request itself, so the operation can be retried.
// It is joined to the error that failed, which stays available to errors.Is
// and errors.As.
var ErrTransactionAborted = errors.New("transaction aborted; retry")

type transactionKey struct{}

// RunInTransaction runs fn inside one database transaction on pool. The
// transaction travels in the context handed to fn, so every repository call
// made with that context (including calls on other repositories sharing the
// same *sqlx.DB) joins it. fn returning an error rolls the transaction back;
// otherwise it is committed. A call made from inside a transaction simply
// joins it. Repositories of every model expose it as their Transactor port.
func RunInTransaction(ctx context.Context, pool *sqlx.DB, fn func(context.Context) error) error {
	if _, ok := transactionFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := pool.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(context.WithValue(ctx, transactionKey{}, tx)); err != nil {
		if isTransactionAbort(err) {
			err = errors.Join(err, ErrTransactionAborted)
		}

		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
		}

		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// WithinTransaction runs fn inside one database transaction of the pool the
// repository holds. See RunInTransaction.
func (r *PostgresTenantRepository) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return RunInTransaction(ctx, r.db, fn)
}

// isTransactionAbort reports whether err carries a PostgreSQL error with which
// the server aborted the transaction, so that the caller can be told to retry
// instead of being handed an opaque failure.
func isTransactionAbort(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == sqlStateSerializationFailure || pgErr.Code == sqlStateDeadlockDetected
}

func transactionFromContext(ctx context.Context) (*sqlx.Tx, bool) {
	tx, ok := ctx.Value(transactionKey{}).(*sqlx.Tx)

	return tx, ok
}

// Executor returns the transaction carried by ctx, or pool when the call is
// not part of a transaction. Repositories of other models that share the pool
// use it to join a transaction opened by WithinTransaction.
func Executor(ctx context.Context, pool *sqlx.DB) sqlx.ExtContext {
	if tx, ok := transactionFromContext(ctx); ok {
		return tx
	}

	return pool
}

func (r *PostgresTenantRepository) executor(ctx context.Context) sqlx.ExtContext {
	return Executor(ctx, r.db)
}
