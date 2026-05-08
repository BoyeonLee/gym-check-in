//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

func setupPaymentsFixture(t *testing.T) (int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-pay", Role: "branch", BranchID: &bid,
	})
	_ = ctx
	return bid, msid, adminID
}

// TestInsertPayment_Positive: a normal grant payment row is inserted and
// retrievable as the original grant.
func TestInsertPayment_Positive(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-pay", Role: "branch", BranchID: &bid,
	})

	id, err := repo.InsertPayment(ctx, pool, repo.PaymentRow{
		MembershipID: msid,
		BranchID:     bid,
		Amount:       150000,
		Method:       "card",
		PaidAt:       time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC),
		PerformedBy:  adminID,
	})
	if err != nil {
		t.Fatalf("InsertPayment: %v", err)
	}
	if id == 0 {
		t.Fatal("zero id returned")
	}

	got, err := repo.GetOriginalGrantPayment(ctx, pool, msid)
	if err != nil {
		t.Fatalf("GetOriginalGrantPayment: %v", err)
	}
	if got == nil || got.ID != id || got.Amount != 150000 || got.Method != "card" {
		t.Errorf("grant row drift: %+v", got)
	}
}

// TestInsertPayment_RejectsZeroAmount: payments.amount CHECK forbids 0; the
// repo returns a wrappable pgx error so the handler can map to 400.
func TestInsertPayment_RejectsZeroAmount(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-pay-zero", Role: "branch", BranchID: &bid,
	})

	_, err := repo.InsertPayment(ctx, pool, repo.PaymentRow{
		MembershipID: msid, BranchID: bid, Amount: 0,
		Method: "cash", PaidAt: time.Now().UTC(), PerformedBy: adminID,
	})
	if err == nil {
		t.Fatal("expected check_violation, got nil")
	}
	mapped := apperr.FromDBError(err)
	if mapped == nil || mapped.Status != 400 || mapped.Code != "INVALID_INPUT" {
		t.Errorf("expected 400 INVALID_INPUT, got %+v", mapped)
	}
}

// TestListPaymentsByMembership_OrderingIncludesRefund — grant + refund rows
// come back in chronological order so the handler payload is stable.
func TestListPaymentsByMembership_OrderingIncludesRefund(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-list", Role: "branch", BranchID: &bid,
	})

	day1 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	if _, err := repo.InsertPayment(ctx, pool, repo.PaymentRow{
		MembershipID: msid, BranchID: bid, Amount: 150000,
		Method: "card", PaidAt: day1, PerformedBy: adminID,
	}); err != nil {
		t.Fatalf("grant insert: %v", err)
	}
	if _, err := repo.InsertPayment(ctx, pool, repo.PaymentRow{
		MembershipID: msid, BranchID: bid, Amount: -150000,
		Method: "card", PaidAt: day2, PerformedBy: adminID,
	}); err != nil {
		t.Fatalf("refund insert: %v", err)
	}

	rows, err := repo.ListPaymentsByMembership(ctx, pool, msid)
	if err != nil {
		t.Fatalf("ListPaymentsByMembership: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Amount != 150000 || rows[1].Amount != -150000 {
		t.Errorf("unexpected ordering: %+v", rows)
	}
}

// TestGetOriginalGrantPayment_NoGrant: no positive payment → nil.
func TestGetOriginalGrantPayment_NoGrant(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})

	got, err := repo.GetOriginalGrantPayment(ctx, pool, msid)
	if err != nil {
		t.Fatalf("GetOriginalGrantPayment: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}
