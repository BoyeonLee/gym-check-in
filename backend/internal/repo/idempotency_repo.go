// idempotency_repo.go owns SQL for the idempotency_keys table. The HTTP
// layer never issues SQL directly — it goes through these helpers, which
// surface the row when valid (created within the 24h window) and write the
// final (status, body) tuple after the handler completes.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// IdempotencyRow is the persisted form of one (key, request) tuple. The
// jsonb response_body comes back as raw bytes so the handler can replay the
// original payload byte-for-byte without re-marshalling.
type IdempotencyRow struct {
	Key            string
	AdminID        int64
	Endpoint       string
	RequestHash    string
	ResponseStatus int
	ResponseBody   []byte
	CreatedAt      time.Time
}

// FindIdempotencyKey returns the stored row when it exists AND is fresher
// than 24h. Older rows are treated as not-present so a recycled UUID after
// the grace period still gets reprocessed (matches docs/API.md "24시간
// 동안 ... 첫 응답 그대로 반환"). (nil, nil) means "no usable row".
func FindIdempotencyKey(ctx context.Context, q Querier, key string, now time.Time) (*IdempotencyRow, error) {
	const stmt = `
		select key, admin_id, endpoint, request_hash,
		       response_status, response_body, created_at
		from idempotency_keys
		where key = $1
		  and created_at >= $2::timestamptz - interval '24 hours'
	`
	var r IdempotencyRow
	err := q.QueryRow(ctx, stmt, key, now).Scan(
		&r.Key, &r.AdminID, &r.Endpoint, &r.RequestHash,
		&r.ResponseStatus, &r.ResponseBody, &r.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("repo: find idempotency key: %w", err)
	}
	return &r, nil
}

// InsertIdempotencyKey writes the final (status, body) tuple for a given key.
// On a primary-key conflict (concurrent inserts of the same key) the existing
// row is preserved — this preserves the "first response wins" guarantee even
// when two clients submit the same key racing each other.
func InsertIdempotencyKey(ctx context.Context, q Querier, row IdempotencyRow) error {
	const stmt = `
		insert into idempotency_keys (
			key, admin_id, endpoint, request_hash,
			response_status, response_body
		)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (key) do nothing
	`
	if _, err := q.Exec(ctx, stmt,
		row.Key, row.AdminID, row.Endpoint, row.RequestHash,
		row.ResponseStatus, row.ResponseBody,
	); err != nil {
		return fmt.Errorf("repo: insert idempotency key: %w", err)
	}
	return nil
}
