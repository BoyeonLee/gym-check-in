//go:build integration

// admins_repo_step4_test.go — coverage for the CRUD + reset surface added in
// step 4. The step-3 test file owns the auth-flow helpers; keep additions
// here so churn doesn't bleed across steps.
package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// TestCreateAdminSetsTempExpiry — fresh admin must come out with
// must_change_password=true and a non-NULL temp_password_expires_at so the
// reset-password / first-login flow works without an extra UPDATE.
func TestCreateAdminSetsTempExpiry(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branchID := testutil.CreateBranch(t, pool, nil)

	now := time.Now().UTC().Truncate(time.Second)
	var newID int64
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		id, err := repo.CreateAdmin(ctx, tx, repo.CreateAdminInput{
			Username:     "newcoach",
			Role:         "branch",
			BranchID:     &branchID,
			PasswordHash: "$2a$12$abcdefghijklmnopqrstuvwxyz", // dummy bcrypt-shaped string
		}, now)
		if err != nil {
			return err
		}
		newID = id
		return nil
	})
	if err != nil {
		t.Fatalf("CreateAdmin: %v", err)
	}

	row, err := repo.FindByID(ctx, pool, newID)
	if err != nil || row == nil {
		t.Fatalf("FindByID: %v %+v", err, row)
	}
	if !row.MustChangePassword {
		t.Errorf("must_change_password should be true")
	}
	if row.TempPasswordExpiresAt == nil {
		t.Fatal("temp_password_expires_at must be non-nil")
	}
	delta := row.TempPasswordExpiresAt.Sub(now)
	if delta < 23*time.Hour || delta > 25*time.Hour {
		t.Errorf("temp expiry should be ~now+24h, delta=%v", delta)
	}
	if row.PasswordUpdatedAt != nil {
		t.Errorf("password_updated_at should be nil for fresh admin, got %v", row.PasswordUpdatedAt)
	}
	if row.Username != "newcoach" || row.Role != "branch" || row.BranchID == nil || *row.BranchID != branchID {
		t.Errorf("row drift: %+v", row)
	}
}

// TestCreateAdminUsernameDuplicate verifies the unique constraint name
// surface — apperr.FromDBError relies on `admins_username_key` to map 23505
// to 409 USERNAME_DUPLICATE.
func TestCreateAdminUsernameDuplicate(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branchID := testutil.CreateBranch(t, pool, nil)

	testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "dupuser", Role: "branch", BranchID: &branchID,
	})
	now := time.Now().UTC()
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.CreateAdmin(ctx, tx, repo.CreateAdminInput{
			Username:     "dupuser",
			Role:         "branch",
			BranchID:     &branchID,
			PasswordHash: "$2a$12$xxxxxxxxxxxxxxxxxxxxxx",
		}, now)
		return err
	})
	if err == nil {
		t.Fatal("expected unique violation, got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected pgconn.PgError, got %T %v", err, err)
	}
	if pgErr.Code != "23505" || pgErr.ConstraintName != "admins_username_key" {
		t.Errorf("expected 23505/admins_username_key, got %s/%s", pgErr.Code, pgErr.ConstraintName)
	}
}

// TestListAdminsJoinsBranchName ensures branch_name comes from the branches
// table via JOIN (NULL for global), and that soft-deleted admins are
// filtered out.
func TestListAdminsJoinsBranchName(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branchID := testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "강남점"})

	gID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "global1", Role: "global",
	})
	bID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "branchA", Role: "branch", BranchID: &branchID,
	})
	delID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "ghost", Role: "global",
	})
	if _, err := pool.Exec(ctx, "update admins set deleted_at=now() where id=$1", delID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	rows, err := repo.ListAdmins(ctx, pool)
	if err != nil {
		t.Fatalf("ListAdmins: %v", err)
	}
	var sawGlobal, sawBranch bool
	for _, r := range rows {
		if r.ID == delID {
			t.Errorf("ListAdmins returned soft-deleted admin %d", delID)
		}
		if r.ID == gID {
			sawGlobal = true
			if r.Role != "global" || r.BranchID != nil || r.BranchName != nil {
				t.Errorf("global row drift: %+v", r)
			}
		}
		if r.ID == bID {
			sawBranch = true
			if r.Role != "branch" || r.BranchID == nil || *r.BranchID != branchID {
				t.Errorf("branch row drift: %+v", r)
			}
			if r.BranchName == nil || *r.BranchName != "강남점" {
				t.Errorf("branch_name JOIN missing: %+v", r.BranchName)
			}
		}
	}
	if !sawGlobal || !sawBranch {
		t.Errorf("expected to see both global and branch admins; got rows=%+v", rows)
	}
}

// TestUpdateAdminPartial — partial PATCH leaves untouched columns alone and
// surfaces ErrNoRows when the target is missing/soft-deleted.
func TestUpdateAdminPartial(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branchA := testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "A"})
	branchB := testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "B"})

	id, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "renameme", Role: "branch", BranchID: &branchA,
	})

	now := time.Now().UTC()

	// Username only.
	newName := "renamed"
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.UpdateAdmin(ctx, tx, id, repo.UpdateAdminInput{Username: &newName}, now)
	}); err != nil {
		t.Fatalf("UpdateAdmin username: %v", err)
	}
	row, _ := repo.FindByID(ctx, pool, id)
	if row.Username != "renamed" || row.Role != "branch" || row.BranchID == nil || *row.BranchID != branchA {
		t.Errorf("partial username drift: %+v", row)
	}

	// branch_id only.
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.UpdateAdmin(ctx, tx, id, repo.UpdateAdminInput{
			BranchIDSet: true, BranchID: &branchB,
		}, now)
	}); err != nil {
		t.Fatalf("UpdateAdmin branch_id: %v", err)
	}
	row, _ = repo.FindByID(ctx, pool, id)
	if row.BranchID == nil || *row.BranchID != branchB {
		t.Errorf("branch_id update drift: %+v", row.BranchID)
	}

	// Promote to global — branch_id must be NULL.
	roleGlobal := "global"
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.UpdateAdmin(ctx, tx, id, repo.UpdateAdminInput{
			Role: &roleGlobal, BranchIDSet: true, BranchID: nil,
		}, now)
	}); err != nil {
		t.Fatalf("UpdateAdmin promote: %v", err)
	}
	row, _ = repo.FindByID(ctx, pool, id)
	if row.Role != "global" || row.BranchID != nil {
		t.Errorf("promote drift: %+v", row)
	}

	// Missing id → ErrNoRows.
	missing := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.UpdateAdmin(ctx, tx, 999_999, repo.UpdateAdminInput{Username: &newName}, now)
	})
	if !errors.Is(missing, pgx.ErrNoRows) {
		t.Errorf("expected ErrNoRows for missing id, got %v", missing)
	}
}

// TestSoftDeleteAdminBumpsPasswordUpdatedAt — when a user is soft-deleted we
// also stamp password_updated_at so any outstanding access/refresh token they
// might still be holding fails on the very next request.
func TestSoftDeleteAdminBumpsPasswordUpdatedAt(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	id, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.SoftDeleteAdmin(ctx, tx, id, now)
	}); err != nil {
		t.Fatalf("SoftDeleteAdmin: %v", err)
	}

	// Use direct SQL to read the row regardless of deleted_at filter.
	var deletedAt *time.Time
	var pwUpdated *time.Time
	if err := pool.QueryRow(ctx,
		"select deleted_at, password_updated_at from admins where id=$1", id,
	).Scan(&deletedAt, &pwUpdated); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("deleted_at must be set")
	}
	if pwUpdated == nil {
		t.Fatal("password_updated_at must be stamped on soft delete")
	}

	// FindByID (which filters deleted_at) returns nil now.
	if row, _ := repo.FindByID(ctx, pool, id); row != nil {
		t.Errorf("FindByID should ignore soft-deleted admin, got %+v", row)
	}

	// Idempotent: second call returns ErrNoRows.
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.SoftDeleteAdmin(ctx, tx, id, now)
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected ErrNoRows on second delete, got %v", err)
	}
}

// TestResetPasswordClearsLockAndStampsTempExpiry — the "operator issues a new
// 24h temp password" path. Hash is replaced; lock + counter cleared; flag set.
func TestResetPasswordClearsLockAndStampsTempExpiry(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	id, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	// Seed a locked / failed state and an old hash.
	if _, err := pool.Exec(ctx, `
		update admins set
			password_hash      = '$2a$12$old',
			failed_login_count = 4,
			locked_until       = now() + interval '5 minutes',
			must_change_password = false,
			temp_password_expires_at = null
		where id=$1`, id); err != nil {
		t.Fatalf("seed: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	newHash := "$2a$12$NEWHASH"
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ResetPassword(ctx, tx, id, newHash, now)
	}); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	row, err := repo.FindByID(ctx, pool, id)
	if err != nil || row == nil {
		t.Fatalf("FindByID: %v %+v", err, row)
	}
	if row.PasswordHash != newHash {
		t.Errorf("hash not updated: %s", row.PasswordHash)
	}
	if !row.MustChangePassword {
		t.Errorf("must_change_password should be true after reset")
	}
	if row.TempPasswordExpiresAt == nil {
		t.Fatal("temp_password_expires_at must be set")
	}
	delta := row.TempPasswordExpiresAt.Sub(now)
	if delta < 23*time.Hour || delta > 25*time.Hour {
		t.Errorf("temp expiry should be ~now+24h, delta=%v", delta)
	}
	if row.FailedLoginCount != 0 {
		t.Errorf("failed_login_count should be 0, got %d", row.FailedLoginCount)
	}
	if row.LockedUntil != nil {
		t.Errorf("locked_until should be NULL, got %v", row.LockedUntil)
	}
	if row.PasswordUpdatedAt == nil || !row.PasswordUpdatedAt.Equal(now) {
		t.Errorf("password_updated_at should equal now, got %v", row.PasswordUpdatedAt)
	}
}

// TestResetPasswordOnSoftDeletedReturnsNoRows — defence in depth: a deleted
// admin must not be revivable via reset-password.
func TestResetPasswordOnSoftDeletedReturnsNoRows(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	id, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})
	if _, err := pool.Exec(ctx, "update admins set deleted_at=now() where id=$1", id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	now := time.Now().UTC()
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ResetPassword(ctx, tx, id, "$2a$12$x", now)
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected ErrNoRows on soft-deleted target, got %v", err)
	}
}

