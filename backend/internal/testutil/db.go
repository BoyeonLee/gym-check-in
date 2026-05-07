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

// SetupDB returns a connected *pgxpool.Pool against TEST_DATABASE_URL.
// On first invocation in a process it applies db/migrations via goose
// (using the embedded database/sql driver) so tests do not need an
// external `goose` binary. Each call truncates all tables for isolation.
//
// If TEST_DATABASE_URL is not set, the test is skipped.
func SetupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}

	migrateOnce.Do(func() {
		migrateErr = applyMigrations(dsn)
	})
	if migrateErr != nil {
		t.Fatalf("testutil: migrations failed: %v", migrateErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := repo.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("testutil: NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

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
