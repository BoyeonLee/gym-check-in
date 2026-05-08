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

// SalesSummaryInput selects the payments rows the summary aggregates over.
// From / To are inclusive paid_at bounds. BranchID is optional (global
// only — the handler enforces RequireGlobal).
type SalesSummaryInput struct {
	From     time.Time
	To       time.Time
	BranchID *int64
}

// SalesMethodBucket is one (method, totals) row in the by_method matrix.
// All three totals are non-negative integers; refund is the absolute
// value of the negative payments so the wire payload reads naturally.
type SalesMethodBucket struct {
	Method      string
	GrossTotal  int
	RefundTotal int
	NetTotal    int
}

// SalesDayBucket is one (paid_at, totals) row in the by_day matrix.
type SalesDayBucket struct {
	Date        time.Time
	GrossTotal  int
	RefundTotal int
	NetTotal    int
}

// SalesSummaryRow holds the aggregated response. Totals are projected
// from payments alone; backend rules forbid back-computing revenue from
// memberships or check_ins.
type SalesSummaryRow struct {
	GrossTotal  int
	RefundTotal int
	NetTotal    int
	ByMethod    []SalesMethodBucket
	ByDay       []SalesDayBucket
}

// SalesSummary aggregates payments into totals + per-method + per-day
// buckets.  Refund_total is reported as |sum(amount<0)| (positive value)
// so the JSON-facing handler can render symmetric numbers.
func SalesSummary(ctx context.Context, q Querier, in SalesSummaryInput) (SalesSummaryRow, error) {
	args := []any{in.From, in.To}
	branchClause := ""
	if in.BranchID != nil {
		args = append(args, *in.BranchID)
		branchClause = " and branch_id = $3"
	}

	// 1) Totals — single row.
	totalsStmt := `
		select
			coalesce(sum(case when amount > 0 then amount else 0 end), 0)::int,
			coalesce(sum(case when amount < 0 then -amount else 0 end), 0)::int,
			coalesce(sum(amount), 0)::int
		from payments
		where paid_at between $1 and $2` + branchClause

	var s SalesSummaryRow
	if err := q.QueryRow(ctx, totalsStmt, args...).Scan(&s.GrossTotal, &s.RefundTotal, &s.NetTotal); err != nil {
		return SalesSummaryRow{}, fmt.Errorf("repo: sales summary totals: %w", err)
	}

	// 2) by_method.
	methodStmt := `
		select method,
			coalesce(sum(case when amount > 0 then amount else 0 end), 0)::int,
			coalesce(sum(case when amount < 0 then -amount else 0 end), 0)::int,
			coalesce(sum(amount), 0)::int
		from payments
		where paid_at between $1 and $2` + branchClause + `
		group by method
		order by method asc`
	rows, err := q.Query(ctx, methodStmt, args...)
	if err != nil {
		return SalesSummaryRow{}, fmt.Errorf("repo: sales summary by_method: %w", err)
	}
	for rows.Next() {
		var b SalesMethodBucket
		if err := rows.Scan(&b.Method, &b.GrossTotal, &b.RefundTotal, &b.NetTotal); err != nil {
			rows.Close()
			return SalesSummaryRow{}, fmt.Errorf("repo: sales summary by_method scan: %w", err)
		}
		s.ByMethod = append(s.ByMethod, b)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SalesSummaryRow{}, fmt.Errorf("repo: sales summary by_method rows: %w", err)
	}
	rows.Close()

	// 3) by_day.
	dayStmt := `
		select paid_at,
			coalesce(sum(case when amount > 0 then amount else 0 end), 0)::int,
			coalesce(sum(case when amount < 0 then -amount else 0 end), 0)::int,
			coalesce(sum(amount), 0)::int
		from payments
		where paid_at between $1 and $2` + branchClause + `
		group by paid_at
		order by paid_at asc`
	rows, err = q.Query(ctx, dayStmt, args...)
	if err != nil {
		return SalesSummaryRow{}, fmt.Errorf("repo: sales summary by_day: %w", err)
	}
	for rows.Next() {
		var b SalesDayBucket
		if err := rows.Scan(&b.Date, &b.GrossTotal, &b.RefundTotal, &b.NetTotal); err != nil {
			rows.Close()
			return SalesSummaryRow{}, fmt.Errorf("repo: sales summary by_day scan: %w", err)
		}
		s.ByDay = append(s.ByDay, b)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SalesSummaryRow{}, fmt.Errorf("repo: sales summary by_day rows: %w", err)
	}
	rows.Close()

	if s.ByMethod == nil {
		s.ByMethod = []SalesMethodBucket{}
	}
	if s.ByDay == nil {
		s.ByDay = []SalesDayBucket{}
	}
	return s, nil
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
