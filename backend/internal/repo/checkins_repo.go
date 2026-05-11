// checkins_repo.go owns SQL for the check_ins table — the kiosk insert
// transaction, the admin-side raw / daily list queries, and the small
// helper FindUnstartedMembership the handler uses to disambiguate
// "no active membership" from "membership exists but its start_date is
// in the future".
//
// All KST-day comparisons are written as `(... AT TIME ZONE 'Asia/Seoul')::date`
// because the connection pool pins session timezone to UTC. Using bare
// CURRENT_DATE here would silently break the day boundary.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// CheckInRow is the in-memory shape of a single check_ins row.
type CheckInRow struct {
	ID           int64
	MemberID     int64
	BranchID     int64
	MembershipID int64
	CheckedInAt  time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CheckInInput is the trio the kiosk handler hands to DoCheckIn. Today
// is the caller-resolved KST date; we pass it through to keep the SQL
// deterministic for tests that pin KST today.
type CheckInInput struct {
	MemberID int64
	BranchID int64
	Today    time.Time // KST date (year-month-day in any TZ — only the date portion is used by the SQL)
}

// CheckInResult bundles the side effects DoCheckIn produces so the
// handler can render a faithful response without re-reading the DB.
type CheckInResult struct {
	Row                  CheckInRow
	Membership           MembershipRow
	DecrementedRemaining bool // pass10 first-of-day check-in
	NewlyExpired         bool // remaining hit zero in this transaction
}

// DoCheckIn locks the active membership, checks for a same-day same-branch
// prior row, optionally decrements pass10 remaining (and flips status to
// 'expired' when remaining hits 0), and inserts the new check_ins row —
// all in the caller's transaction.
//
// Returns (zero, pgx.ErrNoRows) when no active membership covers today —
// the handler then runs FindUnstartedMembership to decide between
// MEMBERSHIP_NOT_STARTED (future-start exists) and NO_ACTIVE_MEMBERSHIP.
func DoCheckIn(ctx context.Context, tx pgx.Tx, in CheckInInput) (CheckInResult, error) {
	// 1) Lock the covering active membership. The SQL uses the caller's
	//    KST `today` for both bounds so the lock is deterministic
	//    regardless of the session timezone. The members JOIN enforces the
	//    branch boundary — memberships has no branch_id column so we anchor
	//    via members.branch_id (and skip soft-deleted members). A forged
	//    request with a foreign branch_id can no longer lock another
	//    branch's active membership; the caller sees pgx.ErrNoRows and the
	//    handler maps that to NO_ACTIVE_MEMBERSHIP without leaking that the
	//    membership exists in a different branch.
	const lockStmt = `
		select ms.id, ms.member_id, ms.type, ms.months, ms.start_date, ms.end_date,
		       ms.remaining, ms.status, ms.pause_start_date, ms.pause_end_date,
		       ms.pause_used, ms.created_at, ms.updated_at
		from memberships ms
		join members m on m.id = ms.member_id
		where ms.member_id = $1
		  and m.branch_id  = $3
		  and m.deleted_at is null
		  and ms.status    = 'active'
		  and ms.start_date <= $2::date
		  and ms.end_date   >= $2::date
		order by ms.end_date asc, ms.id asc
		for update of ms
		limit 1
	`
	m, err := scanMembership(tx.QueryRow(ctx, lockStmt, in.MemberID, in.Today, in.BranchID))
	if err != nil {
		return CheckInResult{}, fmt.Errorf("repo: lock active membership: %w", err)
	}
	if m == nil {
		return CheckInResult{}, pgx.ErrNoRows
	}

	// 2) Same-day / same-branch first check-in?  Used to gate pass10 decrement.
	const sameDayStmt = `
		select 1
		from check_ins
		where member_id = $1
		  and branch_id = $2
		  and (checked_in_at at time zone 'Asia/Seoul')::date = $3::date
		limit 1
	`
	var first bool
	{
		var one int
		row := tx.QueryRow(ctx, sameDayStmt, in.MemberID, in.BranchID, in.Today)
		if err := row.Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				first = true
			} else {
				return CheckInResult{}, fmt.Errorf("repo: same-day lookup: %w", err)
			}
		}
	}

	res := CheckInResult{Membership: *m}

	// 3) pass10 — decrement on first-of-day, flip status to 'expired' when
	//    the decrement bottoms out at 0. The CASE expression keeps both
	//    transitions in a single UPDATE so the row never observes
	//    remaining=0 with status='active'.
	if first && m.Type == "pass10" {
		const decStmt = `
			update memberships
			set remaining = remaining - 1,
			    status    = case when remaining - 1 <= 0 then 'expired' else status end
			where id = $1 and type = 'pass10'
			returning remaining, status
		`
		var newRemaining int
		var newStatus string
		if err := tx.QueryRow(ctx, decStmt, m.ID).Scan(&newRemaining, &newStatus); err != nil {
			return CheckInResult{}, fmt.Errorf("repo: decrement pass10: %w", err)
		}
		res.DecrementedRemaining = true
		res.Membership.Remaining = &newRemaining
		res.Membership.Status = newStatus
		if newStatus == "expired" {
			res.NewlyExpired = true
		}
	}

	// 4) Insert the check_ins row — membership_id is NOT NULL so we use the
	//    locked id directly. checked_in_at defaults to now() inside the DB.
	const insertStmt = `
		insert into check_ins (member_id, branch_id, membership_id)
		values ($1, $2, $3)
		returning id, member_id, branch_id, membership_id, checked_in_at, created_at, updated_at
	`
	row := tx.QueryRow(ctx, insertStmt, in.MemberID, in.BranchID, m.ID)
	if err := row.Scan(
		&res.Row.ID, &res.Row.MemberID, &res.Row.BranchID, &res.Row.MembershipID,
		&res.Row.CheckedInAt, &res.Row.CreatedAt, &res.Row.UpdatedAt,
	); err != nil {
		return CheckInResult{}, fmt.Errorf("repo: insert check_in: %w", err)
	}
	return res, nil
}

// FindUnstartedMembership returns the earliest active-but-future-start
// membership for (member_id, branch_id). Used by the kiosk handler to
// distinguish 422 MEMBERSHIP_NOT_STARTED from 422 NO_ACTIVE_MEMBERSHIP
// when DoCheckIn yields ErrNoRows.
//
// branchID matches the member's branch (members.branch_id) — the
// memberships table itself doesn't carry branch_id directly.
func FindUnstartedMembership(ctx context.Context, q Querier, memberID, branchID int64, today time.Time) (*MembershipRow, error) {
	const stmt = `
		select ms.id, ms.member_id, ms.type, ms.months, ms.start_date, ms.end_date,
		       ms.remaining, ms.status, ms.pause_start_date, ms.pause_end_date,
		       ms.pause_used, ms.created_at, ms.updated_at
		from memberships ms
		join members m on m.id = ms.member_id
		where ms.member_id = $1
		  and m.branch_id  = $2
		  and m.deleted_at is null
		  and ms.status    = 'active'
		  and ms.start_date > $3::date
		order by ms.start_date asc, ms.id asc
		limit 1
	`
	row, err := scanMembership(q.QueryRow(ctx, stmt, memberID, branchID, today))
	if err != nil {
		return nil, fmt.Errorf("repo: find unstarted membership: %w", err)
	}
	return row, nil
}

// CheckInListRow is the join shape returned by ListCheckInsRaw. The
// admin UI shows member_name / branch_name / membership_type alongside
// the check-in itself, so we project them here to avoid N+1 calls.
type CheckInListRow struct {
	ID             int64
	MemberID       int64
	MemberName     string
	BranchID       int64
	BranchName     string
	MembershipID   int64
	MembershipType string
	CheckedInAt    time.Time
	CreatedAt      time.Time
}

// ListCheckInsInput controls the admin list/aggregate query. ScopeBranchID
// is the branch-admin's required filter; BranchFilter is the global-only
// drilldown. From / To are inclusive KST dates (handler validates
// (To - From) <= 92 days and lays the bounds via AT TIME ZONE 'Asia/Seoul').
type ListCheckInsInput struct {
	ScopeBranchID *int64
	BranchFilter  *int64
	From          time.Time // KST date
	To            time.Time // KST date
	Cursor        *ListCursor
	Limit         int
}

// ListCheckInsRaw returns rows ordered by (checked_in_at DESC, id DESC).
// The cursor (t, id) is matched against the same projection so the
// keyset paging lines up with the ORDER BY.
func ListCheckInsRaw(ctx context.Context, q Querier, in ListCheckInsInput) ([]CheckInListRow, *ListCursor, error) {
	if in.Limit <= 0 {
		in.Limit = 20
	}

	conds := []string{
		"(ci.checked_in_at at time zone 'Asia/Seoul')::date >= $1::date",
		"(ci.checked_in_at at time zone 'Asia/Seoul')::date <= $2::date",
	}
	args := []any{in.From, in.To}

	if in.ScopeBranchID != nil {
		args = append(args, *in.ScopeBranchID)
		conds = append(conds, fmt.Sprintf("ci.branch_id = $%d", len(args)))
	} else if in.BranchFilter != nil {
		args = append(args, *in.BranchFilter)
		conds = append(conds, fmt.Sprintf("ci.branch_id = $%d", len(args)))
	}
	if in.Cursor != nil {
		args = append(args, in.Cursor.T)
		idx1 := len(args)
		args = append(args, in.Cursor.ID)
		idx2 := len(args)
		conds = append(conds, fmt.Sprintf("(ci.checked_in_at, ci.id) < ($%d, $%d)", idx1, idx2))
	}
	args = append(args, in.Limit+1)
	limitIdx := len(args)

	stmt := `
		select ci.id, ci.member_id, m.name as member_name,
		       ci.branch_id, b.name as branch_name,
		       ci.membership_id, ms.type as membership_type,
		       ci.checked_in_at, ci.created_at
		from check_ins ci
		join members  m  on m.id  = ci.member_id
		join branches b  on b.id  = ci.branch_id
		join memberships ms on ms.id = ci.membership_id
		where ` + strings.Join(conds, " and ") + `
		order by ci.checked_in_at desc, ci.id desc
		limit $` + fmt.Sprintf("%d", limitIdx)

	rows, err := q.Query(ctx, stmt, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("repo: list check-ins raw: %w", err)
	}
	defer rows.Close()

	out := make([]CheckInListRow, 0, in.Limit)
	for rows.Next() {
		var r CheckInListRow
		if err := rows.Scan(
			&r.ID, &r.MemberID, &r.MemberName,
			&r.BranchID, &r.BranchName,
			&r.MembershipID, &r.MembershipType,
			&r.CheckedInAt, &r.CreatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("repo: list check-ins raw scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("repo: list check-ins raw rows: %w", err)
	}

	var next *ListCursor
	if len(out) > in.Limit {
		last := out[in.Limit-1]
		next = &ListCursor{T: last.CheckedInAt, ID: last.ID}
		out = out[:in.Limit]
	}
	return out, next, nil
}

// DailyCheckInRow is one (member_id, kst_date, branch_id) bucket the daily
// aggregate emits. The natural group key includes branch_id because the
// same member can have memberships in multiple branches and the kiosk
// admin UI surfaces per-branch counts. CheckinCount counts ALL rows in
// the bucket — the pass10 decrement rule lives in DoCheckIn, not the
// aggregator. FirstCheckedInAt is the earliest checked_in_at inside the
// bucket so the wire payload can render a KST timestamp.
type DailyCheckInRow struct {
	MemberID         int64
	MemberName       string
	BranchID         int64
	BranchName       string
	Date             time.Time // KST date
	CheckinCount     int
	FirstCheckedInAt time.Time
}

// ListCheckInsDaily groups check_ins by (member_id, branch_id, KST date).
// The caller must validate that (To - From) <= 92 days before invoking.
// No cursor pagination — by contract the page returns the entire window
// in one shot (per backend/CLAUDE.md).
func ListCheckInsDaily(ctx context.Context, q Querier, in ListCheckInsInput) ([]DailyCheckInRow, error) {
	conds := []string{
		"(ci.checked_in_at at time zone 'Asia/Seoul')::date >= $1::date",
		"(ci.checked_in_at at time zone 'Asia/Seoul')::date <= $2::date",
	}
	args := []any{in.From, in.To}

	if in.ScopeBranchID != nil {
		args = append(args, *in.ScopeBranchID)
		conds = append(conds, fmt.Sprintf("ci.branch_id = $%d", len(args)))
	} else if in.BranchFilter != nil {
		args = append(args, *in.BranchFilter)
		conds = append(conds, fmt.Sprintf("ci.branch_id = $%d", len(args)))
	}

	stmt := `
		select ci.member_id, m.name as member_name,
		       ci.branch_id, b.name as branch_name,
		       (ci.checked_in_at at time zone 'Asia/Seoul')::date as kst_date,
		       count(*)::int as checkin_count,
		       min(ci.checked_in_at)        as first_checked_in_at
		from check_ins ci
		join members  m on m.id = ci.member_id
		join branches b on b.id = ci.branch_id
		where ` + strings.Join(conds, " and ") + `
		group by ci.member_id, m.name, ci.branch_id, b.name,
		         (ci.checked_in_at at time zone 'Asia/Seoul')::date
		order by kst_date desc, ci.member_id asc, ci.branch_id asc
	`

	rows, err := q.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: list check-ins daily: %w", err)
	}
	defer rows.Close()

	out := make([]DailyCheckInRow, 0)
	for rows.Next() {
		var r DailyCheckInRow
		if err := rows.Scan(
			&r.MemberID, &r.MemberName,
			&r.BranchID, &r.BranchName,
			&r.Date, &r.CheckinCount, &r.FirstCheckedInAt,
		); err != nil {
			return nil, fmt.Errorf("repo: list check-ins daily scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list check-ins daily rows: %w", err)
	}
	return out, nil
}
