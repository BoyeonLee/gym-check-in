package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// DefaultMaxBodyBytes is the production cap on request bodies (1 MiB).
// JSON requests in this API are tiny — pause/refund/login carry well under
// 1 KB. The cap exists to harden against memory-pressure DoS attacks.
const DefaultMaxBodyBytes int64 = 1 << 20

// BodyLimit returns a Gin middleware that caps request bodies at maxBytes.
// Pass 0 to use DefaultMaxBodyBytes.
//
// Two checks defend the boundary:
//
//  1. Content-Length: if the client advertises a length above the cap,
//     reject immediately with 400 BODY_TOO_LARGE before allocating any
//     read buffers or routing.
//  2. http.MaxBytesReader: chunked / streamed bodies that lie about
//     their length still hit the cap during read; we recognize the
//     resulting *http.MaxBytesError after the handler returns and
//     convert it to a uniform 400 response if no other status was
//     written.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			abortBodyTooLarge(c)
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()

		// If the handler tried to read past the cap, MaxBytesReader stored
		// an *http.MaxBytesError on c.Errors. Convert that to BODY_TOO_LARGE
		// — but only if nothing has been written yet, so we don't clobber
		// a partially flushed response.
		if c.Writer.Written() {
			return
		}
		for _, gerr := range c.Errors {
			var maxErr *http.MaxBytesError
			if errors.As(gerr.Err, &maxErr) {
				abortBodyTooLarge(c)
				return
			}
		}
	}
}

func abortBodyTooLarge(c *gin.Context) {
	c.Set(ErrorCodeContextKey, "BODY_TOO_LARGE")
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"code":    "BODY_TOO_LARGE",
			"message": "request body exceeds the maximum allowed size",
		},
	})
}
