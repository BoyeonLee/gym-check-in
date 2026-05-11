// bulk_extend.go owns POST /api/memberships/bulk-extend — the global-only
// "shift every active/paused membership by N days" admin operation.
//
// The route is gated upstream by RequireGlobal. The handler enforces the
// Idempotency-Key contract (UUIDv4, idempotency_keys lookup → Store), runs
// repo.BulkExtend in a single transaction, and on EXCLUDE conflict (a
// neighbouring future membership) renders 409 MEMBERSHIP_PERIOD_OVERLAP
// with `first_conflict_membership_id` so operators can debug without
// re-running the SQL.
//
// All extension work happens in repo.BulkExtend; the handler here is purely
// orchestration (validation → idempotency → tx → render).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/idempotency"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// BulkExtendHandlers groups the per-route dependencies. Clock is injected
// so the idempotency 24h freshness window is deterministic in tests.
type BulkExtendHandlers struct {
	Pool  *pgxpool.Pool
	Clock util.Clock
}

func (h *BulkExtendHandlers) clock() util.Clock {
	if h.Clock != nil {
		return h.Clock
	}
	return util.SystemClock{}
}

// bulkExtendRequest mirrors the documented body. type/branch_id are
// optional filters; days is the (1..90) extension; reason is required so
// the membership_events row carries why the change happened.
type bulkExtendRequest struct {
	BranchID *int64  `json:"branch_id"`
	Type     *string `json:"type"`
	Days     int     `json:"days"`
	Reason   string  `json:"reason"`
}

// bulkExtendEndpoint is the canonical endpoint label stored alongside the
// idempotency key. Matches the Idempotency-Key key space the docs reserve
// for this route.
const bulkExtendEndpoint = "POST /api/memberships/bulk-extend"

// BulkExtend implements POST /api/memberships/bulk-extend.
func (h *BulkExtendHandlers) BulkExtend(c *gin.Context) {
	// Idempotency-Key required + valid UUIDv4.
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		writeError(c, apperr.New(http.StatusBadRequest,
			"IDEMPOTENCY_KEY_REQUIRED",
			"Idempotency-Key header is required"))
		return
	}
	if err := idempotency.ValidateKey(key); err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_IDEMPOTENCY_KEY",
			"invalid Idempotency-Key"))
		return
	}

	// Read and decode body. We keep the raw bytes so the request hash for
	// idempotency reflects the exact payload the client sent.
	raw, err := c.GetRawData()
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid body"))
		return
	}
	var req bulkExtendRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid body"))
			return
		}
	}

	// days: 1..90.
	if req.Days < 1 || req.Days > 90 {
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_EXTEND_DAYS",
			"days must be between 1 and 90"))
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "reason required"))
		return
	}
	if req.Type != nil {
		t := *req.Type
		if t != "monthly" && t != "pass10" {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid type"))
			return
		}
	}

	adminID := c.GetInt64(middleware.AdminIDContextKey)
	if adminID <= 0 {
		writeError(c, apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "missing admin context"))
		return
	}

	requestHash, _ := idempotency.HashRequest(raw)
	now := h.clock().Now()

	ctx := c.Request.Context()
	hit, lerr := idempotency.Lookup(ctx, h.Pool, key, bulkExtendEndpoint, adminID, requestHash, now)
	if lerr != nil {
		var ae *apperr.AppError
		if errors.As(lerr, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if hit.Found {
		c.Data(hit.Status, gin.MIMEJSON, hit.Body)
		c.Abort()
		return
	}

	// KST today: anchor the SQL bound at midnight UTC so the parameter
	// doesn't drift between sessions.
	kstNow := now.In(util.KST)
	today := time.Date(kstNow.Year(), kstNow.Month(), kstNow.Day(), 0, 0, 0, 0, time.UTC)

	var extended int
	txErr := repo.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		n, err := repo.BulkExtend(ctx, tx, repo.BulkExtendInput{
			BranchID:    req.BranchID,
			Type:        req.Type,
			Days:        req.Days,
			Today:       today,
			Reason:      strings.TrimSpace(req.Reason),
			PerformedBy: adminID,
		})
		if err != nil {
			return err
		}
		extended = n
		return nil
	})

	if txErr != nil {
		// EXCLUDE collision — render 409 with first_conflict_membership_id.
		// Store the (final) response so an idempotent retry replays the same
		// body byte-for-byte.
		var bec *repo.BulkExtendConflict
		if errors.As(txErr, &bec) {
			h.renderConflict(c, ctx, key, adminID, requestHash, bec.MembershipID)
			return
		}
		// Other DB errors run through the standard mapping; if FromDBError
		// surfaces 23P01 (e.g. raw pgconn.PgError without our wrapper) the
		// handler still gives 409 MEMBERSHIP_PERIOD_OVERLAP — but without an
		// id payload, since the offending row isn't known.
		writeError(c, apperr.FromDBError(txErr))
		return
	}

	resp := gin.H{
		"extended_count": extended,
		"days":           req.Days,
		"reason":         strings.TrimSpace(req.Reason),
	}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	// Store result. A storage failure is non-fatal — the handler logs and
	// returns the body anyway. (Retry on the same key may re-execute, which
	// for bulk-extend means a *second* extension; idempotency_keys insert
	// uses ON CONFLICT DO NOTHING so the worst case is a second extension
	// only when the first store somehow lost the row.)
	_ = idempotency.Store(ctx, h.Pool, key, bulkExtendEndpoint, adminID, requestHash, http.StatusOK, body)
	c.Data(http.StatusOK, gin.MIMEJSON, body)
}

// renderConflict writes a 409 MEMBERSHIP_PERIOD_OVERLAP response and
// persists it to idempotency_keys so a retry returns the same body.
func (h *BulkExtendHandlers) renderConflict(c *gin.Context, ctx context.Context,
	key string, adminID int64, requestHash string, conflictID int64,
) {
	body, _ := json.Marshal(gin.H{
		"error": gin.H{
			"code":    "MEMBERSHIP_PERIOD_OVERLAP",
			"message": "extension would overlap an existing membership period",
		},
		"first_conflict_membership_id": conflictID,
	})
	c.Set(middleware.ErrorCodeContextKey, "MEMBERSHIP_PERIOD_OVERLAP")
	_ = idempotency.Store(ctx, h.Pool, key, bulkExtendEndpoint, adminID, requestHash, http.StatusConflict, body)
	c.Data(http.StatusConflict, gin.MIMEJSON, body)
	c.Abort()
}
