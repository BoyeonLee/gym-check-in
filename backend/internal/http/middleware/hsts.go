package middleware

import "github.com/gin-gonic/gin"

// HSTS returns a Gin middleware that injects Strict-Transport-Security
// in production responses. The header is intentionally absent in
// development (APP_ENV=dev) so local plain-HTTP traffic is not pinned
// to HTTPS by browsers that previously visited a prod build.
//
// Value: "max-age=31536000; includeSubDomains" (1 year). preload is
// not set yet — that decision lands when we register the production
// domain (ADR-010 + OPERATIONS.md).
func HSTS(env string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if env == "prod" {
			c.Writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
