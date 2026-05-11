package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery returns a Gin middleware that catches panics from downstream
// handlers/middleware, logs the panic value plus full stack trace via the
// provided slog.Logger, and writes a uniform error response.
//
// Response shape always matches apperr's wire format:
//
//	{ "error": { "code": "INTERNAL", "message": "<text>" } }
//
// In prod, message is the fixed string "internal server error" — panic
// values and stack frames are NEVER exposed (per backend/CLAUDE.md). In
// dev, the message includes the panic value so developers can iterate
// without tailing logs; the stack itself is still log-only.
//
// We deliberately do not delegate to gin.Recovery() because that helper
// uses fmt-based log lines which do not carry the request_id field nor
// the apperr response envelope.
func Recovery(env string, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}

			panicMsg := fmt.Sprintf("%v", rec)
			stack := string(debug.Stack())
			logger.LogAttrs(c.Request.Context(), slog.LevelError, "http_panic_recovered",
				slog.String("error", panicMsg),
				slog.String("stack", stack),
				slog.String("request_id", c.GetString(RequestIDContextKey)),
				slog.String("method", c.Request.Method),
				slog.String("path", c.Request.URL.Path),
			)

			message := "internal server error"
			if env == "dev" {
				// Surface the panic VALUE only (no stack) so developers can
				// recognize the failure mode in the response. Production must
				// stay opaque — backend/CLAUDE.md forbids leaking internals.
				message = "internal server error: " + panicMsg
			}

			// Mark the response envelope so the access logger can record
			// the error code field consistently with apperr handlers.
			c.Set(ErrorCodeContextKey, "INTERNAL")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    "INTERNAL",
					"message": message,
				},
			})
		}()
		c.Next()
	}
}
