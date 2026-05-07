//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

func TestRevokeAndIsRevoked(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	const jti = "11111111-1111-4111-8111-111111111111"
	if got, err := repo.IsRevoked(ctx, pool, jti); err != nil || got {
		t.Fatalf("expected not revoked: got=%v err=%v", got, err)
	}

	if err := repo.Revoke(ctx, pool, jti, adminID, time.Now().UTC()); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got, err := repo.IsRevoked(ctx, pool, jti); err != nil || !got {
		t.Fatalf("expected revoked: got=%v err=%v", got, err)
	}
	// Idempotent: second Revoke must not error.
	if err := repo.Revoke(ctx, pool, jti, adminID, time.Now().UTC()); err != nil {
		t.Fatalf("Revoke again: %v", err)
	}

	// Different jti remains not revoked.
	if got, err := repo.IsRevoked(ctx, pool, "22222222-2222-4222-8222-222222222222"); err != nil || got {
		t.Fatalf("unrelated jti should be not revoked: got=%v err=%v", got, err)
	}
}
