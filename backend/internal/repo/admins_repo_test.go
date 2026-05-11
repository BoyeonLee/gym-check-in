//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

func TestFindByUsernameAndID(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	branchID := testutil.CreateBranch(t, pool, nil)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "alice", Role: "branch", BranchID: &branchID,
	})

	row, err := repo.FindByUsername(ctx, pool, "alice")
	if err != nil {
		t.Fatalf("FindByUsername: %v", err)
	}
	if row == nil {
		t.Fatal("FindByUsername returned nil row")
	}
	if row.ID != adminID || row.Username != "alice" || row.Role != "branch" {
		t.Errorf("row drift: %+v", row)
	}
	if row.BranchID == nil || *row.BranchID != branchID {
		t.Errorf("branch_id drift: %+v", row.BranchID)
	}

	got, err := repo.FindByID(ctx, pool, adminID)
	if err != nil || got == nil || got.ID != adminID {
		t.Fatalf("FindByID: %v %+v", err, got)
	}

	if row, err := repo.FindByUsername(ctx, pool, "missing"); err != nil || row != nil {
		t.Fatalf("FindByUsername(missing): expected (nil,nil) got (%+v,%v)", row, err)
	}

	// Soft-delete the admin row → both finders should return nil.
	if _, err := pool.Exec(ctx, "update admins set deleted_at = now() where id=$1", adminID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if row, err := repo.FindByUsername(ctx, pool, "alice"); err != nil || row != nil {
		t.Fatalf("FindByUsername after soft-delete: expected (nil,nil) got (%+v,%v)", row, err)
	}
	if row, err := repo.FindByID(ctx, pool, adminID); err != nil || row != nil {
		t.Fatalf("FindByID after soft-delete: expected (nil,nil) got (%+v,%v)", row, err)
	}
}

func TestGetForAccessCheck(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	chk, err := repo.GetForAccessCheck(ctx, pool, adminID)
	if err != nil {
		t.Fatalf("GetForAccessCheck: %v", err)
	}
	if !chk.Exists {
		t.Fatalf("expected admin to exist")
	}
	if chk.PasswordUpdatedAt != nil {
		t.Fatalf("password_updated_at should be NULL for fresh admin, got %v", chk.PasswordUpdatedAt)
	}

	// After password update, PasswordUpdatedAt becomes non-nil.
	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdatePassword(ctx, pool, adminID, "$2a$12$abcdef", now); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	chk, err = repo.GetForAccessCheck(ctx, pool, adminID)
	if err != nil {
		t.Fatalf("GetForAccessCheck: %v", err)
	}
	if !chk.Exists || chk.PasswordUpdatedAt == nil {
		t.Fatalf("expected password_updated_at non-nil; got chk=%+v", chk)
	}

	// Soft-delete: Exists must flip to false.
	if _, err := pool.Exec(ctx, "update admins set deleted_at = now() where id=$1", adminID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	chk, err = repo.GetForAccessCheck(ctx, pool, adminID)
	if err != nil {
		t.Fatalf("GetForAccessCheck after delete: %v", err)
	}
	if chk.Exists {
		t.Fatalf("Exists should be false for soft-deleted admin")
	}

	// Unknown ID also yields Exists=false (no error).
	chk2, err := repo.GetForAccessCheck(ctx, pool, 999_999)
	if err != nil {
		t.Fatalf("GetForAccessCheck(missing): %v", err)
	}
	if chk2.Exists {
		t.Fatalf("Exists should be false for missing admin")
	}
}

func TestRecordLoginSuccessResetsCounter(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	if _, err := pool.Exec(ctx,
		"update admins set failed_login_count=3 where id=$1", adminID); err != nil {
		t.Fatalf("seed counter: %v", err)
	}

	now := time.Now().UTC()
	if err := repo.RecordLoginSuccess(ctx, pool, adminID, now); err != nil {
		t.Fatalf("RecordLoginSuccess: %v", err)
	}
	row, err := repo.FindByID(ctx, pool, adminID)
	if err != nil || row == nil {
		t.Fatalf("FindByID: %v %+v", err, row)
	}
	if row.FailedLoginCount != 0 {
		t.Errorf("failed_login_count should reset to 0, got %d", row.FailedLoginCount)
	}
}

func TestRecordLoginFailureLocksAfterFive(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	// Trigger five failures — locked_until should be set on the 5th.
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := repo.RecordLoginFailure(ctx, pool, adminID, now); err != nil {
			t.Fatalf("RecordLoginFailure attempt %d: %v", i+1, err)
		}
	}
	row, err := repo.FindByID(ctx, pool, adminID)
	if err != nil || row == nil {
		t.Fatalf("FindByID: %v %+v", err, row)
	}
	if row.FailedLoginCount != 5 {
		t.Errorf("counter should be 5 after 5 failures, got %d", row.FailedLoginCount)
	}
	if row.LockedUntil == nil {
		t.Fatal("locked_until should be set after 5 failures")
	}
	delta := row.LockedUntil.Sub(now)
	if delta < 14*time.Minute || delta > 16*time.Minute {
		t.Errorf("locked_until should be ~now+15m, delta=%v", delta)
	}
}

func TestRecordLoginFailureResetsCounterAfterLockExpiry(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	// Simulate "lock just expired" state: counter at 5, locked_until in past.
	past := time.Now().UTC().Add(-1 * time.Minute)
	if _, err := pool.Exec(ctx,
		"update admins set failed_login_count=5, locked_until=$1 where id=$2",
		past, adminID); err != nil {
		t.Fatalf("seed lock-expired state: %v", err)
	}

	// Next failure should restart the counter at 1, not bump to 6.
	if err := repo.RecordLoginFailure(ctx, pool, adminID, time.Now().UTC()); err != nil {
		t.Fatalf("RecordLoginFailure: %v", err)
	}
	row, err := repo.FindByID(ctx, pool, adminID)
	if err != nil || row == nil {
		t.Fatalf("FindByID: %v %+v", err, row)
	}
	if row.FailedLoginCount != 1 {
		t.Errorf("counter should restart at 1 after lock expiry, got %d", row.FailedLoginCount)
	}
}

func TestUpdatePasswordResetsAuthState(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Role: "global", MustChangePassword: true,
	})
	// Seed temp expiry, locked state.
	if _, err := pool.Exec(ctx, `
		update admins set
			temp_password_expires_at = now() + interval '1 hour',
			failed_login_count       = 3,
			locked_until             = now() + interval '5 minutes'
		where id=$1`, adminID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdatePassword(ctx, pool, adminID, "$2a$12$xyz", now); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	row, err := repo.FindByID(ctx, pool, adminID)
	if err != nil || row == nil {
		t.Fatalf("FindByID: %v", err)
	}
	if row.MustChangePassword {
		t.Errorf("must_change_password should be false")
	}
	if row.TempPasswordExpiresAt != nil {
		t.Errorf("temp_password_expires_at should be NULL")
	}
	if row.FailedLoginCount != 0 {
		t.Errorf("failed_login_count should be 0")
	}
	if row.LockedUntil != nil {
		t.Errorf("locked_until should be NULL")
	}
	if row.PasswordUpdatedAt == nil {
		t.Errorf("password_updated_at should be set")
	}
}
