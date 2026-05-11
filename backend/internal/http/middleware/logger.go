package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Context keys exported so other middleware/handlers can populate the
// fields the access logger emits. These keys are part of the package's
// public contract — renaming them is a breaking change.
const (
	// AdminIDContextKey carries the authenticated admin's id (int64) once
	// the auth middleware verifies the access token.
	AdminIDContextKey = "admin_id"
	// ErrorCodeContextKey carries the apperr code (string) of the response,
	// allowing the access log to record it without re-parsing the body.
	ErrorCodeContextKey = "error_code"
)

// Logger returns a Gin middleware that emits one structured access-log
// record per request via the provided slog.Logger.
//
// Fields: request_id, admin_id (when set), ip, method, path, status,
// duration_ms, error_code (when set).
//
// Notably absent: query string, request body, response body, headers
// (Authorization), or any field that could carry PII (phone, birth date)
// or secrets (passwords, JWTs). The path is logged without the query
// component, so a search like /api/members/search?q=<digits> logs only
// "/api/members/search".
func Logger(l *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		attrs := []any{
			"request_id", c.GetString(RequestIDContextKey),
			"ip", c.ClientIP(),
			"method", c.Request.Method,
			"path", c.Request.URL.Path, // path only — query is intentionally omitted
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if v, ok := c.Get(AdminIDContextKey); ok {
			if id, ok := v.(int64); ok {
				attrs = append(attrs, "admin_id", id)
			}
		}
		if code, ok := c.Get(ErrorCodeContextKey); ok {
			if s, ok := code.(string); ok && s != "" {
				attrs = append(attrs, "error_code", s)
			}
		}
		l.LogAttrs(c.Request.Context(), slog.LevelInfo, "http_request", toAttrs(attrs)...)
	}
}

// toAttrs converts a flat key/value slice into typed slog.Attr values so
// the JSON handler emits stable types (int rather than json.Number, etc.).
func toAttrs(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		out = append(out, slog.Any(k, kv[i+1]))
	}
	return out
}
