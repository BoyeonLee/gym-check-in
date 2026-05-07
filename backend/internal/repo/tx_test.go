//go:build integration

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

func TestWithTx_Commits(t *testing.T) {
	pool := testutil.SetupDB(t)

	calls := 0
	err := repo.WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		calls++
		_, err := tx.Exec(context.Background(),
			`insert into branches (name, address) values ($1, $2)`,
			"tx-commit", "tx-commit-addr")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx commit: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls: want 1, got %d", calls)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from branches where name=$1`, "tx-commit").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected committed row, got count=%d", n)
	}
}

func TestWithTx_RollsBackOnError(t *testing.T) {
	pool := testutil.SetupDB(t)

	wantErr := errors.New("boom")
	err := repo.WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		_, _ = tx.Exec(context.Background(),
			`insert into branches (name, address) values ($1, $2)`,
			"rollme", "rollme-addr")
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err: want wantErr, got %v", err)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from branches where name=$1`, "rollme").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected rollback, got count=%d", n)
	}
}

// TestWithTx_RetriesOnSerializationFailure simulates a 40001 by failing the
// first invocation manually. We don't need a real serialization conflict —
// the retry policy keys solely on the SQLSTATE code.
func TestWithTx_RetriesOnSerializationFailure(t *testing.T) {
	pool := testutil.SetupDB(t)

	calls := 0
	err := repo.WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		calls++
		if calls == 1 {
			return &pgconn.PgError{Code: "40001", Message: "simulated serialization_failure"}
		}
		_, err := tx.Exec(context.Background(),
			`insert into branches (name, address) values ($1, $2)`,
			"retry-ok", "retry-ok-addr")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls: want 2 (retried once), got %d", calls)
	}
}

func TestWithTx_RetriesOnDeadlock(t *testing.T) {
	pool := testutil.SetupDB(t)

	calls := 0
	err := repo.WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		calls++
		if calls < 2 {
			return &pgconn.PgError{Code: "40P01", Message: "simulated deadlock_detected"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx deadlock retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls: want 2, got %d", calls)
	}
}

func TestWithTx_GivesUpAfter3Attempts(t *testing.T) {
	pool := testutil.SetupDB(t)

	calls := 0
	err := repo.WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		calls++
		return &pgconn.PgError{Code: "40001", Message: "permanent serialization_failure"}
	})
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}
	if calls != 3 {
		t.Fatalf("calls: want 3 attempts, got %d", calls)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
		t.Fatalf("expected wrapped 40001 PgError, got %v", err)
	}
}

func TestWithTx_NonRetryableErrorReturnsImmediately(t *testing.T) {
	pool := testutil.SetupDB(t)

	calls := 0
	err := repo.WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		calls++
		return &pgconn.PgError{Code: "23505", Message: "duplicate"}
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if calls != 1 {
		t.Fatalf("non-retryable error must not retry; got %d calls", calls)
	}
}

func TestWithTx_ContextCancellationStops(t *testing.T) {
	pool := testutil.SetupDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before starting

	calls := 0
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		calls++
		return nil
	})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
	if calls != 0 {
		t.Fatalf("fn must not run when ctx cancelled, got %d calls", calls)
	}
}
