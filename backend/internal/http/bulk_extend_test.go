//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// validIdempotencyKey / altIdempotencyKey are stable UUIDv4 values used
// across the positive / conflict cases. The shape is what idempotency.ValidateKey
// requires: 8-4-4-4-12 hex with version=4 and variant in 8/9/a/b.
const (
	validIdempotencyKey = "11111111-1111-4111-8111-111111111111"
	altIdempotencyKey   = "22222222-2222-4222-8222-222222222222"
	thirdIdempotencyKey = "33333333-3333-4333-8333-333333333333"
)

// postBulk sends a POST /api/memberships/bulk-extend with the given access
// token and Idempotency-Key (use empty string to omit the header).
func postBulk(t *testing.T, f *adminFixture, access, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/memberships/bulk-extend", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

// seedActiveMonthly inserts a member + a covering monthly membership so
// bulk-extend has a touchable target.
func seedActiveMonthly(t *testing.T, f *adminFixture, branchID int64, startDate, endDate string) (memberID, membershipID int64) {
	t.Helper()
	memberID = testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: branchID})
	membershipID = testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID:  memberID,
		Type:      "monthly",
		StartDate: startDate,
		EndDate:   endDate,
		Status:    "active",
	})
	return
}

func endDateOf(t *testing.T, f *adminFixture, membershipID int64) string {
	t.Helper()
	var d time.Time
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.pool.QueryRow(ctx, `select end_date from memberships where id=$1`, membershipID).Scan(&d); err != nil {
		t.Fatalf("end_date lookup: %v", err)
	}
	return d.Format("2006-01-02")
}

// TestBulkExtend_BranchAdminForbidden: only role='global' may call.
func TestBulkExtend_BranchAdminForbidden(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "branch", &bid)

	rec := postBulk(t, f, access, validIdempotencyKey, map[string]any{
		"days": 7, "reason": "test",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBulkExtend_RequiresIdempotencyKey: header missing → 400 IDEMPOTENCY_KEY_REQUIRED.
func TestBulkExtend_RequiresIdempotencyKey(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	rec := postBulk(t, f, access, "", map[string]any{
		"days": 7, "reason": "test",
	})
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Fatalf("expected 400 IDEMPOTENCY_KEY_REQUIRED, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBulkExtend_InvalidIdempotencyKey: malformed UUID → 400 INVALID_IDEMPOTENCY_KEY.
func TestBulkExtend_InvalidIdempotencyKey(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	rec := postBulk(t, f, access, "not-a-uuid", map[string]any{
		"days": 7, "reason": "test",
	})
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_IDEMPOTENCY_KEY") {
		t.Fatalf("expected 400 INVALID_IDEMPOTENCY_KEY, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBulkExtend_InvalidExtendDays covers the 0 / negative / >90 cases.
func TestBulkExtend_InvalidExtendDays(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	cases := []int{0, -1, 91, 1000}
	for i, days := range cases {
		key := validIdempotencyKey
		if i == 1 {
			key = altIdempotencyKey
		}
		if i == 2 {
			key = thirdIdempotencyKey
		}
		if i == 3 {
			key = "44444444-4444-4444-8444-444444444444"
		}
		rec := postBulk(t, f, access, key, map[string]any{
			"days": days, "reason": "x",
		})
		if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_EXTEND_DAYS") {
			t.Errorf("days=%d: expected 400 INVALID_EXTEND_DAYS, got %d body=%s",
				days, rec.Code, rec.Body.String())
		}
	}
}

// TestBulkExtend_AppliesPlusDays: end_date += days for active rows.
func TestBulkExtend_AppliesPlusDays(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, msID := seedActiveMonthly(t, f, bid, "2026-05-01", "2026-06-01")
	_, access := loginAs(t, f, "global", nil)

	rec := postBulk(t, f, access, validIdempotencyKey, map[string]any{
		"days": 7, "reason": "연휴 보상",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ExtendedCount int `json:"extended_count"`
	}
	mustDecode(t, rec, &resp)
	if resp.ExtendedCount < 1 {
		t.Errorf("extended_count=%d want >=1", resp.ExtendedCount)
	}
	if got := endDateOf(t, f, msID); got != "2026-06-08" {
		t.Errorf("end_date=%s want 2026-06-08", got)
	}
}

// TestBulkExtend_IdempotencyReplay: same key + same body twice → second
// replays first response without applying days a second time.
func TestBulkExtend_IdempotencyReplay(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, msID := seedActiveMonthly(t, f, bid, "2026-05-01", "2026-06-01")
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{"days": 5, "reason": "test"}
	rec1 := postBulk(t, f, access, validIdempotencyKey, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first: %d %s", rec1.Code, rec1.Body.String())
	}
	rec2 := postBulk(t, f, access, validIdempotencyKey, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second: %d %s", rec2.Code, rec2.Body.String())
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("idempotency replay should match byte-for-byte:\n  first=%s\n  second=%s",
			rec1.Body.String(), rec2.Body.String())
	}
	if got := endDateOf(t, f, msID); got != "2026-06-06" {
		t.Errorf("end_date=%s want 2026-06-06 (only one apply)", got)
	}
}

// TestBulkExtend_IdempotencyConflict: same key + different body → 409
// IDEMPOTENCY_KEY_CONFLICT.
func TestBulkExtend_IdempotencyConflict(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	seedActiveMonthly(t, f, bid, "2026-05-01", "2026-06-01")
	_, access := loginAs(t, f, "global", nil)

	rec1 := postBulk(t, f, access, validIdempotencyKey, map[string]any{"days": 7, "reason": "x"})
	if rec1.Code != http.StatusOK {
		t.Fatalf("first: %d %s", rec1.Code, rec1.Body.String())
	}
	rec2 := postBulk(t, f, access, validIdempotencyKey, map[string]any{"days": 5, "reason": "x"})
	if rec2.Code != http.StatusConflict || !hasErrorCode(rec2.Body.Bytes(), "IDEMPOTENCY_KEY_CONFLICT") {
		t.Fatalf("expected 409 IDEMPOTENCY_KEY_CONFLICT, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

// TestBulkExtend_BranchFilter: branch_id filter scopes target rows.
func TestBulkExtend_BranchFilter(t *testing.T) {
	f := newAdminFixture(t)
	a := testutil.CreateBranch(t, f.pool, nil)
	b := testutil.CreateBranch(t, f.pool, nil)
	_, mAID := seedActiveMonthly(t, f, a, "2026-05-01", "2026-06-01")
	_, mBID := seedActiveMonthly(t, f, b, "2026-05-01", "2026-06-01")
	_, access := loginAs(t, f, "global", nil)

	rec := postBulk(t, f, access, validIdempotencyKey, map[string]any{
		"branch_id": a, "days": 7, "reason": "branch-A only",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := endDateOf(t, f, mAID); got != "2026-06-08" {
		t.Errorf("branch A end_date=%s want 2026-06-08", got)
	}
	if got := endDateOf(t, f, mBID); got != "2026-06-01" {
		t.Errorf("branch B end_date=%s should be unchanged", got)
	}
}

// TestBulkExtend_OverlapConflict: a future-start membership exists; the
// extension would push the active row's end into it → 409 with
// first_conflict_membership_id and full rollback (other rows unchanged).
func TestBulkExtend_OverlapConflict(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid, msID := seedActiveMonthly(t, f, bid, "2026-05-01", "2026-06-01")
	// Future membership starts the day after the active one ends.
	futureID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID:  mid,
		Type:      "monthly",
		StartDate: "2026-06-02",
		EndDate:   "2026-07-02",
		Status:    "active",
	})

	// Independent member untouched by the conflict.
	other := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	otherID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID:  other,
		Type:      "monthly",
		StartDate: "2026-05-01",
		EndDate:   "2026-06-01",
		Status:    "active",
	})

	_, access := loginAs(t, f, "global", nil)
	rec := postBulk(t, f, access, validIdempotencyKey, map[string]any{
		"days": 7, "reason": "overlap test",
	})
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "MEMBERSHIP_PERIOD_OVERLAP") {
		t.Fatalf("expected 409 MEMBERSHIP_PERIOD_OVERLAP, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		FirstConflictMembershipID int64 `json:"first_conflict_membership_id"`
	}
	mustDecode(t, rec, &resp)
	if resp.FirstConflictMembershipID != msID {
		t.Errorf("first_conflict_membership_id=%d want %d (the row whose UPDATE failed)",
			resp.FirstConflictMembershipID, msID)
	}
	// Rollback: every membership keeps its original end_date.
	if got := endDateOf(t, f, msID); got != "2026-06-01" {
		t.Errorf("active end_date=%s want unchanged 2026-06-01", got)
	}
	if got := endDateOf(t, f, futureID); got != "2026-07-02" {
		t.Errorf("future end_date=%s want unchanged 2026-07-02", got)
	}
	if got := endDateOf(t, f, otherID); got != "2026-06-01" {
		t.Errorf("other-member end_date=%s want unchanged 2026-06-01", got)
	}
}
