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

// insertPaymentDirect writes a payments row bypassing the handler so sales
// tests can shape the dataset without going through the full grant/refund
// flow. Branch admins are not produced (performed_by uses the seeded global
// admin id) — sales summary doesn't read performed_by.
func insertPaymentDirect(t *testing.T, pool *pgxpool.Pool, membershipID, branchID int64, amount int, method, paidAt string, performedBy int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`insert into payments (membership_id, branch_id, amount, method, paid_at, performed_by)
		 values ($1, $2, $3, $4, $5, $6)`,
		membershipID, branchID, amount, method, paidAt, performedBy,
	); err != nil {
		t.Fatalf("insert payment: %v", err)
	}
}

func seedPaymentScene(t *testing.T, pool *pgxpool.Pool) (branchA, branchB, adminID int64) {
	t.Helper()
	branchA = testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "지점A"})
	branchB = testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "지점B"})

	memberA := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branchA})
	memberB := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branchB})
	mAID := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: memberA})
	mBID := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: memberB})

	adminID, _ = testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "sales-admin-" + t.Name(), Role: "global", MustChangePassword: false,
	})

	// branchA: card grant 150,000 on 2026-05-01, cash refund -50,000 on 2026-05-02
	insertPaymentDirect(t, pool, mAID, branchA, 150000, "card", "2026-05-01", adminID)
	insertPaymentDirect(t, pool, mAID, branchA, -50000, "cash", "2026-05-02", adminID)
	// branchB: cash grant 100,000 on 2026-05-01
	insertPaymentDirect(t, pool, mBID, branchB, 100000, "cash", "2026-05-01", adminID)
	return
}

// TestSales_BranchAdminForbidden: only role='global' can hit /api/sales/summary.
func TestSales_BranchAdminForbidden(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "branch", &bid)

	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/sales/summary?from=2026-05-01&to=2026-05-31", access, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("branch admin must get 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSales_GlobalSummary: gross / refund / net split, by_method (cash/card),
// by_day rows.
func TestSales_GlobalSummary(t *testing.T) {
	f := newAdminFixture(t)
	seedPaymentScene(t, f.pool)
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/sales/summary?from=2026-05-01&to=2026-05-31", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		GrossTotal  int `json:"gross_total"`
		RefundTotal int `json:"refund_total"`
		NetTotal    int `json:"net_total"`
		ByMethod    []struct {
			Method      string `json:"method"`
			GrossTotal  int    `json:"gross_total"`
			RefundTotal int    `json:"refund_total"`
			NetTotal    int    `json:"net_total"`
		} `json:"by_method"`
		ByDay []struct {
			Date        string `json:"date"`
			GrossTotal  int    `json:"gross_total"`
			RefundTotal int    `json:"refund_total"`
			NetTotal    int    `json:"net_total"`
		} `json:"by_day"`
	}
	mustDecode(t, rec, &resp)

	// gross = 150000 (card) + 100000 (cash) = 250000
	if resp.GrossTotal != 250000 {
		t.Errorf("gross_total=%d want 250000", resp.GrossTotal)
	}
	// refund = 50000 (cash absolute)
	if resp.RefundTotal != 50000 {
		t.Errorf("refund_total=%d want 50000", resp.RefundTotal)
	}
	// net = 200000
	if resp.NetTotal != 200000 {
		t.Errorf("net_total=%d want 200000", resp.NetTotal)
	}
	// by_method must include both cash and card.
	got := map[string]struct{ Gross, Refund, Net int }{}
	for _, b := range resp.ByMethod {
		got[b.Method] = struct{ Gross, Refund, Net int }{b.GrossTotal, b.RefundTotal, b.NetTotal}
	}
	if c, ok := got["cash"]; !ok || c.Gross != 100000 || c.Refund != 50000 || c.Net != 50000 {
		t.Errorf("by_method[cash]=%+v want gross=100000 refund=50000 net=50000", c)
	}
	if c, ok := got["card"]; !ok || c.Gross != 150000 || c.Refund != 0 || c.Net != 150000 {
		t.Errorf("by_method[card]=%+v want gross=150000 refund=0 net=150000", c)
	}
	if len(resp.ByDay) != 2 {
		t.Errorf("by_day len=%d want 2 (5/1 + 5/2), rows=%+v", len(resp.ByDay), resp.ByDay)
	}
}

// TestSales_GlobalBranchFilter: ?branchId=A returns only branch A's rows.
func TestSales_GlobalBranchFilter(t *testing.T) {
	f := newAdminFixture(t)
	branchA, _, _ := seedPaymentScene(t, f.pool)
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/sales/summary?from=2026-05-01&to=2026-05-31&branchId="+itoa(branchA), access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		GrossTotal  int `json:"gross_total"`
		RefundTotal int `json:"refund_total"`
		NetTotal    int `json:"net_total"`
	}
	mustDecode(t, rec, &resp)
	if resp.GrossTotal != 150000 || resp.RefundTotal != 50000 || resp.NetTotal != 100000 {
		t.Errorf("filtered totals=%+v want gross=150000 refund=50000 net=100000", resp)
	}
}

// TestSales_InvalidQuery covers the obvious bad-input paths.
func TestSales_InvalidQuery(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	cases := []struct {
		name string
		url  string
	}{
		{"missing from", "/api/sales/summary?to=2026-05-31"},
		{"missing to", "/api/sales/summary?from=2026-05-01"},
		{"bad from", "/api/sales/summary?from=oops&to=2026-05-31"},
		{"bad to", "/api/sales/summary?from=2026-05-01&to=oops"},
		{"to before from", "/api/sales/summary?from=2026-05-31&to=2026-05-01"},
		{"bad branchId", "/api/sales/summary?from=2026-05-01&to=2026-05-31&branchId=abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postWithAuth(t, f.r, http.MethodGet, c.url, access, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestSales_RequiresAuth ensures the route is behind RequireAuth.
func TestSales_RequiresAuth(t *testing.T) {
	f := newAdminFixture(t)
	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/sales/summary?from=2026-05-01&to=2026-05-31", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
}
