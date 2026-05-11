// kiosk.go owns the two unauthenticated kiosk-facing routes:
// GET /api/members/search and GET /api/check-ins/today-count.
//
// "Public" here means "no Authorization header required" — these routes are
// still inside the rate-limiter, body-size, and request-id middleware, so
// the kiosk gets the same baseline protection as authenticated traffic.
//
// Search responses scrub PII (phone, birth_date) via util.MaskPhone /
// util.MaskBirthMD; the raw values must never leave the server through this
// surface. Admin reads (members.go) keep the raw values because the operator
// UI needs them.
package httpapi

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// KioskHandlers groups kiosk-facing handlers. Like the other Handlers
// structs in this package, dependencies arrive once at wiring time.
type KioskHandlers struct {
	Pool *pgxpool.Pool
}

// kioskSearchHit is the trimmed wire shape — note the absence of any field
// that could carry full phone or full birth_date. The order of items in the
// response is the canonical "most recent check-in first" ordering; clients
// MUST NOT re-sort because we never expose the sort key.
type kioskSearchHit struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	PhoneMasked     string `json:"phone_masked"`
	BirthMD         string `json:"birth_md"`
	MemberIDDisplay string `json:"member_id_display"`
}

// SearchMembers implements GET /api/members/search. branchId is required —
// the kiosk uses the localStorage-stored value. Anything else surfaces as
// 400 INVALID_INPUT or QUERY_TOO_SHORT.
func (h *KioskHandlers) SearchMembers(c *gin.Context) {
	branchIDStr := c.Query("branchId")
	if branchIDStr == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "branchId required"))
		return
	}
	branchID, err := strconv.ParseInt(branchIDStr, 10, 64)
	if err != nil || branchID <= 0 {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid branchId"))
		return
	}
	mode := c.Query("mode")
	q := c.Query("q")

	switch mode {
	case "name":
		// Min 2 runes (handles multi-byte Korean correctly via utf8.RuneCountInString).
		if utf8.RuneCountInString(q) < 2 {
			writeError(c, apperr.New(http.StatusBadRequest, "QUERY_TOO_SHORT",
				"name query must be at least 2 characters"))
			return
		}
	case "phone":
		if len(q) != 4 || !isAllDigits(q) {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_PHONE_QUERY",
				"phone query must be exactly 4 digits"))
			return
		}
	case "memberId":
		if _, err := strconv.ParseInt(q, 10, 64); err != nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_MEMBER_ID",
				"memberId must be numeric"))
			return
		}
	default:
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "unknown mode"))
		return
	}

	hits, truncated, err := repo.SearchMembers(c.Request.Context(), h.Pool, repo.SearchInput{
		BranchID: branchID,
		Mode:     mode,
		Q:        q,
	})
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	out := make([]kioskSearchHit, 0, len(hits))
	for _, h := range hits {
		masked, merr := util.MaskPhone(h.Phone)
		if merr != nil {
			// A row that can't be masked indicates corrupt phone data —
			// drop it from the response rather than leaking the raw value.
			continue
		}
		out = append(out, kioskSearchHit{
			ID:              h.ID,
			Name:            h.Name,
			PhoneMasked:     masked,
			BirthMD:         util.MaskBirthMD(h.BirthDate),
			MemberIDDisplay: util.MemberIDDisplay(h.ID),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"items":     out,
		"truncated": truncated,
	})
}

// TodayCount implements GET /api/check-ins/today-count. No auth; kiosk reads
// it on idle / mount and again after a successful check-in.
func (h *KioskHandlers) TodayCount(c *gin.Context) {
	branchIDStr := c.Query("branchId")
	if branchIDStr == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "branchId required"))
		return
	}
	branchID, err := strconv.ParseInt(branchIDStr, 10, 64)
	if err != nil || branchID <= 0 {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid branchId"))
		return
	}
	n, err := repo.CountTodayCheckIns(c.Request.Context(), h.Pool, branchID)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": n})
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
