//go:build integration

package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/auth"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

func init() { gin.SetMode(gin.TestMode) }

func newAuthRouter(pool *pgxpool.Pool, issuer *auth.Issuer) *gin.Engine {
	r := gin.New()
	r.Use(middleware.RequireAuth(issuer, pool))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"admin_id":             c.GetInt64(middleware.AdminIDContextKey),
			"role":                 c.GetString(middleware.RoleContextKey),
			"must_change_password": c.GetBool(middleware.MustChangePasswordContextKey),
		})
	})
	return r
}

func TestRequireAuth_ValidToken(t *testing.T) {
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	now := time.Unix(1_700_000_000, 0)
	issuer := &auth.Issuer{
		AccessSecret:  []byte("a"),
		RefreshSecret: []byte("b"),
		Clock:         &util.FakeClock{Instant: now},
	}
	tok, err := issuer.IssueAccess(auth.AccessClaims{Sub: adminID, Role: "global"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec := callProtected(t, newAuthRouter(pool, issuer), tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if int64(body["admin_id"].(float64)) != adminID {
		t.Errorf("admin_id mismatch: %v", body)
	}
}

func TestRequireAuth_MissingHeader(t *testing.T) {
	pool := testutil.SetupDB(t)
	issuer := &auth.Issuer{AccessSecret: []byte("a"), RefreshSecret: []byte("b")}
	rec := callProtected(t, newAuthRouter(pool, issuer), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !hasErrorCode(rec.Body.Bytes(), "UNAUTHORIZED") {
		t.Fatalf("body should carry UNAUTHORIZED: %s", rec.Body.String())
	}
}

func TestRequireAuth_ExpiredToken(t *testing.T) {
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	now := time.Unix(1_700_000_000, 0)
	issuer := &auth.Issuer{
		AccessSecret:  []byte("a"),
		RefreshSecret: []byte("b"),
		Clock:         &util.FakeClock{Instant: now},
	}
	tok, _ := issuer.IssueAccess(auth.AccessClaims{Sub: adminID, Role: "global"})
	// Advance past expiry.
	issuer.Clock = &util.FakeClock{Instant: now.Add(auth.AccessTTL + time.Second)}

	rec := callProtected(t, newAuthRouter(pool, issuer), tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 expired, got %d", rec.Code)
	}
}

func TestRequireAuth_SoftDeletedAdmin(t *testing.T) {
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})
	if _, err := pool.Exec(context.Background(),
		"update admins set deleted_at = now() where id=$1", adminID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	issuer := &auth.Issuer{
		AccessSecret: []byte("a"), RefreshSecret: []byte("b"),
		Clock: &util.FakeClock{Instant: now},
	}
	tok, _ := issuer.IssueAccess(auth.AccessClaims{Sub: adminID, Role: "global"})
	rec := callProtected(t, newAuthRouter(pool, issuer), tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for soft-deleted admin, got %d", rec.Code)
	}
}

func TestRequireAuth_StaleAfterPasswordChange(t *testing.T) {
	pool := testutil.SetupDB(t)
	adminID, _ := testutil.CreateAdmin(t, pool, &testutil.AdminOpts{Role: "global"})

	// Issue token at instant T1.
	t1 := time.Unix(1_700_000_000, 0)
	issuer := &auth.Issuer{
		AccessSecret: []byte("a"), RefreshSecret: []byte("b"),
		Clock: &util.FakeClock{Instant: t1},
	}
	tok, _ := issuer.IssueAccess(auth.AccessClaims{Sub: adminID, Role: "global"})

	// Bump password_updated_at to T1 + 1s — token's iat = T1 < new value.
	if _, err := pool.Exec(context.Background(),
		"update admins set password_updated_at = $1 where id=$2",
		t1.Add(time.Second), adminID); err != nil {
		t.Fatalf("bump: %v", err)
	}

	rec := callProtected(t, newAuthRouter(pool, issuer), tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 stale, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMustChangePasswordGuard(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.MustChangePasswordContextKey, true)
		c.Next()
	})
	r.Use(middleware.MustChangePasswordGuard())
	r.POST("/api/admin/password", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/api/admin/logout", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/api/admin/refresh", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/members", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, p := range []string{"/api/admin/password", "/api/admin/logout", "/api/admin/refresh"} {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden {
			t.Errorf("path %s should be allowed even with must_change_password=true", p)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/members", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("guard should 403 other paths, got %d", rec.Code)
	}
	if !hasErrorCode(rec.Body.Bytes(), "MUST_CHANGE_PASSWORD") {
		t.Errorf("expected MUST_CHANGE_PASSWORD body: %s", rec.Body.String())
	}
}

func TestRequireGlobalAndRequireBranch(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Branch role from upstream auth.
		c.Set(middleware.RoleContextKey, "branch")
		c.Next()
	})
	g := r.Group("/")
	g.GET("/global", middleware.RequireGlobal(), func(c *gin.Context) { c.Status(http.StatusOK) })
	g.GET("/branch", middleware.RequireBranch(), func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/global", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("RequireGlobal should 403 branch role, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/branch", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("RequireBranch should allow branch role, got %d", rec.Code)
	}
}

// callProtected is the shared invocation pattern for /protected.
func callProtected(t *testing.T, r http.Handler, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
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
