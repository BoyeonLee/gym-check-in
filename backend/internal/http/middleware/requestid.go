// Package middleware holds the cross-cutting Gin middleware: request id,
// access logger, panic recovery, CORS, body size limit, and IP rate limit.
//
// Order matters. The composition root (cmd/server) installs them in this
// sequence so that downstream middleware can rely on the upstream effects:
//
//	requestid → logger → recovery → cors → bodylimit → (rate limit on auth group)
//
// requestid runs first so logger/recovery can echo the value into structured
// log fields and audit entries.
package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDHeader is the canonical HTTP header carrying the request id.
const RequestIDHeader = "X-Request-ID"

// RequestIDContextKey is the gin.Context key under which the resolved id
// is stored. Handlers/middleware should retrieve it via c.GetString.
const RequestIDContextKey = "request_id"

// RequestID returns a Gin middleware that resolves the request id for every
// request and echoes it on the response.
//
// Resolution rules:
//   - If the client sent X-Request-ID and it parses as a UUID, that value
//     is used. Non-UUID values are rejected and a fresh UUIDv4 is generated
//     instead — this prevents callers from injecting arbitrary identifiers
//     into our logs / audit metadata.
//   - Otherwise a fresh UUIDv4 is generated.
//
// The resolved id is stored in c at RequestIDContextKey and copied to the
// response header (RequestIDHeader) before any handler runs, so panic
// recovery / logger can read it even if the chain aborts.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" || !isUUID(id) {
			id = uuid.NewString()
		}
		c.Set(RequestIDContextKey, id)
		c.Writer.Header().Set(RequestIDHeader, id)
		c.Next()
	}
}

// isUUID reports whether s is a valid RFC4122 UUID. We accept any version —
// the goal here is format validation, not version enforcement.
func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
