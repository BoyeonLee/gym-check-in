//go:build integration

package repo_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// TestMembers_InsertAndGet verifies the round-trip: a fresh INSERT lands a row
// with a generated id, and GetMember returns the row joined with branch_name.
func TestMembers_InsertAndGet(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, &testutil.BranchOpts{Name: "강남점"})

	id, err := repo.InsertMember(ctx, pool, repo.CreateMemberInput{
		BranchID:  bid,
		Name:      "김민수",
		Phone:     "01012345678",
		BirthDate: time.Date(1990, 4, 15, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Fatalf("insert returned id=0")
	}

	row, err := repo.GetMember(ctx, pool, id, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row == nil {
		t.Fatalf("get returned nil")
	}
	if row.Name != "김민수" || row.Phone != "01012345678" || row.PhoneLast4 != "5678" {
		t.Errorf("row drift: %+v", row)
	}
	if row.BranchName != "강남점" {
		t.Errorf("branch name join missing: %q", row.BranchName)
	}
}

// TestMembers_PhoneDuplicate confirms the partial unique index blocks a
// re-insert in the same branch (and FromDBError surfaces PHONE_DUPLICATE).
func TestMembers_PhoneDuplicate(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, nil)

	in := repo.CreateMemberInput{
		BranchID:  bid,
		Name:      "first",
		Phone:     "01099998888",
		BirthDate: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, err := repo.InsertMember(ctx, pool, in); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	in.Name = "second"
	_, err := repo.InsertMember(ctx, pool, in)
	if err == nil {
		t.Fatalf("second insert should fail")
	}
	ae := apperr.FromDBError(err)
	if ae.Code != "PHONE_DUPLICATE" {
		t.Errorf("expected PHONE_DUPLICATE, got %s", ae.Code)
	}
}

// TestMembers_GetMember_BranchScope ensures a branch admin can't read a
// member from another branch — GetMember with scopeBranchID returns nil.
func TestMembers_GetMember_BranchScope(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	mine := testutil.CreateBranch(t, pool, nil)
	other := testutil.CreateBranch(t, pool, nil)
	id := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: other})

	row, err := repo.GetMember(ctx, pool, id, &mine)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil for cross-branch read, got %+v", row)
	}
}

// TestMembers_SoftDelete_Hides verifies a soft-deleted row drops out of GET
// and LIST. Hard delete is forbidden by repo design (FK protected).
func TestMembers_SoftDelete_Hides(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, nil)
	id := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})

	if err := repo.SoftDeleteMember(ctx, pool, id, nil, time.Now().UTC()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	row, err := repo.GetMember(ctx, pool, id, nil)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil after soft delete, got %+v", row)
	}
}

// TestMembers_ListPagination seeds 25 rows and pages them via cursor.
// Page 1 should return 20 + next_cursor; page 2 should return 5 + nil.
func TestMembers_ListPagination(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, nil)
	const N = 25
	for i := 0; i < N; i++ {
		testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid})
		// Sleep 1ms so created_at strictly orders rows; the keyset uses
		// (created_at, id) so identical timestamps still resolve via id.
		time.Sleep(time.Millisecond)
	}

	rows1, next1, err := repo.ListMembers(ctx, pool, repo.ListMembersInput{
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(rows1) != 20 {
		t.Fatalf("page 1 len=%d", len(rows1))
	}
	if next1 == nil {
		t.Fatalf("page 1 next_cursor should not be nil")
	}

	rows2, next2, err := repo.ListMembers(ctx, pool, repo.ListMembersInput{
		Cursor: next1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(rows2) != 5 {
		t.Fatalf("page 2 len=%d", len(rows2))
	}
	if next2 != nil {
		t.Fatalf("page 2 next_cursor=%v want nil", next2)
	}

	// No row should appear in both pages.
	seen := map[int64]struct{}{}
	for _, r := range rows1 {
		seen[r.ID] = struct{}{}
	}
	for _, r := range rows2 {
		if _, dup := seen[r.ID]; dup {
			t.Fatalf("row %d appeared in both pages", r.ID)
		}
	}
}

// TestMembers_Search_NamePrefixActiveOnly: only members with an active
// membership are returned; name search is prefix.
func TestMembers_Search_NamePrefixActiveOnly(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, nil)

	// Active members.
	idA := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid, Name: "김민수"})
	testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: idA, Status: "active",
		StartDate: time.Now().UTC().Format("2006-01-02"),
		EndDate:   time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	idB := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid, Name: "김지훈"})
	testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: idB, Status: "active",
		StartDate: time.Now().UTC().Format("2006-01-02"),
		EndDate:   time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	// Inactive member (no active membership).
	_ = testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid, Name: "김현수"})

	hits, truncated, err := repo.SearchMembers(ctx, pool, repo.SearchInput{
		BranchID: bid, Mode: "name", Q: "김", Today: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if truncated {
		t.Errorf("truncated unexpectedly true")
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 active hits, got %d", len(hits))
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.Name, "김") {
			t.Errorf("name not prefix-matched: %q", h.Name)
		}
	}
}

// TestMembers_Search_PhoneAndMemberId verifies the phone (last 4) and
// memberId modes find the right rows. Active membership is required.
func TestMembers_Search_PhoneAndMemberId(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, nil)

	id := testutil.CreateMember(t, pool, &testutil.MemberOpts{
		BranchID: bid, Name: "Targets", Phone: "01087651234",
	})
	testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: id, Status: "active",
		StartDate: time.Now().UTC().Format("2006-01-02"),
		EndDate:   time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})

	hits, _, err := repo.SearchMembers(ctx, pool, repo.SearchInput{
		BranchID: bid, Mode: "phone", Q: "1234", Today: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("phone hit drift: %+v", hits)
	}

	hits, _, err = repo.SearchMembers(ctx, pool, repo.SearchInput{
		BranchID: bid, Mode: "memberId", Q: itoaInt64(id), Today: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("search memberId: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("memberId hit drift: %+v", hits)
	}
}

// TestMembers_Search_Truncated: 21 active members → 20 returned + truncated=true.
func TestMembers_Search_Truncated(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, nil)

	for i := 0; i < 21; i++ {
		mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{
			BranchID: bid, Name: "프리픽스",
		})
		testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
			MemberID: mid, Status: "active",
			StartDate: time.Now().UTC().Format("2006-01-02"),
			EndDate:   time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
		})
	}

	hits, truncated, err := repo.SearchMembers(ctx, pool, repo.SearchInput{
		BranchID: bid, Mode: "name", Q: "프", Today: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !truncated {
		t.Errorf("expected truncated=true")
	}
	if len(hits) != 20 {
		t.Errorf("expected 20 hits, got %d", len(hits))
	}
}

// TestMembers_Search_NameLikeEscape protects against `%`/`_` injection in the
// prefix LIKE: a row whose name happens to contain literal `%` should NOT
// match a different prefix.
func TestMembers_Search_NameLikeEscape(t *testing.T) {
	pool := testutil.SetupDB(t)
	ctx := context.Background()
	bid := testutil.CreateBranch(t, pool, nil)

	// Active member named "abc"; the search query is "a%" — without escaping,
	// that pattern would also match "abc"; with escaping, it requires the
	// literal '%' character so the result must be empty.
	mid := testutil.CreateMember(t, pool, &testutil.MemberOpts{BranchID: bid, Name: "abc"})
	testutil.CreateMembership(t, pool, &testutil.MembershipOpts{
		MemberID: mid, Status: "active",
		StartDate: time.Now().UTC().Format("2006-01-02"),
		EndDate:   time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})

	hits, _, err := repo.SearchMembers(ctx, pool, repo.SearchInput{
		BranchID: bid, Mode: "name", Q: "a%", Today: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for escaped %% pattern, got %d", len(hits))
	}
}

func itoaInt64(v int64) string {
	// minimal helper — strconv would do it too, but keeping the test deps thin.
	if v == 0 {
		return "0"
	}
	neg := false
	if v < 0 {
		neg = true
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
