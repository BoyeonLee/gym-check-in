// members.go owns the five admin-facing /api/members endpoints introduced
// in step 5: list, get-by-id, create, patch, soft-delete.
//
// All routes assume RequireAuth + MustChangePasswordGuard upstream. Branch
// admins are scoped to their own branch in the SQL helpers — the handler
// derives `scopeBranchID` from the access claim and never trusts the body
// for branch identity. Cross-branch lookups always return 404 (existence
// hiding, not 403) per backend/CLAUDE.md.
//
// Admin responses keep PII (phone, birth_date) in raw form — the kiosk-side
// masking lives in kiosk.go.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/http/middleware"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// MemberHandlers groups the per-route handler functions so cmd/server can
// pass dependencies once at wiring time.
type MemberHandlers struct {
	Pool *pgxpool.Pool
}

// memberResponse is the wire shape returned by every admin-facing member
// route. Phone/BirthDate are raw — the kiosk routes mask them separately.
type memberResponse struct {
	ID         int64  `json:"id"`
	BranchID   int64  `json:"branch_id"`
	BranchName string `json:"branch_name"`
	Name       string `json:"name"`
	Phone      string `json:"phone"`
	PhoneLast4 string `json:"phone_last4"`
	BirthDate  string `json:"birth_date"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func toMemberResponse(m repo.MemberRow) memberResponse {
	return memberResponse{
		ID:         m.ID,
		BranchID:   m.BranchID,
		BranchName: m.BranchName,
		Name:       m.Name,
		Phone:      m.Phone,
		PhoneLast4: m.PhoneLast4,
		BirthDate:  m.BirthDate.Format("2006-01-02"),
		CreatedAt:  m.CreatedAt.In(util.KST).Format(time.RFC3339),
		UpdatedAt:  m.UpdatedAt.In(util.KST).Format(time.RFC3339),
	}
}

// phoneRegex enforces the schema CHECK at the handler layer. Anything else
// surfaces as 400 INVALID_PHONE before the round-trip.
var phoneRegex = regexp.MustCompile(`^[0-9]{11}$`)

// scopeFromContext returns the caller's branch_id when role='branch'; nil
// for global admins. Handlers funnel reads/writes through this so the SQL
// layer always knows whether to apply the scope filter.
//
// The middleware stores claims.BranchID (a *int64) via c.Set, so we have to
// type-assert the raw value here — c.GetInt64 would return 0 because the
// stored value is the pointer type, not int64.
func scopeFromContext(c *gin.Context) *int64 {
	if c.GetString(middleware.RoleContextKey) != "branch" {
		return nil
	}
	raw, ok := c.Get(middleware.BranchIDContextKey)
	if !ok {
		return nil
	}
	pid, ok := raw.(*int64)
	if !ok || pid == nil {
		return nil
	}
	bid := *pid
	return &bid
}

// List returns a cursor page of active members. Branch admins are scoped to
// their own branch; globals can drill down via ?branchId=.
func (h *MemberHandlers) List(c *gin.Context) {
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

	var cur *repo.ListCursor
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
		cur = &repo.ListCursor{T: dec.T, ID: dec.ID}
	}

	in := repo.ListMembersInput{
		ScopeBranchID: scopeFromContext(c),
		Cursor:        cur,
		Limit:         limit,
	}
	// Globals may drill down via ?branchId=.  Branch admins ignore the param
	// because their scope is already enforced.
	if in.ScopeBranchID == nil {
		if bs := c.Query("branchId"); bs != "" {
			bid, err := strconv.ParseInt(bs, 10, 64)
			if err != nil {
				writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid branchId"))
				return
			}
			in.BranchFilter = &bid
		}
	}

	rows, next, err := repo.ListMembers(c.Request.Context(), h.Pool, in)
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	out := make([]memberResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, toMemberResponse(r))
	}

	resp := gin.H{"items": out, "next_cursor": nil}
	if next != nil {
		enc := EncodeCursor(Cursor{T: next.T, ID: next.ID})
		resp["next_cursor"] = enc
	}
	c.JSON(http.StatusOK, resp)
}

// GetByID returns a single member. Cross-branch / soft-deleted / missing all
// collapse into 404 to avoid leaking existence to other-branch admins.
func (h *MemberHandlers) GetByID(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	row, err := repo.GetMember(c.Request.Context(), h.Pool, id, scopeFromContext(c))
	if err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	if row == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "member not found"))
		return
	}
	c.JSON(http.StatusOK, toMemberResponse(*row))
}

// memberCreateRequest mirrors POST /api/members body. branch_id is read
// raw — branch admins have it overwritten by their own branch downstream.
type memberCreateRequest struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	BirthDate string `json:"birth_date"`
	BranchID  *int64 `json:"branch_id"`
}

// Create inserts a member. Branch admins always create in their own branch
// (any branch_id field they send is ignored); global admins must specify a
// valid, non-deleted branch_id.
func (h *MemberHandlers) Create(c *gin.Context) {
	var req memberCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 100 {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid name"))
		return
	}
	if !phoneRegex.MatchString(req.Phone) {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_PHONE", "phone must be 11 digits"))
		return
	}
	birth, err := time.Parse("2006-01-02", req.BirthDate)
	if err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid birth_date"))
		return
	}

	// Force branch_id for branch admins; for globals, require an explicit value.
	var branchID int64
	if scope := scopeFromContext(c); scope != nil {
		branchID = *scope
	} else {
		if req.BranchID == nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "branch_id required"))
			return
		}
		branchID = *req.BranchID
	}
	// Verify branch is active so a confused 500 doesn't surface from FK.
	if row, err := repo.GetBranch(c.Request.Context(), h.Pool, branchID); err != nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	} else if row == nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "branch not found"))
		return
	}

	id, err := repo.InsertMember(c.Request.Context(), h.Pool, repo.CreateMemberInput{
		BranchID:  branchID,
		Name:      name,
		Phone:     req.Phone,
		BirthDate: birth,
	})
	if err != nil {
		writeError(c, apperr.FromDBError(err))
		return
	}
	created, err := repo.GetMember(c.Request.Context(), h.Pool, id, nil)
	if err != nil || created == nil {
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	c.JSON(http.StatusCreated, toMemberResponse(*created))
}

// memberUpdateRequest is the partial-update body. Only name/phone/birth_date
// are honoured; any branch_id field is read but discarded (we read it just to
// be permissive about the body — the field is never applied).
type memberUpdateRequest struct {
	Name      *string `json:"name"`
	Phone     *string `json:"phone"`
	BirthDate *string `json:"birth_date"`
	// BranchID is intentionally read but never used. Including it in the
	// struct prevents the JSON decoder from rejecting unknown keys; the
	// member's branch is fixed at creation time.
	BranchID json.RawMessage `json:"branch_id"`
}

// Update applies a partial change. Cross-branch / missing → 404.
func (h *MemberHandlers) Update(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req memberUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid request"))
		return
	}
	in := repo.UpdateMemberInput{}

	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" || len([]rune(trimmed)) > 100 {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid name"))
			return
		}
		in.Name = &trimmed
	}
	if req.Phone != nil {
		if !phoneRegex.MatchString(*req.Phone) {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_PHONE", "phone must be 11 digits"))
			return
		}
		in.Phone = req.Phone
	}
	if req.BirthDate != nil {
		t, err := time.Parse("2006-01-02", *req.BirthDate)
		if err != nil {
			writeError(c, apperr.New(http.StatusBadRequest, "INVALID_INPUT", "invalid birth_date"))
			return
		}
		in.BirthDate = &t
	}

	if err := repo.UpdateMember(c.Request.Context(), h.Pool, id, in, scopeFromContext(c)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "member not found"))
			return
		}
		writeError(c, apperr.FromDBError(err))
		return
	}
	row, err := repo.GetMember(c.Request.Context(), h.Pool, id, scopeFromContext(c))
	if err != nil || row == nil {
		writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "member not found"))
		return
	}
	c.JSON(http.StatusOK, toMemberResponse(*row))
}

// Delete soft-deletes a member. Returns 204 on success and 404 when the
// member is missing, soft-deleted already, or in another branch.
func (h *MemberHandlers) Delete(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if err := repo.SoftDeleteMember(c.Request.Context(), h.Pool, id, scopeFromContext(c), now); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(c, apperr.New(http.StatusNotFound, "NOT_FOUND", "member not found"))
			return
		}
		writeError(c, apperr.New(http.StatusInternalServerError, "INTERNAL", "internal server error"))
		return
	}
	c.Status(http.StatusNoContent)
}
