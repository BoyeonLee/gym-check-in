package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// BranchOpts configures CreateBranch. Nil fields receive sensible defaults
// (unique name/address per call) so tests don't have to invent values.
type BranchOpts struct {
	Name    string
	Address string
}

// AdminOpts configures CreateAdmin. Password defaults to "test1234A" hashed
// with bcrypt cost 4 (fast for tests; production uses cost 12 — see cmd/hashpw).
type AdminOpts struct {
	Username           string
	Role               string // "global" | "branch"
	BranchID           *int64 // required when Role == "branch"
	Password           string
	MustChangePassword bool
}

// MemberOpts configures CreateMember. BirthDate is "YYYY-MM-DD".
type MemberOpts struct {
	BranchID  int64
	Name      string
	Phone     string
	BirthDate string
}

// MembershipOpts configures CreateMembership. StartDate / EndDate are "YYYY-MM-DD".
type MembershipOpts struct {
	MemberID  int64
	Type      string // "monthly" | "pass10"
	Months    *int   // required when Type == "monthly"
	Remaining *int   // required when Type == "pass10"
	StartDate string
	EndDate   string
	Status    string // default "active"
}

// CreateBranch inserts a row in branches and returns its id.
func CreateBranch(t *testing.T, pool *pgxpool.Pool, o *BranchOpts) int64 {
	t.Helper()
	if o == nil {
		o = &BranchOpts{}
	}
	if o.Name == "" {
		o.Name = uniqueName("branch")
	}
	if o.Address == "" {
		o.Address = uniqueName("addr")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx,
		`insert into branches (name, address) values ($1, $2) returning id`,
		o.Name, o.Address,
	).Scan(&id)
	if err != nil {
		t.Fatalf("testutil.CreateBranch: %v", err)
	}
	return id
}

// CreateAdmin inserts a row in admins and returns (id, plaintextPassword).
// The plaintext is returned so handler tests can call /api/admin/login with it.
func CreateAdmin(t *testing.T, pool *pgxpool.Pool, o *AdminOpts) (int64, string) {
	t.Helper()
	if o == nil {
		o = &AdminOpts{}
	}
	if o.Username == "" {
		o.Username = uniqueName("admin")
	}
	if o.Role == "" {
		o.Role = "global"
	}
	if o.Role == "branch" && o.BranchID == nil {
		t.Fatalf("testutil.CreateAdmin: role='branch' requires BranchID")
	}
	if o.Role == "global" && o.BranchID != nil {
		t.Fatalf("testutil.CreateAdmin: role='global' must have nil BranchID")
	}
	plaintext := o.Password
	if plaintext == "" {
		plaintext = "test1234A"
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("testutil.CreateAdmin: hash: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var id int64
	err = pool.QueryRow(ctx,
		`insert into admins (username, password_hash, must_change_password, role, branch_id)
		 values ($1, $2, $3, $4, $5)
		 returning id`,
		o.Username, string(hash), o.MustChangePassword, o.Role, o.BranchID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("testutil.CreateAdmin: insert: %v", err)
	}
	return id, plaintext
}

// CreateMember inserts a row in members and returns its id.
func CreateMember(t *testing.T, pool *pgxpool.Pool, o *MemberOpts) int64 {
	t.Helper()
	if o == nil || o.BranchID == 0 {
		t.Fatalf("testutil.CreateMember: BranchID required")
	}
	if o.Name == "" {
		o.Name = uniqueName("member")
	}
	if o.Phone == "" {
		o.Phone = uniquePhone()
	}
	if o.BirthDate == "" {
		o.BirthDate = "1990-01-01"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx,
		`insert into members (branch_id, name, phone, birth_date)
		 values ($1, $2, $3, $4) returning id`,
		o.BranchID, o.Name, o.Phone, o.BirthDate,
	).Scan(&id)
	if err != nil {
		t.Fatalf("testutil.CreateMember: %v", err)
	}
	return id
}

// CreateMembership inserts a row in memberships and returns its id. Defaults
// (when fields are zero) target a 1-month monthly membership starting today
// in KST so the row passes CHECK constraints out of the box.
func CreateMembership(t *testing.T, pool *pgxpool.Pool, o *MembershipOpts) int64 {
	t.Helper()
	if o == nil || o.MemberID == 0 {
		t.Fatalf("testutil.CreateMembership: MemberID required")
	}
	if o.Type == "" {
		o.Type = "monthly"
	}
	if o.Status == "" {
		o.Status = "active"
	}
	if o.StartDate == "" {
		o.StartDate = time.Now().UTC().Format("2006-01-02")
	}
	if o.EndDate == "" {
		o.EndDate = time.Now().AddDate(0, 1, 0).UTC().Format("2006-01-02")
	}

	switch o.Type {
	case "monthly":
		if o.Months == nil {
			one := 1
			o.Months = &one
		}
		o.Remaining = nil
	case "pass10":
		if o.Remaining == nil {
			ten := 10
			o.Remaining = &ten
		}
		o.Months = nil
	default:
		t.Fatalf("testutil.CreateMembership: unsupported Type %q", o.Type)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var id int64
	err := pool.QueryRow(ctx,
		`insert into memberships (member_id, type, months, remaining, start_date, end_date, status)
		 values ($1, $2, $3, $4, $5, $6, $7)
		 returning id`,
		o.MemberID, o.Type, o.Months, o.Remaining, o.StartDate, o.EndDate, o.Status,
	).Scan(&id)
	if err != nil {
		t.Fatalf("testutil.CreateMembership: %v", err)
	}
	return id
}

// uniqueName returns a per-call unique label safe for short-text columns.
// Time-based with a sub-second component prevents collisions across rapid
// successive calls inside the same test.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

var phoneCounter = newCounter(100000)

// uniquePhone returns a synthetic 11-digit phone that satisfies the CHECK
// constraint and avoids collisions across calls.
func uniquePhone() string {
	n := phoneCounter.next()
	return fmt.Sprintf("010%08d", n%100_000_000)
}
