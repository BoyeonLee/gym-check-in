// payments_repo.go owns SQL for the payments table — INSERT for grant /
// refund rows, plus two read helpers used during refund and history rendering.
//
// All inserts return raw pgx errors so callers can run them through
// apperr.FromDBError (a 23514 amount=0 violation surfaces as 400 INVALID_INPUT).
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PaymentRow is the in-memory shape of a payments row. Amount is signed
// (positive=grant, negative=refund) per the schema CHECK (amount <> 0).
type PaymentRow struct {
	ID           int64
	MembershipID int64
	BranchID     int64
	Amount       int
	Method       string
	PaidAt       time.Time
	Memo         *string
	PerformedBy  int64
	CreatedAt    time.Time
}

const paymentColumns = `
	id, membership_id, branch_id, amount, method, paid_at, memo, performed_by, created_at
`

func scanPayment(row pgx.Row) (*PaymentRow, error) {
	var p PaymentRow
	if err := row.Scan(
		&p.ID, &p.MembershipID, &p.BranchID, &p.Amount, &p.Method,
		&p.PaidAt, &p.Memo, &p.PerformedBy, &p.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// InsertPayment writes a payment row. Returns the generated id. The caller
// is responsible for picking the sign of Amount (grant > 0, refund < 0).
func InsertPayment(ctx context.Context, q Querier, in PaymentRow) (int64, error) {
	const stmt = `
		insert into payments (
			membership_id, branch_id, amount, method,
			paid_at, memo, performed_by
		)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning id
	`
	var id int64
	if err := q.QueryRow(ctx, stmt,
		in.MembershipID, in.BranchID, in.Amount, in.Method,
		in.PaidAt, in.Memo, in.PerformedBy,
	).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// GetOriginalGrantPayment returns the earliest positive payment row for a
// membership. Refund handlers use this to mirror method/amount/branch_id
// onto the negative refund row so by_method matrices stay symmetric.
func GetOriginalGrantPayment(ctx context.Context, q Querier, membershipID int64) (*PaymentRow, error) {
	const stmt = `
		select ` + paymentColumns + `
		from payments
		where membership_id = $1 and amount > 0
		order by paid_at asc, id asc
		limit 1
	`
	row, err := scanPayment(q.QueryRow(ctx, stmt, membershipID))
	if err != nil {
		return nil, fmt.Errorf("repo: get original grant payment: %w", err)
	}
	return row, nil
}

// ListPaymentsByMembership returns every payment row (grants + refunds)
// for a membership, ordered by created_at ASC so the wire payload reads
// chronologically.
func ListPaymentsByMembership(ctx context.Context, q Querier, membershipID int64) ([]PaymentRow, error) {
	const stmt = `
		select ` + paymentColumns + `
		from payments
		where membership_id = $1
		order by created_at asc, id asc
	`
	rows, err := q.Query(ctx, stmt, membershipID)
	if err != nil {
		return nil, fmt.Errorf("repo: list payments: %w", err)
	}
	defer rows.Close()

	out := make([]PaymentRow, 0)
	for rows.Next() {
		var p PaymentRow
		if err := rows.Scan(
			&p.ID, &p.MembershipID, &p.BranchID, &p.Amount, &p.Method,
			&p.PaidAt, &p.Memo, &p.PerformedBy, &p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repo: list payments scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list payments rows: %w", err)
	}
	return out, nil
}
