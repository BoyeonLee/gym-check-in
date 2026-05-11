package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// retryBackoffs is the schedule between attempts on transient errors,
// matching backend/CLAUDE.md (50ms, 100ms, 200ms). With these gaps a
// short serialization or deadlock conflict resolves invisibly to the
// caller; sustained contention surfaces as the original PgError after
// three total attempts.
var retryBackoffs = []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}

// WithTx runs fn inside a Read Committed transaction with automatic
// retry on PostgreSQL serialization_failure (40001) and deadlock_detected
// (40P01). On commit failure the transaction is rolled back.
//
// Behavior:
//   - Up to 3 total attempts, with the backoffs above between them.
//   - Non-transient errors (e.g. unique violations) are returned on the
//     first attempt without retry.
//   - Context cancellation is honoured before each attempt and during the
//     backoff sleep.
//
// IMPORTANT: fn must be idempotent. Because the helper may invoke fn
// multiple times, any non-DB side effects (HTTP calls, file writes,
// publishing messages) would execute repeatedly. Keep fn DB-only.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < len(retryBackoffs); attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := runOnce(ctx, pool, fn)
		if err == nil {
			return nil
		}
		lastErr = err

		if !isTransient(err) {
			return err
		}
		// Sleep before the next attempt — abort early if the caller's
		// context is cancelled while we wait.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryBackoffs[attempt]):
		}
	}
	return lastErr
}

// runOnce executes fn inside a single transaction. The tx is rolled back
// if fn returns an error or commit fails.
func runOnce(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			// Use Background ctx so rollback can run even after caller cancel.
			_ = tx.Rollback(context.Background())
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// isTransient reports whether err is a PostgreSQL transient conflict that
// the caller should retry: serialization_failure or deadlock_detected.
func isTransient(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}
