// memberships_repo.go owns SQL for the memberships table — INSERT and the
// four lifecycle mutators (pause / unpause / cancel-pause / refund) plus
// reads (single, single+detail, current).
//
// All write helpers return raw pgx errors so handlers can run them through
// apperr.FromDBError. The most important mapping is 23P01 (exclusion_violation)
// → 409 MEMBERSHIP_PERIOD_OVERLAP, fired by both the EXCLUDE constraint on
// (member_id, daterange(start_date, end_date, '[]')) WHERE status IN
// ('active','paused') and by pause/extend logic that nudges end_date into a
// neighbouring future membership's window.
//
// Status preconditions (status='active' for pause, status='paused' for
// unpause, etc.) are validated by the handler BEFORE the helper runs — the
// helpers below simply UPDATE the row and trust the caller's check. Race
// safety against concurrent writers comes from row-level locks PostgreSQL
// implicitly takes during UPDATE.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// MembershipRow is the in-memory shape of a memberships row. Pointer fields
// reflect SQL NULL semantics: months only set for 'monthly', remaining only
// for 'pass10', pause_start/end populated when a pause is registered or
// active.
type MembershipRow struct {
	ID             int64
	MemberID       int64
	Type           string
	Months         *int
	StartDate      time.Time
	EndDate        time.Time
	Remaining      *int
	Status         string
	PauseStartDate *time.Time
	PauseEndDate   *time.Time
	PauseUsed      bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const membershipColumns = `
	id, member_id, type, months, start_date, end_date,
	remaining, status, pause_start_date, pause_end_date,
	pause_used, created_at, updated_at
`

func scanMembership(row pgx.Row) (*MembershipRow, error) {
	var m MembershipRow
	if err := row.Scan(
		&m.ID, &m.MemberID, &m.Type, &m.Months,
		&m.StartDate, &m.EndDate, &m.Remaining, &m.Status,
		&m.PauseStartDate, &m.PauseEndDate, &m.PauseUsed,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// GetMembership returns a single membership row scoped to the caller's
// branch when scopeBranchID != nil. Cross-branch / member-soft-deleted /
// missing collapse into (nil, nil) so the handler can render a 404.
func GetMembership(ctx context.Context, q Querier, id int64, scopeBranchID *int64) (*MembershipRow, error) {
	args := []any{id}
	scope := ""
	if scopeBranchID != nil {
		args = append(args, *scopeBranchID)
		scope = " and m.branch_id = $2"
	}
	stmt := `
		select ` + membershipColumns + `
		from memberships ms
		join members m on m.id = ms.member_id
		where ms.id = $1 and m.deleted_at is null` + scope
	row, err := scanMembership(q.QueryRow(ctx, stmt, args...))
	if err != nil {
		return nil, fmt.Errorf("repo: get membership: %w", err)
	}
	return row, nil
}

// MembershipDetail bundles a membership row with its full payment and event
// history so GET /api/memberships/:id renders without N+1 round-trips.
type MembershipDetail struct {
	Membership MembershipRow
	Payments   []PaymentRow
	Events     []EventRow
}

// GetMembershipDetail composes the three reads (membership / payments /
// events) under one helper. If the membership is invisible to the caller's
// scope the function returns (nil, nil) so the handler can map to 404.
func GetMembershipDetail(ctx context.Context, q Querier, id int64, scopeBranchID *int64) (*MembershipDetail, error) {
	m, err := GetMembership(ctx, q, id, scopeBranchID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	payments, err := ListPaymentsByMembership(ctx, q, m.ID)
	if err != nil {
		return nil, err
	}
	events, err := ListEventsByMembership(ctx, q, m.ID)
	if err != nil {
		return nil, err
	}
	return &MembershipDetail{Membership: *m, Payments: payments, Events: events}, nil
}

// GetCurrentMembership returns the most recently created membership in
// status active/paused for memberID. Returns (nil, nil) when none exist —
// both an empty-ledger member and a member with only refunded/expired
// memberships fall here. Used by the grant form to prefill the suggested
// start_date as (current_end + 1 day).
func GetCurrentMembership(ctx context.Context, q Querier, memberID int64) (*MembershipRow, error) {
	const stmt = `
		select ` + membershipColumns + `
		from memberships ms
		where ms.member_id = $1
		  and ms.status in ('active', 'paused')
		order by ms.created_at desc, ms.id desc
		limit 1
	`
	row, err := scanMembership(q.QueryRow(ctx, stmt, memberID))
	if err != nil {
		return nil, fmt.Errorf("repo: get current membership: %w", err)
	}
	return row, nil
}

// GrantInput is the create-payload for InsertMembership. The handler computes
// EndDate based on Type (monthly: start + months month, pass10: start + 2 month).
type GrantInput struct {
	MemberID  int64
	Type      string // "monthly" | "pass10"
	Months    *int   // populated for monthly
	Remaining *int   // populated for pass10 (= 10)
	StartDate time.Time
	EndDate   time.Time
}

// InsertMembership writes a membership row inside the caller's transaction.
// status defaults to 'active'. EXCLUDE violations surface as PostgreSQL 23P01;
// the caller's apperr.FromDBError translates to 409 MEMBERSHIP_PERIOD_OVERLAP.
func InsertMembership(ctx context.Context, tx pgx.Tx, in GrantInput) (int64, error) {
	const stmt = `
		insert into memberships (
			member_id, type, months, remaining,
			start_date, end_date, status
		)
		values ($1, $2, $3, $4, $5, $6, 'active')
		returning id
	`
	var id int64
	if err := tx.QueryRow(ctx, stmt,
		in.MemberID, in.Type, in.Months, in.Remaining,
		in.StartDate, in.EndDate,
	).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// PauseInput configures ApplyPause. Today is supplied so tests / handlers
// can pin "today" deterministically; production passes the KST today.
type PauseInput struct {
	ID             int64
	PauseStartDate time.Time
	PauseEndDate   time.Time
	Today          time.Time
}

// ApplyPause registers a pause window. Trusts the handler to have validated
// status='active', pause_used=false, and the date-range invariants; the
// helper just updates the row.
//
// Behaviour:
//   - end_date += (pause_end - pause_start)
//   - pause_used = true
//   - status = 'paused' iff pause_start <= today, else unchanged ('active')
//
// EXCLUDE violations triggered by the lengthened end_date overlapping a
// future membership surface as 23P01.
func ApplyPause(ctx context.Context, tx pgx.Tx, in PauseInput) error {
	const stmt = `
		update memberships
		set pause_start_date = $2,
		    pause_end_date   = $3,
		    pause_used       = true,
		    end_date         = end_date + ($3::date - $2::date),
		    status           = case
		        when $2::date <= $4::date then 'paused'
		        else status
		    end
		where id = $1
	`
	tag, err := tx.Exec(ctx, stmt, in.ID, in.PauseStartDate, in.PauseEndDate, in.Today)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// UnpauseInput configures ApplyUnpause. ActualPauseEnd is the date the
// membership comes off pause (typically today in handler usage).
type UnpauseInput struct {
	ID             int64
	ActualPauseEnd time.Time
}

// ApplyUnpause shortens end_date by the unused pause days, clears the pause
// markers, and flips status back to 'active'. Caller must verify status was
// 'paused' before calling.
func ApplyUnpause(ctx context.Context, tx pgx.Tx, in UnpauseInput) error {
	const stmt = `
		update memberships
		set end_date         = end_date - (pause_end_date - $2::date),
		    pause_start_date = null,
		    pause_end_date   = null,
		    status           = 'active'
		where id = $1
	`
	tag, err := tx.Exec(ctx, stmt, in.ID, in.ActualPauseEnd)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// CancelPauseInput configures ApplyCancelPause. Today is informational so
// the helper signature is symmetric with PauseInput; the SQL itself reads
// the existing pause_*_date columns.
type CancelPauseInput struct {
	ID    int64
	Today time.Time
}

// ApplyCancelPause undoes a future-scheduled pause: end_date is rolled back
// by the originally-added duration, pause markers are cleared, and pause_used
// flips back to false so the operator may register a fresh pause later.
// Caller must verify status='active' AND pause_used=true AND
// pause_start_date > today before calling.
func ApplyCancelPause(ctx context.Context, tx pgx.Tx, in CancelPauseInput) error {
	const stmt = `
		update memberships
		set end_date         = end_date - (pause_end_date - pause_start_date),
		    pause_start_date = null,
		    pause_end_date   = null,
		    pause_used       = false
		where id = $1
	`
	_ = in.Today
	tag, err := tx.Exec(ctx, stmt, in.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// RefundInput configures ApplyRefund. The helper sets status='refunded'
// only — the matching negative payment row is inserted by the handler via
// InsertPayment in the same transaction.
type RefundInput struct {
	ID int64
}

// ApplyRefund flips status to 'refunded'. Caller must verify the current
// status is one of {active, paused, active+future-start} before calling
// (expired refunds surface 409 MEMBERSHIP_ALREADY_EXPIRED, double-refund
// surfaces 409 via idempotency).
func ApplyRefund(ctx context.Context, tx pgx.Tx, in RefundInput) error {
	const stmt = `
		update memberships
		set status = 'refunded'
		where id = $1
	`
	tag, err := tx.Exec(ctx, stmt, in.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
