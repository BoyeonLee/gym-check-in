package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
)

func bodyLimitRouter(maxBytes int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.BodyLimit(maxBytes))
	r.POST("/sink", func(c *gin.Context) {
		// Drain the body. Real handlers use ShouldBindJSON which propagates
		// the read error to c.Errors automatically; here we simulate that by
		// registering the error explicitly so the middleware can convert it.
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			_ = c.Error(err)
			return
		}
		c.Status(http.StatusOK)
	})
	return r
}

func TestBodyLimit_AcceptsUnderLimit(t *testing.T) {
	r := bodyLimitRouter(1024) // 1KB
	body := bytes.Repeat([]byte("a"), 512)
	req := httptest.NewRequest(http.MethodPost, "/sink", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBodyLimit_RejectsByContentLength(t *testing.T) {
	r := bodyLimitRouter(1024)
	body := bytes.Repeat([]byte("a"), 4096) // 4KB > 1KB
	req := httptest.NewRequest(http.MethodPost, "/sink", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body2 errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body2.Error.Code != "BODY_TOO_LARGE" {
		t.Fatalf("code: want BODY_TOO_LARGE, got %q", body2.Error.Code)
	}
}

func TestBodyLimit_RejectsChunkedOverflow(t *testing.T) {
	// When ContentLength is unknown (Transfer-Encoding: chunked), the
	// middleware must still reject overflow once MaxBytesReader fires.
	r := bodyLimitRouter(64)
	body := strings.Repeat("a", 256)
	req := httptest.NewRequest(http.MethodPost, "/sink", strings.NewReader(body))
	req.ContentLength = -1 // simulate unknown length
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body2 errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body2.Error.Code != "BODY_TOO_LARGE" {
		t.Fatalf("code: want BODY_TOO_LARGE, got %q", body2.Error.Code)
	}
}

func TestBodyLimit_DefaultMaxBytes(t *testing.T) {
	// Sanity: passing 0 falls back to the production default of 1 MiB.
	r := bodyLimitRouter(0)
	body := bytes.Repeat([]byte("a"), 4096)
	req := httptest.NewRequest(http.MethodPost, "/sink", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("default 1MiB should accept 4KB body, got %d body=%s",
			rec.Code, rec.Body.String())
	}
}
