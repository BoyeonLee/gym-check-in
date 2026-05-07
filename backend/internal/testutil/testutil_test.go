//go:build integration

package testutil_test

import (
	"context"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

func TestSetupDB_AppliesMigrations(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// All ten domain tables should exist after SetupDB.
	for _, table := range []string{
		"branches", "admins", "members", "memberships", "membership_events",
		"check_ins", "payments", "revoked_refresh_tokens", "admin_audit_logs",
		"idempotency_keys",
	} {
		var n int
		err := pool.QueryRow(ctx, "select count(*) from "+table).Scan(&n)
		if err != nil {
			t.Fatalf("table %q not queryable: %v", table, err)
		}
	}
}

func TestFactories_CreateRows(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	branchID := testutil.CreateBranch(t, pool, nil)
	if branchID == 0 {
		t.Fatalf("CreateBranch returned 0")
	}

	adminID, plain := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})
	if adminID == 0 || plain == "" {
		t.Fatalf("CreateAdmin returned %d %q", adminID, plain)
	}

	memberID := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branchID})
	if memberID == 0 {
		t.Fatalf("CreateMember returned 0")
	}

	mID := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: memberID})
	if mID == 0 {
		t.Fatalf("CreateMembership returned 0")
	}

	// TruncateAll should wipe everything.
	testutil.TruncateAll(t, pool)
	for _, table := range []string{"branches", "admins", "members", "memberships"} {
		var n int
		err := pool.QueryRow(ctx, "select count(*) from "+table).Scan(&n)
		if err != nil {
			t.Fatalf("count %q: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("table %q should be empty after TruncateAll, got %d", table, n)
		}
	}
}

func TestFreezeTime(t *testing.T) {
	want := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	testutil.FreezeTime(t, want)

	got := testutil.SystemClock.Now()
	if !got.Equal(want) {
		t.Fatalf("FreezeTime: want %s got %s", want, got)
	}
}
