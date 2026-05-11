// Package httpapi wires Gin handlers. SQL must NOT live here — every read
// or write goes through internal/repo. Middleware (auth, audit, rate limit,
// CORS, request id, recovery, body-limit) is added in subsequent steps.
package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pinger is the minimal contract health needs from the connection pool.
// Using an interface keeps the handler trivially testable without depending
// on a real *pgxpool.Pool.
type pinger interface {
	Ping(ctx context.Context) error
}

// RegisterHealth installs GET /api/healthz on the given engine. The handler
// is intentionally exempt from auth/rate-limit middleware — it must always
// be reachable so platform health checks can run.
func RegisterHealth(r *gin.Engine, pool *pgxpool.Pool) {
	registerHealth(r, pool)
}

func registerHealth(r *gin.Engine, p pinger) {
	r.GET("/api/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := p.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{
					"code":    "INTERNAL",
					"message": "database unavailable",
				},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}
