package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS returns a Gin middleware that enforces our single-origin CORS policy
// (backend/CLAUDE.md). The wildcard "*" origin is forbidden — passing it,
// or an empty string, suppresses the Access-Control-Allow-Origin header
// entirely so the browser cannot accept the response.
//
// Allowed methods: GET, POST, PATCH, DELETE, OPTIONS.
// Allowed headers: Authorization, Content-Type, Idempotency-Key, X-Request-ID.
// Credentials: false (we use Authorization headers, not cookies).
// Max-Age: 86400 (preflight cache 24h).
//
// Preflight (OPTIONS) requests short-circuit with 204 No Content carrying
// only the CORS headers — no downstream handler is invoked.
func CORS(allowOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if allowOrigin != "" && allowOrigin != "*" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", allowOrigin)
			c.Writer.Header().Add("Vary", "Origin")
		}
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "false")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
