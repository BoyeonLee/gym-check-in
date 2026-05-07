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
	"strings"
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

// AdminListRow is the projection used by /api/admins (list). It includes a
// joined branch_name (NULL for global admins) so a single query feeds the
// frontend's "지점 식별" requirement without a per-row lookup.
type AdminListRow struct {
	ID                 int64
	Username           string
	Role               string
	BranchID           *int64
	BranchName         *string
	MustChangePassword bool
	LastLoginAt        *time.Time
	CreatedAt          time.Time
}

// CreateAdminInput / UpdateAdminInput keep the writes-only surfaces small.
// Handler is responsible for hashing PlainPassword via auth.HashPassword and
// passing it as PasswordHash here — keeping bcrypt out of repo lets us test
// auth-state mutations without spinning up CPU-heavy hashing per row.
type CreateAdminInput struct {
	Username     string
	Role         string // "global" | "branch"
	BranchID     *int64
	PasswordHash string // bcrypt hash, never plaintext
}

// UpdateAdminInput is a partial-update payload. Nil pointer = leave alone.
//
// BranchIDSet is the explicit "I intend to change branch_id" flag — it lets
// the caller distinguish "absent" (don't touch the column) from "explicit
// NULL" (clear it because role just flipped to global). Without this flag we
// couldn't write a JSON body like `{"role":"global"}` and have the column
// auto-clear. When BranchIDSet=true and BranchID=nil, the column is set to
// NULL; when BranchIDSet=true and BranchID points to an int64, that value is
// used.
type UpdateAdminInput struct {
	Username    *string
	Role        *string
	BranchIDSet bool
	BranchID    *int64
}

// ListAdmins returns all active admins with their branch_name joined.
// Ordering is `id ASC` — small operator list, deterministic for tests.
func ListAdmins(ctx context.Context, q Querier) ([]AdminListRow, error) {
	const stmt = `
		select a.id, a.username, a.role, a.branch_id, b.name,
		       a.must_change_password, a.last_login_at, a.created_at
		from admins a
		left join branches b on b.id = a.branch_id and b.deleted_at is null
		where a.deleted_at is null
		order by a.id asc
	`
	rows, err := q.Query(ctx, stmt)
	if err != nil {
		return nil, fmt.Errorf("repo: list admins: %w", err)
	}
	defer rows.Close()

	out := make([]AdminListRow, 0)
	for rows.Next() {
		var r AdminListRow
		if err := rows.Scan(
			&r.ID, &r.Username, &r.Role, &r.BranchID, &r.BranchName,
			&r.MustChangePassword, &r.LastLoginAt, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repo: list admins scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list admins rows: %w", err)
	}
	return out, nil
}

// CreateAdmin inserts a new admin row with must_change_password=true and
// temp_password_expires_at=now+24h, matching backend/CLAUDE.md ("생성 row의
// must_change_password=true + temp_password_expires_at=now()+24h 강제").
// password_updated_at stays NULL — a fresh admin has no "stale token cutoff".
func CreateAdmin(ctx context.Context, q Querier, in CreateAdminInput, now time.Time) (int64, error) {
	const stmt = `
		insert into admins (
			username, password_hash, must_change_password,
			temp_password_expires_at, role, branch_id
		)
		values ($1, $2, true, $3, $4, $5)
		returning id
	`
	tempExpiry := now.Add(24 * time.Hour)
	var id int64
	if err := q.QueryRow(ctx, stmt,
		in.Username, in.PasswordHash, tempExpiry, in.Role, in.BranchID,
	).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateAdmin applies a partial update over username/role/branch_id. SET
// clauses are appended dynamically using positional placeholders so the
// statement stays parameterized (CLAUDE.md "no string concat for SQL").
//
// password_hash / must_change_password / failed_login_count / locked_until /
// temp_password_expires_at are intentionally NOT settable here — those go
// through dedicated mutators (UpdatePassword / ResetPassword / login flows).
//
// Returns pgx.ErrNoRows for missing/soft-deleted targets.
func UpdateAdmin(ctx context.Context, q Querier, id int64, in UpdateAdminInput, now time.Time) error {
	sets := make([]string, 0, 3)
	args := make([]any, 0, 4)
	args = append(args, id)

	if in.Username != nil {
		args = append(args, *in.Username)
		sets = append(sets, fmt.Sprintf("username = $%d", len(args)))
	}
	if in.Role != nil {
		args = append(args, *in.Role)
		sets = append(sets, fmt.Sprintf("role = $%d", len(args)))
	}
	if in.BranchIDSet {
		args = append(args, in.BranchID) // nil pointer → SQL NULL via pgx
		sets = append(sets, fmt.Sprintf("branch_id = $%d", len(args)))
	}
	if len(sets) == 0 {
		// Nothing to change — short-circuit but verify existence so the
		// caller still gets ErrNoRows for a missing target.
		const checkStmt = `select 1 from admins where id = $1 and deleted_at is null`
		var one int
		if err := q.QueryRow(ctx, checkStmt, id).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return pgx.ErrNoRows
			}
			return fmt.Errorf("repo: update admin existence check: %w", err)
		}
		return nil
	}

	stmt := `update admins set ` + strings.Join(sets, ", ") +
		` where id = $1 and deleted_at is null`
	tag, err := q.Exec(ctx, stmt, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	_ = now
	return nil
}

// SoftDeleteAdmin sets deleted_at=now and bumps password_updated_at=now.
// Bumping password_updated_at is the trick that makes both outstanding
// access AND refresh tokens fail on the next request without needing a
// per-token revocation list — the auth middleware compares claim.iat
// against this column.
func SoftDeleteAdmin(ctx context.Context, q Querier, id int64, now time.Time) error {
	const stmt = `
		update admins
		set deleted_at = $2,
		    password_updated_at = $2
		where id = $1 and deleted_at is null
	`
	tag, err := q.Exec(ctx, stmt, id, now)
	if err != nil {
		return fmt.Errorf("repo: soft delete admin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ResetPassword installs a new (already bcrypt'd) hash and resets every
// auth-state column that should not survive an operator-issued reset:
// must_change_password=true, temp_password_expires_at=now+24h,
// failed_login_count=0, locked_until=NULL, password_updated_at=now (so
// existing tokens become stale).
func ResetPassword(ctx context.Context, q Querier, id int64, newHash string, now time.Time) error {
	const stmt = `
		update admins set
			password_hash            = $2,
			must_change_password     = true,
			temp_password_expires_at = $3,
			failed_login_count       = 0,
			locked_until             = null,
			password_updated_at      = $4
		where id = $1 and deleted_at is null
	`
	tag, err := q.Exec(ctx, stmt, id, newHash, now.Add(24*time.Hour), now)
	if err != nil {
		return fmt.Errorf("repo: reset password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// BumpPasswordUpdatedAt stamps password_updated_at=now without touching the
// hash. The /api/admins PATCH handler uses it whenever role or branch_id
// changes so the affected user's outstanding access/refresh tokens fail on
// the next request — the same iat<password_updated_at check the auth
// middleware already runs covers both kinds of revocation. Returns
// pgx.ErrNoRows for missing/soft-deleted targets.
func BumpPasswordUpdatedAt(ctx context.Context, q Querier, id int64, now time.Time) error {
	const stmt = `
		update admins set password_updated_at = $2
		where id = $1 and deleted_at is null
	`
	tag, err := q.Exec(ctx, stmt, id, now)
	if err != nil {
		return fmt.Errorf("repo: bump password_updated_at: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
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
