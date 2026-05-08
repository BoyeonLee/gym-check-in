// Package batch implements the daily KST midnight maintenance jobs:
// membership status transitions and short-lived bookkeeping table cleanup.
//
// The job is invoked from two paths that share the same RunExpiry function:
//
//   - in-process cron (KST "1 0 * * *" — robfig/cron/v3 — see scheduler.go).
//   - one-shot CLI: `./bin/server batch run-expiry`. External schedulers
//     (systemd timer, k8s CronJob, …) call this when the operator prefers
//     out-of-process scheduling.
//
// Each of the six steps runs in its own transaction so a failure in one
// step does not block the rest. Errors are accumulated in Stats.Errors and
// the caller (cron logger / CLI) decides whether the run "failed". Row
// counts are returned for observability — slog'd once at the end and
// surfaced as JSON when invoked via the CLI.
//
// SQL lives entirely in internal/repo (per backend/CLAUDE.md). This package
// composes repo.* helpers — the statements themselves are owned by
// internal/repo/maintenance_repo.go.
package batch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// Stats summarises a single RunExpiry pass. Each integer is the row count
// affected by its step; Errors holds *all* step errors so an operator can
// see (a) what succeeded and (b) every reason something failed in one
// glance — partial failure is normal here.
type Stats struct {
	RunID              string  `json:"run_id"`
	ExpiredActivated   int64   `json:"expired_activated"`   // active → expired
	PausedReactivated  int64   `json:"paused_reactivated"`  // paused → active (pause_end_date < today)
	ActiveToPaused     int64   `json:"active_to_paused"`    // active → paused (pause_start_date == today)
	DeletedIdempotency int64   `json:"deleted_idempotency"` // idempotency_keys cleanup
	DeletedRefresh     int64   `json:"deleted_refresh"`     // revoked_refresh_tokens cleanup
	DeletedAuditLogs   int64   `json:"deleted_audit_logs"`  // admin_audit_logs cleanup
	Errors             []error `json:"-"`                   // surfaced via slog/CLI text
}

// stepFn is the signature shared by every maintenance step in repo.
type stepFn func(context.Context, repo.Querier) (int64, error)

type step struct {
	name string
	fn   stepFn
	dest *int64
}

// RunExpiry executes the six daily maintenance steps. The clock argument
// exists for symmetry with other layers that mock time; the actual KST
// date arithmetic happens in SQL (via AT TIME ZONE) so this clock only
// affects the run-id timestamp logged at the end. We still take it so
// callers inject util.SystemClock{} explicitly and tests have a single seam.
func RunExpiry(ctx context.Context, pool *pgxpool.Pool, _ util.Clock) (Stats, error) {
	stats := Stats{RunID: uuid.NewString()}

	steps := []step{
		{name: "expire_active_past_end", fn: repo.ExpireActivePastEnd, dest: &stats.ExpiredActivated},
		// pause_end_date < today: a paused membership whose hold ended
		// yesterday. paused → active does NOT recompute end_date — that
		// was done at /pause time; the batch only flips status when the
		// hold elapses.
		{name: "reactivate_paused_past_pause_end", fn: repo.ReactivatePausedPastEnd, dest: &stats.PausedReactivated},
		// pause_start_date == today: a future-scheduled pause whose start
		// has arrived. Flip active → paused; pause_* are already set.
		{name: "schedule_pause_arriving_today", fn: repo.ScheduleArrivedPause, dest: &stats.ActiveToPaused},
		{name: "cleanup_idempotency_keys", fn: repo.CleanupIdempotencyKeys, dest: &stats.DeletedIdempotency},
		{name: "cleanup_revoked_refresh_tokens", fn: repo.CleanupRevokedRefreshTokens, dest: &stats.DeletedRefresh},
		{name: "cleanup_admin_audit_logs", fn: repo.CleanupAuditLogs, dest: &stats.DeletedAuditLogs},
	}

	for _, s := range steps {
		if err := ctx.Err(); err != nil {
			stats.Errors = append(stats.Errors, fmt.Errorf("%s: %w", s.name, err))
			break // context cancelled — no point trying further steps
		}
		count, err := runStep(ctx, pool, s.fn)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Errorf("%s: %w", s.name, err))
			continue // partial failure is allowed — keep going
		}
		*s.dest = count
	}

	logSummary(stats)

	if len(stats.Errors) > 0 {
		return stats, errors.Join(stats.Errors...)
	}
	return stats, nil
}

// runStep executes a single repo helper inside its own transaction so it
// commits independently of the others. Returns the affected row count.
func runStep(ctx context.Context, pool *pgxpool.Pool, fn stepFn) (int64, error) {
	var affected int64
	err := withSimpleTx(ctx, pool, func(tx pgx.Tx) error {
		n, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		affected = n
		return nil
	})
	return affected, err
}

// withSimpleTx is intentionally separate from repo.WithTx: that helper
// retries on serialization/deadlock and is built for application
// transactions whose fn must be idempotent. Batch UPDATE/DELETE statements
// against an idle table at 00:01 KST do not contend, and we explicitly
// want the affected row count (which retry would double-count). A direct
// tx here keeps semantics simple and the dependency graph one-way.
func withSimpleTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.Background())
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// logSummary emits one structured slog record per run so operators can
// scrape "batch.run" lines from the access log.
func logSummary(stats Stats) {
	attrs := []any{
		"event", "batch.run",
		"run_id", stats.RunID,
		"expired_activated", stats.ExpiredActivated,
		"paused_reactivated", stats.PausedReactivated,
		"active_to_paused", stats.ActiveToPaused,
		"deleted_idempotency", stats.DeletedIdempotency,
		"deleted_refresh", stats.DeletedRefresh,
		"deleted_audit_logs", stats.DeletedAuditLogs,
		"error_count", len(stats.Errors),
	}
	if len(stats.Errors) > 0 {
		msgs := make([]string, len(stats.Errors))
		for i, e := range stats.Errors {
			msgs[i] = e.Error()
		}
		attrs = append(attrs, "errors", msgs)
		slog.Error("batch run completed with errors", attrs...)
		return
	}
	slog.Info("batch run completed", attrs...)
}
