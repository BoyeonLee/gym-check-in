//go:build integration

package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

// TestAdmins_BranchAdminForbidden — every /api/admins route is global-only.
// A branch admin must hit 403 even on the read-only list.
func TestAdmins_BranchAdminForbidden(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "branch", &bid)

	rec := postWithAuth(t, f.r, http.MethodGet, "/api/admins", access, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdmins_ListIncludesJoinedBranchName — global admin lists admins; the
// payload carries the joined branch_name and never exposes password_hash /
// temp_password_expires_at.
func TestAdmins_ListIncludesJoinedBranchName(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, &testutil.BranchOpts{Name: "강남점"})
	testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "list-branch-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodGet, "/api/admins", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Body must NOT include password_hash, temp_password_expires_at, or any
	// freshly-issued temporary_password value (the list payload has no business
	// surfacing reset-password output).
	body := rec.Body.String()
	if contains(body, "password_hash") || contains(body, "temp_password") || contains(body, "temporary_password") {
		t.Fatalf("list response leaked sensitive fields: %s", body)
	}
	var resp struct {
		Items []struct {
			ID         int64   `json:"id"`
			Username   string  `json:"username"`
			Role       string  `json:"role"`
			BranchID   *int64  `json:"branch_id"`
			BranchName *string `json:"branch_name"`
		} `json:"items"`
	}
	mustDecode(t, rec, &resp)
	var sawJoin bool
	for _, it := range resp.Items {
		if it.Role == "branch" && it.BranchID != nil && *it.BranchID == bid {
			if it.BranchName == nil || *it.BranchName != "강남점" {
				t.Errorf("branch_name JOIN missing: %+v", it)
			}
			sawJoin = true
		}
	}
	if !sawJoin {
		t.Errorf("did not see branch admin row with joined name: %+v", resp.Items)
	}
}

// TestAdmins_CreateAndDuplicate — happy path plus 409 USERNAME_DUPLICATE on
// the second insert. Response body must not echo the plaintext password.
func TestAdmins_CreateAndDuplicate(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "global", nil)

	body := map[string]any{
		"username":  "newop-" + t.Name(),
		"password":  "GoodPass123",
		"role":      "branch",
		"branch_id": bid,
	}
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/admins", access, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if contains(rec.Body.String(), "GoodPass123") {
		t.Fatalf("create response echoed plaintext password: %s", rec.Body.String())
	}
	if contains(rec.Body.String(), "password_hash") {
		t.Fatalf("create response leaked password_hash: %s", rec.Body.String())
	}
	if n := countAuditRows(t, f.pool, "admin_create"); n != 1 {
		t.Errorf("admin_create audit count=%d", n)
	}
	// Audit metadata MUST NOT carry the password.
	if metaContains(t, f.pool, "admin_create", "GoodPass123") {
		t.Fatalf("audit metadata leaked plaintext password")
	}

	// Duplicate username → 409 USERNAME_DUPLICATE.
	rec = postWithAuth(t, f.r, http.MethodPost, "/api/admins", access, body)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "USERNAME_DUPLICATE") {
		t.Fatalf("expected 409 USERNAME_DUPLICATE, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdmins_CreateWeakPassword — password strength is enforced before insert.
func TestAdmins_CreateWeakPassword(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/admins", access, map[string]any{
		"username": "weak", "password": "abc", "role": "branch", "branch_id": bid,
	})
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "WEAK_PASSWORD") {
		t.Errorf("expected 400 WEAK_PASSWORD, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdmins_CreateRoleBranchMismatch — global with branch_id, branch without
// branch_id, and unknown role all reject with 400 INVALID_ROLE_BRANCH per
// docs/API.md POST /api/admins error catalog.
func TestAdmins_CreateRoleBranchMismatch(t *testing.T) {
	f := newAdminFixture(t)
	_, access := loginAs(t, f, "global", nil)

	cases := []map[string]any{
		{"username": "g1", "password": "GoodPass123", "role": "global", "branch_id": 1}, // global must be NULL
		{"username": "b1", "password": "GoodPass123", "role": "branch"},                  // branch needs id
		{"username": "x1", "password": "GoodPass123", "role": "weird"},                   // unknown role
	}
	for i, c := range cases {
		rec := postWithAuth(t, f.r, http.MethodPost, "/api/admins", access, c)
		if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_ROLE_BRANCH") {
			t.Errorf("case[%d]: expected 400 INVALID_ROLE_BRANCH, got %d %s", i, rec.Code, rec.Body.String())
		}
	}
}

// TestAdmins_PatchSelfRoleBlocked — caller cannot demote/move themselves.
func TestAdmins_PatchSelfRoleBlocked(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	selfID, access := loginAs(t, f, "global", nil)

	// Try to flip own role.
	rec := postWithAuth(t, f.r, http.MethodPatch,
		"/api/admins/"+itoa(selfID), access,
		map[string]any{"role": "branch", "branch_id": bid})
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "CANNOT_MODIFY_SELF_ROLE") {
		t.Errorf("expected 409 CANNOT_MODIFY_SELF_ROLE, got %d %s", rec.Code, rec.Body.String())
	}

	// Username-only edit on self is allowed.
	rec = postWithAuth(t, f.r, http.MethodPatch,
		"/api/admins/"+itoa(selfID), access,
		map[string]any{"username": "renamed-self-" + t.Name()})
	if rec.Code != http.StatusOK {
		t.Errorf("self username edit expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdmins_PatchBranchChangeInvalidatesTokens — flipping branch_id bumps
// password_updated_at, so the affected user's existing access token fails.
func TestAdmins_PatchBranchChangeInvalidatesTokens(t *testing.T) {
	f := newAdminFixture(t)
	branchA := testutil.CreateBranch(t, f.pool, nil)
	branchB := testutil.CreateBranch(t, f.pool, nil)

	// Create an active branch admin and log them in to get a fresh token.
	username := "victim-" + t.Name()
	victimID, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: username, Role: "branch", BranchID: &branchA,
		MustChangePassword: false,
	})
	rec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": username, "password": plain,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("victim login: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	mustDecode(t, rec, &resp)
	victimAccess := resp.AccessToken

	// Old token works pre-patch. Use /api/members which still requires auth
	// (step 5 moved /api/branches to the public/kiosk group).
	pre := postWithAuth(t, f.r, http.MethodGet, "/api/members", victimAccess, nil)
	if pre.Code != http.StatusOK {
		t.Fatalf("victim pre-patch GET expected 200, got %d %s", pre.Code, pre.Body.String())
	}

	// Global moves the victim to branchB.
	_, globalAccess := loginAs(t, f, "global", nil)
	// Sleep 1s so the bumped password_updated_at is strictly > token iat.
	time.Sleep(time.Second)
	rec = postWithAuth(t, f.r, http.MethodPatch,
		"/api/admins/"+itoa(victimID), globalAccess,
		map[string]any{"branch_id": branchB})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch branch_id: %d %s", rec.Code, rec.Body.String())
	}

	// Old token must now be 401.
	post := postWithAuth(t, f.r, http.MethodGet, "/api/members", victimAccess, nil)
	if post.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after branch_id flip, got %d %s", post.Code, post.Body.String())
	}
}

// TestAdmins_PatchUsernameDuplicate — PATCH 시 다른 row와 username 충돌 →
// 409 USERNAME_DUPLICATE (POST 경로 외 PATCH도 동일 코드 노출).
func TestAdmins_PatchUsernameDuplicate(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	taken, _ := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "taken-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	target, _ := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "movable-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	_, access := loginAs(t, f, "global", nil)
	_ = taken // create only

	rec := postWithAuth(t, f.r, http.MethodPatch,
		"/api/admins/"+itoa(target), access,
		map[string]any{"username": "taken-" + t.Name()})
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "USERNAME_DUPLICATE") {
		t.Errorf("expected 409 USERNAME_DUPLICATE, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdmins_PatchInvalidRoleBranch — PATCH로 role/branch_id 조합을 깨면
// 400 INVALID_ROLE_BRANCH (POST 검증 경로가 PATCH에서도 동일하게 동작).
func TestAdmins_PatchInvalidRoleBranch(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	target, _ := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "rbtarget-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	_, access := loginAs(t, f, "global", nil)

	cases := []struct {
		name string
		body map[string]any
	}{
		// branch role인데 branch_id를 NULL(생략)로 — global 전환 의도가 없으므로 무효
		{"branch role + null branch_id 시도", map[string]any{"role": "branch", "branch_id": nil}},
		// global role + branch_id non-null
		{"global role + non-null branch_id", map[string]any{"role": "global", "branch_id": bid}},
		// 알 수 없는 role
		{"unknown role", map[string]any{"role": "weird"}},
	}
	for _, c := range cases {
		rec := postWithAuth(t, f.r, http.MethodPatch,
			"/api/admins/"+itoa(target), access, c.body)
		if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "INVALID_ROLE_BRANCH") {
			t.Errorf("%s: expected 400 INVALID_ROLE_BRANCH, got %d %s",
				c.name, rec.Code, rec.Body.String())
		}
	}
}

// TestAdmins_PatchSoftDeletedNotFound — soft-deleted admin에 PATCH 시 404.
// 미존재와 soft-deleted를 동일 404로 통일하는 보안 모범의 PATCH 경로 검증.
func TestAdmins_PatchSoftDeletedNotFound(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	target, _ := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "ghost-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	_, access := loginAs(t, f, "global", nil)

	// soft-delete via DELETE route.
	if rec := postWithAuth(t, f.r, http.MethodDelete,
		"/api/admins/"+itoa(target), access, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("seed delete: %d %s", rec.Code, rec.Body.String())
	}

	// PATCH on soft-deleted id → 404 (not 409/200).
	rec := postWithAuth(t, f.r, http.MethodPatch,
		"/api/admins/"+itoa(target), access,
		map[string]any{"username": "renamed-after-delete"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 on soft-deleted PATCH, got %d %s", rec.Code, rec.Body.String())
	}

	// 미존재 id도 동일 404.
	rec = postWithAuth(t, f.r, http.MethodPatch,
		"/api/admins/9999999", access,
		map[string]any{"username": "ghost"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing id PATCH expected 404, got %d", rec.Code)
	}
}

// TestAdmins_DeleteSelfBlocked — caller can't delete themselves.
func TestAdmins_DeleteSelfBlocked(t *testing.T) {
	f := newAdminFixture(t)
	selfID, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodDelete,
		"/api/admins/"+itoa(selfID), access, nil)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "CANNOT_DELETE_SELF") {
		t.Errorf("expected 409 CANNOT_DELETE_SELF, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdmins_DeleteHappyAndMissing — happy path + 404 for missing/already-deleted.
func TestAdmins_DeleteHappyAndMissing(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	target, _ := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "doomed-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodDelete,
		"/api/admins/"+itoa(target), access, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if n := countAuditRows(t, f.pool, "admin_delete"); n != 1 {
		t.Errorf("admin_delete audit count=%d", n)
	}

	// Second delete on the same id → 404.
	rec = postWithAuth(t, f.r, http.MethodDelete,
		"/api/admins/"+itoa(target), access, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("second delete expected 404, got %d", rec.Code)
	}

	// Random id → 404.
	rec = postWithAuth(t, f.r, http.MethodDelete, "/api/admins/999999", access, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing id expected 404, got %d", rec.Code)
	}
}

// TestAdmins_ResetPasswordHappyPath — server returns 12-char temp password +
// KST expires_at; can immediately log in with it; metadata never carries the
// plaintext.
func TestAdmins_ResetPasswordHappyPath(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	target, _ := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "resetme-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodPost,
		"/api/admins/"+itoa(target)+"/reset-password", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TemporaryPassword string `json:"temporary_password"`
		ExpiresAt         string `json:"expires_at"`
	}
	mustDecode(t, rec, &resp)
	if len(resp.TemporaryPassword) != 12 {
		t.Errorf("expected 12-char temp password, got %q", resp.TemporaryPassword)
	}
	if resp.ExpiresAt == "" || !contains(resp.ExpiresAt, "+09:00") {
		t.Errorf("expires_at should be KST formatted, got %q", resp.ExpiresAt)
	}

	// Audit row exists and metadata does NOT carry the plaintext.
	if n := countAuditRows(t, f.pool, "password_reset"); n != 1 {
		t.Errorf("password_reset audit count=%d", n)
	}
	if metaContains(t, f.pool, "password_reset", resp.TemporaryPassword) {
		t.Fatalf("audit metadata leaked plaintext temp password")
	}

	// DB must NOT store the plaintext anywhere.
	if dbHasString(t, f.pool, resp.TemporaryPassword) {
		t.Fatalf("plaintext temp password leaked into DB rows")
	}

	// Login with the temp password — must succeed and return must_change=true.
	loginRec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": getUsername(t, f.pool, target),
		"password": resp.TemporaryPassword,
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login with temp: %d %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		MustChangePassword bool `json:"must_change_password"`
	}
	mustDecode(t, loginRec, &loginResp)
	if !loginResp.MustChangePassword {
		t.Errorf("must_change_password should be true after reset")
	}
}

// TestAdmins_ResetPasswordSelfBlocked — operator cannot reset their own
// credential through this endpoint (defence in depth, see CANNOT_RESET_SELF).
func TestAdmins_ResetPasswordSelfBlocked(t *testing.T) {
	f := newAdminFixture(t)
	selfID, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodPost,
		"/api/admins/"+itoa(selfID)+"/reset-password", access, nil)
	if rec.Code != http.StatusConflict || !hasErrorCode(rec.Body.Bytes(), "CANNOT_RESET_SELF") {
		t.Errorf("expected 409 CANNOT_RESET_SELF, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdmins_ResetPasswordExpiresIn24h — after the temp_password_expires_at
// boundary, login flips to TEMP_PASSWORD_EXPIRED.
func TestAdmins_ResetPasswordExpiresIn24h(t *testing.T) {
	f := newAdminFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	target, _ := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "expireme-" + t.Name(), Role: "branch", BranchID: &bid,
	})
	_, access := loginAs(t, f, "global", nil)

	rec := postWithAuth(t, f.r, http.MethodPost,
		"/api/admins/"+itoa(target)+"/reset-password", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	mustDecode(t, rec, &resp)

	// Fast-forward expiry by 25h directly in DB (no clock injection in
	// handlers — see admins_auth.go handlerNow comment).
	if _, err := f.pool.Exec(context.Background(),
		"update admins set temp_password_expires_at = now() - interval '1 hour' where id=$1",
		target,
	); err != nil {
		t.Fatalf("expire: %v", err)
	}

	loginRec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": getUsername(t, f.pool, target),
		"password": resp.TemporaryPassword,
	})
	if loginRec.Code != http.StatusUnauthorized || !hasErrorCode(loginRec.Body.Bytes(), "TEMP_PASSWORD_EXPIRED") {
		t.Fatalf("expected 401 TEMP_PASSWORD_EXPIRED, got %d %s", loginRec.Code, loginRec.Body.String())
	}
}

// ---------- helpers ----------

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// metaContains reports whether any admin_audit_logs row with the given action
// has metadata jsonb containing `sub` as a substring. Used by reset-password /
// admin_create tests to assert plaintext credentials never enter audit rows.
func metaContains(t *testing.T, pool *pgxpool.Pool, action, sub string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"select count(*) from admin_audit_logs where action=$1 and metadata::text like '%' || $2 || '%'",
		action, sub,
	).Scan(&n); err != nil {
		t.Fatalf("metaContains: %v", err)
	}
	return n > 0
}

// dbHasString scans every text-bearing column of admins / admin_audit_logs
// for `sub` to make sure a temp password leak hasn't been written anywhere.
func dbHasString(t *testing.T, pool *pgxpool.Pool, sub string) bool {
	t.Helper()
	queries := []string{
		`select count(*) from admins where password_hash like '%' || $1 || '%'`,
		`select count(*) from admin_audit_logs where metadata::text like '%' || $1 || '%'`,
	}
	for _, q := range queries {
		var n int
		if err := pool.QueryRow(context.Background(), q, sub).Scan(&n); err != nil {
			t.Fatalf("dbHasString: %v", err)
		}
		if n > 0 {
			return true
		}
	}
	return false
}

// getUsername reads the admins.username for `id` regardless of deleted_at —
// reset-password tests need it to log in with the temp password.
func getUsername(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var u string
	if err := pool.QueryRow(context.Background(),
		"select username from admins where id=$1", id,
	).Scan(&u); err != nil {
		t.Fatalf("getUsername: %v", err)
	}
	return u
}
