//go:build integration

package batch_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/batch"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// runExpiry covers the seven scenarios called out in the step plan. Each
// subtest sets up a focused fixture and asserts the resulting state and
// stats; the goal is one row affected per scenario so the counts read
// trivially. Time-sensitive rows are inserted with explicit "now() ±
// interval" timestamps because the SQL itself uses (now() AT TIME ZONE
// 'Asia/Seoul')::date — we cannot freeze the DB clock from Go, so we
// pivot off the production clock and choose offsets large enough (24h+)
// that the day boundary never matters.

func TestRunExpiry_ExpireActiveWhoseEndDateIsPast(t *testing.T) {
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	member := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branch})
	yesterday := todayKST(t).AddDate(0, 0, -1).Format("2006-01-02")
	twoWeeksAgo := todayKST(t).AddDate(0, 0, -14).Format("2006-01-02")
	id := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  member,
		Type:      "monthly",
		StartDate: twoWeeksAgo,
		EndDate:   yesterday, // ends yesterday → must flip to expired
		Status:    "active",
	})

	stats, err := batch.RunExpiry(context.Background(), pool, util.SystemClock{})
	if err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}
	if stats.ExpiredActivated < 1 {
		t.Fatalf("expected ExpiredActivated >= 1, got %d", stats.ExpiredActivated)
	}
	if got := membershipStatus(t, pool, id); got != "expired" {
		t.Fatalf("status = %q, want expired", got)
	}
}

func TestRunExpiry_DoesNotExpireActiveEndingToday(t *testing.T) {
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	member := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branch})
	today := todayKST(t).Format("2006-01-02")
	twoWeeksAgo := todayKST(t).AddDate(0, 0, -14).Format("2006-01-02")
	id := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID:  member,
		Type:      "monthly",
		StartDate: twoWeeksAgo,
		EndDate:   today, // ends today → still active until tomorrow's run
		Status:    "active",
	})

	if _, err := batch.RunExpiry(context.Background(), pool, util.SystemClock{}); err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}
	if got := membershipStatus(t, pool, id); got != "active" {
		t.Fatalf("status = %q, want active (end_date == today must not expire)", got)
	}
}

func TestRunExpiry_PausedToActiveWhenPauseEnded(t *testing.T) {
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	member := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branch})

	today := todayKST(t)
	pauseStart := today.AddDate(0, 0, -10).Format("2006-01-02")
	pauseEnd := today.AddDate(0, 0, -1).Format("2006-01-02") // ended yesterday
	startDate := today.AddDate(0, 0, -20).Format("2006-01-02")
	endDate := today.AddDate(0, 0, 30).Format("2006-01-02")

	var id int64
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := pool.QueryRow(ctx, `
		insert into memberships
		(member_id, type, months, start_date, end_date, status, pause_start_date, pause_end_date, pause_used)
		values ($1, 'monthly', 1, $2, $3, 'paused', $4, $5, true)
		returning id
	`, member, startDate, endDate, pauseStart, pauseEnd).Scan(&id)
	if err != nil {
		t.Fatalf("seed paused membership: %v", err)
	}

	stats, err := batch.RunExpiry(context.Background(), pool, util.SystemClock{})
	if err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}
	if stats.PausedReactivated < 1 {
		t.Fatalf("expected PausedReactivated >= 1, got %d", stats.PausedReactivated)
	}
	status, ps, pe := pauseSnapshot(t, pool, id)
	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
	if ps != nil || pe != nil {
		t.Fatalf("pause_* should be NULL after reactivate, got start=%v end=%v", ps, pe)
	}
}

func TestRunExpiry_ActiveToPausedWhenScheduledPauseArrives(t *testing.T) {
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	member := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branch})

	today := todayKST(t)
	startDate := today.AddDate(0, 0, -10).Format("2006-01-02")
	endDate := today.AddDate(0, 0, 30).Format("2006-01-02")
	pauseStart := today.Format("2006-01-02") // arrives today
	pauseEnd := today.AddDate(0, 0, 5).Format("2006-01-02")

	var id int64
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := pool.QueryRow(ctx, `
		insert into memberships
		(member_id, type, months, start_date, end_date, status, pause_start_date, pause_end_date, pause_used)
		values ($1, 'monthly', 1, $2, $3, 'active', $4, $5, true)
		returning id
	`, member, startDate, endDate, pauseStart, pauseEnd).Scan(&id)
	if err != nil {
		t.Fatalf("seed scheduled-pause membership: %v", err)
	}

	stats, err := batch.RunExpiry(context.Background(), pool, util.SystemClock{})
	if err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}
	if stats.ActiveToPaused < 1 {
		t.Fatalf("expected ActiveToPaused >= 1, got %d", stats.ActiveToPaused)
	}
	if got := membershipStatus(t, pool, id); got != "paused" {
		t.Fatalf("status = %q, want paused", got)
	}
}

func TestRunExpiry_CleanupIdempotencyKeys(t *testing.T) {
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	bid := &branch
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "branch", BranchID: bid})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Old key (25h ago) → must be deleted; fresh key (23h ago) → must survive.
	mustExec(t, ctx, pool, `
		insert into idempotency_keys
		(key, admin_id, endpoint, request_hash, response_status, response_body, created_at)
		values ($1, $2, 'POST /api/test', 'h', 200, '{}'::bytea, now() - interval '25 hours')
	`, "00000000-0000-0000-0000-000000000001", adminID)
	mustExec(t, ctx, pool, `
		insert into idempotency_keys
		(key, admin_id, endpoint, request_hash, response_status, response_body, created_at)
		values ($1, $2, 'POST /api/test', 'h', 200, '{}'::bytea, now() - interval '23 hours')
	`, "00000000-0000-0000-0000-000000000002", adminID)

	stats, err := batch.RunExpiry(context.Background(), pool, util.SystemClock{})
	if err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}
	if stats.DeletedIdempotency < 1 {
		t.Fatalf("expected DeletedIdempotency >= 1, got %d", stats.DeletedIdempotency)
	}

	if !rowExists(t, pool, `select 1 from idempotency_keys where key = $1`, "00000000-0000-0000-0000-000000000002") {
		t.Fatalf("23h-old key was deleted; only 25h-old should be removed")
	}
	if rowExists(t, pool, `select 1 from idempotency_keys where key = $1`, "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("25h-old key was NOT deleted")
	}
}

func TestRunExpiry_CleanupRevokedRefreshTokens(t *testing.T) {
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mustExec(t, ctx, pool, `
		insert into revoked_refresh_tokens (jti, admin_id, revoked_at)
		values ('jti-old', $1, now() - interval '16 hours')
	`, adminID)
	mustExec(t, ctx, pool, `
		insert into revoked_refresh_tokens (jti, admin_id, revoked_at)
		values ('jti-fresh', $1, now() - interval '14 hours')
	`, adminID)

	stats, err := batch.RunExpiry(context.Background(), pool, util.SystemClock{})
	if err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}
	if stats.DeletedRefresh < 1 {
		t.Fatalf("expected DeletedRefresh >= 1, got %d", stats.DeletedRefresh)
	}
	if !rowExists(t, pool, `select 1 from revoked_refresh_tokens where jti = 'jti-fresh'`) {
		t.Fatalf("14h-old refresh row deleted; only 16h-old should be removed")
	}
	if rowExists(t, pool, `select 1 from revoked_refresh_tokens where jti = 'jti-old'`) {
		t.Fatalf("16h-old refresh row NOT deleted")
	}
}

func TestRunExpiry_CleanupAdminAuditLogs(t *testing.T) {
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mustExec(t, ctx, pool, `
		insert into admin_audit_logs (admin_id, action, target_type, target_id, created_at)
		values ($1, 'login_success', 'admin', $1, now() - interval '366 days')
	`, adminID)
	mustExec(t, ctx, pool, `
		insert into admin_audit_logs (admin_id, action, target_type, target_id, created_at)
		values ($1, 'login_success', 'admin', $1, now() - interval '364 days')
	`, adminID)

	stats, err := batch.RunExpiry(context.Background(), pool, util.SystemClock{})
	if err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}
	if stats.DeletedAuditLogs < 1 {
		t.Fatalf("expected DeletedAuditLogs >= 1, got %d", stats.DeletedAuditLogs)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`select count(*) from admin_audit_logs where admin_id = $1`, adminID,
	).Scan(&count); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit log count = %d, want 1 (only the 364-day-old row should remain)", count)
	}
}

// ---- helpers ----

func todayKST(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatalf("load KST: %v", err)
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

func membershipStatus(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var s string
	if err := pool.QueryRow(ctx, `select status from memberships where id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("query status: %v", err)
	}
	return s
}

func pauseSnapshot(t *testing.T, pool *pgxpool.Pool, id int64) (string, *time.Time, *time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var status string
	var ps, pe *time.Time
	if err := pool.QueryRow(ctx,
		`select status, pause_start_date, pause_end_date from memberships where id=$1`, id,
	).Scan(&status, &ps, &pe); err != nil {
		t.Fatalf("query pause snapshot: %v", err)
	}
	return status, ps, pe
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func rowExists(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var x int
	err := pool.QueryRow(ctx, sql, args...).Scan(&x)
	if err == nil {
		return true
	}
	return false
}
