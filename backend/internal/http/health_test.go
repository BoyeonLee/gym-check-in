//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	httpapi "github.com/lboyeon1223/gym-check-in/backend/internal/http"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

func newRouter(t *testing.T) (*gin.Engine, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL not set; integration tests require it. Source .env before running `go test -tags=integration`.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := repo.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpapi.RegisterHealth(r, pool)

	return r, func() { pool.Close() }
}

func TestHealthz_OK(t *testing.T) {
	r, cleanup := newRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Fatalf("body: want status=ok, got %v", body)
	}
}

func TestHealthz_PoolDown(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL not set; integration tests require it. Source .env before running `go test -tags=integration`.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := repo.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	pool.Close() // simulate "pool is down"

	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpapi.RegisterHealth(r, pool)

	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body.Error.Code != "INTERNAL" {
		t.Fatalf("error.code: want INTERNAL, got %q (body=%s)", body.Error.Code, rec.Body.String())
	}
	if strings.TrimSpace(body.Error.Message) == "" {
		t.Fatalf("error.message should not be empty")
	}
}
