package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
)

// captureLogger wires a JSON slog handler against a buffer so tests can
// decode log records and inspect their attributes.
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

func decodeRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("logger emitted nothing")
	}
	// Take only the first line; subsequent records (if any) are not the
	// access log we want to assert on.
	if i := strings.Index(line, "\n"); i >= 0 {
		line = line[:i]
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("decode log line: %v\nraw: %s", err, line)
	}
	return m
}

func TestLogger_EmitsRequiredFields(t *testing.T) {
	logger, buf := captureLogger()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	rec2 := decodeRecord(t, buf)
	for _, k := range []string{"request_id", "ip", "method", "path", "status", "duration_ms"} {
		if _, ok := rec2[k]; !ok {
			t.Errorf("log record missing field %q: %v", k, rec2)
		}
	}
	if rec2["method"] != "GET" {
		t.Errorf("method: want GET, got %v", rec2["method"])
	}
	if rec2["path"] != "/ping" {
		t.Errorf("path: want /ping, got %v", rec2["path"])
	}
	if v, _ := rec2["status"].(float64); int(v) != 200 {
		t.Errorf("status: want 200, got %v", rec2["status"])
	}
}

func TestLogger_OmitsPIIFromQueryAndBody(t *testing.T) {
	logger, buf := captureLogger()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.POST("/api/members/search", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	body := []byte(`{"phone":"01012345678","password":"hunter2","birth_date":"1990-04-15"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/members/search?q=01012345678&password=hunter2", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-jwt-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	raw := buf.String()
	for _, forbidden := range []string{
		"01012345678", "hunter2", "1990-04-15", "secret-jwt-token", "phone", "password", "birth_date",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("log line leaks PII/secret token %q\nraw: %s", forbidden, raw)
		}
	}
	// Path should be present without query string.
	if !strings.Contains(raw, "/api/members/search") {
		t.Errorf("expected path field, raw: %s", raw)
	}
}

func TestLogger_IncludesAdminID(t *testing.T) {
	logger, buf := captureLogger()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.GET("/op", func(c *gin.Context) {
		// Simulate the auth middleware's contract: it stores the resolved
		// admin id in context so the access logger can pick it up.
		c.Set(middleware.AdminIDContextKey, int64(42))
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/op", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	rec2 := decodeRecord(t, buf)
	if v, _ := rec2["admin_id"].(float64); int64(v) != 42 {
		t.Errorf("admin_id: want 42, got %v (record=%v)", rec2["admin_id"], rec2)
	}
}

func TestLogger_IncludesErrorCode(t *testing.T) {
	logger, buf := captureLogger()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.GET("/bad", func(c *gin.Context) {
		c.Set(middleware.ErrorCodeContextKey, "INVALID_INPUT")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_INPUT", "message": "x"}})
	})

	req := httptest.NewRequest(http.MethodGet, "/bad", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	rec2 := decodeRecord(t, buf)
	if rec2["error_code"] != "INVALID_INPUT" {
		t.Errorf("error_code: want INVALID_INPUT, got %v", rec2["error_code"])
	}
	if v, _ := rec2["status"].(float64); int(v) != 400 {
		t.Errorf("status: want 400, got %v", rec2["status"])
	}
}

// Sanity: ensure the helper's slog.Logger doesn't dispatch to the global
// default — we want the captured buffer to be authoritative even if
// production configures slog.SetDefault elsewhere.
func TestLogger_UsesProvidedLogger(t *testing.T) {
	logger, buf := captureLogger()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(context.Background())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if buf.Len() == 0 {
		t.Fatalf("captured logger received nothing")
	}
}
