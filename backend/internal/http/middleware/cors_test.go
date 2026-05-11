package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
)

func corsRouter(origin string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CORS(origin))
	r.POST("/echo", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestCORS_PreflightReturns204(t *testing.T) {
	r := corsRouter("https://example.test")
	req := httptest.NewRequest(http.MethodOptions, "/echo", nil)
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status: want 204, got %d", rec.Code)
	}
}

func TestCORS_PreflightHeaders(t *testing.T) {
	r := corsRouter("https://example.test")
	req := httptest.NewRequest(http.MethodOptions, "/echo", nil)
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	cases := []struct{ key, want string }{
		{"Access-Control-Allow-Origin", "https://example.test"},
		{"Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS"},
		{"Access-Control-Allow-Credentials", "false"},
		{"Access-Control-Max-Age", "86400"},
	}
	for _, c := range cases {
		if got := rec.Header().Get(c.key); got != c.want {
			t.Errorf("%s: want %q, got %q", c.key, c.want, got)
		}
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Authorization", "Content-Type", "Idempotency-Key", "X-Request-ID"} {
		if !strings.Contains(allowHeaders, h) {
			t.Errorf("Allow-Headers missing %q (got %q)", h, allowHeaders)
		}
	}
}

func TestCORS_GETAttachesHeadersAndCallsHandler(t *testing.T) {
	r := corsRouter("https://example.test")
	called := false
	r.GET("/probe", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Origin", "https://example.test")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("downstream handler should run for non-preflight requests")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.test" {
		t.Fatalf("Allow-Origin: want https://example.test, got %q",
			rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_RejectsWildcardOrigin(t *testing.T) {
	// Defence-in-depth: even if misconfigured to "*", we must not advertise
	// it (backend/CLAUDE.md forbids wildcard). Treat "*" as if origin were
	// unset — no Allow-Origin header on the response.
	r := corsRouter("*")
	req := httptest.NewRequest(http.MethodOptions, "/echo", nil)
	req.Header.Set("Origin", "*")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Fatalf("must never emit Allow-Origin: *, got %q", got)
	}
}

func TestCORS_EmptyOriginNoHeader(t *testing.T) {
	r := corsRouter("")
	req := httptest.NewRequest(http.MethodGet, "/echo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("empty configured origin should suppress header, got %q", got)
	}
}
