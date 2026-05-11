// branches.go owns the four /api/branches endpoints introduced in step 4.
// All routes require an authenticated admin; mutating routes (POST/PATCH/
// DELETE) additionally require role='global' (RequireGlobal applied at the
// route group in cmd/server). Reads filter soft-deleted rows in the repo
// layer; mutations stamp audit rows on success.
package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/audit"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// BranchHandlers groups the per-route handler functions so cmd/server can
// pass dependencies once at wiring time.
type BranchHandlers struct {
	Pool *pgxpool.Pool
}

// branchResponse is the wire shape returned by every /api/branches route.
// It deliberately omits deleted_at — clients should never see soft-deleted
// metadata in normal responses (CLAUDE.md soft-delete invariant).
type branchResponse struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Address   *string `json:"address"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

func toBranchResponse(b repo.BranchRow) branchResponse {
	return branchResponse{
		ID:        b.ID,
		Name:      b.Name,
		Address:   b.Address,
		CreatedAt: b.CreatedAt.In(util.KST).Format(time.RFC3339),
		UpdatedAt: b.UpdatedAt.In(util.KST).Format(time.RFC3339),
	}
}

// List returns every active branch ordered by id ASC. Both global and branch
// admins are allowed — branch admins need it to render their own branch info.
func (h *BranchHandlers) List(c *gin.Context) {
	rows, err := repo.ListBranches(c.Request.Context(), h.Pool)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	out := make([]branchResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBranchResponse(r))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// branchCreateRequest mirrors POST /api/branches body. address is optional
// (nullable column), name is required and bounded to 1..50 (db CHECK).
type branchCreateRequest struct {
	Name    string  `json:"name" binding:"required"`
	Address *string `json:"address"`
}

// Create inserts a branch and writes a branch_create audit row.
func (h *BranchHandlers) Create(c *gin.Context) {
	var req branchCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 50 {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid name"))
		return
	}
	addr := normaliseAddress(req.Address)
	// DB CHECK rejects empty/whitespace-only addresses, but normaliseAddress
	// already collapses whitespace-only inputs to NULL — handler keeps the
	// check explicit so 400 surfaces before the round trip.
	if addr != nil && *addr == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid address"))
		return
	}

	id, err := repo.InsertBranch(c.Request.Context(), h.Pool, name, addr)
	if err != nil {
		writeError(c, apperr.FromDBError(err))
		return
	}
	row, err := repo.GetBranch(c.Request.Context(), h.Pool, id)
	if err != nil || row == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	writeAdminTargetAudit(c, h.Pool, audit.BranchCreate, "branch", id, gin.H{"name": name})
	c.JSON(http.StatusCreated, toBranchResponse(*row))
}

// branchUpdateRequest is the partial-update body. Pointer fields distinguish
// "not provided" from "explicit value".
type branchUpdateRequest struct {
	Name    *string `json:"name"`
	Address *string `json:"address"`
}

// Update applies partial changes to an existing branch.
func (h *BranchHandlers) Update(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req branchUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}

	var name *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" || len([]rune(trimmed)) > 50 {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid name"))
			return
		}
		name = &trimmed
	}
	address := normaliseAddress(req.Address)
	if req.Address != nil && address == nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid address"))
		return
	}

	if err := repo.UpdateBranch(c.Request.Context(), h.Pool, id, name, address); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "branch not found"))
			return
		}
		writeError(c, apperr.FromDBError(err))
		return
	}
	row, err := repo.GetBranch(c.Request.Context(), h.Pool, id)
	if err != nil || row == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "branch not found"))
		return
	}
	meta := gin.H{}
	if name != nil {
		meta["name"] = *name
	}
	if address != nil {
		meta["address_changed"] = true
	}
	writeAdminTargetAudit(c, h.Pool, audit.BranchUpdate, "branch", id, meta)
	c.JSON(http.StatusOK, toBranchResponse(*row))
}

// Delete soft-deletes a branch after verifying it has no active members or
// admins. Both checks + the UPDATE happen in one transaction so a concurrent
// member/admin insert can't slip in between.
func (h *BranchHandlers) Delete(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	var memberCount, adminCount int

	err := repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		// Existence first so missing/already-deleted yields 404 instead of
		// "0 in use".
		row, err := repo.GetBranch(c.Request.Context(), tx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return apperr.New(http.StatusNotFound, "NOT_FOUND", "branch not found")
		}
		mc, err := repo.CountActiveMembers(c.Request.Context(), tx, id)
		if err != nil {
			return err
		}
		ac, err := repo.CountActiveAdmins(c.Request.Context(), tx, id)
		if err != nil {
			return err
		}
		if mc > 0 || ac > 0 {
			memberCount, adminCount = mc, ac
			return apperr.New(http.StatusConflict, "BRANCH_IN_USE", "branch has active members or admins")
		}
		if err := repo.SoftDeleteBranch(c.Request.Context(), tx, id, now); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.New(http.StatusNotFound, "NOT_FOUND", "branch not found")
			}
			return err
		}
		return nil
	})
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.FromDBError(err))
		return
	}
	writeAdminTargetAudit(c, h.Pool, audit.BranchDelete, "branch", id, gin.H{
		"member_count": memberCount,
		"admin_count":  adminCount,
	})
	c.Status(http.StatusNoContent)
}

// ---------- helpers ----------

// normaliseAddress trims whitespace; an all-whitespace input collapses to
// nil so callers can't smuggle bad rows past the DB CHECK.
func normaliseAddress(p *string) *string {
	if p == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*p)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// parseIDParam parses :id from the route. On error it writes a 404 and
// returns ok=false so the handler can return without further branching.
func parseIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "not found"))
		return 0, false
	}
	return id, true
}

// writeAdminTargetAudit is the shared audit-row writer used by both the
// branches and admins handlers. target_type / target_id are baked in so the
// log row is queryable per-resource; metadata always carries request_id for
// correlation. Passing the pool explicitly keeps handlers honest about their
// dependencies and avoids gin.Context-as-DI-container.
func writeAdminTargetAudit(c *gin.Context, pool *pgxpool.Pool, action audit.Action,
	targetType string, targetID int64, extra gin.H) {
	adminID := c.GetInt64(middleware.AdminIDContextKey)
	requestID := c.GetString(middleware.RequestIDContextKey)
	meta := map[string]any{}
	if requestID != "" {
		meta["request_id"] = requestID
	}
	for k, v := range extra {
		meta[k] = v
	}
	tt := targetType
	tid := targetID
	aid := adminID
	_ = audit.Log(c.Request.Context(), pool, audit.Entry{
		AdminID:    &aid,
		Action:     action,
		TargetType: &tt,
		TargetID:   &tid,
		IP:         c.ClientIP(),
		UserAgent:  c.GetHeader("User-Agent"),
		Metadata:   meta,
	})
}
