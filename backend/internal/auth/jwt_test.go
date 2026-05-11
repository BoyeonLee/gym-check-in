package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/auth"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// newIssuer wires an Issuer with deterministic clock + UUID generator so
// every test produces reproducible signatures.
func newIssuer(t *testing.T, instant time.Time, jtis ...string) *auth.Issuer {
	t.Helper()
	return &auth.Issuer{
		AccessSecret:  []byte("test-access-secret-32bytes-or-more!!"),
		RefreshSecret: []byte("test-refresh-secret-32bytes-or-more!"),
		Clock:         &util.FakeClock{Instant: instant},
		UUIDGen:       &util.FakeUUIDGen{Values: jtis},
	}
}

func TestIssueParseAccessRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now)
	bid := int64(7)

	tok, err := i.IssueAccess(auth.AccessClaims{
		Sub:                42,
		Username:           "owner",
		Role:               "branch",
		BranchID:           &bid,
		MustChangePassword: true,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if strings.Count(tok, ".") != 2 {
		t.Fatalf("token must have 3 segments: %q", tok)
	}

	got, err := i.ParseAccess(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Sub != 42 || got.Username != "owner" || got.Role != "branch" {
		t.Errorf("identity drift: %+v", got)
	}
	if got.BranchID == nil || *got.BranchID != 7 {
		t.Errorf("branch_id drift: %+v", got.BranchID)
	}
	if !got.MustChangePassword {
		t.Errorf("must_change_password drift")
	}
	if got.Iat != now.Unix() || got.Exp != now.Unix()+int64(auth.AccessTTL.Seconds()) {
		t.Errorf("iat/exp drift: iat=%d exp=%d", got.Iat, got.Exp)
	}
}

func TestParseAccessRejectsExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now)
	tok, err := i.IssueAccess(auth.AccessClaims{Sub: 1, Role: "global"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Advance clock past expiry.
	i.Clock = &util.FakeClock{Instant: now.Add(auth.AccessTTL + time.Second)}
	if _, err := i.ParseAccess(tok); !apperr.IsCode(err, "UNAUTHORIZED") {
		t.Fatalf("expected UNAUTHORIZED, got %v", err)
	}
}

func TestParseAccessRejectsBadSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now)
	tok, _ := i.IssueAccess(auth.AccessClaims{Sub: 1, Role: "global"})

	// Flip the last character of the signature segment.
	tampered := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	if _, err := i.ParseAccess(tampered); !apperr.IsCode(err, "UNAUTHORIZED") {
		t.Fatalf("expected UNAUTHORIZED on tampered sig, got %v", err)
	}
}

func TestParseAccessRejectsAlgNone(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now)

	// Forge a token with alg=none — must be rejected.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(auth.AccessClaims{
		Sub: 1, Role: "global",
		Iat: now.Unix(), Exp: now.Unix() + 1800,
	})
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	forged := header + "." + payload + "." // empty signature
	if _, err := i.ParseAccess(forged); !apperr.IsCode(err, "UNAUTHORIZED") {
		t.Fatalf("expected UNAUTHORIZED for alg=none, got %v", err)
	}
}

func TestParseAccessRejectsMissingClaims(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now)

	cases := map[string]map[string]any{
		"missing-sub":  {"role": "global", "iat": now.Unix(), "exp": now.Unix() + 1800},
		"missing-role": {"sub": 1, "iat": now.Unix(), "exp": now.Unix() + 1800},
		"missing-iat":  {"sub": 1, "role": "global", "exp": now.Unix() + 1800},
		"missing-exp":  {"sub": 1, "role": "global", "iat": now.Unix()},
		"bad-role":     {"sub": 1, "role": "owner", "iat": now.Unix(), "exp": now.Unix() + 1800},
	}
	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			tok := signWithSecret(t, i.AccessSecret, claims)
			if _, err := i.ParseAccess(tok); !apperr.IsCode(err, "UNAUTHORIZED") {
				t.Fatalf("expected UNAUTHORIZED, got %v", err)
			}
		})
	}
}

func TestParseAccessRejectsMalformedToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now)

	for _, tok := range []string{"", "abc", "a.b", "a.b.c.d"} {
		if _, err := i.ParseAccess(tok); !apperr.IsCode(err, "UNAUTHORIZED") {
			t.Fatalf("expected UNAUTHORIZED for %q, got %v", tok, err)
		}
	}
}

func TestIssueParseRefreshRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now, "11111111-1111-4111-8111-111111111111")

	tok, jti, err := i.IssueRefresh(99)
	if err != nil {
		t.Fatalf("issue refresh: %v", err)
	}
	if jti != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("jti not from generator: %q", jti)
	}
	got, err := i.ParseRefresh(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Sub != 99 || got.Jti != jti {
		t.Errorf("identity drift: %+v", got)
	}
	if got.Iat != now.Unix() || got.Exp != now.Unix()+int64(auth.RefreshTTL.Seconds()) {
		t.Errorf("iat/exp drift: iat=%d exp=%d", got.Iat, got.Exp)
	}
}

func TestParseRefreshRejectsExpiredAndCrossSecret(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	i := newIssuer(t, now, "11111111-1111-4111-8111-111111111111")
	tok, _, _ := i.IssueRefresh(1)

	// Expired
	i.Clock = &util.FakeClock{Instant: now.Add(auth.RefreshTTL + time.Second)}
	if _, err := i.ParseRefresh(tok); !apperr.IsCode(err, "UNAUTHORIZED") {
		t.Fatalf("expected UNAUTHORIZED for expired refresh, got %v", err)
	}

	// Reset clock and verify access secret cannot validate refresh (and vice versa).
	i.Clock = &util.FakeClock{Instant: now}
	access, _ := i.IssueAccess(auth.AccessClaims{Sub: 1, Role: "global"})
	if _, err := i.ParseRefresh(access); !apperr.IsCode(err, "UNAUTHORIZED") {
		t.Fatalf("expected UNAUTHORIZED when access token used as refresh, got %v", err)
	}
	if _, err := i.ParseAccess(tok); !apperr.IsCode(err, "UNAUTHORIZED") {
		t.Fatalf("expected UNAUTHORIZED when refresh token used as access, got %v", err)
	}
}

// signWithSecret builds a HS256 token from a raw claim map. Used by tests
// that need to construct invalid tokens (missing claims, bad role) without
// going through the production marshalling path.
func signWithSecret(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(pBytes)
	signing := header + "." + payload
	return signing + "." + hmacSHA256B64(t, secret, signing)
}

func hmacSHA256B64(t *testing.T, secret []byte, signing string) string {
	t.Helper()
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
