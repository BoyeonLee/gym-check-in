// memberships.go owns the six membership lifecycle endpoints — grant,
// detail, pause, unpause, cancel-pause, refund. Bulk-extend lives in
// bulk_extend.go because it is a global-only batch operation with its
// own idempotency flow.
//
// Routing assumptions: every route is wired into the authenticated /api
// group with RequireAuth + MustChangePasswordGuard upstream. Branch
// admins are scoped to their own branch through scopeFromContext; cross-
// branch / soft-deleted / missing targets always collapse into 404 per
// backend/CLAUDE.md.
//
// Transaction shape: grant / pause / unpause / cancel-pause / refund all
// run under repo.WithTx so the membership UPDATE, the optional payments
// INSERT and the membership_events ledger row commit atomically. The
// EXCLUDE constraint on memberships (no overlapping active/paused
// periods per member) surfaces as PostgreSQL 23P01, which
// apperr.FromDBError rewrites to 409 MEMBERSHIP_PERIOD_OVERLAP.
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
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/idempotency"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// MembershipsHandler groups the per-route dependencies. Clock is injected
// so KST today and the idempotency 24h freshness window stay deterministic
// in tests.
type MembershipsHandler struct {
	Pool  *pgxpool.Pool
	Clock util.Clock
}

func (h *MembershipsHandler) clock() util.Clock {
	if h.Clock != nil {
		return h.Clock
	}
	return util.SystemClock{}
}

// kstToday returns midnight at today's KST date, anchored at UTC so the
// parameter survives pgx's TIME ZONE=UTC session setting without drifting
// to "yesterday" near the day boundary.
func (h *MembershipsHandler) kstToday() time.Time {
	kstNow := h.clock().Now().In(util.KST)
	return time.Date(kstNow.Year(), kstNow.Month(), kstNow.Day(), 0, 0, 0, 0, time.UTC)
}

// Endpoint labels stamped into idempotency_keys.endpoint so a key reused
// across different operations surfaces as IDEMPOTENCY_KEY_CONFLICT
// rather than silently replaying the wrong stored response.
const (
	grantEndpoint  = "POST /api/members/:id/memberships"
	refundEndpoint = "POST /api/memberships/:id/refund"
)

// ---- wire shapes ---------------------------------------------------

type membershipResponse struct {
	ID             int64   `json:"id"`
	MemberID       int64   `json:"member_id"`
	BranchID       int64   `json:"branch_id"`
	Type           string  `json:"type"`
	Months         *int    `json:"months"`
	Remaining      *int    `json:"remaining"`
	Status         string  `json:"status"`
	StartDate      string  `json:"start_date"`
	EndDate        string  `json:"end_date"`
	PauseStartDate *string `json:"pause_start_date"`
	PauseEndDate   *string `json:"pause_end_date"`
	PauseUsed      bool    `json:"pause_used"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

func toMembershipResponse(m repo.MembershipRow, branchID int64) membershipResponse {
	out := membershipResponse{
		ID:        m.ID,
		MemberID:  m.MemberID,
		BranchID:  branchID,
		Type:      m.Type,
		Months:    m.Months,
		Remaining: m.Remaining,
		Status:    m.Status,
		StartDate: m.StartDate.Format("2006-01-02"),
		EndDate:   m.EndDate.Format("2006-01-02"),
		PauseUsed: m.PauseUsed,
		CreatedAt: m.CreatedAt.In(util.KST).Format(time.RFC3339),
		UpdatedAt: m.UpdatedAt.In(util.KST).Format(time.RFC3339),
	}
	if m.PauseStartDate != nil {
		s := m.PauseStartDate.Format("2006-01-02")
		out.PauseStartDate = &s
	}
	if m.PauseEndDate != nil {
		s := m.PauseEndDate.Format("2006-01-02")
		out.PauseEndDate = &s
	}
	return out
}

type paymentResponse struct {
	ID           int64   `json:"id"`
	MembershipID int64   `json:"membership_id"`
	BranchID     int64   `json:"branch_id"`
	Amount       int     `json:"amount"`
	Method       string  `json:"method"`
	PaidAt       string  `json:"paid_at"`
	Memo         *string `json:"memo"`
	PerformedBy  int64   `json:"performed_by"`
	CreatedAt    string  `json:"created_at"`
}

func toPaymentResponse(p repo.PaymentRow) paymentResponse {
	return paymentResponse{
		ID:           p.ID,
		MembershipID: p.MembershipID,
		BranchID:     p.BranchID,
		Amount:       p.Amount,
		Method:       p.Method,
		PaidAt:       p.PaidAt.Format("2006-01-02"),
		Memo:         p.Memo,
		PerformedBy:  p.PerformedBy,
		CreatedAt:    p.CreatedAt.In(util.KST).Format(time.RFC3339),
	}
}

func toPaymentResponses(rows []repo.PaymentRow) []paymentResponse {
	out := make([]paymentResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, toPaymentResponse(p))
	}
	return out
}

type eventResponse struct {
	ID             int64   `json:"id"`
	MembershipID   int64   `json:"membership_id"`
	Action         string  `json:"action"`
	PauseStartDate *string `json:"pause_start_date"`
	PauseEndDate   *string `json:"pause_end_date"`
	ActualPauseEnd *string `json:"actual_pause_end"`
	ExtendDays     *int    `json:"extend_days"`
	Reason         string  `json:"reason"`
	PerformedBy    int64   `json:"performed_by"`
	CreatedAt      string  `json:"created_at"`
}

func toEventResponse(e repo.EventRow) eventResponse {
	out := eventResponse{
		ID:           e.ID,
		MembershipID: e.MembershipID,
		Action:       e.Action,
		ExtendDays:   e.ExtendDays,
		Reason:       e.Reason,
		PerformedBy:  e.PerformedBy,
		CreatedAt:    e.CreatedAt.In(util.KST).Format(time.RFC3339),
	}
	if e.PauseStartDate != nil {
		s := e.PauseStartDate.Format("2006-01-02")
		out.PauseStartDate = &s
	}
	if e.PauseEndDate != nil {
		s := e.PauseEndDate.Format("2006-01-02")
		out.PauseEndDate = &s
	}
	if e.ActualPauseEnd != nil {
		s := e.ActualPauseEnd.Format("2006-01-02")
		out.ActualPauseEnd = &s
	}
	return out
}

func toEventResponses(rows []repo.EventRow) []eventResponse {
	out := make([]eventResponse, 0, len(rows))
	for _, e := range rows {
		out = append(out, toEventResponse(e))
	}
	return out
}

// renderMembership re-fetches the membership (post-mutation) and renders
// the standard `{ "membership": ... }` envelope. Returns false (with an
// error already written) when the row vanished between transaction
// commit and the re-read; that "shouldn't happen" path is treated as 500.
func (h *MembershipsHandler) renderMembership(c *gin.Context, id int64, status int) bool {
	row, err := repo.GetMembership(c.Request.Context(), h.Pool, id, nil)
	if err != nil || row == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return false
	}
	member, err := repo.GetMember(c.Request.Context(), h.Pool, row.MemberID, nil)
	if err != nil || member == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return false
	}
	c.JSON(status, gin.H{
		"membership": toMembershipResponse(*row, member.BranchID),
	})
	return true
}

// ---- Get -----------------------------------------------------------

// Get implements GET /api/memberships/:id.
func (h *MembershipsHandler) Get(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	scope := scopeFromContext(c)
	detail, err := repo.GetMembershipDetail(c.Request.Context(), h.Pool, id, scope)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if detail == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "membership not found"))
		return
	}
	member, err := repo.GetMember(c.Request.Context(), h.Pool, detail.Membership.MemberID, nil)
	if err != nil || member == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"membership": toMembershipResponse(detail.Membership, member.BranchID),
		"payments":   toPaymentResponses(detail.Payments),
		"events":     toEventResponses(detail.Events),
	})
}

// ---- Grant ---------------------------------------------------------

type membershipGrantPaymentReq struct {
	Amount int    `json:"amount"`
	Method string `json:"method"`
	// PaidAt and BranchID are intentionally absent — the docs say the
	// server sets both. A client that sends them is ignored, not 400'd,
	// because the spec is explicit that those fields are server-owned.
}

type membershipGrantRequest struct {
	Type      string                    `json:"type"`
	Months    *int                      `json:"months"`
	StartDate string                    `json:"start_date"`
	Payment   membershipGrantPaymentReq `json:"payment"`
}

// Grant implements POST /api/members/:id/memberships.
func (h *MembershipsHandler) Grant(c *gin.Context) {
	memberID, ok := parseIDParam(c)
	if !ok {
		return
	}

	// Idempotency-Key header — required + must be UUIDv4.
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		writeError(c, apperr.New(http.StatusBadRequest,
			"IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required"))
		return
	}
	if err := idempotency.ValidateKey(key); err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_IDEMPOTENCY_KEY", "invalid Idempotency-Key"))
		return
	}

	// Read raw body so idempotency.HashRequest reflects the exact payload.
	raw, err := c.GetRawData()
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid body"))
		return
	}
	var req membershipGrantRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid body"))
			return
		}
	}

	// Shape validation.
	if req.Type != "monthly" && req.Type != "pass10" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid type"))
		return
	}
	if req.Type == "monthly" {
		if req.Months == nil || *req.Months < 1 {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_MONTHS", "months must be >= 1"))
			return
		}
	}
	if req.Payment.Amount <= 0 {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_AMOUNT", "amount must be positive"))
		return
	}
	if req.Payment.Method != "cash" && req.Payment.Method != "card" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid method"))
		return
	}
	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid start_date"))
		return
	}

	// start_date must be today or later (KST).
	today := h.kstToday()
	if startDate.Before(today) {
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_START_DATE", "start_date must be today or in the future"))
		return
	}

	// Resolve member + branch_id under the caller's scope.  Cross-branch
	// or soft-deleted member collapses to 404.
	scope := scopeFromContext(c)
	member, err := repo.GetMember(c.Request.Context(), h.Pool, memberID, scope)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if member == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "member not found"))
		return
	}

	adminID := c.GetInt64(middleware.AdminIDContextKey)
	if adminID <= 0 {
		writeError(c, apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "missing admin context"))
		return
	}

	// Idempotency lookup (replay or conflict).
	requestHash, _ := idempotency.HashRequest(raw)
	now := h.clock().Now()
	hit, lerr := idempotency.Lookup(c.Request.Context(), h.Pool, key, grantEndpoint, adminID, requestHash, now)
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

	// Compute end_date / remaining / months.
	var (
		endDate   time.Time
		months    *int
		remaining *int
	)
	switch req.Type {
	case "monthly":
		endDate = startDate.AddDate(0, *req.Months, 0)
		months = req.Months
	case "pass10":
		endDate = startDate.AddDate(0, 2, 0)
		ten := 10
		remaining = &ten
	}

	var (
		newMembershipID int64
		newPaymentID    int64
	)
	txErr := repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		mid, err := repo.InsertMembership(c.Request.Context(), tx, repo.GrantInput{
			MemberID:  memberID,
			Type:      req.Type,
			Months:    months,
			Remaining: remaining,
			StartDate: startDate,
			EndDate:   endDate,
		})
		if err != nil {
			return err
		}
		pid, err := repo.InsertPayment(c.Request.Context(), tx, repo.PaymentRow{
			MembershipID: mid,
			BranchID:     member.BranchID,
			Amount:       req.Payment.Amount,
			Method:       req.Payment.Method,
			PaidAt:       today,
			PerformedBy:  adminID,
		})
		if err != nil {
			return err
		}
		newMembershipID = mid
		newPaymentID = pid
		return nil
	})
	if txErr != nil {
		writeError(c, apperr.FromDBError(txErr))
		return
	}

	// Re-read the rows we just inserted so the response matches what the
	// DB committed (including defaulted columns like created_at).
	detail, err := repo.GetMembershipDetail(c.Request.Context(), h.Pool, newMembershipID, nil)
	if err != nil || detail == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	var paymentResp paymentResponse
	for _, p := range detail.Payments {
		if p.ID == newPaymentID {
			paymentResp = toPaymentResponse(p)
			break
		}
	}

	resp := gin.H{
		"membership": toMembershipResponse(detail.Membership, member.BranchID),
		"payment":    paymentResp,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	// Store the response so a retry replays it byte-for-byte.  A store
	// failure is logged-only — the response still goes out.
	_ = idempotency.Store(c.Request.Context(), h.Pool, key, grantEndpoint, adminID, requestHash, http.StatusCreated, body)
	c.Data(http.StatusCreated, gin.MIMEJSON, body)
}

// ---- Pause ---------------------------------------------------------

type membershipPauseRequest struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Reason    string `json:"reason"`
}

// Pause implements POST /api/memberships/:id/pause.
func (h *MembershipsHandler) Pause(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req membershipPauseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "reason required"))
		return
	}
	pauseStart, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_PAUSE_RANGE", "invalid start_date"))
		return
	}
	pauseEnd, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_PAUSE_RANGE", "invalid end_date"))
		return
	}

	scope := scopeFromContext(c)
	membership, err := repo.GetMembership(c.Request.Context(), h.Pool, id, scope)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if membership == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "membership not found"))
		return
	}
	if membership.PauseUsed {
		writeError(c, apperr.New(http.StatusConflict,
			"PAUSE_ALREADY_USED", "pause already used for this membership"))
		return
	}

	today := h.kstToday()
	msStart := dateOnly(membership.StartDate)
	msEnd := dateOnly(membership.EndDate)
	if pauseStart.After(pauseEnd) {
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_PAUSE_RANGE", "start_date must be <= end_date"))
		return
	}
	if pauseStart.Before(today) {
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_PAUSE_RANGE", "start_date must be today or future"))
		return
	}
	if pauseStart.Before(msStart) {
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_PAUSE_RANGE", "start_date before membership start_date"))
		return
	}
	if pauseEnd.After(msEnd) {
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_PAUSE_RANGE", "end_date after membership end_date"))
		return
	}

	adminID := c.GetInt64(middleware.AdminIDContextKey)

	txErr := repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := repo.ApplyPause(c.Request.Context(), tx, repo.PauseInput{
			ID:             id,
			PauseStartDate: pauseStart,
			PauseEndDate:   pauseEnd,
			Today:          today,
		}); err != nil {
			return err
		}
		return repo.InsertEvent(c.Request.Context(), tx, repo.EventRow{
			MembershipID:   id,
			Action:         "pause",
			PauseStartDate: &pauseStart,
			PauseEndDate:   &pauseEnd,
			Reason:         reason,
			PerformedBy:    adminID,
		})
	})
	if txErr != nil {
		writeError(c, apperr.FromDBError(txErr))
		return
	}
	h.renderMembership(c, id, http.StatusOK)
}

// ---- Unpause -------------------------------------------------------

type membershipReasonRequest struct {
	Reason string `json:"reason"`
}

// Unpause implements POST /api/memberships/:id/unpause.
func (h *MembershipsHandler) Unpause(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req membershipReasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "reason required"))
		return
	}

	scope := scopeFromContext(c)
	membership, err := repo.GetMembership(c.Request.Context(), h.Pool, id, scope)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if membership == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "membership not found"))
		return
	}
	if membership.Status != "paused" {
		writeError(c, apperr.New(http.StatusConflict, "NOT_PAUSED", "membership is not paused"))
		return
	}

	today := h.kstToday()
	adminID := c.GetInt64(middleware.AdminIDContextKey)
	actualPauseEnd := today

	txErr := repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := repo.ApplyUnpause(c.Request.Context(), tx, repo.UnpauseInput{
			ID:             id,
			ActualPauseEnd: today,
		}); err != nil {
			return err
		}
		return repo.InsertEvent(c.Request.Context(), tx, repo.EventRow{
			MembershipID:   id,
			Action:         "unpause",
			ActualPauseEnd: &actualPauseEnd,
			Reason:         reason,
			PerformedBy:    adminID,
		})
	})
	if txErr != nil {
		writeError(c, apperr.FromDBError(txErr))
		return
	}
	h.renderMembership(c, id, http.StatusOK)
}

// ---- CancelPause ---------------------------------------------------

// CancelPause implements POST /api/memberships/:id/cancel-pause.
func (h *MembershipsHandler) CancelPause(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req membershipReasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "reason required"))
		return
	}

	scope := scopeFromContext(c)
	membership, err := repo.GetMembership(c.Request.Context(), h.Pool, id, scope)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if membership == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "membership not found"))
		return
	}

	today := h.kstToday()
	// Eligibility: status='active' AND pause_used=true AND pause_start_date > today
	if membership.Status != "active" || !membership.PauseUsed ||
		membership.PauseStartDate == nil || !dateOnly(*membership.PauseStartDate).After(today) {
		writeError(c, apperr.New(http.StatusConflict,
			"PAUSE_NOT_SCHEDULED", "no scheduled future pause to cancel"))
		return
	}

	adminID := c.GetInt64(middleware.AdminIDContextKey)

	txErr := repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := repo.ApplyCancelPause(c.Request.Context(), tx, repo.CancelPauseInput{
			ID:    id,
			Today: today,
		}); err != nil {
			return err
		}
		return repo.InsertEvent(c.Request.Context(), tx, repo.EventRow{
			MembershipID: id,
			Action:       "cancel_pause",
			Reason:       reason,
			PerformedBy:  adminID,
		})
	})
	if txErr != nil {
		writeError(c, apperr.FromDBError(txErr))
		return
	}
	h.renderMembership(c, id, http.StatusOK)
}

// ---- Refund --------------------------------------------------------

// Refund implements POST /api/memberships/:id/refund.
func (h *MembershipsHandler) Refund(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	// Idempotency-Key first — header errors before any DB I/O.
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		writeError(c, apperr.New(http.StatusBadRequest,
			"IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required"))
		return
	}
	if err := idempotency.ValidateKey(key); err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.New(http.StatusBadRequest,
			"INVALID_IDEMPOTENCY_KEY", "invalid Idempotency-Key"))
		return
	}

	raw, err := c.GetRawData()
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid body"))
		return
	}
	var req membershipReasonRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid body"))
			return
		}
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "reason required"))
		return
	}

	scope := scopeFromContext(c)
	membership, err := repo.GetMembership(c.Request.Context(), h.Pool, id, scope)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if membership == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "membership not found"))
		return
	}

	adminID := c.GetInt64(middleware.AdminIDContextKey)
	if adminID <= 0 {
		writeError(c, apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "missing admin context"))
		return
	}

	// Idempotency replay BEFORE status guard: a replay of a successful
	// refund must return the stored 200 body, not a fresh 409 on the now-
	// refunded row.
	requestHash, _ := idempotency.HashRequest(raw)
	now := h.clock().Now()
	hit, lerr := idempotency.Lookup(c.Request.Context(), h.Pool, key, refundEndpoint, adminID, requestHash, now)
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

	switch membership.Status {
	case "expired":
		writeError(c, apperr.New(http.StatusConflict,
			"MEMBERSHIP_ALREADY_EXPIRED", "expired membership cannot be refunded"))
		return
	case "refunded":
		// A different Idempotency-Key targeting an already-refunded row.
		writeError(c, apperr.New(http.StatusConflict,
			apperr.CodeConflict, "membership already refunded"))
		return
	case "active", "paused":
		// proceed
	default:
		writeError(c, apperr.New(http.StatusConflict,
			apperr.CodeConflict, "membership not refundable"))
		return
	}

	today := h.kstToday()
	var newPaymentID int64
	txErr := repo.WithTx(c.Request.Context(), h.Pool, func(tx pgx.Tx) error {
		orig, err := repo.GetOriginalGrantPayment(c.Request.Context(), tx, id)
		if err != nil {
			return err
		}
		if orig == nil {
			return apperr.New(http.StatusConflict, apperr.CodeConflict,
				"no original payment row to mirror")
		}
		if err := repo.ApplyRefund(c.Request.Context(), tx, repo.RefundInput{ID: id}); err != nil {
			return err
		}
		pid, err := repo.InsertPayment(c.Request.Context(), tx, repo.PaymentRow{
			MembershipID: id,
			BranchID:     orig.BranchID,
			Amount:       -orig.Amount,
			Method:       orig.Method,
			PaidAt:       today,
			PerformedBy:  adminID,
		})
		if err != nil {
			return err
		}
		newPaymentID = pid
		return repo.InsertEvent(c.Request.Context(), tx, repo.EventRow{
			MembershipID: id,
			Action:       "refund",
			Reason:       reason,
			PerformedBy:  adminID,
		})
	})
	if txErr != nil {
		// apperr.AppError surfaces with its own code; otherwise fall through
		// to FromDBError (which handles 23P01 / 23505 / generic 500).
		var ae *apperr.AppError
		if errors.As(txErr, &ae) {
			writeError(c, ae)
			return
		}
		writeError(c, apperr.FromDBError(txErr))
		return
	}

	updated, err := repo.GetMembership(c.Request.Context(), h.Pool, id, nil)
	if err != nil || updated == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	member, err := repo.GetMember(c.Request.Context(), h.Pool, updated.MemberID, nil)
	if err != nil || member == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	payments, err := repo.ListPaymentsByMembership(c.Request.Context(), h.Pool, id)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	var refundPayment paymentResponse
	for _, p := range payments {
		if p.ID == newPaymentID {
			refundPayment = toPaymentResponse(p)
			break
		}
	}

	resp := gin.H{
		"membership":     toMembershipResponse(*updated, member.BranchID),
		"refund_payment": refundPayment,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	_ = idempotency.Store(c.Request.Context(), h.Pool, key, refundEndpoint, adminID, requestHash, http.StatusOK, body)
	c.Data(http.StatusOK, gin.MIMEJSON, body)
}

// dateOnly truncates a time.Time to its (year, month, day) at midnight UTC,
// matching the parameter shape we feed to pgx for date columns.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
