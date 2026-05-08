// checkins.go owns the two /api/check-ins admin/kiosk routes:
//
//   - POST /api/check-ins (kiosk, no auth) — record one check-in.
//   - GET  /api/check-ins (admin)         — raw or daily-aggregate list.
//
// The kiosk POST is intentionally PII-free in its response: only the new row
// id, the KST-formatted timestamp, and a stripped membership snapshot leave
// the server. Member id / branch id are the request inputs the client
// already holds, and names / phones / birth dates never appear here (the
// surface is unauthenticated and the response gets logged).
//
// The 5-second LRU lives on this handler. The cache key is "<member>:<branch>"
// and the value is the verbatim JSON body the previous request received so a
// double-tap re-renders the same payload byte-for-byte (idempotency).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/cache"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// CheckInHandlers groups the two routes' dependencies. Cache and Clock are
// injected so tests can pin the LRU's clock alongside the handler's KST
// resolution; nil falls back to the system clock.
type CheckInHandlers struct {
	Pool  *pgxpool.Pool
	Cache *cache.LRU
	Clock util.Clock
}

// clock returns the injected clock or a SystemClock fallback so callers don't
// have to wire one in explicitly.
func (h *CheckInHandlers) clock() util.Clock {
	if h.Clock != nil {
		return h.Clock
	}
	return util.SystemClock{}
}

// kioskMembership is the kiosk-facing slice of the membership row. End_date
// helps the kiosk show "expires soon" hints without a follow-up call;
// remaining is only present for pass10.
type kioskMembership struct {
	Type      string `json:"type"`
	Remaining *int   `json:"remaining,omitempty"`
	EndDate   string `json:"end_date"`
	// ExpiredAfter signals that THIS check-in flipped the membership to
	// 'expired' (pass10 hit zero). The kiosk uses this to show "마지막 횟수
	// 사용 완료" rather than the regular completion screen.
	ExpiredAfter bool `json:"expired_after,omitempty"`
}

// kioskCheckInResponse is the wire payload for a successful check-in. Note
// the absence of any member-name / phone / birth field — see the package
// comment for the rationale. We also strip member_id / branch_id because
// they are request inputs the client already holds (docs/API.md spec).
type kioskCheckInResponse struct {
	ID          int64           `json:"id"`
	CheckedInAt string          `json:"checked_in_at"`
	Membership  kioskMembership `json:"membership"`
}

// checkInRequest is the kiosk POST body.
type checkInRequest struct {
	MemberID int64 `json:"memberId"`
	BranchID int64 `json:"branchId"`
}

// CreateCheckIn implements POST /api/check-ins. Order of operations:
//
//  1. Parse + validate the body. Bad ids surface as 400 INVALID_INPUT
//     (validation, not 422 — 422 is reserved for "valid request, business
//     rule blocks it").
//  2. Look up the (member, branch) pair in the 5s LRU. A hit replays the
//     stored bytes verbatim — same status, same body — without touching
//     the DB.
//  3. WithTx → repo.DoCheckIn. ErrNoRows demands a follow-up
//     FindUnstartedMembership to disambiguate MEMBERSHIP_NOT_STARTED from
//     NO_ACTIVE_MEMBERSHIP. Both surface as 422.
//  4. On success store the rendered body in the LRU and return it.
func (h *CheckInHandlers) Create(c *gin.Context) {
	var req checkInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	if req.MemberID <= 0 || req.BranchID <= 0 {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "memberId/branchId required"))
		return
	}

	cacheKey := strconv.FormatInt(req.MemberID, 10) + ":" + strconv.FormatInt(req.BranchID, 10)
	if h.Cache != nil {
		if v, ok := h.Cache.Get(cacheKey); ok {
			c.Data(v.Status, gin.MIMEJSON, v.Body)
			c.Abort()
			return
		}
	}

	// KST today: convert the wall clock to Asia/Seoul, take the date portion,
	// then anchor it back at midnight UTC so the SQL parameter doesn't drift.
	now := h.clock().Now()
	kstNow := now.In(util.KST)
	today := time.Date(kstNow.Year(), kstNow.Month(), kstNow.Day(), 0, 0, 0, 0, time.UTC)

	ctx := c.Request.Context()
	var result repo.CheckInResult
	txErr := repo.WithTx(ctx, h.Pool, func(tx pgx.Tx) error {
		r, err := repo.DoCheckIn(ctx, tx, repo.CheckInInput{
			MemberID: req.MemberID,
			BranchID: req.BranchID,
			Today:    today,
		})
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if txErr != nil {
		// No active membership covers today — disambiguate by looking for a
		// future-start one. The handler swallows pgx.ErrNoRows here; any
		// other DB error falls through to FromDBError below.
		if errors.Is(txErr, pgx.ErrNoRows) {
			h.handleNoActiveMembership(c, ctx, req, today)
			return
		}
		writeError(c, apperr.FromDBError(txErr))
		return
	}

	resp := kioskCheckInResponse{
		ID:          result.Row.ID,
		CheckedInAt: result.Row.CheckedInAt.In(util.KST).Format(time.RFC3339),
		Membership: kioskMembership{
			Type:         result.Membership.Type,
			Remaining:    result.Membership.Remaining,
			EndDate:      result.Membership.EndDate.Format("2006-01-02"),
			ExpiredAfter: result.NewlyExpired,
		},
	}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if h.Cache != nil {
		h.Cache.Set(cacheKey, cache.CheckInResult{
			ID:          result.Row.ID,
			CheckedInAt: result.Row.CheckedInAt,
			Body:        body,
			Status:      http.StatusCreated,
		})
	}
	c.Data(http.StatusCreated, gin.MIMEJSON, body)
}

// handleNoActiveMembership renders 422 — either MEMBERSHIP_NOT_STARTED (a
// future-start active membership exists) or NO_ACTIVE_MEMBERSHIP (no
// membership covers today, expired/refunded/paused/missing all collapse here).
func (h *CheckInHandlers) handleNoActiveMembership(c *gin.Context, ctx context.Context, req checkInRequest, today time.Time) {
	future, err := repo.FindUnstartedMembership(ctx, h.Pool, req.MemberID, req.BranchID, today)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if future != nil {
		writeErrorWith(c,
			apperr.New(http.StatusUnprocessableEntity, "MEMBERSHIP_NOT_STARTED", "membership not started yet"),
			gin.H{"start_date": future.StartDate.Format("2006-01-02")},
		)
		return
	}
	writeError(c, apperr.New(http.StatusUnprocessableEntity, "NO_ACTIVE_MEMBERSHIP", "no active membership for today"))
}

// listCheckInRaw is one row of GET /api/check-ins?aggregate=raw.
type listCheckInRaw struct {
	ID             int64  `json:"id"`
	MemberID       int64  `json:"member_id"`
	MemberName     string `json:"member_name"`
	BranchID       int64  `json:"branch_id"`
	BranchName     string `json:"branch_name"`
	MembershipID   int64  `json:"membership_id"`
	MembershipType string `json:"membership_type"`
	CheckedInAt    string `json:"checked_in_at"`
}

// listCheckInDaily is one row of GET /api/check-ins?aggregate=daily.
// The (member_id, date, branch_id) tuple is the natural group key —
// the same member can have memberships in multiple branches, so the
// daily summary projects branch identity alongside the count and the
// first KST timestamp inside the bucket (docs/API.md contract).
type listCheckInDaily struct {
	MemberID         int64  `json:"member_id"`
	MemberName       string `json:"member_name"`
	BranchID         int64  `json:"branch_id"`
	BranchName       string `json:"branch_name"`
	Date             string `json:"date"`
	CheckinCount     int    `json:"checkin_count"`
	FirstCheckedInAt string `json:"first_checked_in_at"`
}

// List implements GET /api/check-ins. Branch admins are scoped to their own
// branch (server-side filter, not query); globals can drill down via
// ?branchId=. The aggregate switch chooses between cursor-paginated raw rows
// and the unpaginated daily summary (capped at 92 days).
func (h *CheckInHandlers) List(c *gin.Context) {
	fromStr := c.Query("from")
	toStr := c.Query("to")
	if fromStr == "" || toStr == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "from/to required"))
		return
	}
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid from"))
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid to"))
		return
	}
	if to.Before(from) {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "to before from"))
		return
	}
	// Inclusive day-count: from=2026-05-01 to=2026-05-01 is 1 day.
	dayCount := int(to.Sub(from).Hours()/24) + 1

	aggregate := c.Query("aggregate")
	if aggregate == "" {
		aggregate = "raw"
	}
	if aggregate != "raw" && aggregate != "daily" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_AGGREGATE", "aggregate must be raw or daily"))
		return
	}
	if aggregate == "daily" && dayCount > 92 {
		writeError(c, apperr.New(http.StatusBadRequest, "RANGE_TOO_LARGE", "daily aggregate limited to 92 days"))
		return
	}

	in := repo.ListCheckInsInput{
		ScopeBranchID: scopeFromContext(c),
		From:          from,
		To:            to,
	}
	// Globals may drill down via ?branchId=. Branch admins ignore the param.
	if in.ScopeBranchID == nil {
		if bs := c.Query("branchId"); bs != "" {
			bid, err := strconv.ParseInt(bs, 10, 64)
			if err != nil || bid <= 0 {
				writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid branchId"))
				return
			}
			in.BranchFilter = &bid
		}
	}

	if aggregate == "daily" {
		rows, err := repo.ListCheckInsDaily(c.Request.Context(), h.Pool, in)
		if err != nil {
			writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
			return
		}
		out := make([]listCheckInDaily, 0, len(rows))
		for _, r := range rows {
			out = append(out, listCheckInDaily{
				MemberID:         r.MemberID,
				MemberName:       r.MemberName,
				BranchID:         r.BranchID,
				BranchName:       r.BranchName,
				Date:             r.Date.Format("2006-01-02"),
				CheckinCount:     r.CheckinCount,
				FirstCheckedInAt: r.FirstCheckedInAt.In(util.KST).Format(time.RFC3339),
			})
		}
		c.JSON(http.StatusOK, gin.H{"items": out})
		return
	}

	// raw mode — cursor-paginated.
	limit, err := ParseLimit(c.Query("limit"), DefaultListLimit, MaxListLimit)
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_LIMIT", "invalid limit"))
		return
	}
	in.Limit = limit
	if cs := c.Query("cursor"); cs != "" {
		dec, err := DecodeCursor(cs)
		if err != nil {
			var ae *apperr.AppError
			if errors.As(err, &ae) {
				writeError(c, ae)
				return
			}
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor"))
			return
		}
		in.Cursor = &repo.ListCursor{T: dec.T, ID: dec.ID}
	}

	rows, next, err := repo.ListCheckInsRaw(c.Request.Context(), h.Pool, in)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	out := make([]listCheckInRaw, 0, len(rows))
	for _, r := range rows {
		out = append(out, listCheckInRaw{
			ID:             r.ID,
			MemberID:       r.MemberID,
			MemberName:     r.MemberName,
			BranchID:       r.BranchID,
			BranchName:     r.BranchName,
			MembershipID:   r.MembershipID,
			MembershipType: r.MembershipType,
			CheckedInAt:    r.CheckedInAt.In(util.KST).Format(time.RFC3339),
		})
	}
	resp := gin.H{"items": out, "next_cursor": nil}
	if next != nil {
		resp["next_cursor"] = EncodeCursor(Cursor{T: next.T, ID: next.ID})
	}
	c.JSON(http.StatusOK, resp)
}
