// Package testutil provides shared helpers for repo/handler integration tests.
// It owns: spinning up a configured pgxpool against TEST_DATABASE_URL,
// applying goose migrations once per process, and per-test isolation
// (TruncateAll). All factory helpers (CreateBranch/Admin/Member/Membership)
// live next to it so that handler tests can compose realistic fixtures
// without writing SQL inline.
package testutil

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

// testIsolationLockKey is a fixed advisory-lock key shared across every
// package's integration tests. `go test ./...` runs packages in parallel
// processes that all hit the same TEST_DATABASE_URL, so TruncateAll calls
// from one package would otherwise wipe rows another package's test just
// inserted. We acquire this PostgreSQL advisory lock on a dedicated
// connection for the lifetime of each SetupDB() call, serializing tests
// across processes. Ordinary `pg_advisory_unlock` releases on cleanup;
// if the test crashes, the session closes and the lock auto-releases.
const testIsolationLockKey int64 = 0x67796D5F636B696E // "gym_ckin"

// SetupDB returns a connected *pgxpool.Pool against TEST_DATABASE_URL.
// On first invocation in a process it applies db/migrations via goose
// (using the embedded database/sql driver) so tests do not need an
// external `goose` binary. Each call truncates all tables for isolation.
//
// If TEST_DATABASE_URL is not set, the test FAILS rather than skips.
// Skipping was the original behavior but it produced silent false-PASS
// under harness execution — a child Claude that ran `go test
// -tags=integration ./...` without sourcing .env would see `[no tests
// run]` and report success while every integration assertion was
// quietly bypassed. Hard-fail forces the operator (or supervisor) to
// fix the env before claiming a pass.
//
// Per-test cross-process serialization: SetupDB holds a PostgreSQL
// advisory lock on a dedicated connection from t's start through
// t.Cleanup. While the lock is held, no other test in any process may
// proceed past its own SetupDB() — this is the only sane way to prevent
// `go test ./... -tags=integration` from flake-deleting data.
func SetupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL not set; integration tests require it. Source .env (e.g. `set -a; source .env; set +a`) before running `go test -tags=integration`.")
	}

	migrateOnce.Do(func() {
		migrateErr = applyMigrations(dsn)
	})
	if migrateErr != nil {
		t.Fatalf("testutil: migrations failed: %v", migrateErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	pool, err := repo.NewPool(ctx, dsn)
	if err != nil {
		cancel()
		t.Fatalf("testutil: NewPool: %v", err)
	}

	// Acquire one connection and hold it for the test's lifetime so
	// pg_advisory_lock survives until cleanup. If the lock acquisition
	// blocks past the ctx deadline, treat it as a fatal test setup error.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		cancel()
		pool.Close()
		t.Fatalf("testutil: acquire conn for advisory lock: %v", err)
	}
	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", testIsolationLockKey); err != nil {
		conn.Release()
		cancel()
		pool.Close()
		t.Fatalf("testutil: pg_advisory_lock: %v", err)
	}
	cancel()

	t.Cleanup(func() {
		// Best-effort unlock. Failing to unlock is non-fatal: closing
		// the connection ends the session and PostgreSQL drops the lock.
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = conn.Exec(releaseCtx, "select pg_advisory_unlock($1)", testIsolationLockKey)
		releaseCancel()
		conn.Release()
		pool.Close()
	})

	TruncateAll(t, pool)
	return pool
}

// migrationsDir returns the absolute path of db/migrations relative to this
// source file's location. We rely on runtime.Caller so it works regardless
// of the directory tests are invoked from.
func migrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = backend/internal/testutil/db.go → ../../../db/migrations
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "db", "migrations"))
}

func applyMigrations(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, migrationsDir())
}

// TruncateAll empties every domain table and resets bigserial sequences,
// guaranteeing per-test isolation. CASCADE handles FKs so order doesn't matter.
func TruncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// goose_db_version is intentionally excluded — wiping it would force
	// a re-migration cycle on the next SetupDB call.
	const stmt = `truncate table
		idempotency_keys,
		admin_audit_logs,
		revoked_refresh_tokens,
		membership_events,
		payments,
		check_ins,
		memberships,
		members,
		admins,
		branches
	restart identity cascade`

	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("testutil: truncate: %v", err)
	}
}
