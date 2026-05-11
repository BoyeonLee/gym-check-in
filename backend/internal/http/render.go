package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
)

// writeError renders an *apperr.AppError as the standard envelope and stamps
// the access logger's error_code field. Handlers should funnel every error
// path through this helper so logs and responses stay consistent.
func writeError(c *gin.Context, e *apperr.AppError) {
	if e == nil {
		return
	}
	c.Set(middleware.ErrorCodeContextKey, e.Code)
	c.AbortWithStatusJSON(e.Status, gin.H{
		"error": gin.H{
			"code":    e.Code,
			"message": e.Message,
		},
	})
}

// writeErrorWith renders an apperr alongside extra top-level fields (e.g.
// the unlock_at timestamp returned with ACCOUNT_LOCKED).
func writeErrorWith(c *gin.Context, e *apperr.AppError, extra gin.H) {
	if e == nil {
		return
	}
	c.Set(middleware.ErrorCodeContextKey, e.Code)
	body := gin.H{
		"error": gin.H{
			"code":    e.Code,
			"message": e.Message,
		},
	}
	for k, v := range extra {
		body[k] = v
	}
	c.AbortWithStatusJSON(e.Status, body)
}
