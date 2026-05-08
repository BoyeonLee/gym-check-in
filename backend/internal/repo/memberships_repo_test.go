//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// dateUTC is a small helper that returns midnight UTC of (y, m, d) so date
// columns survive round-tripping without TZ drift.
func dateUTC(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// insertMembershipTest wraps WithTx + InsertMembership so individual tests
// stay readable. Returns the new id or the underlying pgx error.
func insertMembershipTest(t *testing.T, pool *pgxpool.Pool, in repo.GrantInput) (int64, error) {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		got, err := repo.InsertMembership(ctx, tx, in)
		if err != nil {
			return err
		}
		id = got
		return nil
	})
	return id, err
}

// TestInsertMembership_MonthlyAndPass10 — both shapes round-trip via
// GetMembership and the start/end fields are preserved.
func TestInsertMembership_MonthlyAndPass10(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})

	threeMonths := 3
	mon := repo.GrantInput{
		MemberID: mid, Type: "monthly",
		Months:    &threeMonths,
		StartDate: dateUTC(2026, 5, 8), EndDate: dateUTC(2026, 8, 8),
	}
	id, err := insertMembershipTest(t, pool, mon)
	if err != nil {
		t.Fatalf("monthly insert: %v", err)
	}
	got, err := repo.GetMembership(ctx, pool, id, &bid)
	if err != nil || got == nil {
		t.Fatalf("get monthly: %v %+v", err, got)
	}
	if got.Type != "monthly" || got.Months == nil || *got.Months != 3 {
		t.Errorf("monthly drift: %+v", got)
	}

	// pass10 starts after the monthly ends so the EXCLUDE constraint is happy.
	ten := 10
	pass := repo.GrantInput{
		MemberID: mid, Type: "pass10",
		Remaining: &ten,
		StartDate: dateUTC(2026, 8, 9), EndDate: dateUTC(2026, 10, 9),
	}
	id2, err := insertMembershipTest(t, pool, pass)
	if err != nil {
		t.Fatalf("pass10 insert: %v", err)
	}
	got2, err := repo.GetMembership(ctx, pool, id2, &bid)
	if err != nil || got2 == nil {
		t.Fatalf("get pass10: %v %+v", err, got2)
	}
	if got2.Type != "pass10" || got2.Remaining == nil || *got2.Remaining != 10 {
		t.Errorf("pass10 drift: %+v", got2)
	}
}

// TestInsertMembership_OverlapRejected — same member's overlapping period
// triggers PostgreSQL 23P01, which apperr maps to 409 MEMBERSHIP_PERIOD_OVERLAP.
func TestInsertMembership_OverlapRejected(t *testing.T) {
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	one := 1
	first := repo.GrantInput{
		MemberID: mid, Type: "monthly", Months: &one,
		StartDate: dateUTC(2026, 5, 1), EndDate: dateUTC(2026, 6, 1),
	}
	if _, err := insertMembershipTest(t, pool, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	overlap := repo.GrantInput{
		MemberID: mid, Type: "monthly", Months: &one,
		StartDate: dateUTC(2026, 5, 15), EndDate: dateUTC(2026, 6, 15),
	}
	_, err := insertMembershipTest(t, pool, overlap)
	if err == nil {
		t.Fatal("expected exclusion violation, got nil")
	}
	mapped := apperr.FromDBError(err)
	if mapped == nil || mapped.Status != 409 || mapped.Code != "MEMBERSHIP_PERIOD_OVERLAP" {
		t.Errorf("expected 409 MEMBERSHIP_PERIOD_OVERLAP, got %+v", mapped)
	}
}

// TestInsertMembership_AdjacentNonOverlap — schema's daterange uses '[]'
// (inclusive) so two memberships sharing an endpoint DO overlap. A truly
// adjacent next-membership starts the day AFTER end_date.
func TestInsertMembership_AdjacentNonOverlap(t *testing.T) {
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	one := 1
	first := repo.GrantInput{
		MemberID: mid, Type: "monthly", Months: &one,
		StartDate: dateUTC(2026, 5, 1), EndDate: dateUTC(2026, 5, 30),
	}
	if _, err := insertMembershipTest(t, pool, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	next := repo.GrantInput{
		MemberID: mid, Type: "monthly", Months: &one,
		StartDate: dateUTC(2026, 5, 31), EndDate: dateUTC(2026, 6, 30),
	}
	if _, err := insertMembershipTest(t, pool, next); err != nil {
		t.Fatalf("adjacent insert should succeed: %v", err)
	}
}

// TestGetMembership_OtherBranchReturnsNil — branch admins must not see
// memberships belonging to a member from a different branch.
func TestGetMembership_OtherBranchReturnsNil(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	mine := testutil.CreateBranch(t, pool, nil)
	other := testutil.CreateBranch(t, pool, nil)
	otherMember := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: other})
	otherMs := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: otherMember})

	got, err := repo.GetMembership(ctx, pool, otherMs, &mine)
	if err != nil {
		t.Fatalf("GetMembership: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// TestApplyPause_ImmediateAndExtendsEnd — pause that starts today flips
// status to paused and extends end_date by the pause duration; pause_used
// becomes true.
func TestApplyPause_ImmediateAndExtendsEnd(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: mid, StartDate: "2026-04-01", EndDate: "2026-05-30",
	})

	today := dateUTC(2026, 4, 1)
	pauseEnd := dateUTC(2026, 4, 7)
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ApplyPause(ctx, tx, repo.PauseInput{
			ID: msid, PauseStartDate: today, PauseEndDate: pauseEnd, Today: today,
		})
	}); err != nil {
		t.Fatalf("ApplyPause: %v", err)
	}

	got, err := repo.GetMembership(ctx, pool, msid, &bid)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.Status != "paused" || !got.PauseUsed {
		t.Errorf("status/pause_used drift: %+v", got)
	}
	want := dateUTC(2026, 6, 5) // 5/30 + 6 days
	if !got.EndDate.Equal(want) {
		t.Errorf("end_date drift: got %s want %s", got.EndDate, want)
	}
}

// TestApplyUnpause_ShortensEnd — 4/1~4/7 paused with original end 5/30. On
// 4/6 unpause leaves 1 remaining pause day → end_date 5/29.
func TestApplyUnpause_ShortensEnd(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: mid, StartDate: "2026-04-01", EndDate: "2026-05-30",
	})

	pauseStart := dateUTC(2026, 4, 1)
	pauseEnd := dateUTC(2026, 4, 7)
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ApplyPause(ctx, tx, repo.PauseInput{
			ID: msid, PauseStartDate: pauseStart, PauseEndDate: pauseEnd, Today: pauseStart,
		})
	}); err != nil {
		t.Fatalf("ApplyPause: %v", err)
	}

	apr6 := dateUTC(2026, 4, 6)
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ApplyUnpause(ctx, tx, repo.UnpauseInput{ID: msid, ActualPauseEnd: apr6})
	}); err != nil {
		t.Fatalf("ApplyUnpause: %v", err)
	}

	got, err := repo.GetMembership(ctx, pool, msid, &bid)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.Status != "active" || got.PauseStartDate != nil || got.PauseEndDate != nil {
		t.Errorf("unpause cleanup drift: %+v", got)
	}
	want := dateUTC(2026, 5, 29)
	if !got.EndDate.Equal(want) {
		t.Errorf("end_date drift: got %s want %s", got.EndDate, want)
	}
}

// TestApplyCancelPause_RestoresEnd — future-scheduled pause registered on
// 4/30 (5/15~5/20). cancel-pause restores end_date to the pre-pause value
// and clears pause_used.
func TestApplyCancelPause_RestoresEnd(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: mid, StartDate: "2026-04-01", EndDate: "2026-05-30",
	})

	pauseStart := dateUTC(2026, 5, 15)
	pauseEnd := dateUTC(2026, 5, 20)
	apr30 := dateUTC(2026, 4, 30)
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ApplyPause(ctx, tx, repo.PauseInput{
			ID: msid, PauseStartDate: pauseStart, PauseEndDate: pauseEnd, Today: apr30,
		})
	}); err != nil {
		t.Fatalf("ApplyPause: %v", err)
	}
	got, _ := repo.GetMembership(ctx, pool, msid, &bid)
	if got.Status != "active" || !got.PauseUsed {
		t.Fatalf("future pause not registered correctly: %+v", got)
	}

	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ApplyCancelPause(ctx, tx, repo.CancelPauseInput{ID: msid, Today: apr30})
	}); err != nil {
		t.Fatalf("ApplyCancelPause: %v", err)
	}
	got, _ = repo.GetMembership(ctx, pool, msid, &bid)
	if got.PauseUsed || got.PauseStartDate != nil || got.PauseEndDate != nil {
		t.Errorf("cancel-pause cleanup drift: %+v", got)
	}
	want := dateUTC(2026, 5, 30)
	if !got.EndDate.Equal(want) {
		t.Errorf("end_date drift: got %s want %s", got.EndDate, want)
	}
}

// TestApplyRefund_StatusChangesToRefunded.
func TestApplyRefund_StatusChangesToRefunded(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})

	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return repo.ApplyRefund(ctx, tx, repo.RefundInput{ID: msid})
	}); err != nil {
		t.Fatalf("ApplyRefund: %v", err)
	}
	got, _ := repo.GetMembership(ctx, pool, msid, &bid)
	if got.Status != "refunded" {
		t.Errorf("expected refunded, got %s", got.Status)
	}
}

// TestGetMembershipDetail_BundlesPaymentsAndEvents — single call must return
// the membership row plus its payments and events.
func TestGetMembershipDetail_BundlesPaymentsAndEvents(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-detail", Role: "branch", BranchID: &bid,
	})

	if _, err := repo.InsertPayment(ctx, pool, repo.PaymentRow{
		MembershipID: msid, BranchID: bid, Amount: 100000,
		Method: "cash", PaidAt: dateUTC(2026, 5, 1), PerformedBy: adminID,
	}); err != nil {
		t.Fatalf("InsertPayment: %v", err)
	}
	if err := repo.InsertEvent(ctx, pool, repo.EventRow{
		MembershipID: msid, Action: "refund", Reason: "x", PerformedBy: adminID,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	det, err := repo.GetMembershipDetail(ctx, pool, msid, &bid)
	if err != nil {
		t.Fatalf("GetMembershipDetail: %v", err)
	}
	if det == nil {
		t.Fatal("nil detail")
	}
	if det.Membership.ID != msid {
		t.Errorf("membership id drift: %+v", det.Membership)
	}
	if len(det.Payments) != 1 {
		t.Errorf("expected 1 payment, got %d", len(det.Payments))
	}
	if len(det.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(det.Events))
	}
}

// TestGetCurrentMembership_ReturnsNilWhenNoneActive — only active/paused
// memberships are eligible; refunded/expired are excluded.
func TestGetCurrentMembership_ReturnsNilWhenNoneActive(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: mid, Status: "expired",
		StartDate: "2025-01-01", EndDate: "2025-02-01",
	})
	got, err := repo.GetCurrentMembership(ctx, pool, mid)
	if err != nil {
		t.Fatalf("GetCurrentMembership: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}
