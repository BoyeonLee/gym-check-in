// branches_repo.go owns SQL for the branches table.
//
// All reads filter `deleted_at IS NULL` per backend/CLAUDE.md soft-delete rule;
// the helpers expose pre-filtered rows so handlers can't accidentally surface
// deleted branches. Writes return raw pgx errors so the handler layer can run
// them through apperr.FromDBError to map 23505 unique_violation on
// `branches_address_key` → 409 ADDRESS_DUPLICATE.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// BranchRow mirrors a single branches row. DeletedAt stays in the struct only
// for completeness — list/get helpers below filter soft-deleted rows out before
// returning.
type BranchRow struct {
	ID        int64
	Name      string
	Address   *string
	DeletedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

const branchColumns = `id, name, address, deleted_at, created_at, updated_at`

func scanBranch(row pgx.Row) (*BranchRow, error) {
	var b BranchRow
	if err := row.Scan(&b.ID, &b.Name, &b.Address, &b.DeletedAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// ListBranches returns all active branches ordered by id ASC. There is no
// pagination — gym chain size is small (handful of branches) per ADR-016.
func ListBranches(ctx context.Context, q Querier) ([]BranchRow, error) {
	stmt := `select ` + branchColumns + ` from branches where deleted_at is null order by id asc`
	rows, err := q.Query(ctx, stmt)
	if err != nil {
		return nil, fmt.Errorf("repo: list branches: %w", err)
	}
	defer rows.Close()

	out := make([]BranchRow, 0)
	for rows.Next() {
		var b BranchRow
		if err := rows.Scan(&b.ID, &b.Name, &b.Address, &b.DeletedAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("repo: list branches scan: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list branches rows: %w", err)
	}
	return out, nil
}

// GetBranch returns the active branch identified by id, or (nil, nil) when
// missing or soft-deleted (handlers translate that into 404).
func GetBranch(ctx context.Context, q Querier, id int64) (*BranchRow, error) {
	stmt := `select ` + branchColumns + ` from branches where id = $1 and deleted_at is null`
	b, err := scanBranch(q.QueryRow(ctx, stmt, id))
	if err != nil {
		return nil, fmt.Errorf("repo: get branch: %w", err)
	}
	return b, nil
}

// InsertBranch creates a new branch and returns the generated id. Address is
// optional; pass nil for NULL.
func InsertBranch(ctx context.Context, q Querier, name string, address *string) (int64, error) {
	const stmt = `insert into branches (name, address) values ($1, $2) returning id`
	var id int64
	if err := q.QueryRow(ctx, stmt, name, address).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateBranch applies partial changes. Either name or address (or both) may
// be provided — nil pointers leave the column untouched. Returns pgx.ErrNoRows
// when the row is missing or soft-deleted so callers can map to 404.
func UpdateBranch(ctx context.Context, q Querier, id int64, name *string, address *string) error {
	// Build dynamic SET clause with positional placeholders so SQL stays
	// parameterized (CLAUDE.md "no string concat") and we never push more args
	// than the placeholders we generate.
	sets := make([]string, 0, 2)
	args := make([]any, 0, 3)
	args = append(args, id)
	if name != nil {
		args = append(args, *name)
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)))
	}
	if address != nil {
		args = append(args, *address)
		sets = append(sets, fmt.Sprintf("address = $%d", len(args)))
	}
	if len(sets) == 0 {
		// Nothing to update — verify existence so the caller still gets 404
		// for a missing row instead of a misleading 200.
		const checkStmt = `select 1 from branches where id = $1 and deleted_at is null`
		var one int
		if err := q.QueryRow(ctx, checkStmt, id).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return pgx.ErrNoRows
			}
			return fmt.Errorf("repo: update branch existence check: %w", err)
		}
		return nil
	}

	stmt := `update branches set ` + strings.Join(sets, ", ") +
		` where id = $1 and deleted_at is null`
	tag, err := q.Exec(ctx, stmt, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SoftDeleteBranch sets deleted_at = now. Caller (handler) is responsible for
// running the BRANCH_IN_USE pre-checks — keep this helper focused on the
// mutation so it composes inside WithTx with CountActiveMembers/Admins.
// Returns pgx.ErrNoRows when the row is already missing/soft-deleted.
func SoftDeleteBranch(ctx context.Context, tx pgx.Tx, id int64, now time.Time) error {
	const stmt = `update branches set deleted_at = $2 where id = $1 and deleted_at is null`
	tag, err := tx.Exec(ctx, stmt, id, now)
	if err != nil {
		return fmt.Errorf("repo: soft delete branch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// CountActiveMembers returns the number of non-deleted members attached to a
// branch — used by DELETE /api/branches/:id to enforce 409 BRANCH_IN_USE.
func CountActiveMembers(ctx context.Context, q Querier, branchID int64) (int, error) {
	const stmt = `select count(*) from members where branch_id = $1 and deleted_at is null`
	var n int
	if err := q.QueryRow(ctx, stmt, branchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count active members: %w", err)
	}
	return n, nil
}

// CountActiveAdmins returns the number of non-deleted admins attached to a
// branch — used by DELETE /api/branches/:id to enforce 409 BRANCH_IN_USE.
func CountActiveAdmins(ctx context.Context, q Querier, branchID int64) (int, error) {
	const stmt = `select count(*) from admins where branch_id = $1 and deleted_at is null`
	var n int
	if err := q.QueryRow(ctx, stmt, branchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count active admins: %w", err)
	}
	return n, nil
}
