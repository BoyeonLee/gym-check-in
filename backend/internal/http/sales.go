// sales.go owns GET /api/sales/summary, the global-only daily/method/branch
// roll-up over the payments table.
//
// Revenue is *only* derived from payments — backend/CLAUDE.md forbids
// back-computing it from memberships or check_ins. The handler validates
// the date range, hands off to repo.SalesSummary, and renders the wire
// shape documented in docs/API.md.
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

// SalesHandlers groups the per-route handler dependencies.
type SalesHandlers struct {
	Pool *pgxpool.Pool
}

// salesMethodBucket is the wire shape for one entry in by_method. Method is
// always present; cash/card both appear even when the totals are zero so the
// frontend can render a stable two-row table.
type salesMethodBucket struct {
	Method      string `json:"method"`
	GrossTotal  int    `json:"gross_total"`
	RefundTotal int    `json:"refund_total"`
	NetTotal    int    `json:"net_total"`
}

type salesDayBucket struct {
	Date        string `json:"date"`
	GrossTotal  int    `json:"gross_total"`
	RefundTotal int    `json:"refund_total"`
	NetTotal    int    `json:"net_total"`
}

type salesSummaryResponse struct {
	GrossTotal  int                 `json:"gross_total"`
	RefundTotal int                 `json:"refund_total"`
	NetTotal    int                 `json:"net_total"`
	ByMethod    []salesMethodBucket `json:"by_method"`
	ByDay       []salesDayBucket    `json:"by_day"`
}

// Summary implements GET /api/sales/summary. Global-only — the route is
// gated upstream by RequireGlobal in cmd/server.
//
// Validation:
//   - from / to are required ISO dates (YYYY-MM-DD).
//   - to >= from. (to < from = 400 INVALID_INPUT.)
//   - branchId, when present, is a positive int (global drilldown).
//
// Range cap: docs/API.md doesn't pin a max for sales; backend/CLAUDE.md only
// recommends "60일 미만 권장". The 92-day cap belongs to /api/check-ins
// (aggregate=daily). Sales summaries can legitimately span a fiscal year
// so we don't enforce one here.
func (h *SalesHandlers) Summary(c *gin.Context) {
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

	in := repo.SalesSummaryInput{From: from, To: to}
	if bs := c.Query("branchId"); bs != "" {
		bid, err := strconv.ParseInt(bs, 10, 64)
		if err != nil || bid <= 0 {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid branchId"))
			return
		}
		in.BranchID = &bid
	}

	row, err := repo.SalesSummary(c.Request.Context(), h.Pool, in)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}

	resp := salesSummaryResponse{
		GrossTotal:  row.GrossTotal,
		RefundTotal: row.RefundTotal,
		NetTotal:    row.NetTotal,
		ByMethod:    make([]salesMethodBucket, 0, len(row.ByMethod)),
		ByDay:       make([]salesDayBucket, 0, len(row.ByDay)),
	}
	for _, b := range row.ByMethod {
		resp.ByMethod = append(resp.ByMethod, salesMethodBucket{
			Method:      b.Method,
			GrossTotal:  b.GrossTotal,
			RefundTotal: b.RefundTotal,
			NetTotal:    b.NetTotal,
		})
	}
	for _, b := range row.ByDay {
		resp.ByDay = append(resp.ByDay, salesDayBucket{
			Date:        b.Date.Format("2006-01-02"),
			GrossTotal:  b.GrossTotal,
			RefundTotal: b.RefundTotal,
			NetTotal:    b.NetTotal,
		})
	}
	c.JSON(http.StatusOK, resp)
}
