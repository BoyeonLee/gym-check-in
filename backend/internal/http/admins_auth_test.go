//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func init() { gin.SetMode(gin.TestMode) }

// authFixture wires the minimal router needed for auth handler tests:
// RequestID + Recovery + RequireAuth on the protected scope. No rate limit
// here — those are exercised separately and would require time advances we
// don't want to inject in every test.
type authFixture struct {
	pool   *pgxpool.Pool
	clock  *util.FakeClock
	issuer *auth.Issuer
	r      *gin.Engine
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	pool := testutil.SetupDB(t)

	// Auth-fixture clock stays anchored in the past on purpose. It drives
	// JWT iat for tokens this fixture issues. password_updated_at is
	// stamped via handlerNow() (real wall clock), and the
	// stale-after-password-change check requires `iat < password_updated_at`.
	// Anchoring iat in 2023 guarantees the gap regardless of how fast a
	// password-change test runs (a wall-clock-now anchor would put iat
	// and password_updated_at inside the same second and the check would
	// erroneously pass).
	clock := &util.FakeClock{Instant: time.Unix(1_700_000_000, 0)}
	uuids := &util.FakeUUIDGen{Values: []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
		"44444444-4444-4444-8444-444444444444",
	}}
	issuer := &auth.Issuer{
		AccessSecret:  []byte("test-access-secret-" + t.Name()),
		RefreshSecret: []byte("test-refresh-secret-" + t.Name()),
		Clock:         clock,
		UUIDGen:       uuids,
	}
	h := &httpapi.AuthHandlers{
		Pool:   pool,
		Issuer: issuer,
		Clock:  clock,
	}
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery("dev", testLogger()))
	r.POST("/api/admin/login", h.Login)
	r.POST("/api/admin/refresh", h.Refresh)
	priv := r.Group("/api/admin")
	priv.Use(middleware.RequireAuth(issuer, pool))
	priv.POST("/logout", h.Logout)
	priv.POST("/password", h.PasswordChange)
	return &authFixture{pool: pool, clock: clock, issuer: issuer, r: r}
}

func TestLogin_Success(t *testing.T) {
	f := newAuthFixture(t)
	bid := testutil.CreateBranch(t, f.pool, nil)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "alice", Role: "branch", BranchID: &bid,
		MustChangePassword: true,
	})

	rec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": "alice", "password": plain,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AccessToken        string `json:"access_token"`
		RefreshToken       string `json:"refresh_token"`
		MustChangePassword bool   `json:"must_change_password"`
		Role               string `json:"role"`
		BranchID           *int64 `json:"branch_id"`
		Username           string `json:"username"`
	}
	mustDecode(t, rec, &resp)
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Errorf("tokens missing: %+v", resp)
	}
	if !resp.MustChangePassword {
		t.Errorf("must_change_password should be true")
	}
	if resp.Role != "branch" || resp.BranchID == nil || *resp.BranchID != bid {
		t.Errorf("role/branch drift: %+v", resp)
	}
	// Audit row should exist.
	if got := countAuditRows(t, f.pool, "login_success"); got != 1 {
		t.Errorf("login_success audit count=%d, want 1", got)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	f := newAuthFixture(t)
	_, _ = testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "alice", Role: "global"})
	rec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": "alice", "password": "wrong-password",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !hasErrorCode(rec.Body.Bytes(), "UNAUTHORIZED") {
		t.Errorf("body should be UNAUTHORIZED: %s", rec.Body.String())
	}
	if got := countAuditRows(t, f.pool, "login_failure"); got != 1 {
		t.Errorf("login_failure audit count=%d, want 1", got)
	}
}

func TestLogin_UnknownUsername(t *testing.T) {
	f := newAuthFixture(t)
	rec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": "ghost", "password": "anything-works",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	// Same generic UNAUTHORIZED — no enumeration.
	if !hasErrorCode(rec.Body.Bytes(), "UNAUTHORIZED") {
		t.Errorf("body should be UNAUTHORIZED: %s", rec.Body.String())
	}
}

func TestLogin_AccountLocked(t *testing.T) {
	f := newAuthFixture(t)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "alice", Role: "global", Password: "Password1",
	})
	// Trigger 5 wrong attempts → account locks.
	for i := 0; i < 5; i++ {
		_ = postJSON(t, f.r, "/api/admin/login", map[string]any{
			"username": "alice", "password": "wrong-password",
		})
	}
	// 6th attempt with CORRECT password → still 401 ACCOUNT_LOCKED.
	rec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": "alice", "password": plain,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !hasErrorCode(rec.Body.Bytes(), "ACCOUNT_LOCKED") {
		t.Errorf("expected ACCOUNT_LOCKED: %s", rec.Body.String())
	}

	// Simulate 15 minutes elapsed: clear locked_until manually (FakeClock
	// drives token issuance, but DB-side now() is real). Reset locked_until.
	if _, err := f.pool.Exec(context.Background(),
		"update admins set locked_until = now() - interval '1 minute' where username='alice'"); err != nil {
		t.Fatalf("expire lock: %v", err)
	}
	rec = postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": "alice", "password": plain,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("after lock expiry expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLogin_TempPasswordExpired(t *testing.T) {
	f := newAuthFixture(t)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{
		Username: "alice", Role: "global", MustChangePassword: true,
	})
	if _, err := f.pool.Exec(context.Background(),
		"update admins set temp_password_expires_at = now() - interval '1 hour' where username='alice'"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := postJSON(t, f.r, "/api/admin/login", map[string]any{
		"username": "alice", "password": plain,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !hasErrorCode(rec.Body.Bytes(), "TEMP_PASSWORD_EXPIRED") {
		t.Errorf("expected TEMP_PASSWORD_EXPIRED: %s", rec.Body.String())
	}
}

func TestRefresh_RoundTripAndRevoked(t *testing.T) {
	f := newAuthFixture(t)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "alice", Role: "global"})
	access, refresh := loginOK(t, f.r, "alice", plain)

	// Advance fake clock so the new access token's iat differs from the
	// original — without this they're byte-identical because the issuer
	// signs over identical claims.
	f.clock.Instant = f.clock.Instant.Add(time.Second)
	rec := postJSON(t, f.r, "/api/admin/refresh", map[string]any{"refresh_token": refresh})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct{ AccessToken string `json:"access_token"` }
	mustDecode(t, rec, &resp)
	if resp.AccessToken == "" || resp.AccessToken == access {
		t.Errorf("expected new access token, got %q", resp.AccessToken)
	}

	// Logout invalidates the refresh jti.
	logoutResp := postWithAuth(t, f.r, http.MethodPost, "/api/admin/logout", access, map[string]any{
		"refresh_token": refresh,
	})
	if logoutResp.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logoutResp.Code, logoutResp.Body.String())
	}
	// Same refresh now 401.
	rec = postJSON(t, f.r, "/api/admin/refresh", map[string]any{"refresh_token": refresh})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", rec.Code)
	}
}

func TestRefresh_StaleAfterPasswordChange(t *testing.T) {
	f := newAuthFixture(t)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "alice", Role: "global"})
	access, refresh := loginOK(t, f.r, "alice", plain)

	// Change password — clock advances 1s so password_updated_at > issued iat.
	f.clock.Instant = f.clock.Instant.Add(time.Second)
	rec := postWithAuth(t, f.r, http.MethodPost, "/api/admin/password", access, map[string]any{
		"current_password": plain, "new_password": "BrandNew1Pass",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("password change status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Old refresh should now be 401 — its iat predates password_updated_at.
	rec = postJSON(t, f.r, "/api/admin/refresh", map[string]any{"refresh_token": refresh})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 stale refresh, got %d", rec.Code)
	}
	// Old access also denied.
	rec = postWithAuth(t, f.r, http.MethodPost, "/api/admin/logout", access, map[string]any{
		"refresh_token": refresh,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 stale access on logout, got %d", rec.Code)
	}
}

func TestPasswordChange_WeakPassword(t *testing.T) {
	f := newAuthFixture(t)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "alice", Role: "global"})
	access, _ := loginOK(t, f.r, "alice", plain)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/admin/password", access, map[string]any{
		"current_password": plain, "new_password": "abc",
	})
	if rec.Code != http.StatusBadRequest || !hasErrorCode(rec.Body.Bytes(), "WEAK_PASSWORD") {
		t.Fatalf("expected 400 WEAK_PASSWORD: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPasswordChange_WrongCurrent(t *testing.T) {
	f := newAuthFixture(t)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "alice", Role: "global"})
	access, _ := loginOK(t, f.r, "alice", plain)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/admin/password", access, map[string]any{
		"current_password": "wrong-current", "new_password": "ValidPass123",
	})
	// Token이 valid한 상태에서 폼의 "현재 비번"만 틀린 케이스 → 클라이언트가 일반
	// UNAUTHORIZED(토큰 무효)와 분기해 폼 인라인 에러를 띄울 수 있도록 별도 코드.
	if rec.Code != http.StatusUnauthorized || !hasErrorCode(rec.Body.Bytes(), "WRONG_CURRENT_PASSWORD") {
		t.Fatalf("expected 401 WRONG_CURRENT_PASSWORD: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLogout_IdempotentSecondCall(t *testing.T) {
	f := newAuthFixture(t)
	_, plain := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "alice", Role: "global"})
	access, refresh := loginOK(t, f.r, "alice", plain)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/admin/logout", access, map[string]any{
		"refresh_token": refresh,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first logout=%d", rec.Code)
	}
	// Second call with same refresh token: still 204 (idempotent).
	rec = postWithAuth(t, f.r, http.MethodPost, "/api/admin/logout", access, map[string]any{
		"refresh_token": refresh,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second logout should be idempotent 204, got %d", rec.Code)
	}
}

func TestLogout_RefreshSubMismatch(t *testing.T) {
	f := newAuthFixture(t)
	_, p1 := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "alice", Role: "global"})
	_, p2 := testutil.CreateAdmin(t, f.pool, &testutil.AdminOpts{Username: "bob", Role: "global"})
	accessAlice, _ := loginOK(t, f.r, "alice", p1)
	_, refreshBob := loginOK(t, f.r, "bob", p2)

	rec := postWithAuth(t, f.r, http.MethodPost, "/api/admin/logout", accessAlice, map[string]any{
		"refresh_token": refreshBob,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when refresh sub != access sub, got %d body=%s",
			rec.Code, rec.Body.String())
	}
}

// --- helpers ---

func loginOK(t *testing.T, r http.Handler, u, p string) (string, string) {
	t.Helper()
	rec := postJSON(t, r, "/api/admin/login", map[string]any{
		"username": u, "password": p,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	mustDecode(t, rec, &resp)
	return resp.AccessToken, resp.RefreshToken
}

func postJSON(t *testing.T, r http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func postWithAuth(t *testing.T, r http.Handler, method, path, access string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func mustDecode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
}

func hasErrorCode(body []byte, code string) bool {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false
	}
	return env.Error.Code == code
}

func countAuditRows(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"select count(*) from admin_audit_logs where action=$1", action).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}
