// audit_logs_repo.go owns the SQL for admin_audit_logs.
//
// CRITICAL: All SQL touching admin_audit_logs must live here. Higher-level
// packages (e.g. internal/audit) call into this repo rather than executing
// SQL directly — that keeps backend/CLAUDE.md's "SQL is repo-only" rule
// intact.
package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// AuditLogRow is the shape internal/audit hands to InsertAuditLog. The
// repo layer takes pre-validated values (IP already parsed, metadata
// already JSON-encoded) so SQL stays the only concern here.
type AuditLogRow struct {
	AdminID      *int64
	Action       string
	TargetType   *string
	TargetID     *int64
	IP           string // empty string → NULL inet
	UserAgent    string
	MetadataJSON []byte // pre-marshaled JSON; empty → NULL jsonb
}

// AuditLogQuerier is the narrow Exec surface InsertAuditLog needs.
// Both *pgxpool.Pool and pgx.Tx satisfy it, so callers can append audit
// writes inside an existing transaction or fire them off the pool directly.
type AuditLogQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// InsertAuditLog persists one row. Errors are returned to the caller; the
// caller decides whether to swallow them (audit is best-effort) or
// propagate (e.g. tests that assert the row was written).
//
// The SQL keeps NULL semantics for ip (empty string) and metadata
// (zero-length slice) so the audit layer can stay declarative.
func InsertAuditLog(ctx context.Context, q AuditLogQuerier, row AuditLogRow) error {
	const stmt = `
		insert into admin_audit_logs
			(admin_id, action, target_type, target_id, ip, user_agent, metadata)
		values ($1, $2, $3, $4, nullif($5, '')::inet, nullif($6, ''), nullif($7, '')::jsonb)
	`
	var meta string
	if len(row.MetadataJSON) > 0 {
		meta = string(row.MetadataJSON)
	}
	if _, err := q.Exec(ctx, stmt,
		row.AdminID, row.Action, row.TargetType, row.TargetID,
		row.IP, row.UserAgent, meta,
	); err != nil {
		return fmt.Errorf("repo: insert admin_audit_logs: %w", err)
	}
	return nil
}
