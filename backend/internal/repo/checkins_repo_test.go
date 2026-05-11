//go:build integration

package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// kstToday returns the KST current date at midnight UTC — matches what
// the kiosk handler resolves and what DoCheckIn expects for in.Today.
func kstToday() time.Time {
	loc, _ := time.LoadLocation("Asia/Seoul")
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// TestDoCheckIn_MonthlyInsertsRow — happy path for a 'monthly' membership:
// one row in check_ins, no decrement, no expiry transition.
func TestDoCheckIn_MonthlyInsertsRow(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	today := kstToday()
	one := 1
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "monthly",
		Months:    &one,
		StartDate: today.Format("2006-01-02"),
		EndDate:   today.AddDate(0, 1, 0).Format("2006-01-02"),
		Status:    "active",
	})

	var res repo.CheckInResult
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		r, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{MemberID: mid, BranchID: bid, Today: today})
		if err != nil {
			return err
		}
		res = r
		return nil
	})
	if err != nil {
		t.Fatalf("DoCheckIn: %v", err)
	}
	if res.Row.ID == 0 {
		t.Fatalf("expected check_ins row id, got 0")
	}
	if res.DecrementedRemaining || res.NewlyExpired {
		t.Errorf("monthly should not decrement: %+v", res)
	}
}

// TestDoCheckIn_Pass10_DecrementsAndExpires — pass10 with remaining=1
// triggers both DecrementedRemaining=true and NewlyExpired=true on the
// same transaction.
func TestDoCheckIn_Pass10_DecrementsAndExpires(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	today := kstToday()
	one := 1
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "pass10",
		Remaining: &one,
		StartDate: today.Format("2006-01-02"),
		EndDate:   today.AddDate(0, 2, 0).Format("2006-01-02"),
		Status:    "active",
	})

	var res repo.CheckInResult
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		r, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{MemberID: mid, BranchID: bid, Today: today})
		if err != nil {
			return err
		}
		res = r
		return nil
	})
	if err != nil {
		t.Fatalf("DoCheckIn: %v", err)
	}
	if !res.DecrementedRemaining {
		t.Errorf("expected pass10 first-of-day to decrement")
	}
	if !res.NewlyExpired {
		t.Errorf("expected status flip to expired when remaining hits 0")
	}
	if res.Membership.Status != "expired" {
		t.Errorf("expected post-decrement status='expired', got %q", res.Membership.Status)
	}
}

// TestDoCheckIn_SameDayTwice_DoesNotDecrementTwice — second pass10
// check-in inserts another row but leaves remaining unchanged.
func TestDoCheckIn_SameDayTwice_DoesNotDecrementTwice(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})

	today := kstToday()
	five := 5
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "pass10",
		Remaining: &five,
		StartDate: today.Format("2006-01-02"),
		EndDate:   today.AddDate(0, 2, 0).Format("2006-01-02"),
		Status:    "active",
	})

	for i := 0; i < 2; i++ {
		err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
			_, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{MemberID: mid, BranchID: bid, Today: today})
			return err
		})
		if err != nil {
			t.Fatalf("DoCheckIn iter=%d: %v", i, err)
		}
	}

	// remaining should be 4 (5 - 1, only first decrement applied).
	var remaining int
	if err := pool.QueryRow(ctx, `select remaining from memberships where member_id=$1`, mid).Scan(&remaining); err != nil {
		t.Fatalf("query remaining: %v", err)
	}
	if remaining != 4 {
		t.Errorf("expected remaining=4 after 2 same-day check-ins, got %d", remaining)
	}
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from check_ins where member_id=$1`, mid).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 2 {
		t.Errorf("expected 2 check_ins rows, got %d", rows)
	}
}

// TestDoCheckIn_NoActiveMembership_ReturnsErrNoRows — no membership at all
// → caller-friendly pgx.ErrNoRows so the handler can map to NO_ACTIVE_MEMBERSHIP.
func TestDoCheckIn_NoActiveMembership_ReturnsErrNoRows(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})

	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{MemberID: mid, BranchID: bid, Today: kstToday()})
		return err
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

// TestFindUnstartedMembership_ReturnsFutureRow — disambiguates
// MEMBERSHIP_NOT_STARTED from NO_ACTIVE_MEMBERSHIP for the handler.
func TestFindUnstartedMembership_ReturnsFutureRow(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})

	today := kstToday()
	tomorrow := today.AddDate(0, 0, 1)
	one := 1
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "monthly",
		Months:    &one,
		StartDate: tomorrow.Format("2006-01-02"),
		EndDate:   tomorrow.AddDate(0, 1, 0).Format("2006-01-02"),
		Status:    "active",
	})

	row, err := repo.FindUnstartedMembership(ctx, pool, mid, bid, today)
	if err != nil {
		t.Fatalf("FindUnstartedMembership: %v", err)
	}
	if row == nil {
		t.Fatalf("expected future-start row, got nil")
	}

	// And a member with no future row → nil.
	mid2 := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	row2, err := repo.FindUnstartedMembership(ctx, pool, mid2, bid, today)
	if err != nil {
		t.Fatalf("FindUnstartedMembership empty: %v", err)
	}
	if row2 != nil {
		t.Fatalf("expected nil for member without future membership, got %+v", row2)
	}
}

// TestListCheckInsRaw_BranchScope — branch admins must only see their branch.
func TestListCheckInsRaw_BranchScope(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid1 := testutil.CreateBranch(t, pool, nil)
	bid2 := testutil.CreateBranch(t, pool, nil)
	m1 := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid1})
	m2 := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid2})
	one := 1
	today := kstToday()
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: m1, Type: "monthly", Months: &one,
		StartDate: today.Format("2006-01-02"),
		EndDate:   today.AddDate(0, 1, 0).Format("2006-01-02"),
		Status:    "active",
	})
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: m2, Type: "monthly", Months: &one,
		StartDate: today.Format("2006-01-02"),
		EndDate:   today.AddDate(0, 1, 0).Format("2006-01-02"),
		Status:    "active",
	})

	// Insert one check-in in each branch.
	for _, mid := range []struct {
		mid int64
		bid int64
	}{{m1, bid1}, {m2, bid2}} {
		err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
			_, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{MemberID: mid.mid, BranchID: mid.bid, Today: today})
			return err
		})
		if err != nil {
			t.Fatalf("DoCheckIn: %v", err)
		}
	}

	scoped := bid1
	rows, _, err := repo.ListCheckInsRaw(ctx, pool, repo.ListCheckInsInput{
		ScopeBranchID: &scoped,
		From:          today, To: today, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListCheckInsRaw: %v", err)
	}
	if len(rows) != 1 || rows[0].BranchID != bid1 {
		t.Fatalf("expected only branch %d row, got %+v", bid1, rows)
	}

	// Global admin (no scope) sees both.
	all, _, err := repo.ListCheckInsRaw(ctx, pool, repo.ListCheckInsInput{
		From: today, To: today, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListCheckInsRaw global: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("global admin should see 2 rows, got %d", len(all))
	}
}

// TestListCheckInsDaily_GroupsByMemberAndKstDate — two same-day check-ins
// collapse into one daily row with checkin_count=2.
func TestListCheckInsDaily_GroupsByMemberAndKstDate(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	one := 1
	today := kstToday()
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly", Months: &one,
		StartDate: today.Format("2006-01-02"),
		EndDate:   today.AddDate(0, 1, 0).Format("2006-01-02"),
		Status:    "active",
	})

	for i := 0; i < 2; i++ {
		if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
			_, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{MemberID: mid, BranchID: bid, Today: today})
			return err
		}); err != nil {
			t.Fatalf("DoCheckIn: %v", err)
		}
	}

	rows, err := repo.ListCheckInsDaily(ctx, pool, repo.ListCheckInsInput{
		From: today, To: today,
	})
	if err != nil {
		t.Fatalf("ListCheckInsDaily: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 daily row, got %d (%+v)", len(rows), rows)
	}
	r := rows[0]
	if r.CheckinCount != 2 {
		t.Errorf("expected checkin_count=2, got %d", r.CheckinCount)
	}
	// API.md daily contract: branch_id/branch_name + first_checked_in_at
	// must accompany the (member_id, date) bucket.
	if r.BranchID != bid {
		t.Errorf("branch_id=%d want %d", r.BranchID, bid)
	}
	if r.BranchName == "" {
		t.Errorf("branch_name empty: %+v", r)
	}
	if r.FirstCheckedInAt.IsZero() {
		t.Errorf("first_checked_in_at zero: %+v", r)
	}
}

// TestDoCheckIn_RejectsForeignBranch — DoCheckIn locks the active membership
// scoped to (member_id, branch_id) so a forged request that names a foreign
// branch_id must yield pgx.ErrNoRows (handler maps to 422
// NO_ACTIVE_MEMBERSHIP without revealing that the membership exists in
// another branch). Backend boundary rule from backend/CLAUDE.md "체크인".
func TestDoCheckIn_RejectsForeignBranch(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	branchA := testutil.CreateBranch(t, pool, nil)
	branchB := testutil.CreateBranch(t, pool, nil)
	// Member + active membership belong to branch A.
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branchA})
	today := kstToday()
	one := 1
	_ = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "monthly",
		Months:    &one,
		StartDate: today.Format("2006-01-02"),
		EndDate:   today.AddDate(0, 1, 0).Format("2006-01-02"),
		Status:    "active",
	})

	// Calling DoCheckIn with branch B must return ErrNoRows even though
	// the member's branch A membership is otherwise eligible.
	err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{
			MemberID: mid, BranchID: branchB, Today: today,
		})
		return err
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for foreign branch, got %v", err)
	}

	// Sanity — same call against the correct branch A succeeds, proving
	// the membership itself is checkin-eligible (so the rejection above
	// really comes from the branch boundary, not some unrelated reason).
	if err := repo.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{
			MemberID: mid, BranchID: branchA, Today: today,
		})
		return err
	}); err != nil {
		t.Fatalf("expected branch-A check-in to succeed, got %v", err)
	}
}
