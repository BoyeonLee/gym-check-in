//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// The maintenance helpers are pure single-statement SQL and have no time
// injection — they pivot off (now() AT TIME ZONE 'Asia/Seoul')::date or
// "now() - interval '...'". Tests therefore seed rows with explicit
// offsets large enough (24h+) that the day boundary cannot affect the
// outcome, mirroring batch_test.go.

func TestMaintenance_ExpireActivePastEnd(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	member := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branch})

	today := todayKSTRepo(t)
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := today.AddDate(0, 0, 1).Format("2006-01-02")
	twoWeeksAgo := today.AddDate(0, 0, -14).Format("2006-01-02")

	expiredID := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: member, Type: "monthly", StartDate: twoWeeksAgo, EndDate: yesterday, Status: "active",
	})
	stillActiveID := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: member, Type: "monthly", StartDate: today.Format("2006-01-02"), EndDate: tomorrow, Status: "active",
	})

	n, err := repo.ExpireActivePastEnd(ctx, pool)
	if err != nil {
		t.Fatalf("ExpireActivePastEnd: %v", err)
	}
	if n < 1 {
		t.Fatalf("RowsAffected = %d, want >= 1", n)
	}
	if got := membershipStatusRepo(t, pool, expiredID); got != "expired" {
		t.Fatalf("expired row status = %q, want expired", got)
	}
	if got := membershipStatusRepo(t, pool, stillActiveID); got != "active" {
		t.Fatalf("active row status = %q, want active (must not flip when end_date >= today)", got)
	}
}

func TestMaintenance_ReactivatePausedPastEnd(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	member := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branch})

	today := todayKSTRepo(t)
	pauseStart := today.AddDate(0, 0, -10).Format("2006-01-02")
	pauseEnd := today.AddDate(0, 0, -1).Format("2006-01-02")
	startDate := today.AddDate(0, 0, -20).Format("2006-01-02")
	endDate := today.AddDate(0, 0, 30).Format("2006-01-02")

	var id int64
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := pool.QueryRow(cctx, `
		insert into memberships
		(member_id, type, months, start_date, end_date, status, pause_start_date, pause_end_date, pause_used)
		values ($1, 'monthly', 1, $2, $3, 'paused', $4, $5, true)
		returning id
	`, member, startDate, endDate, pauseStart, pauseEnd).Scan(&id)
	if err != nil {
		t.Fatalf("seed paused membership: %v", err)
	}

	n, err := repo.ReactivatePausedPastEnd(ctx, pool)
	if err != nil {
		t.Fatalf("ReactivatePausedPastEnd: %v", err)
	}
	if n < 1 {
		t.Fatalf("RowsAffected = %d, want >= 1", n)
	}
	status, ps, pe := pauseSnapshotRepo(t, pool, id)
	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
	if ps != nil || pe != nil {
		t.Fatalf("pause_* should be NULL after reactivate, got start=%v end=%v", ps, pe)
	}
}

func TestMaintenance_ScheduleArrivedPause(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	member := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: branch})

	today := todayKSTRepo(t)
	startDate := today.AddDate(0, 0, -10).Format("2006-01-02")
	endDate := today.AddDate(0, 0, 30).Format("2006-01-02")
	pauseStart := today.Format("2006-01-02")
	pauseEnd := today.AddDate(0, 0, 5).Format("2006-01-02")

	var id int64
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := pool.QueryRow(cctx, `
		insert into memberships
		(member_id, type, months, start_date, end_date, status, pause_start_date, pause_end_date, pause_used)
		values ($1, 'monthly', 1, $2, $3, 'active', $4, $5, true)
		returning id
	`, member, startDate, endDate, pauseStart, pauseEnd).Scan(&id)
	if err != nil {
		t.Fatalf("seed scheduled-pause membership: %v", err)
	}

	n, err := repo.ScheduleArrivedPause(ctx, pool)
	if err != nil {
		t.Fatalf("ScheduleArrivedPause: %v", err)
	}
	if n < 1 {
		t.Fatalf("RowsAffected = %d, want >= 1", n)
	}
	if got := membershipStatusRepo(t, pool, id); got != "paused" {
		t.Fatalf("status = %q, want paused", got)
	}
}

func TestMaintenance_CleanupIdempotencyKeys(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	branch := testutil.CreateBranch(t, pool, nil)
	bid := &branch
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "branch", BranchID: bid})

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mustExecRepo(t, cctx, pool, `
		insert into idempotency_keys
		(key, admin_id, endpoint, request_hash, response_status, response_body, created_at)
		values ($1, $2, 'POST /api/test', 'h', 200, '{}'::bytea, now() - interval '25 hours')
	`, "00000000-0000-0000-0000-000000000a01", adminID)
	mustExecRepo(t, cctx, pool, `
		insert into idempotency_keys
		(key, admin_id, endpoint, request_hash, response_status, response_body, created_at)
		values ($1, $2, 'POST /api/test', 'h', 200, '{}'::bytea, now() - interval '23 hours')
	`, "00000000-0000-0000-0000-000000000a02", adminID)

	n, err := repo.CleanupIdempotencyKeys(ctx, pool)
	if err != nil {
		t.Fatalf("CleanupIdempotencyKeys: %v", err)
	}
	if n < 1 {
		t.Fatalf("RowsAffected = %d, want >= 1", n)
	}
	if !rowExistsRepo(t, pool, `select 1 from idempotency_keys where key = $1`, "00000000-0000-0000-0000-000000000a02") {
		t.Fatalf("23h-old key was deleted; only 25h-old should be removed")
	}
	if rowExistsRepo(t, pool, `select 1 from idempotency_keys where key = $1`, "00000000-0000-0000-0000-000000000a01") {
		t.Fatalf("25h-old key was NOT deleted")
	}
}

func TestMaintenance_CleanupRevokedRefreshTokens(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mustExecRepo(t, cctx, pool, `
		insert into revoked_refresh_tokens (jti, admin_id, revoked_at)
		values ('jti-mr-old', $1, now() - interval '16 hours')
	`, adminID)
	mustExecRepo(t, cctx, pool, `
		insert into revoked_refresh_tokens (jti, admin_id, revoked_at)
		values ('jti-mr-fresh', $1, now() - interval '14 hours')
	`, adminID)

	n, err := repo.CleanupRevokedRefreshTokens(ctx, pool)
	if err != nil {
		t.Fatalf("CleanupRevokedRefreshTokens: %v", err)
	}
	if n < 1 {
		t.Fatalf("RowsAffected = %d, want >= 1", n)
	}
	if !rowExistsRepo(t, pool, `select 1 from revoked_refresh_tokens where jti = 'jti-mr-fresh'`) {
		t.Fatalf("14h-old refresh row deleted; only 16h-old should be removed")
	}
	if rowExistsRepo(t, pool, `select 1 from revoked_refresh_tokens where jti = 'jti-mr-old'`) {
		t.Fatalf("16h-old refresh row NOT deleted")
	}
}

func TestMaintenance_CleanupAuditLogs(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mustExecRepo(t, cctx, pool, `
		insert into admin_audit_logs (admin_id, action, target_type, target_id, created_at)
		values ($1, 'login_success', 'admin', $1, now() - interval '366 days')
	`, adminID)
	mustExecRepo(t, cctx, pool, `
		insert into admin_audit_logs (admin_id, action, target_type, target_id, created_at)
		values ($1, 'login_success', 'admin', $1, now() - interval '364 days')
	`, adminID)

	n, err := repo.CleanupAuditLogs(ctx, pool)
	if err != nil {
		t.Fatalf("CleanupAuditLogs: %v", err)
	}
	if n < 1 {
		t.Fatalf("RowsAffected = %d, want >= 1", n)
	}

	var count int
	if err := pool.QueryRow(cctx,
		`select count(*) from admin_audit_logs where admin_id = $1`, adminID,
	).Scan(&count); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit log count = %d, want 1 (only the 364-day-old row should remain)", count)
	}
}

// ---- helpers (suffixed Repo to avoid clash with batch_test.go helpers) ----

func todayKSTRepo(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatalf("load KST: %v", err)
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

func membershipStatusRepo(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var s string
	if err := pool.QueryRow(ctx, `select status from memberships where id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("query status: %v", err)
	}
	return s
}

func pauseSnapshotRepo(t *testing.T, pool *pgxpool.Pool, id int64) (string, *time.Time, *time.Time) {
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

func mustExecRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func rowExistsRepo(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var x int
	err := pool.QueryRow(ctx, sql, args...).Scan(&x)
	return err == nil
}
