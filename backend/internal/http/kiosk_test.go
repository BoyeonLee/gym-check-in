//go:build integration

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// TestKiosk_BranchesPublic confirms GET /api/branches is reachable without
// any Authorization header (kiosk initialisation needs the list before
// anyone has logged in).
func TestKiosk_BranchesPublic(t *testing.T) {
	f := newAdminFixture(t)
	testutil.CreateBranch(t, f.pool, &testutil.BranchOpts{Name: "공개지점"})
	rec := postWithAuth(t, f.r, http.MethodGet, "/api/branches", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("public /api/branches expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "공개지점") {
		t.Errorf("response missing branch: %s", rec.Body.String())
	}
}

// TestKiosk_SearchPublicAndMasked: no auth header is required, and the
// response masks phone and birth_date.
func TestKiosk_SearchPublicAndMasked(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	mid := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{
		BranchID: bid, Name: "김민수", Phone: "01012345678", BirthDate: "1990-04-15",
	})
	testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: mid, Status: "active",
		StartDate: time.Now().UTC().Format("2006-01-02"),
		EndDate:   time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})

	url := "/api/members/search?branchId=" + itoa(bid) + "&mode=phone&q=5678"
	rec := postWithAuth(t, f.r, http.MethodGet, url, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if contains(body, "01012345678") {
		t.Fatalf("kiosk response leaked raw phone: %s", body)
	}
	if contains(body, "1990-04-15") {
		t.Fatalf("kiosk response leaked raw birth_date: %s", body)
	}
	if !contains(body, "010-****-5678") {
		t.Errorf("expected masked phone, body=%s", body)
	}
	if !contains(body, "**-04-15") {
		t.Errorf("expected masked birth, body=%s", body)
	}
	if !contains(body, "#"+itoa(mid)) {
		t.Errorf("expected member_id_display #%d, body=%s", mid, body)
	}
}

// TestKiosk_SearchExcludesInactive: a member without an active membership
// must not show up in the kiosk search. Both candidates share the prefix
// "김민" so the 2-character minimum is satisfied while still letting the
// EXISTS filter do the actual exclusion.
func TestKiosk_SearchExcludesInactive(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)

	idA := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid, Name: "김민수"})
	testutil.CreateMembership(t, f.pool, &testutil.MembershipOpts{
		MemberID: idA, Status: "active",
		StartDate: time.Now().UTC().Format("2006-01-02"),
		EndDate:   time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	idB := testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: bid, Name: "김민호"})

	url := "/api/members/search?branchId=" + itoa(bid) + "&mode=name&q=" + urlEncode("김민")
	rec := postWithAuth(t, f.r, http.MethodGet, url, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			ID int64 `json:"id"`
		} `json:"results"`
	}
	mustDecode(t, rec, &resp)
	if len(resp.Results) != 1 || resp.Results[0].ID != idA {
		t.Errorf("expected exactly one hit (id=%d), got %+v", idA, resp.Results)
	}
	for _, h := range resp.Results {
		if h.ID == idB {
			t.Errorf("inactive member %d should be excluded", idB)
		}
	}
}

func TestKiosk_SearchInvalidInput(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)

	cases := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"name 1자",
			"/api/members/search?branchId=" + itoa(bid) + "&mode=name&q=" + urlEncode("김"),
			"QUERY_TOO_SHORT",
		},
		{"phone 3자리",
			"/api/members/search?branchId=" + itoa(bid) + "&mode=phone&q=123",
			"INVALID_INPUT",
		},
		{"memberId non-numeric",
			"/api/members/search?branchId=" + itoa(bid) + "&mode=memberId&q=abc",
			"INVALID_INPUT",
		},
		{"unknown mode",
			"/api/members/search?branchId=" + itoa(bid) + "&mode=other&q=hi",
			"INVALID_INPUT",
		},
		{"branchId missing",
			"/api/members/search?mode=phone&q=1234",
			"INVALID_INPUT",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postWithAuth(t, f.r, http.MethodGet, c.url, "", nil)
			if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), c.wantErr) {
				t.Fatalf("expected 400 %s, got %d %s", c.wantErr, rec.Code, rec.Body.String())
			}
		})
	}

	url := "/api/members/search?branchId=" + itoa(bid) + "&mode=name&q=" + urlEncode("김민")
	rec := postWithAuth(t, f.r, http.MethodGet, url, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("name 2자 expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestKiosk_TodayCount(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	rec := postWithAuth(t, f.r, http.MethodGet,
		"/api/check-ins/today-count?branchId="+itoa(bid), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"count":0`) {
		t.Errorf("expected count:0, body=%s", rec.Body.String())
	}

	rec = postWithAuth(t, f.r, http.MethodGet, "/api/check-ins/today-count", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing branchId, got %d", rec.Code)
	}
}

// urlEncode performs minimal percent-encoding for non-ASCII query bytes used
// in these tests; net/url would also work but this keeps the helper local.
func urlEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, r := range []byte(s) {
		switch {
		case 'A' <= r && r <= 'Z', 'a' <= r && r <= 'z', '0' <= r && r <= '9',
			r == '-', r == '_', r == '.', r == '~':
			b.WriteByte(r)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[r>>4])
			b.WriteByte(hex[r&0x0F])
		}
	}
	return b.String()
}
