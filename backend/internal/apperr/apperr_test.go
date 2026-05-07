package apperr

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNewAndError(t *testing.T) {
	e := New(http.StatusConflict, "PHONE_DUPLICATE", "phone already in use")
	if e.Code != "PHONE_DUPLICATE" || e.Status != http.StatusConflict {
		t.Fatalf("unexpected fields: %+v", e)
	}
	if e.Error() == "" {
		t.Fatalf("Error() should not be empty")
	}
	if e.Unwrap() != nil {
		t.Fatalf("Unwrap should be nil when no cause")
	}
}

func TestWrapAndUnwrap(t *testing.T) {
	cause := errors.New("boom")
	e := Wrap(http.StatusInternalServerError, "INTERNAL", "internal", cause)
	if !errors.Is(e, cause) {
		t.Fatalf("errors.Is should match wrapped cause")
	}
}

func TestIsCode(t *testing.T) {
	e := New(http.StatusConflict, "USERNAME_DUPLICATE", "duplicate")
	if !IsCode(e, "USERNAME_DUPLICATE") {
		t.Fatalf("IsCode should return true for matching code")
	}
	if IsCode(errors.New("plain"), "USERNAME_DUPLICATE") {
		t.Fatalf("IsCode should return false for non-AppError")
	}
	if IsCode(nil, "X") {
		t.Fatalf("IsCode(nil, ...) should be false")
	}
}

func TestFromDBError_Nil(t *testing.T) {
	if got := FromDBError(nil); got != nil {
		t.Fatalf("FromDBError(nil) should return nil, got %+v", got)
	}
}

func TestFromDBError_NonPg(t *testing.T) {
	got := FromDBError(errors.New("connection reset"))
	if got == nil || got.Code != CodeInternal || got.Status != http.StatusInternalServerError {
		t.Fatalf("non-pg err should map to INTERNAL/500, got %+v", got)
	}
}

func TestFromDBError_UniqueViolations(t *testing.T) {
	cases := []struct {
		constraint string
		wantCode   string
	}{
		{"members_branch_phone_unique", "PHONE_DUPLICATE"},
		{"admins_username_key", "USERNAME_DUPLICATE"},
		{"branches_address_key", "ADDRESS_DUPLICATE"},
		{"some_other_unique", "CONFLICT"},
	}
	for _, c := range cases {
		t.Run(c.constraint, func(t *testing.T) {
			pgErr := &pgconn.PgError{Code: "23505", ConstraintName: c.constraint}
			got := FromDBError(pgErr)
			if got == nil || got.Code != c.wantCode || got.Status != http.StatusConflict {
				t.Fatalf("constraint=%s wantCode=%s got=%+v", c.constraint, c.wantCode, got)
			}
		})
	}
}

func TestFromDBError_ExclusionViolation(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23P01"}
	got := FromDBError(pgErr)
	if got == nil || got.Code != "MEMBERSHIP_PERIOD_OVERLAP" || got.Status != http.StatusConflict {
		t.Fatalf("23P01 should map to MEMBERSHIP_PERIOD_OVERLAP/409, got %+v", got)
	}
}

func TestFromDBError_CheckViolation(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23514"}
	got := FromDBError(pgErr)
	if got == nil || got.Code != "INVALID_INPUT" || got.Status != http.StatusBadRequest {
		t.Fatalf("23514 should map to INVALID_INPUT/400, got %+v", got)
	}
}

func TestFromDBError_NotNullAndFK(t *testing.T) {
	for _, code := range []string{"23502", "23503"} {
		pgErr := &pgconn.PgError{Code: code}
		got := FromDBError(pgErr)
		if got == nil || got.Code != CodeInternal || got.Status != http.StatusInternalServerError {
			t.Fatalf("%s should map to INTERNAL/500, got %+v", code, got)
		}
	}
}

func TestFromDBError_Transient(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		pgErr := &pgconn.PgError{Code: code}
		got := FromDBError(pgErr)
		if got == nil || got.Code != CodeTransient {
			t.Fatalf("%s should map to TRANSIENT, got %+v", code, got)
		}
	}
}
