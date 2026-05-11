//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// ---- date helpers ----
//
// kstAddDaysStr is defined in checkins_test.go (shared in this package).

// kstAddMonthsStr returns today (KST) + months formatted "YYYY-MM-DD".
func kstAddMonthsStr(months int) string {
	kstNow := time.Now().In(util.KST)
	today := time.Date(kstNow.Year(), kstNow.Month(), kstNow.Day(), 0, 0, 0, 0, time.UTC)
	return today.AddDate(0, months, 0).Format("2006-01-02")
}

// ---- HTTP helpers ----

// postMembership sends a POST/GET to a membership endpoint with optional
// Authorization and Idempotency-Key headers. Pass empty strings to omit.
func postMembership(t *testing.T, f *adminFixture, method, path, access, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body == nil {
		rdr = bytes.NewReader([]byte("{}"))
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
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

// insertPaymentRow inserts a positive payment row so subsequent refund
// flows have an original grant payment to mirror.
func insertPaymentRow(t *testing.T, pool *pgxpool.Pool, membershipID, branchID, performedBy int64, amount int, method string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kstNow := time.Now().In(util.KST)
	today := time.Date(kstNow.Year(), kstNow.Month(), kstNow.Day(), 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`insert into payments (membership_id, branch_id, amount, method, paid_at, performed_by)
		 values ($1, $2, $3, $4, $5, $6)`,
		membershipID, branchID, amount, method, today, performedBy,
	); err != nil {
		t.Fatalf("insertPaymentRow: %v", err)
	}
}

// Stable UUIDv4-shaped keys used across grant / refund cases.
const (
	membershipsKey1 = "55555555-5555-4555-8555-555555555555"
	membershipsKey2 = "66666666-6666-4666-8666-666666666666"
	membershipsKey3 = "77777777-7777-4777-8777-777777777777"
	membershipsKey4 = "88888888-8888-4888-8888-888888888888"
	membershipsKey5 = "99999999-9999-4999-8999-999999999999"
	membershipsKey6 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab"
	membershipsKey7 = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbc"
	membershipsKey8 = "cccccccc-cccc-4ccc-8ccc-ccccccccccca"
)

// grantPath returns the URL for POST /api/members/:id/memberships.
func grantPath(memberID int64) string {
	return "/api/members/" + itoa(memberID) + "/memberships"
}

// membershipPath returns the base URL for /api/memberships/:id endpoints.
func membershipPath(id int64) string {
	return "/api/memberships/" + itoa(id)
}

// ---------- Grant ----------

func TestMembership_Grant_Monthly_Success(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	start := kstAddDaysStr(0)
	body := map[string]any{
		"type":       "monthly",
		"months":     3,
		"start_date": start,
		"payment":    map[string]any{"amount": 150000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			ID        int64  `json:"id"`
			Type      string `json:"type"`
			Months    *int   `json:"months"`
			Remaining *int   `json:"remaining"`
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
			BranchID  int64  `json:"branch_id"`
			Status    string `json:"status"`
		} `json:"membership"`
		Payment struct {
			ID       int64  `json:"id"`
			Amount   int    `json:"amount"`
			Method   string `json:"method"`
			BranchID int64  `json:"branch_id"`
			PaidAt   string `json:"paid_at"`
		} `json:"payment"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Type != "monthly" || resp.Membership.Months == nil || *resp.Membership.Months != 3 {
		t.Errorf("type/months drift: %+v", resp.Membership)
	}
	want := kstAddMonthsStr(3)
	if resp.Membership.EndDate != want {
		t.Errorf("end_date=%s want %s", resp.Membership.EndDate, want)
	}
	if resp.Payment.Amount != 150000 || resp.Payment.Method != "card" {
		t.Errorf("payment drift: %+v", resp.Payment)
	}
	if resp.Payment.BranchID != bid {
		t.Errorf("payment.branch_id=%d want %d", resp.Payment.BranchID, bid)
	}
	if resp.Payment.PaidAt != kstAddDaysStr(0) {
		t.Errorf("payment.paid_at=%s want %s (KST today)", resp.Payment.PaidAt, kstAddDaysStr(0))
	}
}

func TestMembership_Grant_Pass10_Success(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type":       "pass10",
		"start_date": kstAddDaysStr(0),
		"payment":    map[string]any{"amount": 100000, "method": "cash"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			Type      string `json:"type"`
			Remaining *int   `json:"remaining"`
			EndDate   string `json:"end_date"`
		} `json:"membership"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Type != "pass10" || resp.Membership.Remaining == nil || *resp.Membership.Remaining != 10 {
		t.Errorf("pass10 drift: %+v", resp.Membership)
	}
	if resp.Membership.EndDate != kstAddMonthsStr(2) {
		t.Errorf("end_date=%s want %s", resp.Membership.EndDate, kstAddMonthsStr(2))
	}
}

func TestMembership_Grant_IdempotencyReplay(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type":       "monthly",
		"months":     1,
		"start_date": kstAddDaysStr(0),
		"payment":    map[string]any{"amount": 50000, "method": "card"},
	}
	rec1 := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	rec2 := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("replay should match byte-for-byte:\n  first=%s\n  second=%s", rec1.Body.String(), rec2.Body.String())
	}
	// Only one membership row should exist for this member.
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`select count(*) from memberships where member_id=$1`, mid).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("membership count=%d want 1 (idempotent replay must not insert again)", count)
	}
}

func TestMembership_Grant_IdempotencyConflict(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	body1 := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
		"payment": map[string]any{"amount": 50000, "method": "card"},
	}
	body2 := map[string]any{
		"type": "monthly", "months": 6, "start_date": kstAddDaysStr(0),
		"payment": map[string]any{"amount": 200000, "method": "card"},
	}
	rec1 := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec1.Code, rec1.Body.String())
	}
	rec2 := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body2)
	if rec2.Code != http.StatusConflict || !hasErrorCode(rec2.Body.Bytes(), "IDEMPOTENCY_KEY_CONFLICT") {
		t.Fatalf("expected 409 IDEMPOTENCY_KEY_CONFLICT, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestMembership_Grant_IdempotencyKeyRequired(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
		"payment": map[string]any{"amount": 50000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, "", body)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Fatalf("expected 400 IDEMPOTENCY_KEY_REQUIRED, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Grant_InvalidIdempotencyKey(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
		"payment": map[string]any{"amount": 50000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, "not-a-uuid", body)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_IDEMPOTENCY_KEY") {
		t.Fatalf("expected 400 INVALID_IDEMPOTENCY_KEY, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Grant_InvalidStartDate(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(-1), // yesterday
		"payment": map[string]any{"amount": 50000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_START_DATE") {
		t.Fatalf("expected 400 INVALID_START_DATE, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Grant_InvalidAmount(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	for i, amount := range []int{0, -1} {
		key := membershipsKey1
		if i == 1 {
			key = membershipsKey2
		}
		body := map[string]any{
			"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
			"payment": map[string]any{"amount": amount, "method": "card"},
		}
		rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, key, body)
		if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_AMOUNT") {
			t.Errorf("amount=%d: expected 400 INVALID_AMOUNT, got %d %s", amount, rec.Code, rec.Body.String())
		}
	}
}

func TestMembership_Grant_InvalidInput(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	cases := []struct {
		name     string
		body     map[string]any
		key      string
		wantCode string
	}{
		{
			name: "bad_type",
			body: map[string]any{"type": "quarterly", "start_date": kstAddDaysStr(0),
				"payment": map[string]any{"amount": 1000, "method": "card"}},
			key:      membershipsKey1,
			wantCode: "INVALID_INPUT",
		},
		{
			name: "missing_months",
			body: map[string]any{"type": "monthly", "start_date": kstAddDaysStr(0),
				"payment": map[string]any{"amount": 1000, "method": "card"}},
			key:      membershipsKey2,
			wantCode: "INVALID_MONTHS",
		},
		{
			name: "zero_months",
			body: map[string]any{"type": "monthly", "months": 0, "start_date": kstAddDaysStr(0),
				"payment": map[string]any{"amount": 1000, "method": "card"}},
			key:      membershipsKey3,
			wantCode: "INVALID_MONTHS",
		},
		{
			name: "bad_method",
			body: map[string]any{"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
				"payment": map[string]any{"amount": 1000, "method": "bitcoin"}},
			key:      membershipsKey4,
			wantCode: "INVALID_INPUT",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, tc.key, tc.body)
			if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), tc.wantCode) {
				t.Errorf("expected 400 %s, got %d %s", tc.wantCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMembership_Grant_OtherBranchMember_404(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, nil)
	other := testutil.CreateBranch(t, f.pool, nil)
	otherMember := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: other})
	_, access := loginAs(t, f, "branch", &mine)

	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
		"payment": map[string]any{"amount": 1000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(otherMember), access, membershipsKey1, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Grant_SoftDeletedMember_404(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	if _, err := f.pool.Exec(context.Background(),
		`update members set deleted_at=now() where id=$1`, mid); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
		"payment": map[string]any{"amount": 1000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Grant_PeriodOverlap(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	// Seed an active membership covering today..today+30.
	testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	// New grant whose start (today+5) falls inside the existing window.
	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(5),
		"payment": map[string]any{"amount": 1000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "MEMBERSHIP_PERIOD_OVERLAP") {
		t.Fatalf("expected 409 MEMBERSHIP_PERIOD_OVERLAP, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Grant_FutureNoOverlap_OK(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	// Existing active membership today..today+30.
	testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	// New grant starting one day after the existing membership ends.
	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(31),
		"payment": map[string]any{"amount": 1000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("future no-overlap should succeed, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Grant_IgnoresClientPaidAtAndBranchID(t *testing.T) {
	f := newAdminFixture(t)
	memberBranch := testutil.CreateBranch(t, f.pool, nil)
	otherBranch := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: memberBranch})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type":       "monthly",
		"months":     1,
		"start_date": kstAddDaysStr(0),
		"payment": map[string]any{
			"amount":    1000,
			"method":    "card",
			"paid_at":   kstAddDaysStr(-5),
			"branch_id": otherBranch,
		},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Payment struct {
			PaidAt   string `json:"paid_at"`
			BranchID int64  `json:"branch_id"`
		} `json:"payment"`
	}
	mustDecode(t, rec, &resp)
	if resp.Payment.PaidAt != kstAddDaysStr(0) {
		t.Errorf("paid_at=%s want %s (server overrides client value)", resp.Payment.PaidAt, kstAddDaysStr(0))
	}
	if resp.Payment.BranchID != memberBranch {
		t.Errorf("branch_id=%d want %d (server uses member branch, ignores client)", resp.Payment.BranchID, memberBranch)
	}
}

func TestMembership_Grant_TimestampKSTOffset(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"type": "monthly", "months": 1, "start_date": kstAddDaysStr(0),
		"payment": map[string]any{"amount": 1000, "method": "card"},
	}
	rec := postMembership(t, f, http.MethodPost, grantPath(mid), access, membershipsKey1, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			CreatedAt string `json:"created_at"`
		} `json:"membership"`
		Payment struct {
			CreatedAt string `json:"created_at"`
		} `json:"payment"`
	}
	mustDecode(t, rec, &resp)
	if !strings.HasSuffix(resp.Membership.CreatedAt, "+09:00") {
		t.Errorf("membership.created_at must end with +09:00, got %q", resp.Membership.CreatedAt)
	}
	if !strings.HasSuffix(resp.Payment.CreatedAt, "+09:00") {
		t.Errorf("payment.created_at must end with +09:00, got %q", resp.Payment.CreatedAt)
	}
}

// ---------- Get ----------

func TestMembership_Get_Success(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	adminID, access := loginAs(t, f, "global", nil)
	insertPaymentRow(t, f.pool, msID, bid, adminID, 150000, "card")

	rec := postMembership(t, f, http.MethodGet, membershipPath(msID), access, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			ID       int64 `json:"id"`
			BranchID int64 `json:"branch_id"`
		} `json:"membership"`
		Payments []struct {
			Amount int `json:"amount"`
		} `json:"payments"`
		Events []any `json:"events"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.ID != msID || resp.Membership.BranchID != bid {
		t.Errorf("membership drift: %+v", resp.Membership)
	}
	if len(resp.Payments) != 1 || resp.Payments[0].Amount != 150000 {
		t.Errorf("payments drift: %+v", resp.Payments)
	}
	if len(resp.Events) != 0 {
		t.Errorf("events should be empty, got %+v", resp.Events)
	}
}

func TestMembership_Get_AfterRefund_HasNegativePayment(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	adminID, access := loginAs(t, f, "global", nil)
	insertPaymentRow(t, f.pool, msID, bid, adminID, 150000, "card")

	// Refund
	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, map[string]any{"reason": "test"})
	if rec.Code != http.StatusOK {
		t.Fatalf("refund status=%d body=%s", rec.Code, rec.Body.String())
	}

	// GET
	rec = postMembership(t, f, http.MethodGet, membershipPath(msID), access, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			Status string `json:"status"`
		} `json:"membership"`
		Payments []struct {
			Amount int `json:"amount"`
		} `json:"payments"`
		Events []struct {
			Action string `json:"action"`
		} `json:"events"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Status != "refunded" {
		t.Errorf("status=%s want refunded", resp.Membership.Status)
	}
	if len(resp.Payments) != 2 {
		t.Errorf("payments count=%d want 2 (grant + refund)", len(resp.Payments))
	}
	hasNeg := false
	for _, p := range resp.Payments {
		if p.Amount < 0 {
			hasNeg = true
		}
	}
	if !hasNeg {
		t.Errorf("expected at least one negative payment row, got %+v", resp.Payments)
	}
	hasRefund := false
	for _, e := range resp.Events {
		if e.Action == "refund" {
			hasRefund = true
		}
	}
	if !hasRefund {
		t.Errorf("expected refund event, got %+v", resp.Events)
	}
}

func TestMembership_Get_OtherBranch_404(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, nil)
	other := testutil.CreateBranch(t, f.pool, nil)
	otherMember := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: other})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: otherMember, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "branch", &mine)

	rec := postMembership(t, f, http.MethodGet, membershipPath(msID), access, "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Get_NotExist_404(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	rec := postMembership(t, f, http.MethodGet, "/api/memberships/999999", access, "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- Pause ----------

func TestMembership_Pause_Immediate_Success(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"start_date": kstAddDaysStr(0),
		"end_date":   kstAddDaysStr(7),
		"reason":     "여행",
	}
	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/pause", access, "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			Status  string `json:"status"`
			EndDate string `json:"end_date"`
		} `json:"membership"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Status != "paused" {
		t.Errorf("status=%s want paused (start_date <= today)", resp.Membership.Status)
	}
	// end_date extended by 7 days (pause window).
	if resp.Membership.EndDate != kstAddDaysStr(37) {
		t.Errorf("end_date=%s want %s", resp.Membership.EndDate, kstAddDaysStr(37))
	}
}

func TestMembership_Pause_Future_StaysActive(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"start_date": kstAddDaysStr(5),
		"end_date":   kstAddDaysStr(10),
		"reason":     "예약",
	}
	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/pause", access, "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			Status         string  `json:"status"`
			EndDate        string  `json:"end_date"`
			PauseUsed      bool    `json:"pause_used"`
			PauseStartDate *string `json:"pause_start_date"`
		} `json:"membership"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Status != "active" {
		t.Errorf("status=%s want active (future pause not yet reached)", resp.Membership.Status)
	}
	if !resp.Membership.PauseUsed {
		t.Errorf("pause_used should be true after registering a pause")
	}
	if resp.Membership.PauseStartDate == nil || *resp.Membership.PauseStartDate != kstAddDaysStr(5) {
		t.Errorf("pause_start_date drift: %+v", resp.Membership.PauseStartDate)
	}
	// end_date extended by 5 days (pause window).
	if resp.Membership.EndDate != kstAddDaysStr(35) {
		t.Errorf("end_date=%s want %s", resp.Membership.EndDate, kstAddDaysStr(35))
	}
}

func TestMembership_Pause_AlreadyUsed(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	if _, err := f.pool.Exec(context.Background(),
		`update memberships set pause_used=true where id=$1`, msID); err != nil {
		t.Fatalf("flip pause_used: %v", err)
	}
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"start_date": kstAddDaysStr(0), "end_date": kstAddDaysStr(5),
		"reason": "retry",
	}
	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/pause", access, "", body)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "PAUSE_ALREADY_USED") {
		t.Fatalf("expected 409 PAUSE_ALREADY_USED, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Pause_InvalidPauseRange(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})

	// Membership with future start so we can hit "pause start < membership start".
	msFuture := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(10), EndDate: kstAddDaysStr(40), Status: "active",
	})

	// Independent member so we can test other variants without overlap.
	mid2 := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msNow := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid2, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(10), Status: "active",
	})

	_, access := loginAs(t, f, "global", nil)

	cases := []struct {
		name  string
		ms    int64
		start string
		end   string
	}{
		{"start_after_end", msNow, kstAddDaysStr(5), kstAddDaysStr(3)},
		{"start_before_today", msNow, kstAddDaysStr(-1), kstAddDaysStr(2)},
		{"start_before_ms_start", msFuture, kstAddDaysStr(5), kstAddDaysStr(8)},
		{"end_after_ms_end", msNow, kstAddDaysStr(1), kstAddDaysStr(99)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"start_date": tc.start, "end_date": tc.end, "reason": "x",
			}
			rec := postMembership(t, f, http.MethodPost,
				membershipPath(tc.ms)+"/pause", access, "", body)
			if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_PAUSE_RANGE") {
				t.Errorf("expected 400 INVALID_PAUSE_RANGE, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMembership_Pause_OverlapWithFuture(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	// Active membership today..today+10. Pause extends end_date by 7 → today+17.
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(10), Status: "active",
	})
	// Future membership today+11..today+30: pause extension would push end into it.
	futureID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(11), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"start_date": kstAddDaysStr(0), "end_date": kstAddDaysStr(7),
		"reason": "overlap",
	}
	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/pause", access, "", body)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "MEMBERSHIP_PERIOD_OVERLAP") {
		t.Fatalf("expected 409 MEMBERSHIP_PERIOD_OVERLAP, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Sanity: future membership end_date untouched.
	var got time.Time
	if err := f.pool.QueryRow(context.Background(),
		`select end_date from memberships where id=$1`, futureID).Scan(&got); err != nil {
		t.Fatalf("end_date lookup: %v", err)
	}
	if got.Format("2006-01-02") != kstAddDaysStr(30) {
		t.Errorf("future end_date=%s want unchanged %s", got.Format("2006-01-02"), kstAddDaysStr(30))
	}
}

func TestMembership_Pause_NotFound(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	body := map[string]any{
		"start_date": kstAddDaysStr(0), "end_date": kstAddDaysStr(3), "reason": "x",
	}
	rec := postMembership(t, f, http.MethodPost, "/api/memberships/999999/pause", access, "", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- Unpause ----------

func TestMembership_Unpause_Success(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	// Original end_date = today+30. Pause today-2..today+5 (7 days) extended
	// to today+37 at registration. Unpausing today: end_date -= (today+5 -
	// today) = -5 days → today+32.
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(-5), EndDate: kstAddDaysStr(37), Status: "active",
	})
	// CHECK constraint: status='paused' requires pause_*_date NOT NULL,
	// so set them together with the status flip in one UPDATE.
	if _, err := f.pool.Exec(context.Background(), `
		update memberships
		set pause_start_date=$2::date, pause_end_date=$3::date,
		    pause_used=true, status='paused'
		where id=$1`,
		msID, kstAddDaysStr(-2), kstAddDaysStr(5)); err != nil {
		t.Fatalf("seed pause: %v", err)
	}
	_, access := loginAs(t, f, "global", nil)

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/unpause",
		access, "", map[string]any{"reason": "early return"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			Status         string  `json:"status"`
			EndDate        string  `json:"end_date"`
			PauseStartDate *string `json:"pause_start_date"`
		} `json:"membership"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Status != "active" {
		t.Errorf("status=%s want active", resp.Membership.Status)
	}
	if resp.Membership.PauseStartDate != nil {
		t.Errorf("pause_start_date=%v want nil", resp.Membership.PauseStartDate)
	}
	// end_date = today+37 - (today+5 - today-2) - (today+5 - today)
	//          = today+37 - 7 - 5 = today+25.
	if resp.Membership.EndDate != kstAddDaysStr(25) {
		t.Errorf("end_date=%s want %s", resp.Membership.EndDate, kstAddDaysStr(25))
	}
}

func TestMembership_Unpause_NotPaused(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/unpause",
		access, "", map[string]any{"reason": "x"})
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "NOT_PAUSED") {
		t.Fatalf("expected 409 NOT_PAUSED, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Unpause_NotFound(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	rec := postMembership(t, f, http.MethodPost, "/api/memberships/999999/unpause",
		access, "", map[string]any{"reason": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- CancelPause ----------

func TestMembership_CancelPause_Success(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	// Active membership, future pause 5..10 registered (end already extended by 5).
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(35), Status: "active",
	})
	if _, err := f.pool.Exec(context.Background(), `
		update memberships
		set pause_start_date=$2::date, pause_end_date=$3::date, pause_used=true
		where id=$1`,
		msID, kstAddDaysStr(5), kstAddDaysStr(10)); err != nil {
		t.Fatalf("seed pause: %v", err)
	}
	_, access := loginAs(t, f, "global", nil)

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/cancel-pause",
		access, "", map[string]any{"reason": "철회"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			Status         string  `json:"status"`
			EndDate        string  `json:"end_date"`
			PauseUsed      bool    `json:"pause_used"`
			PauseStartDate *string `json:"pause_start_date"`
		} `json:"membership"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Status != "active" {
		t.Errorf("status=%s want active", resp.Membership.Status)
	}
	if resp.Membership.PauseUsed {
		t.Errorf("pause_used=true want false (re-enable future pause)")
	}
	if resp.Membership.PauseStartDate != nil {
		t.Errorf("pause_start_date=%v want nil", resp.Membership.PauseStartDate)
	}
	// end_date = today+35 - 5 = today+30 (restored)
	if resp.Membership.EndDate != kstAddDaysStr(30) {
		t.Errorf("end_date=%s want %s", resp.Membership.EndDate, kstAddDaysStr(30))
	}
}

func TestMembership_CancelPause_NotScheduled(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})

	cases := []struct {
		name   string
		setup  func(t *testing.T) int64
	}{
		{
			name: "status_paused",
			setup: func(t *testing.T) int64 {
				m := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
				id := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
					MemberID: m, Type: "monthly",
					StartDate: kstAddDaysStr(-5), EndDate: kstAddDaysStr(30), Status: "active",
				})
				if _, err := f.pool.Exec(context.Background(), `
					update memberships
					set pause_start_date=$2::date, pause_end_date=$3::date,
					    pause_used=true, status='paused'
					where id=$1`,
					id, kstAddDaysStr(-2), kstAddDaysStr(5)); err != nil {
					t.Fatalf("seed: %v", err)
				}
				return id
			},
		},
		{
			name: "pause_not_used",
			setup: func(t *testing.T) int64 {
				return testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
					MemberID: mid, Type: "monthly",
					StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(40), Status: "active",
				})
			},
		},
	}
	_, access := loginAs(t, f, "global", nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.setup(t)
			rec := postMembership(t, f, http.MethodPost, membershipPath(id)+"/cancel-pause",
				access, "", map[string]any{"reason": "x"})
			if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "PAUSE_NOT_SCHEDULED") {
				t.Errorf("expected 409 PAUSE_NOT_SCHEDULED, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMembership_CancelPause_NotFound(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	rec := postMembership(t, f, http.MethodPost, "/api/memberships/999999/cancel-pause",
		access, "", map[string]any{"reason": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- Refund ----------

func TestMembership_Refund_Success(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	adminID, access := loginAs(t, f, "global", nil)
	insertPaymentRow(t, f.pool, msID, bid, adminID, 150000, "card")

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, map[string]any{"reason": "fully refunded"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Membership struct {
			Status string `json:"status"`
		} `json:"membership"`
		RefundPayment struct {
			Amount   int    `json:"amount"`
			Method   string `json:"method"`
			BranchID int64  `json:"branch_id"`
			PaidAt   string `json:"paid_at"`
		} `json:"refund_payment"`
	}
	mustDecode(t, rec, &resp)
	if resp.Membership.Status != "refunded" {
		t.Errorf("status=%s want refunded", resp.Membership.Status)
	}
	if resp.RefundPayment.Amount != -150000 || resp.RefundPayment.Method != "card" || resp.RefundPayment.BranchID != bid {
		t.Errorf("refund_payment drift: %+v", resp.RefundPayment)
	}
	if resp.RefundPayment.PaidAt != kstAddDaysStr(0) {
		t.Errorf("paid_at=%s want %s (KST today)", resp.RefundPayment.PaidAt, kstAddDaysStr(0))
	}
}

func TestMembership_Refund_PausedAllowed(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(-5), EndDate: kstAddDaysStr(30), Status: "active",
	})
	if _, err := f.pool.Exec(context.Background(), `
		update memberships
		set pause_start_date=$2::date, pause_end_date=$3::date,
		    pause_used=true, status='paused'
		where id=$1`,
		msID, kstAddDaysStr(-2), kstAddDaysStr(5)); err != nil {
		t.Fatalf("seed pause: %v", err)
	}
	adminID, access := loginAs(t, f, "global", nil)
	insertPaymentRow(t, f.pool, msID, bid, adminID, 100000, "cash")

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, map[string]any{"reason": "paused refund"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Refund_FutureStartAllowed(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(10), EndDate: kstAddDaysStr(40), Status: "active",
	})
	adminID, access := loginAs(t, f, "global", nil)
	insertPaymentRow(t, f.pool, msID, bid, adminID, 50000, "cash")

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, map[string]any{"reason": "future cancel"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Refund_Expired(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(-60), EndDate: kstAddDaysStr(-30), Status: "expired",
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, map[string]any{"reason": "late"})
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "MEMBERSHIP_ALREADY_EXPIRED") {
		t.Fatalf("expected 409 MEMBERSHIP_ALREADY_EXPIRED, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Refund_IdempotencyReplay(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	adminID, access := loginAs(t, f, "global", nil)
	insertPaymentRow(t, f.pool, msID, bid, adminID, 150000, "card")

	body := map[string]any{"reason": "test"}
	rec1 := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first: %d %s", rec1.Code, rec1.Body.String())
	}
	rec2 := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second: %d %s", rec2.Code, rec2.Body.String())
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("replay should match byte-for-byte:\n  first=%s\n  second=%s", rec1.Body.String(), rec2.Body.String())
	}
	// Only one refund (negative) payment row.
	var negCount int
	if err := f.pool.QueryRow(context.Background(),
		`select count(*) from payments where membership_id=$1 and amount<0`, msID).Scan(&negCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if negCount != 1 {
		t.Errorf("refund payment count=%d want 1 (replay must not insert again)", negCount)
	}
}

func TestMembership_Refund_IdempotencyKeyRequired(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, "", map[string]any{"reason": "x"})
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Fatalf("expected 400 IDEMPOTENCY_KEY_REQUIRED, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Refund_InvalidIdempotencyKey(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, "not-a-uuid", map[string]any{"reason": "x"})
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_IDEMPOTENCY_KEY") {
		t.Fatalf("expected 400 INVALID_IDEMPOTENCY_KEY, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_Refund_IgnoresClientFields(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	adminID, access := loginAs(t, f, "global", nil)
	insertPaymentRow(t, f.pool, msID, bid, adminID, 150000, "card")

	// Client tries to inject amount/method/paid_at/branch_id; server must ignore.
	body := map[string]any{
		"reason":    "test",
		"amount":    99999,
		"method":    "cash",
		"paid_at":   kstAddDaysStr(-5),
		"branch_id": 99999,
	}
	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RefundPayment struct {
			Amount   int    `json:"amount"`
			Method   string `json:"method"`
			BranchID int64  `json:"branch_id"`
			PaidAt   string `json:"paid_at"`
		} `json:"refund_payment"`
	}
	mustDecode(t, rec, &resp)
	if resp.RefundPayment.Amount != -150000 || resp.RefundPayment.Method != "card" {
		t.Errorf("amount/method client overrides should be ignored: %+v", resp.RefundPayment)
	}
	if resp.RefundPayment.BranchID != bid {
		t.Errorf("branch_id=%d want %d (original branch)", resp.RefundPayment.BranchID, bid)
	}
	if resp.RefundPayment.PaidAt != kstAddDaysStr(0) {
		t.Errorf("paid_at=%s want today", resp.RefundPayment.PaidAt)
	}
}

func TestMembership_Refund_OtherBranch_404(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, nil)
	other := testutil.CreateBranch(t, f.pool, nil)
	otherMember := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: other})
	msID := testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: otherMember, Type: "monthly",
		StartDate: kstAddDaysStr(0), EndDate: kstAddDaysStr(30), Status: "active",
	})
	_, access := loginAs(t, f, "branch", &mine)

	rec := postMembership(t, f, http.MethodPost, membershipPath(msID)+"/refund",
		access, membershipsKey1, map[string]any{"reason": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- Auth / guard ----------

func TestMembership_NoAuth_401(t *testing.T) {
	f := newAdminFixture(t)
	rec := postMembership(t, f, http.MethodGet, "/api/memberships/1", "", "", nil)
	if rec.Code != http.StatusUnauthorized || !hasErrorCode(rec.Body.Bytes(), "UNAUTHORIZED") {
		t.Fatalf("expected 401 UNAUTHORIZED, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMembership_MustChangePasswordBlocks(t *testing.T) {
	f := newAdminFixture(t)
	// Create an admin with must_change_password=true.
	username := "mcp-" + t.Name()
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: username, Role: "global", MustChangePassword: true,
	})
	rec := postJSON(t, f.r, "/api/admin/login",
		map[string]any{"username": username, "password": plain})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	mustDecode(t, rec, &loginResp)

	rec = postMembership(t, f, http.MethodGet, "/api/memberships/1", loginResp.AccessToken, "", nil)
	if rec.Code != http.StatusForbidden || !hasErrorCode(rec.Body.Bytes(), "MUST_CHANGE_PASSWORD") {
		t.Fatalf("expected 403 MUST_CHANGE_PASSWORD, got %d body=%s", rec.Code, rec.Body.String())
	}
}
