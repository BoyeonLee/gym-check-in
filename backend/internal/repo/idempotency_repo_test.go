//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// TestIdempotencyKey_FreshLookup verifies the typical Lookup→Insert→Lookup
// cycle: an unseen key returns nil, the stored row is replayed verbatim, and
// 24h-old rows are filtered out so a recycled UUID after the grace period
// re-enters the normal flow.
func TestIdempotencyKey_FreshLookup(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	bid := testutil.CreateBranch(t, pool, nil)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-idem", Role: "branch", BranchID: &bid,
	})

	now := time.Now().UTC()
	const key = "11111111-1111-4111-8111-111111111111"

	// Initial lookup: nothing stored → (nil, nil).
	got, err := repo.FindIdempotencyKey(ctx, pool, key, now)
	if err != nil {
		t.Fatalf("FindIdempotencyKey (empty): %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}

	// Insert a row.
	row := repo.IdempotencyRow{
		Key:            key,
		AdminID:        adminID,
		Endpoint:       "POST /api/members/:id/memberships",
		RequestHash:    "deadbeef",
		ResponseStatus: 201,
		ResponseBody:   []byte(`{"membership":{"id":1}}`),
	}
	if err := repo.InsertIdempotencyKey(ctx, pool, row); err != nil {
		t.Fatalf("InsertIdempotencyKey: %v", err)
	}

	// Subsequent lookup within the 24h window must replay the stored row.
	got, err = repo.FindIdempotencyKey(ctx, pool, key, now)
	if err != nil {
		t.Fatalf("FindIdempotencyKey: %v", err)
	}
	if got == nil {
		t.Fatal("expected stored row, got nil")
	}
	if got.AdminID != adminID || got.Endpoint != row.Endpoint ||
		got.RequestHash != row.RequestHash || got.ResponseStatus != 201 {
		t.Errorf("row drift: %+v", got)
	}
	if string(got.ResponseBody) != `{"membership":{"id":1}}` {
		t.Errorf("response body drift: %q", got.ResponseBody)
	}
}

// TestIdempotencyKey_ConflictPreservesFirst — a second insert with the same
// key but a different request hash must NOT overwrite the first row, so the
// caller can detect the body drift and surface 409 IDEMPOTENCY_KEY_CONFLICT.
func TestIdempotencyKey_ConflictPreservesFirst(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	bid := testutil.CreateBranch(t, pool, nil)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-idem", Role: "branch", BranchID: &bid,
	})
	const key = "22222222-2222-4222-8222-222222222222"
	now := time.Now().UTC()

	first := repo.IdempotencyRow{
		Key: key, AdminID: adminID, Endpoint: "POST /x",
		RequestHash: "aaaa", ResponseStatus: 201,
		ResponseBody: []byte(`{"a":1}`),
	}
	if err := repo.InsertIdempotencyKey(ctx, pool, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	second := repo.IdempotencyRow{
		Key: key, AdminID: adminID, Endpoint: "POST /x",
		RequestHash: "bbbb", ResponseStatus: 200,
		ResponseBody: []byte(`{"a":2}`),
	}
	if err := repo.InsertIdempotencyKey(ctx, pool, second); err != nil {
		t.Fatalf("second insert (conflict): %v", err)
	}

	got, err := repo.FindIdempotencyKey(ctx, pool, key, now)
	if err != nil {
		t.Fatalf("FindIdempotencyKey: %v", err)
	}
	if got == nil {
		t.Fatal("expected row")
	}
	if got.RequestHash != "aaaa" || string(got.ResponseBody) != `{"a":1}` {
		t.Errorf("conflict overwrote stored row: %+v", got)
	}
}

// TestIdempotencyKey_ExpiredIs24hStale — a row older than 24h is invisible
// to FindIdempotencyKey so the next call re-enters the normal flow.
func TestIdempotencyKey_ExpiredIs24hStale(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)

	bid := testutil.CreateBranch(t, pool, nil)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-idem", Role: "branch", BranchID: &bid,
	})
	const key = "33333333-3333-4333-8333-333333333333"

	if err := repo.InsertIdempotencyKey(ctx, pool, repo.IdempotencyRow{
		Key: key, AdminID: adminID, Endpoint: "POST /x",
		RequestHash: "h", ResponseStatus: 201,
		ResponseBody: []byte(`{}`),
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Backdate the row past the 24h horizon.
	if _, err := pool.Exec(ctx,
		`update idempotency_keys set created_at = now() - interval '25 hours' where key = $1`,
		key,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := repo.FindIdempotencyKey(ctx, pool, key, time.Now().UTC())
	if err != nil {
		t.Fatalf("FindIdempotencyKey: %v", err)
	}
	if got != nil {
		t.Fatalf("expected expired row to be invisible, got %+v", got)
	}
}
