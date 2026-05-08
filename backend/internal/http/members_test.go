//go:build integration

package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// ---------- members admin routes ----------

func TestMembers_GET_RequiresAuth(t *testing.T) {
	f := newAdminFixture(t)
	rec := postWithAuth(t, f.r, http.MethodGet, "/api/members", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestMembers_BranchAdminScopedList: a branch admin sees only their branch
// members; a row from another branch is never returned.
func TestMembers_BranchAdminScopedList(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, &testutil.BranchOpts{Name: "내지점"})
	other := testutil.CreateBranch(t, f.pool, &testutil.BranchOpts{Name: "다른지점"})
	mineMember := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: mine, Name: "내회원"})
	otherMember := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: other, Name: "남회원"})

	_, access := loginAs(t, f, "branch", &mine)
	rec := postWithAuth(t, f.r, http.MethodGet, "/api/members", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			ID         int64  `json:"id"`
			BranchName string `json:"branch_name"`
		} `json:"items"`
	}
	mustDecode(t, rec, &resp)
	seen := map[int64]bool{}
	for _, it := range resp.Items {
		seen[it.ID] = true
		if it.BranchName == "" {
			t.Errorf("response item missing branch_name: %+v", it)
		}
	}
	if !seen[mineMember] {
		t.Errorf("own member id %d missing from list: %+v", mineMember, resp.Items)
	}
	if seen[otherMember] {
		t.Errorf("other-branch member %d leaked into list: %+v", otherMember, resp.Items)
	}
}

// TestMembers_GetById_OtherBranch_404: a branch admin querying a member from
// another branch must receive 404 (not 403) per the existence-hiding rule.
func TestMembers_GetById_OtherBranch_404(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, nil)
	other := testutil.CreateBranch(t, f.pool, nil)
	otherMember := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: other})

	_, access := loginAs(t, f, "branch", &mine)
	rec := postWithAuth(t, f.r, http.MethodGet, "/api/members/"+itoa(otherMember), access, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestMembers_Pagination: 25 members → page 1 returns 20 + cursor; page 2
// returns 5 + nil cursor; no row appears twice.
func TestMembers_Pagination(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	for i := 0; i < 25; i++ {
		testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
		time.Sleep(time.Millisecond)
	}
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodGet, "/api/members?limit=20", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page1: %d %s", rec.Code, rec.Body.String())
	}
	var p1 struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	mustDecode(t, rec, &p1)
	if len(p1.Items) != 20 {
		t.Fatalf("page1 len=%d", len(p1.Items))
	}
	if p1.NextCursor == nil {
		t.Fatalf("page1 next_cursor=nil")
	}

	rec = postWithAuth(t, f.r, http.MethodGet, "/api/members?limit=20&cursor="+*p1.NextCursor, access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page2: %d %s", rec.Code, rec.Body.String())
	}
	var p2 struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	mustDecode(t, rec, &p2)
	if len(p2.Items) != 5 {
		t.Fatalf("page2 len=%d", len(p2.Items))
	}
	if p2.NextCursor != nil {
		t.Errorf("page2 next_cursor should be nil, got %v", p2.NextCursor)
	}

	seen := map[int64]struct{}{}
	for _, it := range p1.Items {
		seen[it.ID] = struct{}{}
	}
	for _, it := range p2.Items {
		if _, dup := seen[it.ID]; dup {
			t.Errorf("row %d appears in both pages", it.ID)
		}
	}
}

func TestMembers_LimitOutOfRange(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodGet, "/api/members?limit=101", access, nil)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_LIMIT") {
		t.Errorf("limit=101 expected 400 INVALID_LIMIT, got %d %s", rec.Code, rec.Body.String())
	}
	rec = postWithAuth(t, f.r, http.MethodGet, "/api/members?limit=100", access, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("limit=100 expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestMembers_BadCursor(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	rec := postWithAuth(t, f.r, http.MethodGet, "/api/members?cursor=not-a-cursor", access, nil)
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_CURSOR") {
		t.Fatalf("expected 400 INVALID_CURSOR, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestMembers_CreateAndPhoneDuplicate: branch admin creates a member and
// receives 201; the same phone in the same branch yields 409 PHONE_DUPLICATE.
func TestMembers_CreateAndPhoneDuplicate(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "branch", &bid)

	body := map[string]any{
		"name":       "김민수",
		"phone":      "01012345678",
		"birth_date": "1990-04-15",
	}
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/members", access, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rec = postWithAuth(t, f.r, http.MethodPost, "/api/members", access, body)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "PHONE_DUPLICATE") {
		t.Fatalf("expected 409 PHONE_DUPLICATE, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestMembers_BranchAdminPostForcesOwnBranch: a branch admin sending a
// branch_id field has it overwritten to their own branch.
func TestMembers_BranchAdminPostForcesOwnBranch(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, nil)
	other := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "branch", &mine)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/members", access, map[string]any{
		"name":       "spoof",
		"phone":      "01077778888",
		"birth_date": "1990-01-01",
		"branch_id":  other,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID       int64 `json:"id"`
		BranchID int64 `json:"branch_id"`
	}
	mustDecode(t, rec, &resp)
	if resp.BranchID != mine {
		t.Errorf("branch_id was not forced to caller's branch: got=%d mine=%d", resp.BranchID, mine)
	}
}

func TestMembers_CreateInvalidPhone(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "branch", &bid)
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/members", access, map[string]any{
		"name": "n", "phone": "010-1234-5678", "birth_date": "1990-01-01",
	})
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_PHONE") {
		t.Fatalf("expected 400 INVALID_PHONE, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestMembers_PatchIgnoresBranchID: PATCH with branch_id field must NOT move
// the member to another branch.
func TestMembers_PatchIgnoresBranchID(t *testing.T) {
	f := newAdminFixture(t)
	mine := testutil.CreateBranch(t, f.pool, nil)
	other := testutil.CreateBranch(t, f.pool, nil)
	id := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: mine, Name: "before"})
	_, access := loginAs(t, f, "branch", &mine)

	rec := postWithAuth(t, f.r, http.MethodPatch, "/api/members/"+itoa(id), access, map[string]any{
		"name":      "after",
		"branch_id": other,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID       int64  `json:"id"`
		BranchID int64  `json:"branch_id"`
		Name     string `json:"name"`
	}
	mustDecode(t, rec, &resp)
	if resp.BranchID != mine {
		t.Errorf("branch_id changed via PATCH: got=%d want=%d", resp.BranchID, mine)
	}
	if resp.Name != "after" {
		t.Errorf("name not updated: %q", resp.Name)
	}
}

func TestMembers_DeleteAndGet404(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	id := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid})
	_, access := loginAs(t, f, "branch", &bid)

	rec := postWithAuth(t, f.r, http.MethodDelete, "/api/members/"+itoa(id), access, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = postWithAuth(t, f.r, http.MethodGet, "/api/members/"+itoa(id), access, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rec.Code)
	}
}

// TestMembers_AdminListExposesFullPII: admin responses include the raw
// 11-digit phone and full birth_date (the masking is kiosk-only).
func TestMembers_AdminListExposesFullPII(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	testutil.CreateMember(t, f.pool, &testutil.MemberOpts{
		BranchID: bid, Name: "n", Phone: "01099887766", BirthDate: "1985-06-22",
	})
	_, access := loginAs(t, f, "branch", &bid)
	rec := postWithAuth(t, f.r, http.MethodGet, "/api/members", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, "01099887766") {
		t.Errorf("admin response should contain raw phone, body=%s", body)
	}
	if !contains(body, "1985-06-22") {
		t.Errorf("admin response should contain raw birth_date, body=%s", body)
	}
}

