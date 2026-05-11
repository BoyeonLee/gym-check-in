// Package apperr is the project's unified error type and DB error mapper.
// All domain/handler errors should be returned as *AppError so that the
// HTTP layer can render { "error": { "code": "...", "message": "..." } }
// with the correct status without leaking internals.
package apperr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// Standard error codes that don't belong to a specific domain.
const (
	CodeInternal  = "INTERNAL"
	CodeConflict  = "CONFLICT"
	CodeTransient = "TRANSIENT"
)

// AppError is the canonical error type. It carries an HTTP status and a stable
// machine-readable Code (see docs/API.md catalog). Wrap external errors via Wrap.
type AppError struct {
	Code    string
	Message string
	Status  int
	Cause   error
}

// New creates an AppError without a wrapped cause.
func New(status int, code, message string) *AppError {
	return &AppError{Code: code, Message: message, Status: status}
}

// Wrap creates an AppError carrying an underlying cause for diagnostics.
// The cause is reachable via errors.Is / errors.As.
func Wrap(status int, code, message string, cause error) *AppError {
	return &AppError{Code: code, Message: message, Status: status, Cause: cause}
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil AppError>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause for errors.Is/As traversal.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsCode reports whether err (or any wrapped error) is an *AppError with the given code.
func IsCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var ae *AppError
	if errors.As(err, &ae) {
		return ae.Code == code
	}
	return false
}

// FromDBError maps a pgx/pgconn error to a baseline AppError.
// Callers may swap the returned Code/Message for context-specific values
// (e.g. INVALID_AMOUNT instead of INVALID_INPUT for amount checks).
//
// Mapping (PostgreSQL SQLSTATE):
//
//	23505 unique_violation       → 409, code by constraint name
//	23P01 exclusion_violation    → 409 MEMBERSHIP_PERIOD_OVERLAP
//	23514 check_violation        → 400 INVALID_INPUT
//	23502 not_null_violation     → 500 INTERNAL  (should not happen on happy path)
//	23503 foreign_key_violation  → 500 INTERNAL
//	40001 / 40P01 transient      → 500 TRANSIENT (caller's retry helper recognises this)
//	other / non-pg               → 500 INTERNAL
func FromDBError(err error) *AppError {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return Wrap(http.StatusInternalServerError, CodeInternal, "internal server error", err)
	}
	switch pgErr.Code {
	case "23505":
		return Wrap(http.StatusConflict, codeForUniqueConstraint(pgErr.ConstraintName), "duplicate value", err)
	case "23P01":
		return Wrap(http.StatusConflict, "MEMBERSHIP_PERIOD_OVERLAP", "membership period overlaps", err)
	case "23514":
		return Wrap(http.StatusBadRequest, "INVALID_INPUT", "invalid input", err)
	case "23502", "23503":
		return Wrap(http.StatusInternalServerError, CodeInternal, "internal server error", err)
	case "40001", "40P01":
		return Wrap(http.StatusInternalServerError, CodeTransient, "transient db error, retry", err)
	default:
		return Wrap(http.StatusInternalServerError, CodeInternal, "internal server error", err)
	}
}

func codeForUniqueConstraint(name string) string {
	switch name {
	case "members_branch_phone_unique":
		return "PHONE_DUPLICATE"
	case "admins_username_key":
		return "USERNAME_DUPLICATE"
	case "branches_address_key":
		return "ADDRESS_DUPLICATE"
	default:
		return CodeConflict
	}
}
