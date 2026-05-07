package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
)

// requestIDRouter wires only the RequestID middleware and a tiny echo handler
// that surfaces the in-context id so the test can assert it propagated.
func requestIDRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/_probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"request_id": c.GetString(middleware.RequestIDContextKey),
		})
	})
	return r
}

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	r := requestIDRouter()
	req := httptest.NewRequest(http.MethodGet, "/_probe", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	got := rec.Header().Get(middleware.RequestIDHeader)
	if got == "" {
		t.Fatalf("expected generated X-Request-ID header, got empty")
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("expected UUID-format request id, got %q: %v", got, err)
	}
}

func TestRequestID_PassesThroughValidUUID(t *testing.T) {
	r := requestIDRouter()
	want := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/_probe", nil)
	req.Header.Set(middleware.RequestIDHeader, want)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get(middleware.RequestIDHeader); got != want {
		t.Fatalf("X-Request-ID echo: want %q, got %q", want, got)
	}
}

func TestRequestID_RejectsNonUUIDInput(t *testing.T) {
	// Defence-in-depth: a client must not be able to plant arbitrary strings
	// (e.g. ";DROP TABLE", or someone else's request id) into our access logs
	// or audit metadata via the X-Request-ID header.
	r := requestIDRouter()
	req := httptest.NewRequest(http.MethodGet, "/_probe", nil)
	req.Header.Set(middleware.RequestIDHeader, "not-a-uuid")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	got := rec.Header().Get(middleware.RequestIDHeader)
	if got == "" || got == "not-a-uuid" {
		t.Fatalf("expected fresh UUID to replace bogus input, got %q", got)
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("expected fresh UUID, got %q: %v", got, err)
	}
}

func TestRequestID_StoresInContext(t *testing.T) {
	r := requestIDRouter()
	want := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/_probe", nil)
	req.Header.Set(middleware.RequestIDHeader, want)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Body should mirror the header value, proving c.Set ran before the
	// handler — downstream middleware (logger, audit) depend on this.
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, want) {
		t.Fatalf("expected body to contain %q, got %q", want, body)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
