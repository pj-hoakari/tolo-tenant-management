package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type transactionKey struct{}

// WithinTransaction runs fn inside one database transaction. The transaction
// travels in the context handed to fn, so every repository call made with that
// context (including calls on other repositories sharing the same *sqlx.DB)
// joins it. fn returning an error rolls the transaction back; otherwise it is
// committed. A call made from inside a transaction simply joins it.
func (r *PostgresTenantRepository) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := transactionFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(context.WithValue(ctx, transactionKey{}, tx)); err != nil {
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

func transactionFromContext(ctx context.Context) (*sqlx.Tx, bool) {
	tx, ok := ctx.Value(transactionKey{}).(*sqlx.Tx)

	return tx, ok
}

// executor returns the transaction carried by ctx, or the connection pool when
// the call is not part of a transaction.
func (r *PostgresTenantRepository) executor(ctx context.Context) sqlx.ExtContext {
	if tx, ok := transactionFromContext(ctx); ok {
		return tx
	}

	return r.db
}
