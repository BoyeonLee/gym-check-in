//go:build integration

package httpapi_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/auth"
	httpapi "github.com/lboyeon1223/gym-check-in/backend/internal/http"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// adminFixture wires the admin/branches routes (step 4) on top of the auth
// chain so tests look exactly like a real admin session: login → use access
// token → call /api/admins or /api/branches. The fixture also exposes the
// pool and clock so tests can assert audit rows or fast-forward time.
type adminFixture struct {
	pool   *pgxpool.Pool
	clock  *util.FakeClock
	issuer *auth.Issuer
	r      *gin.Engine
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	pool := testutil.SetupDB(t)

	clock := &util.FakeClock{Instant: time.Unix(1_700_000_000, 0)}
	uuids := &util.FakeUUIDGen{Values: []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"dddddddd-dddd-4ddd-8ddd-dddddddddddd",
	}}
	issuer := &auth.Issuer{
		AccessSecret:  []byte("test-access-secret-" + t.Name()),
		RefreshSecret: []byte("test-refresh-secret-" + t.Name()),
		Clock:         clock,
		UUIDGen:       uuids,
	}
	authH := &httpapi.AuthHandlers{Pool: pool, Issuer: issuer, Clock: clock}
	branchH := &httpapi.BranchHandlers{Pool: pool}
	adminH := &httpapi.AdminHandlers{Pool: pool}
	memberH := &httpapi.MemberHandlers{Pool: pool}
	kioskH := &httpapi.KioskHandlers{Pool: pool}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery("dev", testLogger()))
	r.POST("/api/admin/login", authH.Login)

	// Step 5 — public kiosk routes share the same /api prefix as the
	// authenticated group. GET /api/branches lives here so the kiosk can
	// initialise without any token.
	pub := r.Group("/api")
	{
		pub.GET("/branches", branchH.List)
		pub.GET("/members/search", kioskH.SearchMembers)
		pub.GET("/check-ins/today-count", kioskH.TodayCount)
	}

	priv := r.Group("/api")
	priv.Use(middleware.RequireAuth(issuer, pool))
	priv.Use(middleware.MustChangePasswordGuard())
	{
		priv.GET("/members", memberH.List)
		priv.GET("/members/:id", memberH.GetByID)
		priv.POST("/members", memberH.Create)
		priv.PATCH("/members/:id", memberH.Update)
		priv.DELETE("/members/:id", memberH.Delete)

		g := priv.Group("", middleware.RequireGlobal())
		g.POST("/branches", branchH.Create)
		g.PATCH("/branches/:id", branchH.Update)
		g.DELETE("/branches/:id", branchH.Delete)

		g.GET("/admins", adminH.List)
		g.POST("/admins", adminH.Create)
		g.PATCH("/admins/:id", adminH.Update)
		g.DELETE("/admins/:id", adminH.Delete)
		g.POST("/admins/:id/reset-password", adminH.ResetPassword)
	}

	return &adminFixture{pool: pool, clock: clock, issuer: issuer, r: r}
}

// loginAs creates an admin (with must_change_password=false so they bypass
// the guard) and returns a valid access token. Used by tests that need a
// caller with a known role/branch.
func loginAs(t *testing.T, f *adminFixture, role string, branchID *int64) (int64, string) {
	t.Helper()
	username := "actor-" + t.Name()
	id, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: username, Role: role, BranchID: branchID,
		MustChangePassword: false,
	})
	rec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": username, "password": plain,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login as %s: status=%d body=%s", role, rec.Code, rec.Body.String())
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	mustDecode(t, rec, &resp)
	return id, resp.AccessToken
}

// ---------- branches ----------

// TestBranches_ListPublic — step 5 moved GET /api/branches to the public
// (kiosk-facing) group. A request without any Authorization header must
// succeed; this test guards against a future regression that re-attaches
// the auth middleware.
func TestBranches_ListPublic(t *testing.T) {
	f := newAdminFixture(t)
	rec := postWithAuth(t, f.r, http.MethodGet, "/api/branches", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBranches_ListAndCreateRoundTrip(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{"name": "강남점", "address": "서울 강남구 1번지"}
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/branches", access, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      int64   `json:"id"`
		Name    string  `json:"name"`
		Address *string `json:"address"`
	}
	mustDecode(t, rec, &created)
	if created.ID == 0 || created.Name != "강남점" || created.Address == nil || *created.Address != "서울 강남구 1번지" {
		t.Errorf("created drift: %+v", created)
	}

	// Audit row recorded.
	if n := countAuditRows(t, f.pool, "branch_create"); n != 1 {
		t.Errorf("branch_create audit count=%d", n)
	}

	rec = postWithAuth(t, f.r, http.MethodGet, "/api/branches", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Items []struct {
			ID      int64   `json:"id"`
			Name    string  `json:"name"`
			Address *string `json:"address"`
		} `json:"items"`
	}
	mustDecode(t, rec, &listed)
	if len(listed.Items) == 0 {
		t.Fatalf("list empty body=%s", rec.Body.String())
	}
}

func TestBranches_BranchAdminCannotMutate(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "branch", &bid)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/branches", access,
		map[string]any{"name": "denied"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("branch admin POST /branches expected 403, got %d body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestBranches_CreateAddressDuplicate(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	addr := "duplicate-addr-" + t.Name()
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/branches", access,
		map[string]any{"name": "first", "address": addr})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first: %d", rec.Code)
	}
	rec = postWithAuth(t, f.r, http.MethodPost, "/api/branches", access,
		map[string]any{"name": "second", "address": addr})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !hasErrorCode(rec.Body.Bytes(), "ADDRESS_DUPLICATE") {
		t.Errorf("expected ADDRESS_DUPLICATE, got %s", rec.Body.String())
	}
}

func TestBranches_CreateInvalidName(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/branches", access,
		map[string]any{"name": ""})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty name expected 400, got %d", rec.Code)
	}
}

func TestBranches_PatchUpdatesAndDuplicate(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	id1 := testutil.CreateBranch(t, f.pool, &testutil.BranchOpts{Name: "before", Address: "addr-A"})
	id2 := testutil.CreateBranch(t, f.pool, &testutil.BranchOpts{Name: "second", Address: "addr-B"})

	rec := postWithAuth(t, f.r, http.MethodPatch,
		"/api/branches/"+itoa(id1), access,
		map[string]any{"name": "after"})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d body=%s", rec.Code, rec.Body.String())
	}
	if n := countAuditRows(t, f.pool, "branch_update"); n != 1 {
		t.Errorf("branch_update audit count=%d", n)
	}

	// Conflict: try to set id2's address to id1's.
	rec = postWithAuth(t, f.r, http.MethodPatch,
		"/api/branches/"+itoa(id2), access,
		map[string]any{"address": "addr-A"})
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "ADDRESS_DUPLICATE") {
		t.Errorf("expected 409 ADDRESS_DUPLICATE, got %d %s", rec.Code, rec.Body.String())
	}

	// Missing id → 404.
	rec = postWithAuth(t, f.r, http.MethodPatch, "/api/branches/999999", access,
		map[string]any{"name": "x"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing id expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBranches_DeleteEmptyAndInUse(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)
	empty := testutil.CreateBranch(t, f.pool, nil)

	rec := postWithAuth(t, f.r, http.MethodDelete, "/api/branches/"+itoa(empty), access, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete empty expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if n := countAuditRows(t, f.pool, "branch_delete"); n != 1 {
		t.Errorf("branch_delete audit count=%d", n)
	}

	// Branch with member → BRANCH_IN_USE.
	used := testutil.CreateBranch(t, f.pool, nil)
	testutil.CreateMember(t, f.pool, &testutil.MemberOpts{BranchID: used})
	rec = postWithAuth(t, f.r, http.MethodDelete, "/api/branches/"+itoa(used), access, nil)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "BRANCH_IN_USE") {
		t.Fatalf("expected 409 BRANCH_IN_USE, got %d %s", rec.Code, rec.Body.String())
	}

	// Branch with branch admin → BRANCH_IN_USE.
	usedAdmin := testutil.CreateBranch(t, f.pool, nil)
	testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Role: "branch", BranchID: &usedAdmin,
	})
	rec = postWithAuth(t, f.r, http.MethodDelete, "/api/branches/"+itoa(usedAdmin), access, nil)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "BRANCH_IN_USE") {
		t.Fatalf("expected 409 BRANCH_IN_USE (admin), got %d %s", rec.Code, rec.Body.String())
	}

	// Already deleted → 404 on second call.
	rec = postWithAuth(t, f.r, http.MethodDelete, "/api/branches/"+itoa(empty), access, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("second delete expected 404, got %d", rec.Code)
	}
}

// ---------- shared helpers ----------

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
