//go:build integration

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// TestInsertEvent_PauseAndUnpauseRoundtrip — pause then unpause events are
// inserted with the appropriate columns populated and come back chronologically.
func TestInsertEvent_PauseAndUnpauseRoundtrip(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-evt", Role: "branch", BranchID: &bid,
	})

	pauseStart := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	pauseEnd := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	if err := repo.InsertEvent(ctx, pool, repo.EventRow{
		MembershipID:   msid,
		Action:         "pause",
		PauseStartDate: &pauseStart,
		PauseEndDate:   &pauseEnd,
		Reason:         "여행",
		PerformedBy:    adminID,
	}); err != nil {
		t.Fatalf("insert pause: %v", err)
	}

	actualEnd := time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC)
	if err := repo.InsertEvent(ctx, pool, repo.EventRow{
		MembershipID:   msid,
		Action:         "unpause",
		ActualPauseEnd: &actualEnd,
		Reason:         "조기 복귀",
		PerformedBy:    adminID,
	}); err != nil {
		t.Fatalf("insert unpause: %v", err)
	}

	rows, err := repo.ListEventsByMembership(ctx, pool, msid)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 events, got %d", len(rows))
	}
	// DESC ordering — newest first (per docs/API.md GET
	// /api/memberships/:id contract). The unpause was inserted second so
	// it sits at index 0; the original pause is at index 1.
	if rows[0].Action != "unpause" || rows[1].Action != "pause" {
		t.Errorf("unexpected ordering (want DESC newest-first): %+v", rows)
	}
	if rows[1].PauseStartDate == nil || !rows[1].PauseStartDate.Equal(pauseStart) {
		t.Errorf("pause_start drift: %+v", rows[1].PauseStartDate)
	}
	if rows[0].ActualPauseEnd == nil || !rows[0].ActualPauseEnd.Equal(actualEnd) {
		t.Errorf("actual_pause_end drift: %+v", rows[0].ActualPauseEnd)
	}
}

// TestInsertEvent_RejectsUnknownAction — schema CHECK only allows the five
// canonical actions; anything else fails at the DB layer.
func TestInsertEvent_RejectsUnknownAction(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupDB(t)
	bid := testutil.CreateBranch(t, pool, nil)
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
	msid := testutil.CreateMembership(t, pool, &testutil.MembershipOpts{MemberID: mid})
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{
		Username: "op-evt-bad", Role: "branch", BranchID: &bid,
	})

	err := repo.InsertEvent(ctx, pool, repo.EventRow{
		MembershipID: msid, Action: "invalid_action", Reason: "x",
		PerformedBy: adminID,
	})
	if err == nil {
		t.Fatal("expected check_violation, got nil")
	}
}
