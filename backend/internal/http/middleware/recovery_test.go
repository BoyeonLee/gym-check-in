package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
)

func recoveryRouter(env string, logger *slog.Logger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(env, logger))
	r.GET("/boom", func(c *gin.Context) { panic("kaboom") })
	r.GET("/boom-err", func(c *gin.Context) { panic(io.EOF) })
	return r
}

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestRecovery_Returns500WithINTERNAL(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := recoveryRouter("prod", logger)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
	var body errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body.Error.Code != "INTERNAL" {
		t.Fatalf("code: want INTERNAL, got %q", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Fatalf("message must not be empty")
	}
}

func TestRecovery_ProdHidesPanicValueAndStack(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := recoveryRouter("prod", logger)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "kaboom") {
		t.Fatalf("prod response leaked panic value: %s", body)
	}
	if strings.Contains(body, "goroutine ") || strings.Contains(body, ".go:") {
		t.Fatalf("prod response leaked stack trace: %s", body)
	}
}

func TestRecovery_DevExposesPanicMessageButNotStack(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := recoveryRouter("dev", logger)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "kaboom") {
		t.Fatalf("dev response should include panic value, got %s", body)
	}
	if strings.Contains(body, "goroutine ") {
		t.Fatalf("dev response must not include stack frames: %s", body)
	}
}

func TestRecovery_LogsStackTrace(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := recoveryRouter("prod", logger)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	logged := buf.String()
	if !strings.Contains(logged, `"error":"kaboom"`) {
		t.Errorf("expected panic value in log error field, got %s", logged)
	}
	if !strings.Contains(logged, "stack") {
		t.Errorf("expected stack key in log, got %s", logged)
	}
	if !strings.Contains(logged, "request_id") {
		t.Errorf("expected request_id in log, got %s", logged)
	}
}

func TestRecovery_HandlesErrorPanic(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := recoveryRouter("prod", logger)

	req := httptest.NewRequest(http.MethodGet, "/boom-err", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d body=%s", rec.Code, rec.Body.String())
	}
}
