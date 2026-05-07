// admins_repo.go owns SQL for the admins table needed by step 3 (auth) —
// FindByUsername / FindByID, GetForAccessCheck (used by Auth middleware to
// detect soft-deleted or post-password-change tokens), and the login state
// mutators RecordLoginSuccess, RecordLoginFailure, UpdatePassword.
//
// Per backend/CLAUDE.md only this file may issue SQL against the admins
// table; service / handler code goes through the functions below.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the narrow surface admin SQL needs. Both *pgxpool.Pool and
// pgx.Tx satisfy it, so callers can compose a transaction or run on the
// pool directly.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// AdminRow is the in-memory shape of a single admins row. Soft-deleted
// rows are filtered by the FindBy* helpers, so DeletedAt is always nil
// in returned values — kept on the struct for use by repo internal SQL
// scanning convenience.
type AdminRow struct {
	ID                    int64
	Username              string
	PasswordHash          string
	MustChangePassword    bool
	TempPasswordExpiresAt *time.Time
	Role                  string
	BranchID              *int64
	LastLoginAt           *time.Time
	PasswordUpdatedAt     *time.Time
	FailedLoginCount      int
	LockedUntil           *time.Time
	DeletedAt             *time.Time
}

// AdminAccessCheck is what the Auth middleware needs on every request: does
// this admin still exist (deleted_at IS NULL) and when was their password
// last changed (so stale access tokens can be rejected). One SELECT, two
// signals — see backend/CLAUDE.md "DB 1쿼리 추가 비용" for the design.
type AdminAccessCheck struct {
	Exists            bool
	PasswordUpdatedAt *time.Time
}

const adminColumns = `
	id,
	username,
	password_hash,
	must_change_password,
	temp_password_expires_at,
	role,
	branch_id,
	last_login_at,
	password_updated_at,
	failed_login_count,
	locked_until,
	deleted_at
`

// scanAdmin maps a single row into AdminRow. Caller is expected to issue a
// SELECT that lists adminColumns in the same order.
func scanAdmin(row pgx.Row) (*AdminRow, error) {
	var a AdminRow
	if err := row.Scan(
		&a.ID,
		&a.Username,
		&a.PasswordHash,
		&a.MustChangePassword,
		&a.TempPasswordExpiresAt,
		&a.Role,
		&a.BranchID,
		&a.LastLoginAt,
		&a.PasswordUpdatedAt,
		&a.FailedLoginCount,
		&a.LockedUntil,
		&a.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// FindByUsername returns the admin whose username matches and deleted_at IS
// NULL. (nil, nil) signifies "no such active admin".
func FindByUsername(ctx context.Context, q Querier, username string) (*AdminRow, error) {
	stmt := `select ` + adminColumns + ` from admins where username = $1 and deleted_at is null`
	a, err := scanAdmin(q.QueryRow(ctx, stmt, username))
	if err != nil {
		return nil, fmt.Errorf("repo: find admin by username: %w", err)
	}
	return a, nil
}

// FindByID is the id counterpart of FindByUsername.
func FindByID(ctx context.Context, q Querier, id int64) (*AdminRow, error) {
	stmt := `select ` + adminColumns + ` from admins where id = $1 and deleted_at is null`
	a, err := scanAdmin(q.QueryRow(ctx, stmt, id))
	if err != nil {
		return nil, fmt.Errorf("repo: find admin by id: %w", err)
	}
	return a, nil
}

// GetForAccessCheck performs the per-request auth check the middleware needs.
// Exists is false when the admin row is missing or soft-deleted; in that
// case the caller MUST treat the access token as invalid.
func GetForAccessCheck(ctx context.Context, q Querier, id int64) (AdminAccessCheck, error) {
	const stmt = `select password_updated_at from admins where id = $1 and deleted_at is null`
	var pwUpdated *time.Time
	err := q.QueryRow(ctx, stmt, id).Scan(&pwUpdated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AdminAccessCheck{Exists: false}, nil
		}
		return AdminAccessCheck{}, fmt.Errorf("repo: get for access check: %w", err)
	}
	return AdminAccessCheck{Exists: true, PasswordUpdatedAt: pwUpdated}, nil
}

// RecordLoginSuccess clears the failure counter and stamps last_login_at.
// locked_until is intentionally left untouched; a stale (past) lock value
// is harmless because login checks `locked_until > now`.
func RecordLoginSuccess(ctx context.Context, q Querier, id int64, now time.Time) error {
	const stmt = `
		update admins
		set failed_login_count = 0,
		    last_login_at      = $2
		where id = $1 and deleted_at is null
	`
	if _, err := q.Exec(ctx, stmt, id, now); err != nil {
		return fmt.Errorf("repo: record login success: %w", err)
	}
	return nil
}

// RecordLoginFailure bumps failed_login_count and locks the account on the
// 5th consecutive failure. Special case: if the row's previous lock has
// already expired (locked_until <= now AND failed_login_count >= 5) the
// counter restarts at 1 — see backend/CLAUDE.md "잠금 해제 시점에 자동 0으로
// 리셋되지는 않는다, 틀리면 카운터 1부터 다시 누적".
func RecordLoginFailure(ctx context.Context, q Querier, id int64, now time.Time) error {
	const stmt = `
		update admins
		set failed_login_count = case
			when locked_until is not null
				and locked_until <= $2
				and failed_login_count >= 5
				then 1
			else failed_login_count + 1
		end,
		locked_until = case
			when (case
				when locked_until is not null
					and locked_until <= $2
					and failed_login_count >= 5
					then 1
				else failed_login_count + 1
			end) >= 5
				then $2 + interval '15 minutes'
			else locked_until
		end
		where id = $1 and deleted_at is null
	`
	if _, err := q.Exec(ctx, stmt, id, now); err != nil {
		return fmt.Errorf("repo: record login failure: %w", err)
	}
	return nil
}

// UpdatePassword applies a new hash and clears all auth state that should
// not survive a password change: must_change_password, temp expiry, lock,
// failed counter. password_updated_at is set to `now` so subsequent stale
// access/refresh tokens (with iat < now) fail validation.
func UpdatePassword(ctx context.Context, q Querier, id int64, newHash string, now time.Time) error {
	const stmt = `
		update admins set
			password_hash            = $2,
			must_change_password     = false,
			temp_password_expires_at = null,
			password_updated_at      = $3,
			failed_login_count       = 0,
			locked_until             = null
		where id = $1 and deleted_at is null
	`
	if _, err := q.Exec(ctx, stmt, id, newHash, now); err != nil {
		return fmt.Errorf("repo: update password: %w", err)
	}
	return nil
}
