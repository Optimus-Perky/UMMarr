package store

import (
	"context"
	"database/sql"
	"fmt"
)

// WithTx runs fn inside a transaction, committing on nil return and
// rolling back otherwise - keeps sql.Tx mechanics out of callers like the
// sync package, so a failure partway through a multi-step sync (e.g.
// season 3 of 5 failing to fetch) leaves nothing partially written.
func WithTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if already committed

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
