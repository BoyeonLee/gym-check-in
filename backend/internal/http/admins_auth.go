// admins_auth.go owns the four authentication endpoints — login / refresh /
// logout / password — that gate every other admin-facing route. Handler
// behavior follows backend/CLAUDE.md ("인증·세션") and docs/API.md exactly:
// generic UNAUTHORIZED for any auth-resolution failure (no username
// enumeration), explicit ACCOUNT_LOCKED / TEMP_PASSWORD_EXPIRED branches,
// audit row written for each outcome, and DB-side stale-token enforcement
// via password_updated_at.
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/audit"
	"github.com/lboyeon1223/gym-check-in/backend/internal/auth"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// AuthHandlers carries the dependencies the four auth endpoints need.
// Wire it once in cmd/server and bind methods to routes.
//
// Time semantics: handler-level "now" is always wall-clock time
// (time.Now().UTC()) because lockout / temp-password expiry / password_updated_at
// must compare against DB-side now(). The Clock field exists only for
// completeness so tests can supply a deterministic clock when adding new
// flows that don't touch DB times — current handlers don't read it.
type AuthHandlers struct {
	Pool   *pgxpool.Pool
	Issuer *auth.Issuer
	Clock  util.Clock
}

// now returns the single source of "now" for handler-level comparisons.
// It always uses wall-clock time; see the type comment for the rationale.
func handlerNow() time.Time { return time.Now().UTC() }

// loginRequest mirrors POST /api/admin/login body (docs/API.md).
type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// loginResponse mirrors the success body of POST /api/admin/login.
type loginResponse struct {
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	ExpiresIn          int    `json:"expires_in"`
	MustChangePassword bool   `json:"must_change_password"`
	Role               string `json:"role"`
	BranchID           *int64 `json:"branch_id"`
	Username           string `json:"username"`
}

// Login authenticates an admin and issues access + refresh tokens.
//
// All credential-resolution failures (unknown username, wrong password,
// soft-deleted admin) return the same generic 401 UNAUTHORIZED to avoid
// account enumeration. The two narrower 401 codes — ACCOUNT_LOCKED and
// TEMP_PASSWORD_EXPIRED — are returned only when the supplied username DOES
// match an active admin and one of those preconditions fails; tests
// constrain this surface so fixing one path can't widen enumeration.
func (h *AuthHandlers) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}

	ctx := c.Request.Context()
	now := handlerNow()
	requestID := c.GetString(middleware.RequestIDContextKey)
	clientIP := c.ClientIP()
	userAgent := c.GetHeader("User-Agent")

	admin, err := repo.FindByUsername(ctx, h.Pool, req.Username)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if admin == nil {
		// Unknown username — log a failure (admin_id NULL, username metadata).
		writeAuditFailure(ctx, h.Pool, nil, clientIP, userAgent, requestID, "unknown_username", req.Username)
		writeError(c, apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized"))
		return
	}

	if admin.LockedUntil != nil && admin.LockedUntil.After(now) {
		writeAuditFailure(ctx, h.Pool, &admin.ID, clientIP, userAgent, requestID, "locked", req.Username)
		unlockKST := admin.LockedUntil.In(util.KST).Format(time.RFC3339)
		writeErrorWith(c,
			apperr.New(http.StatusUnauthorized, "ACCOUNT_LOCKED", "account locked"),
			gin.H{"unlock_at": unlockKST},
		)
		return
	}
	if admin.MustChangePassword && admin.TempPasswordExpiresAt != nil && admin.TempPasswordExpiresAt.Before(now) {
		writeAuditFailure(ctx, h.Pool, &admin.ID, clientIP, userAgent, requestID, "temp_expired", req.Username)
		writeError(c, apperr.New(http.StatusUnauthorized, "TEMP_PASSWORD_EXPIRED", "temporary password expired"))
		return
	}

	if err := auth.VerifyPassword(admin.PasswordHash, req.Password); err != nil {
		// Failure: bump counter / set lock.
		_ = repo.RecordLoginFailure(ctx, h.Pool, admin.ID, now)
		writeAuditFailure(ctx, h.Pool, &admin.ID, clientIP, userAgent, requestID, "wrong_password", req.Username)
		writeError(c, apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized"))
		return
	}

	if err := repo.RecordLoginSuccess(ctx, h.Pool, admin.ID, now); err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	// Issue tokens. Access carries the role/branch context so guards can
	// decide without a DB hit; refresh stays minimal.
	access, err := h.Issuer.IssueAccess(auth.AccessClaims{
		Sub:                admin.ID,
		Username:           admin.Username,
		Role:               admin.Role,
		BranchID:           admin.BranchID,
		MustChangePassword: admin.MustChangePassword,
	})
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	refresh, _, err := h.Issuer.IssueRefresh(admin.ID)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	writeAuditOK(ctx, h.Pool, audit.LoginSuccess, admin.ID, clientIP, userAgent, requestID, gin.H{
		"username": admin.Username,
	})

	c.JSON(http.StatusOK, loginResponse{
		AccessToken:        access,
		RefreshToken:       refresh,
		ExpiresIn:          int(auth.AccessTTL.Seconds()),
		MustChangePassword: admin.MustChangePassword,
		Role:               admin.Role,
		BranchID:           admin.BranchID,
		Username:           admin.Username,
	})
}

// refreshRequest mirrors POST /api/admin/refresh body.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh issues a fresh access token. The refresh token is validated
// (signature, expiry, jti revocation, stale-after-password-change), then we
// re-load the admin row so the new access claim reflects current
// role/branch_id (which may have changed since the original login).
func (h *AuthHandlers) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusUnauthorized, "INVALID_REFRESH", "invalid refresh token"))
		return
	}
	ctx := c.Request.Context()
	now := handlerNow()

	claims, err := h.Issuer.ParseRefresh(req.RefreshToken)
	if err != nil || claims == nil {
		writeError(c, apperr.New(http.StatusUnauthorized, "INVALID_REFRESH", "invalid refresh token"))
		return
	}
	revoked, err := repo.IsRevoked(ctx, h.Pool, claims.Jti)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if revoked {
		writeError(c, apperr.New(http.StatusUnauthorized, "INVALID_REFRESH", "invalid refresh token"))
		return
	}

	admin, err := repo.FindByID(ctx, h.Pool, claims.Sub)
	if err != nil || admin == nil {
		writeError(c, apperr.New(http.StatusUnauthorized, "INVALID_REFRESH", "invalid refresh token"))
		return
	}
	if admin.PasswordUpdatedAt != nil && claims.Iat < admin.PasswordUpdatedAt.Unix() {
		writeError(c, apperr.New(http.StatusUnauthorized, "INVALID_REFRESH", "invalid refresh token"))
		return
	}

	access, err := h.Issuer.IssueAccess(auth.AccessClaims{
		Sub:                admin.ID,
		Username:           admin.Username,
		Role:               admin.Role,
		BranchID:           admin.BranchID,
		MustChangePassword: admin.MustChangePassword,
	})
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	_ = now

	c.JSON(http.StatusOK, gin.H{
		"access_token":         access,
		"expires_in":           int(auth.AccessTTL.Seconds()),
		"must_change_password": admin.MustChangePassword,
		"role":                 admin.Role,
		"branch_id":            admin.BranchID,
		"username":             admin.Username,
	})
}

// Logout revokes the refresh token's jti. Access auth is enforced via the
// RequireAuth middleware on the route group, so we already have the caller's
// admin id in context — we additionally verify the refresh token's sub
// matches before adding the jti to the revocation list (defence against a
// caller maliciously revoking somebody else's session).
func (h *AuthHandlers) Logout(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	ctx := c.Request.Context()
	now := handlerNow()
	adminID := c.GetInt64(middleware.AdminIDContextKey)

	claims, err := h.Issuer.ParseRefresh(req.RefreshToken)
	if err != nil || claims == nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid refresh token"))
		return
	}
	if claims.Sub != adminID {
		writeError(c, apperr.New(http.StatusForbidden, "FORBIDDEN", "refresh token does not match caller"))
		return
	}

	if err := repo.Revoke(ctx, h.Pool, claims.Jti, adminID, now); err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	writeAuditOK(ctx, h.Pool, audit.Logout, adminID,
		c.ClientIP(), c.GetHeader("User-Agent"),
		c.GetString(middleware.RequestIDContextKey), nil)

	c.Status(http.StatusNoContent)
}

// passwordChangeRequest mirrors POST /api/admin/password body.
type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

// PasswordChange validates the new password's strength, re-checks the
// current password (bcrypt), then performs the update inside a retryable
// transaction. After success every refresh/access token issued before the
// new password_updated_at is invalidated automatically — see backend/
// CLAUDE.md "비번 변경 후 stale access/refresh 동시 무효화".
func (h *AuthHandlers) PasswordChange(c *gin.Context) {
	var req passwordChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	if err := auth.ValidateStrength(req.NewPassword); err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusBadRequest, "WEAK_PASSWORD", "weak password"))
		return
	}

	ctx := c.Request.Context()
	now := handlerNow()
	adminID := c.GetInt64(middleware.AdminIDContextKey)
	requestID := c.GetString(middleware.RequestIDContextKey)
	clientIP := c.ClientIP()
	userAgent := c.GetHeader("User-Agent")

	if err := repo.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		admin, err := repo.FindByID(ctx, tx, adminID)
		if err != nil {
			return err
		}
		if admin == nil {
			return apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		}
		if err := auth.VerifyPassword(admin.PasswordHash, req.CurrentPassword); err != nil {
			return apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		}
		newHash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			return apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error")
		}
		if err := repo.UpdatePassword(ctx, tx, adminID, newHash, now); err != nil {
			return err
		}
		return nil
	}); err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	writeAuditOK(ctx, h.Pool, audit.PasswordChange, adminID, clientIP, userAgent, requestID, nil)
	c.Status(http.StatusNoContent)
}

// writeAuditOK is the success-path audit shim. Failures inside audit are
// already swallowed by audit.Log (see ADR-011) so callers stay simple.
func writeAuditOK(ctx context.Context, pool *pgxpool.Pool, action audit.Action, adminID int64,
	ip, userAgent, requestID string, extra gin.H) {
	meta := map[string]any{}
	if requestID != "" {
		meta["request_id"] = requestID
	}
	for k, v := range extra {
		meta[k] = v
	}
	id := adminID
	_ = audit.Log(ctx, pool, audit.Entry{
		AdminID:   &id,
		Action:    action,
		IP:        ip,
		UserAgent: userAgent,
		Metadata:  meta,
	})
}

// writeAuditFailure records a login_failure with the reason in metadata.
// PII is intentionally limited to the username supplied (no password, no
// hash, no token) — backend/CLAUDE.md forbids PII / secrets in log fields.
func writeAuditFailure(ctx context.Context, pool *pgxpool.Pool, adminID *int64,
	ip, userAgent, requestID, reason, username string) {
	meta := map[string]any{
		"reason":   reason,
		"username": username,
	}
	if requestID != "" {
		meta["request_id"] = requestID
	}
	_ = audit.Log(ctx, pool, audit.Entry{
		AdminID:   adminID,
		Action:    audit.LoginFailure,
		IP:        ip,
		UserAgent: userAgent,
		Metadata:  meta,
	})
}
