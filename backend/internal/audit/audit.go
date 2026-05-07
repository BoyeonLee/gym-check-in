// Package audit owns the small write-only API for admin_audit_logs.
//
// The middleware/handler layers call Log to record administrative actions
// (login, logout, password change, admin/branch CRUD). Member and
// membership changes are tracked elsewhere (membership_events,
// payments.performed_by) — DO NOT add helpers here for those flows.
//
// Step 2 ships only the helper definition; the actual call sites are
// installed by the auth and admin/branch handlers in steps 3 and 4.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

// Action is a closed enum corresponding to admin_audit_logs.action values.
type Action string

const (
	LoginSuccess   Action = "login_success"
	LoginFailure   Action = "login_failure"
	Logout         Action = "logout"
	PasswordChange Action = "password_change"
	PasswordReset  Action = "password_reset"
	AdminCreate    Action = "admin_create"
	AdminUpdate    Action = "admin_update"
	AdminDelete    Action = "admin_delete"
	BranchCreate   Action = "branch_create"
	BranchUpdate   Action = "branch_update"
	BranchDelete   Action = "branch_delete"
)

// Entry is the payload recorded by Log. AdminID may be nil for login
// failures where the supplied username doesn't match any admin row.
// Metadata is serialized to jsonb and should always include "request_id".
type Entry struct {
	AdminID    *int64
	Action     Action
	TargetType *string
	TargetID   *int64
	IP         string // empty string → NULL inet column
	UserAgent  string
	Metadata   map[string]any
}

// Log inserts one row into admin_audit_logs.
//
// Audit failures must not break the primary request flow: the caller
// always sees nil from this function, while the underlying error is
// recorded via slog so operators still notice. The intent matches
// ADR-011 (audit is a defence-in-depth side channel, not a request
// dependency).
func Log(ctx context.Context, pool *pgxpool.Pool, e Entry) error {
	var ip string
	if e.IP != "" {
		// Parse defensively; a malformed value collapses to "" → NULL inet.
		// Parsing here avoids a DB round trip on obviously bad input.
		if addr, err := netip.ParseAddr(e.IP); err == nil {
			ip = addr.String()
		}
	}
	var metaRaw []byte
	if len(e.Metadata) > 0 {
		if raw, err := json.Marshal(e.Metadata); err == nil {
			metaRaw = raw
		} else {
			slog.Error("audit: marshal metadata", "error", err.Error())
		}
	}

	// SQL execution lives in internal/repo per backend/CLAUDE.md
	// ("SQL is repo-only"). The audit package stays a thin enum + adapter.
	if err := repo.InsertAuditLog(ctx, pool, repo.AuditLogRow{
		AdminID:      e.AdminID,
		Action:       string(e.Action),
		TargetType:   e.TargetType,
		TargetID:     e.TargetID,
		IP:           ip,
		UserAgent:    e.UserAgent,
		MetadataJSON: metaRaw,
	}); err != nil {
		// Swallow the error so the caller's primary work is not blocked,
		// but make sure the failure is observable.
		slog.Error("audit: insert failed",
			"error", err.Error(),
			"action", string(e.Action),
		)
		return nil
	}
	return nil
}

// Validate offers a helper for tests / future call sites to verify an
// Entry before issuing the insert — currently unused inside Log itself
// because invalid fields collapse to NULL/best-effort, but exposed so
// step 3/4 handlers can fail fast on programmer mistakes.
func (e Entry) Validate() error {
	switch e.Action {
	case LoginSuccess, LoginFailure, Logout, PasswordChange, PasswordReset,
		AdminCreate, AdminUpdate, AdminDelete,
		BranchCreate, BranchUpdate, BranchDelete:
	default:
		return fmt.Errorf("audit: unknown action %q", string(e.Action))
	}
	return nil
}
