package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// mustChangePasswordExemptions lists the routes a token with
// must_change_password=true may still hit. The set matches backend/CLAUDE.md
// ("/api/admin/{password,logout,refresh}") and is path-aware as a defence in
// depth: even if the guard is mistakenly applied to one of these routes, the
// admin can still complete the password-change flow.
var mustChangePasswordExemptions = map[string]struct{}{
	"/api/admin/password": {},
	"/api/admin/logout":   {},
	"/api/admin/refresh":  {},
}

// MustChangePasswordGuard blocks every authenticated request whose access
// token carries must_change_password=true, except for the three exempt
// routes above. Place AFTER RequireAuth so the flag is populated.
func MustChangePasswordGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !c.GetBool(MustChangePasswordContextKey) {
			c.Next()
			return
		}
		// Normalize trailing slashes so /api/admin/password/ also passes.
		path := strings.TrimRight(c.Request.URL.Path, "/")
		if _, ok := mustChangePasswordExemptions[path]; ok {
			c.Next()
			return
		}
		c.Set(ErrorCodeContextKey, "MUST_CHANGE_PASSWORD")
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"code":    "MUST_CHANGE_PASSWORD",
				"message": "password change required",
			},
		})
	}
}

// RequireGlobal allows only role='global' admins through. Place AFTER
// RequireAuth.
func RequireGlobal() gin.HandlerFunc {
	return roleGuard("global")
}

// RequireBranch allows only role='branch' admins through. Place AFTER
// RequireAuth.
func RequireBranch() gin.HandlerFunc {
	return roleGuard("branch")
}

func roleGuard(want string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(RoleContextKey) == want {
			c.Next()
			return
		}
		c.Set(ErrorCodeContextKey, "FORBIDDEN")
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"code":    "FORBIDDEN",
				"message": "forbidden",
			},
		})
	}
}
