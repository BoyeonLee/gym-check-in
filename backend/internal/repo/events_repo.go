// events_repo.go owns SQL for the membership_events ledger. Every
// pause/unpause/cancel-pause/refund/bulk-extend transition writes one row
// here so the membership detail page can render a complete history without
// inferring it from the membership row's current state.
package repo

import (
	"context"
	"fmt"
	"time"
)

// EventRow is the in-memory shape of a membership_events row. The pointer
// fields are populated only for the actions that use them — pause sets
// PauseStartDate / PauseEndDate, unpause sets ActualPauseEnd, bulk_extend
// sets ExtendDays.
type EventRow struct {
	ID             int64
	MembershipID   int64
	Action         string
	PauseStartDate *time.Time
	PauseEndDate   *time.Time
	ActualPauseEnd *time.Time
	ExtendDays     *int
	Reason         string
	PerformedBy    int64
	CreatedAt      time.Time
}

// InsertEvent appends a ledger row. Action must be one of the five values
// allowed by the schema CHECK ('pause' | 'unpause' | 'cancel_pause' |
// 'refund' | 'bulk_extend'); otherwise PostgreSQL returns 23514 and the
// caller's apperr.FromDBError surfaces 400 INVALID_INPUT.
func InsertEvent(ctx context.Context, q Querier, in EventRow) error {
	const stmt = `
		insert into membership_events (
			membership_id, action,
			pause_start_date, pause_end_date,
			actual_pause_end, extend_days,
			reason, performed_by
		)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	if _, err := q.Exec(ctx, stmt,
		in.MembershipID, in.Action,
		in.PauseStartDate, in.PauseEndDate,
		in.ActualPauseEnd, in.ExtendDays,
		in.Reason, in.PerformedBy,
	); err != nil {
		return err
	}
	return nil
}

// ListEventsByMembership returns every ledger row for the membership in
// reverse-chronological order so the UI history table reads top-to-bottom
// newest to oldest (per docs/API.md GET /api/memberships/:id contract).
func ListEventsByMembership(ctx context.Context, q Querier, membershipID int64) ([]EventRow, error) {
	const stmt = `
		select id, membership_id, action,
		       pause_start_date, pause_end_date,
		       actual_pause_end, extend_days,
		       reason, performed_by, created_at
		from membership_events
		where membership_id = $1
		order by created_at desc, id desc
	`
	rows, err := q.Query(ctx, stmt, membershipID)
	if err != nil {
		return nil, fmt.Errorf("repo: list events: %w", err)
	}
	defer rows.Close()

	out := make([]EventRow, 0)
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(
			&e.ID, &e.MembershipID, &e.Action,
			&e.PauseStartDate, &e.PauseEndDate,
			&e.ActualPauseEnd, &e.ExtendDays,
			&e.Reason, &e.PerformedBy, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repo: list events scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list events rows: %w", err)
	}
	return out, nil
}
