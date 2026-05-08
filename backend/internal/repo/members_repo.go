// members_repo.go owns SQL for the members table.
//
// Reads filter `deleted_at IS NULL` so soft-deleted rows never leak into
// list/get responses (CLAUDE.md soft-delete invariant). Branch-admin scope
// is enforced as an optional `scopeBranchID` parameter — when non-nil the
// helper appends `branch_id = $scope` so a cross-branch read silently returns
// nil (which the handler maps to 404, not 403, per the security note in
// backend/CLAUDE.md).
//
// Writes return raw pgx errors so the handler layer can run them through
// apperr.FromDBError to map 23505 on `members_branch_phone_unique` →
// 409 PHONE_DUPLICATE.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// MemberRow is the in-memory shape used by handler responses. BranchName
// comes from a JOIN against branches so list pages don't N+1.
type MemberRow struct {
	ID         int64
	BranchID   int64
	BranchName string
	Name       string
	Phone      string
	PhoneLast4 string
	BirthDate  time.Time
	DeletedAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const memberSelectColumns = `
	m.id, m.branch_id, b.name AS branch_name,
	m.name, m.phone, m.phone_last4, m.birth_date,
	m.deleted_at, m.created_at, m.updated_at
`

func scanMember(row pgx.Row) (*MemberRow, error) {
	var m MemberRow
	if err := row.Scan(
		&m.ID, &m.BranchID, &m.BranchName,
		&m.Name, &m.Phone, &m.PhoneLast4, &m.BirthDate,
		&m.DeletedAt, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// GetMember returns the active member by id, scoped to the caller's branch
// when scopeBranchID != nil. Returns (nil, nil) for missing / soft-deleted /
// cross-branch — caller maps to 404.
func GetMember(ctx context.Context, q Querier, id int64, scopeBranchID *int64) (*MemberRow, error) {
	args := []any{id}
	scope := ""
	if scopeBranchID != nil {
		args = append(args, *scopeBranchID)
		scope = " and m.branch_id = $2"
	}
	stmt := `
		select ` + memberSelectColumns + `
		from members m
		left join branches b on b.id = m.branch_id
		where m.id = $1 and m.deleted_at is null` + scope
	row, err := scanMember(q.QueryRow(ctx, stmt, args...))
	if err != nil {
		return nil, fmt.Errorf("repo: get member: %w", err)
	}
	return row, nil
}

// CreateMemberInput is the partial-write payload accepted by InsertMember.
// Validation (phone regex, name length) happens in the handler so errors map
// to the right user-facing code; the DB CHECK is the safety net.
type CreateMemberInput struct {
	BranchID  int64
	Name      string
	Phone     string
	BirthDate time.Time
}

// InsertMember creates a row and returns the generated id.
func InsertMember(ctx context.Context, q Querier, in CreateMemberInput) (int64, error) {
	const stmt = `
		insert into members (branch_id, name, phone, birth_date)
		values ($1, $2, $3, $4)
		returning id
	`
	var id int64
	if err := q.QueryRow(ctx, stmt, in.BranchID, in.Name, in.Phone, in.BirthDate).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateMemberInput is the partial-update payload. Pointer fields distinguish
// "absent" (leave alone) from "explicit value". branch_id is intentionally
// absent — the schema allows changing it but the product rule forbids
// transferring a member to another branch (handler ignores any incoming
// branch_id field).
type UpdateMemberInput struct {
	Name      *string
	Phone     *string
	BirthDate *time.Time
}

// UpdateMember applies a partial update bounded to {name, phone, birth_date}.
// Returns pgx.ErrNoRows for missing/soft-deleted/cross-branch targets.
func UpdateMember(ctx context.Context, q Querier, id int64, in UpdateMemberInput, scopeBranchID *int64) error {
	sets := make([]string, 0, 3)
	args := make([]any, 0, 5)
	args = append(args, id)

	if in.Name != nil {
		args = append(args, *in.Name)
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)))
	}
	if in.Phone != nil {
		args = append(args, *in.Phone)
		sets = append(sets, fmt.Sprintf("phone = $%d", len(args)))
	}
	if in.BirthDate != nil {
		args = append(args, *in.BirthDate)
		sets = append(sets, fmt.Sprintf("birth_date = $%d", len(args)))
	}

	scope := ""
	if scopeBranchID != nil {
		args = append(args, *scopeBranchID)
		scope = fmt.Sprintf(" and branch_id = $%d", len(args))
	}

	if len(sets) == 0 {
		// Nothing to set — verify existence so the caller still gets a 404 for
		// a missing target instead of a misleading 200.
		stmt := `select 1 from members where id = $1 and deleted_at is null` + scope
		var one int
		if err := q.QueryRow(ctx, stmt, args...).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return pgx.ErrNoRows
			}
			return fmt.Errorf("repo: update member existence check: %w", err)
		}
		return nil
	}

	stmt := `update members set ` + strings.Join(sets, ", ") +
		` where id = $1 and deleted_at is null` + scope
	tag, err := q.Exec(ctx, stmt, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SoftDeleteMember sets deleted_at = now. Caller should run this inside the
// same scope check the rest of the helpers use.
func SoftDeleteMember(ctx context.Context, q Querier, id int64, scopeBranchID *int64, now time.Time) error {
	args := []any{id, now}
	scope := ""
	if scopeBranchID != nil {
		args = append(args, *scopeBranchID)
		scope = " and branch_id = $3"
	}
	stmt := `update members set deleted_at = $2
		where id = $1 and deleted_at is null` + scope
	tag, err := q.Exec(ctx, stmt, args...)
	if err != nil {
		return fmt.Errorf("repo: soft delete member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListMembersInput controls the cursor-paginated list. ScopeBranchID enforces
// the branch-admin filter; BranchFilter is a global-only optional drill-down.
// Cursor is the last (created_at, id) tuple from the previous page; Limit
// bounds the page size (handler caps to 100).
type ListMembersInput struct {
	ScopeBranchID *int64
	Cursor        *ListCursor
	Limit         int
	BranchFilter  *int64
}

// ListCursor mirrors httpapi.Cursor — the repo deliberately does NOT import
// the http package, so we duplicate the small struct here to keep the
// dependency direction handler→repo.
type ListCursor struct {
	T  time.Time
	ID int64
}

// ListMembers returns rows ordered by created_at DESC, id DESC with keyset
// pagination. Returns (rows, nextCursor, error); nextCursor is nil when the
// page is the last one.
func ListMembers(ctx context.Context, q Querier, in ListMembersInput) ([]MemberRow, *ListCursor, error) {
	if in.Limit <= 0 {
		in.Limit = 20
	}

	conds := []string{"m.deleted_at is null"}
	args := make([]any, 0, 5)

	if in.ScopeBranchID != nil {
		args = append(args, *in.ScopeBranchID)
		conds = append(conds, fmt.Sprintf("m.branch_id = $%d", len(args)))
	} else if in.BranchFilter != nil {
		args = append(args, *in.BranchFilter)
		conds = append(conds, fmt.Sprintf("m.branch_id = $%d", len(args)))
	}
	if in.Cursor != nil {
		args = append(args, in.Cursor.T)
		idx1 := len(args)
		args = append(args, in.Cursor.ID)
		idx2 := len(args)
		conds = append(conds, fmt.Sprintf("(m.created_at, m.id) < ($%d, $%d)", idx1, idx2))
	}
	args = append(args, in.Limit+1)
	limitIdx := len(args)

	stmt := `
		select ` + memberSelectColumns + `
		from members m
		left join branches b on b.id = m.branch_id
		where ` + strings.Join(conds, " and ") + `
		order by m.created_at desc, m.id desc
		limit $` + fmt.Sprintf("%d", limitIdx)
	rows, err := q.Query(ctx, stmt, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("repo: list members: %w", err)
	}
	defer rows.Close()

	out := make([]MemberRow, 0, in.Limit)
	for rows.Next() {
		var m MemberRow
		if err := rows.Scan(
			&m.ID, &m.BranchID, &m.BranchName,
			&m.Name, &m.Phone, &m.PhoneLast4, &m.BirthDate,
			&m.DeletedAt, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("repo: list members scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("repo: list members rows: %w", err)
	}

	var next *ListCursor
	if len(out) > in.Limit {
		// We fetched limit+1 — the extra row is the proof that "another page
		// exists" and its predecessor's (created_at, id) becomes the cursor.
		last := out[in.Limit-1]
		next = &ListCursor{T: last.CreatedAt, ID: last.ID}
		out = out[:in.Limit]
	}
	return out, next, nil
}

// SearchInput configures the kiosk-facing search. Mode is "name" | "phone" |
// "memberId"; the handler validates each before reaching here. Today is the
// SearchInput drives the kiosk search. The "active membership" gate uses
// KST today computed inside SQL (`(now() AT TIME ZONE 'Asia/Seoul')::date`),
// so callers don't pass a clock — this matches the project rule that DB
// session timezone is UTC and KST conversion must be explicit.
type SearchInput struct {
	BranchID int64
	Mode     string
	Q        string
}

// SearchHit is the trimmed-down kiosk row. The repo returns Phone (full)
// and BirthDate (full) for the handler to mask; callers MUST NOT serialize
// these fields directly.
type SearchHit struct {
	ID              int64
	Name            string
	Phone           string
	BirthDate       time.Time
	LastCheckedInAt *time.Time
}

// SearchMembers powers GET /api/members/search. Active membership filter is
// applied via EXISTS so a member with only paused/refunded/expired rows is
// excluded. The query fetches limit+1 rows so the handler can flip
// `truncated=true` when more matches exist than will fit on screen.
func SearchMembers(ctx context.Context, q Querier, in SearchInput) ([]SearchHit, bool, error) {
	const limit = 20

	args := []any{in.BranchID}
	var modeCond string
	switch in.Mode {
	case "name":
		// Escape the LIKE wildcards (`%`, `_`, `\`) so a hostile search query
		// can't match every row or DoS the index. The third argument tells
		// PostgreSQL the escape character.
		args = append(args, escapeLike(in.Q)+"%")
		modeCond = "m.name like $2 escape '\\'"
	case "phone":
		args = append(args, in.Q)
		modeCond = "m.phone_last4 = $2"
	case "memberId":
		args = append(args, in.Q)
		modeCond = "m.id = $2::bigint"
	default:
		return nil, false, fmt.Errorf("repo: search members: unknown mode %q", in.Mode)
	}
	args = append(args, limit+1)

	// Active-membership gate uses KST today computed in SQL — the pool pins
	// the session timezone to UTC, so the explicit `AT TIME ZONE 'Asia/Seoul'`
	// is required for the comparison to be correct around KST midnight.
	stmt := `
		select m.id, m.name, m.phone, m.birth_date,
		       (select max(checked_in_at) from check_ins ci where ci.member_id = m.id) as last_ci
		from members m
		where m.branch_id = $1
		  and m.deleted_at is null
		  and ` + modeCond + `
		  and exists (
		      select 1 from memberships ms
		      where ms.member_id = m.id
		        and ms.status = 'active'
		        and ms.start_date <= (now() at time zone 'Asia/Seoul')::date
		        and ms.end_date   >= (now() at time zone 'Asia/Seoul')::date
		  )
		order by last_ci desc nulls last, m.id asc
		limit $3
	`
	rows, err := q.Query(ctx, stmt, args...)
	if err != nil {
		return nil, false, fmt.Errorf("repo: search members: %w", err)
	}
	defer rows.Close()

	out := make([]SearchHit, 0, limit)
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.Name, &h.Phone, &h.BirthDate, &h.LastCheckedInAt); err != nil {
			return nil, false, fmt.Errorf("repo: search members scan: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("repo: search members rows: %w", err)
	}

	truncated := false
	if len(out) > limit {
		truncated = true
		out = out[:limit]
	}
	return out, truncated, nil
}

// escapeLike escapes the three LIKE-special characters so caller input is
// always treated as a literal prefix. The chosen escape character is `\`,
// matching the SQL `escape '\\'` clause above.
func escapeLike(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(s)
}

// CountTodayCheckIns returns the KST-today check-in count for branchID. The
// SQL converts checked_in_at to KST and compares to KST(now)::date so the
// query stays correct regardless of the session timezone (we pin to UTC).
func CountTodayCheckIns(ctx context.Context, q Querier, branchID int64) (int, error) {
	const stmt = `
		select count(*) from check_ins
		where branch_id = $1
		  and (checked_in_at at time zone 'Asia/Seoul')::date
		    = (now() at time zone 'Asia/Seoul')::date
	`
	var n int
	if err := q.QueryRow(ctx, stmt, branchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count today check-ins: %w", err)
	}
	return n, nil
}
