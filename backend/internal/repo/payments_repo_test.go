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

// TestSalesSummary_GrossRefundNetSeparation seeds a small ledger and asserts
// gross_total / refund_total / net_total split, by_method/by_day decomposition.
func TestSalesSummary_GrossRefundNetSeparation(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-summary", Role: "branch", BranchID: &bid,
	})

	d1 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)

	// Day 1: cash grant 100,000 + card grant 200,000 + card refund -100,000.
	for _, p := range []repo.PaymentRow{
		{MembershipID: msid, BranchID: bid, Amount: 100000, Method: "cash", PaidAt: d1, PerformedBy: adminID},
		{MembershipID: msid, BranchID: bid, Amount: 200000, Method: "card", PaidAt: d1, PerformedBy: adminID},
		{MembershipID: msid, BranchID: bid, Amount: -100000, Method: "card", PaidAt: d1, PerformedBy: adminID},
	} {
		if _, err := repo.InsertPayment(ctx, pool, p); err != nil {
			t.Fatalf("InsertPayment: %v", err)
		}
	}
	// Day 2: cash grant 50,000.
	if _, err := repo.InsertPayment(ctx, pool, repo.PaymentRow{
		MembershipID: msid, BranchID: bid, Amount: 50000, Method: "cash", PaidAt: d2, PerformedBy: adminID,
	}); err != nil {
		t.Fatalf("InsertPayment: %v", err)
	}

	got, err := repo.SalesSummary(ctx, pool, repo.SalesSummaryInput{
		From: d1, To: d2,
	})
	if err != nil {
		t.Fatalf("SalesSummary: %v", err)
	}
	if got.Total.Gross != 350000 || got.Total.Refund != 100000 || got.Total.Net != 250000 {
		t.Errorf("totals: gross=%d refund=%d net=%d (want 350000/100000/250000)",
			got.Total.Gross, got.Total.Refund, got.Total.Net)
	}
	// by_method: expect cash grant 150000, card grant 200000 / refund 100000.
	var cashGross, cardGross, cardRefund int
	for _, m := range got.ByMethod {
		switch m.Method {
		case "cash":
			cashGross = m.Gross
		case "card":
			cardGross = m.Gross
			cardRefund = m.Refund
		}
	}
	if cashGross != 150000 || cardGross != 200000 || cardRefund != 100000 {
		t.Errorf("by_method drift: cashGross=%d cardGross=%d cardRefund=%d", cashGross, cardGross, cardRefund)
	}
	// by_day: 2 buckets, and day1 must contain cash+card breakdowns.
	if len(got.ByDay) != 2 {
		t.Fatalf("expected 2 by_day rows, got %d", len(got.ByDay))
	}
	// Locate day1.
	var day1Bucket repo.SalesDayBucket
	for _, b := range got.ByDay {
		if b.Date.Equal(d1) {
			day1Bucket = b
		}
	}
	if day1Bucket.Cash.Gross != 100000 || day1Bucket.Cash.Refund != 0 || day1Bucket.Cash.Net != 100000 {
		t.Errorf("by_day d1 cash split drift: %+v", day1Bucket.Cash)
	}
	if day1Bucket.Card.Gross != 200000 || day1Bucket.Card.Refund != 100000 || day1Bucket.Card.Net != 100000 {
		t.Errorf("by_day d1 card split drift: %+v", day1Bucket.Card)
	}
	if day1Bucket.Total.Gross != 300000 || day1Bucket.Total.Refund != 100000 || day1Bucket.Total.Net != 200000 {
		t.Errorf("by_day d1 total drift: %+v", day1Bucket.Total)
	}
}

// TestSalesSummary_BranchFilter — branch_id parameter limits the rows.
func TestSalesSummary_BranchFilter(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	b1 := testutil.CreateBranch(t, pool, nil)
	b2 := testutil.CreateBranch(t, pool, nil)
	mid1 := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: b1})
	mid2 := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: b2})
	msid1 := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid1})
	msid2 := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid2})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})
	d := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)

	for _, p := range []repo.PaymentRow{
		{MembershipID: msid1, BranchID: b1, Amount: 100000, Method: "cash", PaidAt: d, PerformedBy: adminID},
		{MembershipID: msid2, BranchID: b2, Amount: 200000, Method: "cash", PaidAt: d, PerformedBy: adminID},
	} {
		if _, err := repo.InsertPayment(ctx, pool, p); err != nil {
			t.Fatalf("InsertPayment: %v", err)
		}
	}

	got, err := repo.SalesSummary(ctx, pool, repo.SalesSummaryInput{From: d, To: d, BranchID: &b1})
	if err != nil {
		t.Fatalf("SalesSummary: %v", err)
	}
	if got.Total.Gross != 100000 {
		t.Errorf("expected b1-scoped gross=100000, got %d", got.Total.Gross)
	}
}
