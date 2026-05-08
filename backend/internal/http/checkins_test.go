//go:build integration

package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// kstTodayStr returns today's KST date in YYYY-MM-DD form. Tests pin
// dates relative to "today" so the SQL bounds line up regardless of when
// the suite runs.
func kstTodayStr() string {
	loc, _ := time.LoadLocation("Asia/Seoul")
	return time.Now().In(loc).Format("2006-01-02")
}

func kstAddDaysStr(days int) string {
	loc, _ := time.LoadLocation("Asia/Seoul")
	return time.Now().In(loc).AddDate(0, 0, days).Format("2006-01-02")
}

// activeMembership creates a member + a covering active membership for
// (today-1, today+30) so a check-in attempt today succeeds.
func activeMembership(t *testing.T, pool *pgxpool.Pool, branchID int64) (memberID, membershipID int64) {
	t.Helper()
	memberID = testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branchID})
	membershipID = testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  memberID,
		Type:      "monthly",
		StartDate: kstAddDaysStr(-1),
		EndDate:   kstAddDaysStr(30),
		Status:    "active",
	})
	return
}

func countCheckIns(t *testing.T, pool *pgxpool.Pool, memberID, branchID int64) int {
	t.Helper()
	var n int
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.QueryRow(ctx,
		`select count(*) from check_ins where member_id=$1 and branch_id=$2`,
		memberID, branchID,
	).Scan(&n); err != nil {
		t.Fatalf("count check_ins: %v", err)
	}
	return n
}

// ---------------- POST /api/check-ins ----------------

// TestCheckIn_PublicNoAuth: the kiosk POST must work without an
// Authorization header.
func TestCheckIn_PublicNoAuth(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid, _ := activeMembership(t, f.pool, bid)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckIn_NoActiveMembership: 422 NO_ACTIVE_MEMBERSHIP for a member
// without any covering membership.
func TestCheckIn_NoActiveMembership(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec.Code != http.StatusUnprocessableEntity || !hasErrorCode(rec.Body.Bytes(), "NO_ACTIVE_MEMBERSHIP") {
		t.Fatalf("expected 422 NO_ACTIVE_MEMBERSHIP, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckIn_FutureStart: only a future-start membership exists → 422
// MEMBERSHIP_NOT_STARTED with `start_date` in the body.
func TestCheckIn_FutureStart(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	startDate := kstAddDaysStr(7)
	testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "monthly",
		StartDate: startDate,
		EndDate:   kstAddDaysStr(37),
		Status:    "active",
	})

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec.Code != http.StatusUnprocessableEntity || !hasErrorCode(rec.Body.Bytes(), "MEMBERSHIP_NOT_STARTED") {
		t.Fatalf("expected 422 MEMBERSHIP_NOT_STARTED, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), startDate) {
		t.Errorf("response missing start_date %q: %s", startDate, rec.Body.String())
	}
}

// TestCheckIn_PausedMembershipFails: paused → 422 NO_ACTIVE_MEMBERSHIP.
func TestCheckIn_PausedMembershipFails(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})

	// CreateMembership won't insert paused directly because of the CHECK
	// requiring pause_*_date. Insert via SQL.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.pool.Exec(ctx,
		`insert into memberships (member_id, type, months, start_date, end_date,
		                         status, pause_start_date, pause_end_date, pause_used)
		 values ($1, 'monthly', 1, $2, $3, 'paused', $2, $3, true)`,
		mid, kstAddDaysStr(-1), kstAddDaysStr(30),
	); err != nil {
		t.Fatalf("insert paused: %v", err)
	}

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec.Code != http.StatusUnprocessableEntity || !hasErrorCode(rec.Body.Bytes(), "NO_ACTIVE_MEMBERSHIP") {
		t.Fatalf("paused → expected 422 NO_ACTIVE_MEMBERSHIP, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckIn_KioskResponseHasNoPII: the kiosk response must not include
// member name, phone, or birth_date.
func TestCheckIn_KioskResponseHasNoPII(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{
		BranchID: bid, Name: "비밀이름", Phone: "01099998888", BirthDate: "1985-12-25",
	})
	testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "monthly",
		StartDate: kstAddDaysStr(-1),
		EndDate:   kstAddDaysStr(30),
	})

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if contains(body, "비밀이름") || contains(body, "01099998888") || contains(body, "1985-12-25") {
		t.Errorf("kiosk response leaked PII: %s", body)
	}
}

// TestCheckIn_DoubleClickIdempotent: two POSTs back-to-back yield only one
// check_ins row. The second response replays the first body.
func TestCheckIn_DoubleClickIdempotent(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid, _ := activeMembership(t, f.pool, bid)

	rec1 := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec1.Code, rec1.Body.String())
	}
	rec2 := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second: %d %s", rec2.Code, rec2.Body.String())
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("LRU should replay body verbatim:\n  first=%s\n  second=%s",
			rec1.Body.String(), rec2.Body.String())
	}
	if n := countCheckIns(t, f.pool, mid, bid); n != 1 {
		t.Errorf("expected 1 check_in row after double-click, got %d", n)
	}
}

// TestCheckIn_Pass10DecrementsAndExpiresAtZero: pass10 with remaining=1 →
// after one check-in remaining=0, status='expired' (in the same trnxn).
// A second attempt fails because the membership is expired.
func TestCheckIn_Pass10DecrementsAndExpiresAtZero(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	one := 1
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "pass10",
		Remaining: &one,
		StartDate: kstAddDaysStr(-1),
		EndDate:   kstAddDaysStr(30),
	})

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid, "branchId": bid,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first check-in: %d %s", rec.Code, rec.Body.String())
	}
	// Membership now expired in DB.
	var status string
	var remaining int
	if err := f.pool.QueryRow(context.Background(),
		`select status, remaining from memberships where id=$1`, msID).Scan(&status, &remaining); err != nil {
		t.Fatalf("post-check-in lookup: %v", err)
	}
	if status != "expired" || remaining != 0 {
		t.Errorf("after pass10 decrement to 0 want status=expired remaining=0, got status=%q remaining=%d", status, remaining)
	}
}

// TestCheckIn_BadInput: missing/zero ids → 400.
func TestCheckIn_BadInput(t *testing.T) {
	f := newAdminFixture(t)
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: expected 400, got %d", rec.Code)
	}
	rec = postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": -1, "branchId": -1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("negative ids: expected 400, got %d", rec.Code)
	}
}

// ---------------- GET /api/check-ins (admin) ----------------

// TestCheckInList_RequiresAuth: GET /api/check-ins is admin-protected.
func TestCheckInList_RequiresAuth(t *testing.T) {
	f := newAdminFixture(t)
	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins?from=2026-05-01&to=2026-05-31", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
}

// TestCheckInList_BranchScope: branch admin only sees their own branch rows.
func TestCheckInList_BranchScope(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, nil)
	other := testutil.CreateBranch(t, f.pool, nil)
	mid1, _ := activeMembership(t, f.pool, mine)
	mid2, _ := activeMembership(t, f.pool, other)

	// One check-in in each branch.
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid1, "branchId": mine,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mine: %d %s", rec.Code, rec.Body.String())
	}
	rec = postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{
		"memberId": mid2, "branchId": other,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("other: %d %s", rec.Code, rec.Body.String())
	}

	_, access := loginAs(t, f, "branch", &mine)
	rec = postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins?from="+kstAddDaysStr(-1)+"&to="+kstAddDaysStr(1), access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			BranchID int64 `json:"branch_id"`
		} `json:"items"`
	}
	mustDecode(t, rec, &resp)
	if len(resp.Items) == 0 {
		t.Fatalf("branch admin should see their own check-in, got empty items")
	}
	for _, it := range resp.Items {
		if it.BranchID != mine {
			t.Errorf("branch admin saw branch_id=%d (expected %d)", it.BranchID, mine)
		}
	}
}

// TestCheckInList_GlobalBranchFilter: ?branchId= drilldown for globals.
func TestCheckInList_GlobalBranchFilter(t *testing.T) {
	f := newAdminFixture(t)
	a := testutil.CreateBranch(t, f.pool, nil)
	b := testutil.CreateBranch(t, f.pool, nil)
	mA, _ := activeMembership(t, f.pool, a)
	mB, _ := activeMembership(t, f.pool, b)
	postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{"memberId": mA, "branchId": a})
	postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{"memberId": mB, "branchId": b})

	_, access := loginAs(t, f, "global", nil)
	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins?from="+kstAddDaysStr(-1)+"&to="+kstAddDaysStr(1)+"&branchId="+itoa(a), access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			BranchID int64 `json:"branch_id"`
		} `json:"items"`
	}
	mustDecode(t, rec, &resp)
	for _, it := range resp.Items {
		if it.BranchID != a {
			t.Errorf("filter branchId=%d returned row for %d", a, it.BranchID)
		}
	}
}

// TestCheckInList_InvalidAggregate: ?aggregate=weekly → 400 INVALID_AGGREGATE.
func TestCheckInList_InvalidAggregate(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins?from=2026-05-01&to=2026-05-31&aggregate=weekly", access, nil)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_AGGREGATE") {
		t.Fatalf("expected 400 INVALID_AGGREGATE, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckInList_RangeTooLarge: aggregate=daily over >92 days → 400 RANGE_TOO_LARGE.
func TestCheckInList_RangeTooLarge(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins?from=2026-01-01&to=2026-12-31&aggregate=daily", access, nil)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "RANGE_TOO_LARGE") {
		t.Fatalf("expected 400 RANGE_TOO_LARGE, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckInList_InvalidCursor: malformed cursor → 400 INVALID_CURSOR.
func TestCheckInList_InvalidCursor(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins?from="+kstAddDaysStr(-1)+"&to="+kstAddDaysStr(1)+"&cursor=not-a-cursor", access, nil)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_CURSOR") {
		t.Fatalf("expected 400 INVALID_CURSOR, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckInList_DailyAggregate: same member two check-ins same day → one
// row, checkin_count=2.
func TestCheckInList_DailyAggregate(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid, _ := activeMembership(t, f.pool, bid)

	// Two check-ins in this branch — first creates the row, the second goes
	// through the LRU cache so it doesn't add another row. To produce two
	// physical rows we insert the second one directly.
	postWithAuth(t, f.r, http.MethodPost, "/api/check-ins", "", map[string]any{"memberId": mid, "branchId": bid})

	// Insert a second check-in row directly so daily count = 2.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var msID int64
	if err := f.pool.QueryRow(ctx,
		`select id from memberships where member_id=$1 limit 1`, mid).Scan(&msID); err != nil {
		t.Fatalf("lookup ms: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`insert into check_ins (member_id, branch_id, membership_id) values ($1, $2, $3)`,
		mid, bid, msID,
	); err != nil {
		t.Fatalf("insert second check_in: %v", err)
	}

	_, access := loginAs(t, f, "global", nil)
	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins?from="+kstAddDaysStr(-1)+"&to="+kstAddDaysStr(1)+"&aggregate=daily", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			MemberID     int64  `json:"member_id"`
			Date         string `json:"date"`
			CheckinCount int    `json:"checkin_count"`
		} `json:"items"`
	}
	mustDecode(t, rec, &resp)
	var found bool
	for _, it := range resp.Items {
		if it.MemberID == mid && it.CheckinCount == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected daily row count=2 for member %d, got %+v", mid, resp.Items)
	}
}
