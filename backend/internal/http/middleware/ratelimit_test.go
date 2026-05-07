package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

func rateLimitRouter(l *middleware.Limiter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(l.Middleware())
	r.GET("/login", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func issueFromIP(r http.Handler, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = ip + ":12345"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRateLimit_AllowsUpToMax(t *testing.T) {
	clock := &util.FakeClock{Instant: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)}
	l := middleware.NewLimiter(15*time.Minute, 60, clock)
	r := rateLimitRouter(l)

	for i := 0; i < 60; i++ {
		rec := issueFromIP(r, "10.0.0.1")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimit_BlocksOverMax(t *testing.T) {
	clock := &util.FakeClock{Instant: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)}
	l := middleware.NewLimiter(15*time.Minute, 60, clock)
	r := rateLimitRouter(l)

	for i := 0; i < 60; i++ {
		issueFromIP(r, "10.0.0.1")
	}
	rec := issueFromIP(r, "10.0.0.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: want 429, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatalf("Retry-After header should be set on 429")
	}
	if got := rec.Body.String(); !contains(got, "RATE_LIMITED") {
		t.Fatalf("body should contain RATE_LIMITED, got %s", got)
	}
}

func TestRateLimit_TracksPerIP(t *testing.T) {
	clock := &util.FakeClock{Instant: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)}
	l := middleware.NewLimiter(15*time.Minute, 60, clock)
	r := rateLimitRouter(l)

	for i := 0; i < 60; i++ {
		issueFromIP(r, "10.0.0.1")
	}
	// IP #1 is now exhausted, but a fresh IP must still be served.
	if rec := issueFromIP(r, "10.0.0.2"); rec.Code != http.StatusOK {
		t.Fatalf("fresh ip: want 200, got %d", rec.Code)
	}
}

func TestRateLimit_RecoversAfterWindow(t *testing.T) {
	start := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	clock := &util.FakeClock{Instant: start}
	l := middleware.NewLimiter(15*time.Minute, 60, clock)
	r := rateLimitRouter(l)

	for i := 0; i < 60; i++ {
		issueFromIP(r, "10.0.0.1")
	}
	if rec := issueFromIP(r, "10.0.0.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 before window passes, got %d", rec.Code)
	}

	// Advance past the full window — the bucket should refill completely.
	clock.Instant = start.Add(16 * time.Minute)
	if rec := issueFromIP(r, "10.0.0.1"); rec.Code != http.StatusOK {
		t.Fatalf("after window, want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRateLimit_PartialRefill(t *testing.T) {
	start := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	clock := &util.FakeClock{Instant: start}
	l := middleware.NewLimiter(15*time.Minute, 60, clock)
	r := rateLimitRouter(l)

	for i := 0; i < 60; i++ {
		issueFromIP(r, "10.0.0.1")
	}
	// Half the window → ~30 tokens replenished.
	clock.Instant = start.Add(450 * time.Second) // 7.5 minutes
	allowed := 0
	for i := 0; i < 60; i++ {
		if issueFromIP(r, "10.0.0.1").Code == http.StatusOK {
			allowed++
		}
	}
	if allowed < 25 || allowed > 35 {
		t.Fatalf("partial refill: want ~30 allowed, got %d", allowed)
	}
}
