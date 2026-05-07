//go:build integration

package repo_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

// requires TEST_DATABASE_URL.
func TestNewPool_AppliesUTC(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := repo.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	var tz string
	if err := pool.QueryRow(ctx, "show timezone").Scan(&tz); err != nil {
		t.Fatalf("show timezone: %v", err)
	}
	if tz != "UTC" {
		t.Fatalf("session timezone should be UTC, got %q", tz)
	}
}

func TestNewPool_BadDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := repo.NewPool(ctx, "not-a-valid-dsn"); err == nil {
		t.Fatalf("expected error for bad dsn")
	}
}
