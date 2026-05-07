package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
)

func hstsRouter(env string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.HSTS(env))
	r.GET("/anything", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestHSTS_Prod_AddsHeader(t *testing.T) {
	r := hstsRouter("prod")
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	got := rec.Header().Get("Strict-Transport-Security")
	want := "max-age=31536000; includeSubDomains"
	if got != want {
		t.Fatalf("HSTS header: want %q, got %q", want, got)
	}
}

func TestHSTS_Dev_OmitsHeader(t *testing.T) {
	r := hstsRouter("dev")
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("dev should not emit HSTS, got %q", got)
	}
}
