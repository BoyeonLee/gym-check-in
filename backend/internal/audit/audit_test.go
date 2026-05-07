//go:build integration

package audit_test

import (
	"context"
	"testing"

	"github.com/lboyeon1223/gym-check-in/backend/internal/audit"
	"github.com/lboyeon1223/gym-check-in/backend/internal/testutil"
)

func ptrInt64(v int64) *int64 { return &v }
func ptrStr(s string) *string  { return &s }

func TestAudit_Log_InsertsRow(t *testing.T) {
	pool := testutil.SetupDB(t)
	branchID := testutil.CreateBranch(t, pool, nil)
	adminID, _ := testutil.CreateAdmin(t, pool, nil)

	err := audit.Log(context.Background(), pool, audit.Entry{
		AdminID:    &adminID,
		Action:     audit.LoginSuccess,
		TargetType: ptrStr("admin"),
		TargetID:   &adminID,
		IP:         "127.0.0.1",
		UserAgent:  "test-agent/1.0",
		Metadata:   map[string]any{"request_id": "req-1", "branch_id": branchID},
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	var (
		gotAdminID    int64
		gotAction     string
		gotTargetType string
		gotTargetID   int64
		gotUA         string
		gotMetaRaw    []byte
	)
	if err := pool.QueryRow(context.Background(),
		`select admin_id, action, target_type, target_id, user_agent, metadata::text
		 from admin_audit_logs order by id desc limit 1`,
	).Scan(&gotAdminID, &gotAction, &gotTargetType, &gotTargetID, &gotUA, &gotMetaRaw); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotAdminID != adminID || gotAction != "login_success" ||
		gotTargetType != "admin" || gotTargetID != adminID ||
		gotUA != "test-agent/1.0" {
		t.Fatalf("row mismatch: admin=%d action=%s target_type=%s target_id=%d ua=%s",
			gotAdminID, gotAction, gotTargetType, gotTargetID, gotUA)
	}
	if len(gotMetaRaw) == 0 {
		t.Fatalf("metadata jsonb should not be empty")
	}
}

func TestAudit_Log_NullableFields(t *testing.T) {
	pool := testutil.SetupDB(t)

	// Login failure: admin_id may be NULL when the username doesn't match.
	if err := audit.Log(context.Background(), pool, audit.Entry{
		AdminID:   nil,
		Action:    audit.LoginFailure,
		IP:        "", // empty IP must convert to NULL inet (not "" which fails parse)
		UserAgent: "",
		Metadata:  map[string]any{"request_id": "req-2", "username": "ghost"},
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	var (
		adminID    *int64
		ip         *string
		targetType *string
		targetID   *int64
	)
	if err := pool.QueryRow(context.Background(),
		`select admin_id, host(ip)::text, target_type, target_id
		 from admin_audit_logs order by id desc limit 1`,
	).Scan(&adminID, &ip, &targetType, &targetID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if adminID != nil {
		t.Fatalf("admin_id should be NULL, got %v", *adminID)
	}
	if ip != nil {
		t.Fatalf("ip should be NULL when caller passed empty string, got %q", *ip)
	}
	if targetType != nil {
		t.Fatalf("target_type should be NULL")
	}
	if targetID != nil {
		t.Fatalf("target_id should be NULL")
	}
}

func TestAudit_Log_InvalidIPDoesNotPropagateButLogs(t *testing.T) {
	// A malformed ip string must not crash the caller — Log must gracefully
	// nil out the field rather than failing the whole audit insert.
	pool := testutil.SetupDB(t)

	if err := audit.Log(context.Background(), pool, audit.Entry{
		Action:   audit.Logout,
		IP:       "not-an-ip",
		Metadata: map[string]any{"request_id": "req-3"},
	}); err != nil {
		t.Fatalf("Log returned error for bad IP: %v", err)
	}

	var ip *string
	if err := pool.QueryRow(context.Background(),
		`select host(ip)::text from admin_audit_logs order by id desc limit 1`,
	).Scan(&ip); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if ip != nil {
		t.Fatalf("malformed IP should resolve to NULL, got %q", *ip)
	}
}
