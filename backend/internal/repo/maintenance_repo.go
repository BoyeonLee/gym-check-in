// maintenance_repo.go owns SQL for the daily KST midnight batch
// (internal/batch). The batch package composes these helpers; it does not
// embed any SQL itself. Keeping the statements here preserves the
// "SQL lives in internal/repo only" invariant from backend/CLAUDE.md.
//
// Each helper is a single statement and returns the affected row count so
// the caller can surface counters in slog/CLI output. KST date arithmetic
// is done in SQL via "(now() AT TIME ZONE 'Asia/Seoul')::date" so behavior
// does not depend on the session timezone (pinned to UTC in NewPool).
package repo

import (
	"context"
	"fmt"
)

// ExpireActivePastEnd flips active memberships whose end_date has already
// passed (in KST) to status='expired'. Step 1 of the daily batch.
func ExpireActivePastEnd(ctx context.Context, q Querier) (int64, error) {
	const stmt = `
		update memberships
		   set status = 'expired'
		 where status = 'active'
		   and end_date < (now() at time zone 'Asia/Seoul')::date
	`
	ct, err := q.Exec(ctx, stmt)
	if err != nil {
		return 0, fmt.Errorf("repo: expire active past end: %w", err)
	}
	return ct.RowsAffected(), nil
}

// ReactivatePausedPastEnd returns paused memberships to active once their
// pause window has elapsed (pause_end_date < today KST). pause_*_date are
// cleared; end_date is NOT recomputed because the pause-time mutation
// already extended it. Step 2 of the daily batch.
func ReactivatePausedPastEnd(ctx context.Context, q Querier) (int64, error) {
	const stmt = `
		update memberships
		   set status = 'active', pause_start_date = null, pause_end_date = null
		 where status = 'paused'
		   and pause_end_date < (now() at time zone 'Asia/Seoul')::date
	`
	ct, err := q.Exec(ctx, stmt)
	if err != nil {
		return 0, fmt.Errorf("repo: reactivate paused past end: %w", err)
	}
	return ct.RowsAffected(), nil
}

// ScheduleArrivedPause flips active memberships whose future-scheduled
// pause has now arrived (pause_start_date == today KST) to paused. The
// pause_* fields are already populated by the original /pause call.
// Step 3 of the daily batch.
func ScheduleArrivedPause(ctx context.Context, q Querier) (int64, error) {
	const stmt = `
		update memberships
		   set status = 'paused'
		 where status = 'active'
		   and pause_start_date = (now() at time zone 'Asia/Seoul')::date
	`
	ct, err := q.Exec(ctx, stmt)
	if err != nil {
		return 0, fmt.Errorf("repo: schedule arrived pause: %w", err)
	}
	return ct.RowsAffected(), nil
}

// CleanupIdempotencyKeys deletes idempotency_keys rows older than 24h —
// the docs/API.md window after which a recycled key is allowed to
// reprocess. Step 4 of the daily batch.
func CleanupIdempotencyKeys(ctx context.Context, q Querier) (int64, error) {
	const stmt = `delete from idempotency_keys where created_at < now() - interval '24 hours'`
	ct, err := q.Exec(ctx, stmt)
	if err != nil {
		return 0, fmt.Errorf("repo: cleanup idempotency keys: %w", err)
	}
	return ct.RowsAffected(), nil
}

// CleanupRevokedRefreshTokens deletes refresh-token revocation rows older
// than the refresh JWT lifetime (15h). Tokens past their natural expiry no
// longer need an explicit denylist entry — the JWT exp claim itself
// rejects them. Step 5 of the daily batch.
func CleanupRevokedRefreshTokens(ctx context.Context, q Querier) (int64, error) {
	const stmt = `delete from revoked_refresh_tokens where revoked_at < now() - interval '15 hours'`
	ct, err := q.Exec(ctx, stmt)
	if err != nil {
		return 0, fmt.Errorf("repo: cleanup revoked refresh tokens: %w", err)
	}
	return ct.RowsAffected(), nil
}

// CleanupAuditLogs trims admin_audit_logs to a 1-year retention window per
// backend/CLAUDE.md and db/CLAUDE.md. Step 6 of the daily batch.
func CleanupAuditLogs(ctx context.Context, q Querier) (int64, error) {
	const stmt = `delete from admin_audit_logs where created_at < now() - interval '1 year'`
	ct, err := q.Exec(ctx, stmt)
	if err != nil {
		return 0, fmt.Errorf("repo: cleanup audit logs: %w", err)
	}
	return ct.RowsAffected(), nil
}
