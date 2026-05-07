// admins.go owns the five /api/admins endpoints introduced in step 4.
// Every route requires role='global' (RequireGlobal applied at the route
// group). The list/create/update/delete/reset-password surface lets a
// global operator manage branch admins; reset-password is the only path
// where a one-time plaintext temporary password leaves the server.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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

// AdminHandlers carries the dependencies the admin endpoints need. cmd/server
// constructs one instance and binds the methods to a route group guarded by
// RequireAuth + MustChangePasswordGuard + RequireGlobal.
type AdminHandlers struct {
	Pool *pgxpool.Pool
}

// adminListItem is the wire shape for GET /api/admins. password_hash and
// temp_password_expires_at are intentionally absent — operator UIs see the
// 12-char temp password only on reset-password's response (one-shot).
type adminListItem struct {
	ID                 int64   `json:"id"`
	Username           string  `json:"username"`
	Role               string  `json:"role"`
	BranchID           *int64  `json:"branch_id"`
	BranchName         *string `json:"branch_name"`
	MustChangePassword bool    `json:"must_change_password"`
	LastLoginAt        *string `json:"last_login_at"`
	CreatedAt          string  `json:"created_at"`
}

func toAdminListItem(r repo.AdminListRow) adminListItem {
	var lastLogin *string
	if r.LastLoginAt != nil {
		s := r.LastLoginAt.In(util.KST).Format(time.RFC3339)
		lastLogin = &s
	}
	return adminListItem{
		ID:                 r.ID,
		Username:           r.Username,
		Role:               r.Role,
		BranchID:           r.BranchID,
		BranchName:         r.BranchName,
		MustChangePassword: r.MustChangePassword,
		LastLoginAt:        lastLogin,
		CreatedAt:          r.CreatedAt.In(util.KST).Format(time.RFC3339),
	}
}

// List returns active admins with their branch_name joined. id ASC ordering
// is stable for tests; pagination isn't applied because the operator pool is
// a handful of rows even for a multi-branch chain.
func (h *AdminHandlers) List(c *gin.Context) {
	rows, err := repo.ListAdmins(c.Request.Context(), h.Pool)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	out := make([]adminListItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAdminListItem(r))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// adminCreateRequest mirrors POST /api/admins body. branch_id is required for
// role='branch' and forbidden for role='global'; the handler enforces both
// (DB CHECK is the safety net).
type adminCreateRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Role     string `json:"role" binding:"required"`
	BranchID *int64 `json:"branch_id"`
}

// adminCreateResponse deliberately omits the plaintext password the caller
// sent — echoing it back would let a logged response leak credentials. Use
// reset-password to obtain a server-issued temp password.
type adminCreateResponse struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	BranchID           *int64 `json:"branch_id"`
	MustChangePassword bool   `json:"must_change_password"`
}

// Create inserts a new admin. The supplied password is hashed once (cost 12)
// and the row is created with must_change_password=true /
// temp_password_expires_at=now+24h so the new operator is forced through the
// password-change flow on first login.
func (h *AdminHandlers) Create(c *gin.Context) {
	var req adminCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid username"))
		return
	}
	if err := auth.ValidateStrength(req.Password); err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusBadRequest, "WEAK_PASSWORD", "weak password"))
		return
	}
	if !validRoleBranch(req.Role, req.BranchID) {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_ROLE_BRANCH", "role/branch_id mismatch"))
		return
	}
	// Verify branch_id points at an active branch — DB FK is the safety net,
	// but this surface a 400 rather than a confusing 500 with the FK error.
	if req.Role == "branch" {
		row, err := repo.GetBranch(c.Request.Context(), h.Pool, *req.BranchID)
		if err != nil {
			writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
			return
		}
		if row == nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "branch not found"))
			return
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	now := time.Now().UTC()
	var newID int64
	err = repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		id, err := repo.CreateAdmin(c.Request.Context(), tx, repo.CreateAdminInput{
			Username:     username,
			Role:         req.Role,
			BranchID:     req.BranchID,
			PasswordHash: hash,
		}, now)
		if err != nil {
			return err
		}
		newID = id
		return nil
	})
	if err != nil {
		writeError(c, apperr.FromDBError(err))
		return
	}

	writeAdminTargetAudit(c, h.Pool, audit.AdminCreate, "admin", newID, gin.H{
		"username": username,
		"role":     req.Role,
	})
	c.JSON(http.StatusCreated, adminCreateResponse{
		ID:                 newID,
		Username:           username,
		Role:               req.Role,
		BranchID:           req.BranchID,
		MustChangePassword: true,
	})
}

// adminUpdateRequest is a partial-update body. branchIDProvided reads the raw
// JSON to distinguish "branch_id absent" from "branch_id explicitly null"
// (needed for promotion to global, which clears the column).
type adminUpdateRequest struct {
	Username         *string `json:"username"`
	Role             *string `json:"role"`
	BranchID         *int64  `json:"branch_id"`
	branchIDProvided bool
}

// UnmarshalJSON populates branchIDProvided when the JSON object contains the
// branch_id key (regardless of whether the value is null). Without this flag
// we can't tell apart `{"role":"global"}` from `{"role":"global","branch_id":null}`
// — and the latter is what the frontend sends when promoting to global.
func (r *adminUpdateRequest) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["username"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		r.Username = &s
	}
	if v, ok := raw["role"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		r.Role = &s
	}
	if v, ok := raw["branch_id"]; ok {
		r.branchIDProvided = true
		if string(v) == "null" {
			r.BranchID = nil
		} else {
			var id int64
			if err := json.Unmarshal(v, &id); err != nil {
				return err
			}
			r.BranchID = &id
		}
	}
	return nil
}

// Update applies partial changes (username/role/branch_id). branch_id changes
// (or any change that flips the user's authority) bump password_updated_at so
// the user's outstanding access/refresh tokens fail on the next request.
func (h *AdminHandlers) Update(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req adminUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	callerID := c.GetInt64(middleware.AdminIDContextKey)

	// Pre-compute the post-update role/branch so we can validate the combination
	// before issuing the SQL UPDATE (and so we know whether to bump the token
	// killswitch).
	existing, err := repo.FindByID(c.Request.Context(), h.Pool, id)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if existing == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "admin not found"))
		return
	}

	roleAfter := existing.Role
	if req.Role != nil {
		roleAfter = *req.Role
	}
	branchAfter := existing.BranchID
	if req.branchIDProvided {
		branchAfter = req.BranchID
	}

	// Block self role/branch_id mutation. Username-only edits on yourself are
	// fine — operator might just be renaming.
	if id == callerID {
		roleChanging := req.Role != nil && *req.Role != existing.Role
		branchChanging := req.branchIDProvided && !int64PtrEq(req.BranchID, existing.BranchID)
		if roleChanging || branchChanging {
			writeError(c, apperr.New(http.StatusConflict, "CANNOT_MODIFY_SELF_ROLE",
				"cannot modify own role or branch_id"))
			return
		}
	}

	if !validRoleBranch(roleAfter, branchAfter) {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_ROLE_BRANCH", "role/branch_id mismatch"))
		return
	}
	if roleAfter == "branch" && branchAfter != nil {
		row, err := repo.GetBranch(c.Request.Context(), h.Pool, *branchAfter)
		if err != nil {
			writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
			return
		}
		if row == nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "branch not found"))
			return
		}
	}

	branchChanged := req.branchIDProvided && !int64PtrEq(req.BranchID, existing.BranchID)
	roleChanged := req.Role != nil && *req.Role != existing.Role
	bumpToken := branchChanged || roleChanged

	now := time.Now().UTC()
	in := repo.UpdateAdminInput{
		Username:    req.Username,
		Role:        req.Role,
		BranchIDSet: req.branchIDProvided,
		BranchID:    req.BranchID,
	}
	err = repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		if uerr := repo.UpdateAdmin(c.Request.Context(), tx, id, in, now); uerr != nil {
			return uerr
		}
		if bumpToken {
			if perr := repo.BumpPasswordUpdatedAt(c.Request.Context(), tx, id, now); perr != nil {
				return perr
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "admin not found"))
			return
		}
		writeError(c, apperr.FromDBError(err))
		return
	}

	updated, err := repo.FindByID(c.Request.Context(), h.Pool, id)
	if err != nil || updated == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "admin not found"))
		return
	}

	meta := gin.H{}
	if req.Username != nil {
		meta["username"] = *req.Username
	}
	if req.Role != nil {
		meta["role"] = *req.Role
	}
	if req.branchIDProvided {
		meta["branch_id_changed"] = true
	}
	writeAdminTargetAudit(c, h.Pool, audit.AdminUpdate, "admin", id, meta)

	c.JSON(http.StatusOK, gin.H{
		"id":                   updated.ID,
		"username":             updated.Username,
		"role":                 updated.Role,
		"branch_id":            updated.BranchID,
		"must_change_password": updated.MustChangePassword,
	})
}

// Delete soft-deletes the target. The repo helper bumps password_updated_at,
// which combined with the auth middleware's iat<password_updated_at check
// kills both the user's outstanding access AND refresh tokens on the next
// request — no per-token revocation list needed.
func (h *AdminHandlers) Delete(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	callerID := c.GetInt64(middleware.AdminIDContextKey)
	if id == callerID {
		writeError(c, apperr.New(http.StatusConflict, "CANNOT_DELETE_SELF",
			"cannot delete own admin account"))
		return
	}

	now := time.Now().UTC()
	err := repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		return repo.SoftDeleteAdmin(c.Request.Context(), tx, id, now)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "admin not found"))
			return
		}
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	writeAdminTargetAudit(c, h.Pool, audit.AdminDelete, "admin", id, nil)
	c.Status(http.StatusNoContent)
}

// resetPasswordResponse is the one and only place a server-issued plaintext
// password leaves the API. Operator must capture it on the spot — there is
// no "show again" surface.
type resetPasswordResponse struct {
	TemporaryPassword string `json:"temporary_password"`
	ExpiresAt         string `json:"expires_at"`
}

// ResetPassword issues a fresh 12-char temp password, writes the bcrypt hash
// to the row, and forces the target to change it on next login (within 24h).
// Self-reset is blocked as a defence-in-depth: a global admin who lost their
// own password recovers via the seed CLI per OPERATIONS.md, not via this
// endpoint (a compromised global session shouldn't be able to extend its own
// access by re-rolling its password through this surface).
func (h *AdminHandlers) ResetPassword(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	callerID := c.GetInt64(middleware.AdminIDContextKey)
	if id == callerID {
		writeError(c, apperr.New(http.StatusConflict, "CANNOT_RESET_SELF",
			"cannot reset own password"))
		return
	}

	target, err := repo.FindByID(c.Request.Context(), h.Pool, id)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if target == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "admin not found"))
		return
	}

	plain, err := auth.GenerateTempPassword(nil)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	hash, err := auth.HashPassword(plain)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	now := time.Now().UTC()
	expires := now.Add(24 * time.Hour)

	err = repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		return repo.ResetPassword(c.Request.Context(), tx, id, hash, now)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "admin not found"))
			return
		}
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	// Audit metadata MUST NOT carry the plaintext or hash. expires_at is fine
	// (it's already on the response) and helps operators correlate.
	writeAdminTargetAudit(c, h.Pool, audit.PasswordReset, "admin", id, gin.H{
		"expires_at": expires.In(util.KST).Format(time.RFC3339),
	})

	c.JSON(http.StatusOK, resetPasswordResponse{
		TemporaryPassword: plain,
		ExpiresAt:         expires.In(util.KST).Format(time.RFC3339),
	})
}

// ---------- helpers ----------

// validRoleBranch enforces the schema CHECK at the handler layer for crisper
// error codes: global → branch_id MUST be NULL, branch → branch_id MUST be set.
func validRoleBranch(role string, branchID *int64) bool {
	switch role {
	case "global":
		return branchID == nil
	case "branch":
		return branchID != nil
	default:
		return false
	}
}

// int64PtrEq compares two *int64 by value, treating nil==nil as equal.
func int64PtrEq(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

