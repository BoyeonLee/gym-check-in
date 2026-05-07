//go:build integration

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

// TestInsertAndGetBranch covers the happy path for InsertBranch + GetBranch
// and the (nil, nil) "missing/soft-deleted" contract that handlers translate
// to 404.
func TestInsertAndGetBranch(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	addr := "Seoul Test Address 1"
	id, err := repo.InsertBranch(ctx, pool, "강남점", &addr)
	if err != nil {
		t.Fatalf("InsertBranch: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected id > 0, got %d", id)
	}

	got, err := repo.GetBranch(ctx, pool, id)
	if err != nil || got == nil {
		t.Fatalf("GetBranch: %v %+v", err, got)
	}
	if got.Name != "강남점" || got.Address == nil || *got.Address != addr {
		t.Errorf("row drift: %+v", got)
	}

	// Missing id → (nil, nil).
	if row, err := repo.GetBranch(ctx, pool, 999_999); err != nil || row != nil {
		t.Fatalf("GetBranch(missing): expected (nil,nil) got (%+v,%v)", row, err)
	}

	// Soft-delete then GetBranch must return (nil, nil).
	if _, err := pool.Exec(ctx, "update branches set deleted_at = now() where id=$1", id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if row, err := repo.GetBranch(ctx, pool, id); err != nil || row != nil {
		t.Fatalf("GetBranch(soft-deleted): expected (nil,nil) got (%+v,%v)", row, err)
	}
}

// TestInsertBranchAddressDuplicate proves the unique constraint on
// branches.address surfaces as 23505 with constraint name `branches_address_key`,
// which apperr.FromDBError turns into 409 ADDRESS_DUPLICATE.
func TestInsertBranchAddressDuplicate(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	addr := "Duplicate Address"
	if _, err := repo.InsertBranch(ctx, pool, "first", &addr); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err := repo.InsertBranch(ctx, pool, "second", &addr)
	if err == nil {
		t.Fatal("expected unique violation, got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected *pgconn.PgError, got %T", err)
	}
	if pgErr.Code != "23505" {
		t.Errorf("expected SQLSTATE 23505, got %q", pgErr.Code)
	}
	if pgErr.ConstraintName != "branches_address_key" {
		t.Errorf("expected constraint=branches_address_key, got %q", pgErr.ConstraintName)
	}
}

// TestListBranchesFiltersSoftDeleted confirms ListBranches never surfaces
// deleted rows and orders by id ASC.
func TestListBranchesFiltersSoftDeleted(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	a := testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "alpha"})
	b := testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "bravo"})
	c := testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "charlie"})

	if _, err := pool.Exec(ctx, "update branches set deleted_at = now() where id=$1", b); err != nil {
		t.Fatalf("soft delete b: %v", err)
	}

	out, err := repo.ListBranches(ctx, pool)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	ids := make([]int64, 0, len(out))
	for _, r := range out {
		ids = append(ids, r.ID)
		if r.DeletedAt != nil {
			t.Errorf("ListBranches returned soft-deleted row: %+v", r)
		}
	}
	// Expect [a, c] in that order; soft-deleted b is filtered out.
	if len(ids) < 2 || ids[0] != a {
		t.Errorf("expected first id %d, got %v", a, ids)
	}
	for _, id := range ids {
		if id == b {
			t.Errorf("soft-deleted branch %d returned in list", b)
		}
	}
	_ = c
}

// TestUpdateBranchPartial verifies that nil fields leave the column alone and
// that missing/soft-deleted rows return pgx.ErrNoRows.
func TestUpdateBranchPartial(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	addr := "Original"
	id, err := repo.InsertBranch(ctx, pool, "before", &addr)
	if err != nil {
		t.Fatalf("InsertBranch: %v", err)
	}

	// Update name only — address must stay "Original".
	newName := "after"
	if err := repo.UpdateBranch(ctx, pool, id, &newName, nil); err != nil {
		t.Fatalf("UpdateBranch: %v", err)
	}
	got, _ := repo.GetBranch(ctx, pool, id)
	if got.Name != "after" || got.Address == nil || *got.Address != "Original" {
		t.Errorf("partial drift: %+v", got)
	}

	// Update address only.
	newAddr := "Replaced"
	if err := repo.UpdateBranch(ctx, pool, id, nil, &newAddr); err != nil {
		t.Fatalf("UpdateBranch addr: %v", err)
	}
	got, _ = repo.GetBranch(ctx, pool, id)
	if got.Address == nil || *got.Address != "Replaced" || got.Name != "after" {
		t.Errorf("addr-only drift: %+v", got)
	}

	// Missing id → ErrNoRows.
	if err := repo.UpdateBranch(ctx, pool, 999_999, &newName, nil); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected pgx.ErrNoRows for missing id, got %v", err)
	}

	// Soft-deleted id → ErrNoRows.
	if _, err := pool.Exec(ctx, "update branches set deleted_at = now() where id=$1", id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := repo.UpdateBranch(ctx, pool, id, &newName, nil); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected pgx.ErrNoRows for soft-deleted id, got %v", err)
	}
}

// TestSoftDeleteBranchTx covers the happy path inside a transaction plus the
// idempotent ErrNoRows when called twice.
func TestSoftDeleteBranchTx(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	id := testutil.CreateBranch(t, pool, nil)

	now := time.Now().UTC()
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.SoftDeleteBranch(ctx, tx, id, now)
	}); err != nil {
		t.Fatalf("SoftDeleteBranch: %v", err)
	}
	// After delete, GetBranch returns nil.
	if row, _ := repo.GetBranch(ctx, pool, id); row != nil {
		t.Fatalf("expected GetBranch nil after soft delete, got %+v", row)
	}
	// Second call — already soft-deleted → ErrNoRows.
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.SoftDeleteBranch(ctx, tx, id, now)
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected ErrNoRows on second delete, got %v", err)
	}
}

// TestCountActiveMembersAndAdmins exercises the BRANCH_IN_USE pre-check
// helpers. Soft-deleted rows must not contribute to the counts.
func TestCountActiveMembersAndAdmins(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	branchID := testutil.CreateBranch(t, pool, nil)

	// Initially zero on both.
	if n, err := repo.CountActiveMembers(ctx, pool, branchID); err != nil || n != 0 {
		t.Fatalf("CountActiveMembers initial: n=%d err=%v", n, err)
	}
	if n, err := repo.CountActiveAdmins(ctx, pool, branchID); err != nil || n != 0 {
		t.Fatalf("CountActiveAdmins initial: n=%d err=%v", n, err)
	}

	// Add 1 member + 1 admin.
	memberID := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branchID})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Role: "branch", BranchID: &branchID,
	})
	if n, _ := repo.CountActiveMembers(ctx, pool, branchID); n != 1 {
		t.Errorf("expected 1 member, got %d", n)
	}
	if n, _ := repo.CountActiveAdmins(ctx, pool, branchID); n != 1 {
		t.Errorf("expected 1 admin, got %d", n)
	}

	// Soft-delete both → counters back to 0.
	if _, err := pool.Exec(ctx, "update members set deleted_at = now() where id=$1", memberID); err != nil {
		t.Fatalf("soft del member: %v", err)
	}
	if _, err := pool.Exec(ctx, "update admins set deleted_at = now() where id=$1", adminID); err != nil {
		t.Fatalf("soft del admin: %v", err)
	}
	if n, _ := repo.CountActiveMembers(ctx, pool, branchID); n != 0 {
		t.Errorf("expected 0 members after delete, got %d", n)
	}
	if n, _ := repo.CountActiveAdmins(ctx, pool, branchID); n != 0 {
		t.Errorf("expected 0 admins after delete, got %d", n)
	}
}
