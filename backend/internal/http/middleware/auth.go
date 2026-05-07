package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/auth"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

// Context keys populated by RequireAuth so downstream guards/handlers can
// read role / branch_id without re-parsing the access token.
const (
	RoleContextKey               = "role"
	BranchIDContextKey           = "branch_id"
	UsernameContextKey           = "username"
	MustChangePasswordContextKey = "must_change_password"
)

// RequireAuth verifies the access token, then runs ONE DB lookup
// (GetForAccessCheck) to enforce two revocation modes the JWT alone cannot
// catch:
//
//  1. The admin row was soft-deleted after the token was issued — Exists
//     becomes false, response is 401.
//  2. The admin's password (or any state we treat as a token-killer) was
//     updated after the token was issued — claims.Iat < password_updated_at,
//     response is 401.
//
// The single SELECT keeps round-trips at one per request as required by
// backend/CLAUDE.md.
func RequireAuth(issuer *auth.Issuer, pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := bearerToken(c.GetHeader("Authorization"))
		if tok == "" {
			abortUnauthorized(c)
			return
		}
		claims, err := issuer.ParseAccess(tok)
		if err != nil || claims == nil {
			abortUnauthorized(c)
			return
		}
		check, err := repo.GetForAccessCheck(c.Request.Context(), pool, claims.Sub)
		if err != nil {
			// DB error → treat as auth failure rather than 500. The slog
			// access logger still records the failure with error_code.
			abortUnauthorized(c)
			return
		}
		if !check.Exists {
			abortUnauthorized(c)
			return
		}
		if check.PasswordUpdatedAt != nil && claims.Iat < check.PasswordUpdatedAt.Unix() {
			abortUnauthorized(c)
			return
		}

		c.Set(AdminIDContextKey, claims.Sub)
		c.Set(RoleContextKey, claims.Role)
		c.Set(BranchIDContextKey, claims.BranchID)
		c.Set(UsernameContextKey, claims.Username)
		c.Set(MustChangePasswordContextKey, claims.MustChangePassword)
		c.Next()
	}
}

// bearerToken extracts the value following "Bearer " (case-insensitive prefix).
// Returns empty string if the header is missing or shaped differently — the
// caller treats both as 401.
func bearerToken(header string) string {
	if header == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func abortUnauthorized(c *gin.Context) {
	c.Set(ErrorCodeContextKey, "UNAUTHORIZED")
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"code":    "UNAUTHORIZED",
			"message": "unauthorized",
		},
	})
}
