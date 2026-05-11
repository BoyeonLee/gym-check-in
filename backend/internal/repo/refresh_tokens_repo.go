// refresh_tokens_repo.go owns SQL for revoked_refresh_tokens. The table is
// the negative-list keyed by jti — a refresh token is rejected at /api/admin/
// refresh if its jti appears here. Logout, password change side-effects, and
// admin DELETE all funnel through Revoke.
//
// IMPORTANT: there is no "issued_refresh_tokens" table. We cannot enumerate a
// user's outstanding refresh jtis to bulk-revoke. Bulk invalidation
// (password change, role change) is therefore implemented by bumping
// admins.password_updated_at — refresh validation compares claim.iat against
// it. The dedicated table here only catches the explicit logout case where
// we *do* know the jti from the request body.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// IsRevoked reports whether jti is on the revoked list.
func IsRevoked(ctx context.Context, q Querier, jti string) (bool, error) {
	const stmt = `select 1 from revoked_refresh_tokens where jti = $1`
	var one int
	err := q.QueryRow(ctx, stmt, jti).Scan(&one)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("repo: is revoked: %w", err)
}

// Revoke records a jti as revoked. ON CONFLICT DO NOTHING makes the call
// idempotent — logout retries or duplicate refresh-stream invalidations are
// no-ops rather than 23505 unique violations.
func Revoke(ctx context.Context, q Querier, jti string, adminID int64, now time.Time) error {
	const stmt = `
		insert into revoked_refresh_tokens (jti, admin_id, revoked_at)
		values ($1, $2, $3)
		on conflict (jti) do nothing
	`
	if _, err := q.Exec(ctx, stmt, jti, adminID, now); err != nil {
		return fmt.Errorf("repo: revoke refresh token: %w", err)
	}
	return nil
}
