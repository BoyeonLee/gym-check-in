package idempotency_test

import (
	"strings"
	"testing"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/idempotency"
)

// TestValidateKey covers the UUIDv4 regex contract: the canonical example
// passes, while empty / wrong-version / wrong-length / random-string inputs
// are rejected with apperr.AppError code "INVALID_IDEMPOTENCY_KEY".
func TestValidateKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid v4", "11111111-1111-4111-8111-111111111111", true},
		{"valid v4 mixed case", "AaBbCcDd-1234-4ABC-8def-aaaabbbbcccc", true},
		{"empty", "", false},
		{"random string", "not-a-uuid", false},
		{"v1", "f47ac10b-58cc-1372-a567-0e02b2c3d479", false},
		{"missing dashes", "11111111111141118111111111111111", false},
		{"too short", "11111111-1111-4111-8111-11111111111", false},
		{"trailing junk", "11111111-1111-4111-8111-111111111111x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := idempotency.ValidateKey(tc.in)
			if tc.ok {
				if err != nil {
					t.Errorf("expected ok, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q", tc.in)
			}
			if !apperr.IsCode(err, "INVALID_IDEMPOTENCY_KEY") {
				t.Errorf("expected INVALID_IDEMPOTENCY_KEY, got %v", err)
			}
		})
	}
}

// TestHashRequest_CanonicalEqualsKeyOrderInvariant — JSON objects with the
// same logical content must produce the same hash even if the keys arrive in
// a different order or with extra whitespace. This prevents a perfectly fine
// retry from being mis-flagged as IDEMPOTENCY_KEY_CONFLICT.
func TestHashRequest_CanonicalEqualsKeyOrderInvariant(t *testing.T) {
	a := []byte(`{"a":1,"b":2}`)
	b := []byte(`{"b":2,"a":1}`)
	c := []byte(`{ "b": 2,   "a": 1 }`)

	ha, err := idempotency.HashRequest(a)
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	hb, err := idempotency.HashRequest(b)
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	hc, err := idempotency.HashRequest(c)
	if err != nil {
		t.Fatalf("hash c: %v", err)
	}
	if ha != hb || ha != hc {
		t.Errorf("canonical hash drift: a=%s b=%s c=%s", ha, hb, hc)
	}
	if ha == "" {
		t.Error("hash returned empty")
	}
	// Different content must hash differently.
	hd, err := idempotency.HashRequest([]byte(`{"a":2,"b":1}`))
	if err != nil {
		t.Fatalf("hash d: %v", err)
	}
	if ha == hd {
		t.Errorf("expected different hashes for different content: %s vs %s", ha, hd)
	}
}

// TestHashRequest_NestedAndArrays — nested objects keep their key-order
// canonicalisation; arrays preserve element order (intentional — order is
// semantic for arrays).
func TestHashRequest_NestedAndArrays(t *testing.T) {
	x := []byte(`{"outer":{"a":1,"b":[3,2,1]}}`)
	y := []byte(`{"outer":{"b":[3,2,1],"a":1}}`)

	hx, _ := idempotency.HashRequest(x)
	hy, _ := idempotency.HashRequest(y)
	if hx != hy {
		t.Errorf("nested key-order canonicalisation broken: %s vs %s", hx, hy)
	}

	z := []byte(`{"outer":{"a":1,"b":[1,2,3]}}`)
	hz, _ := idempotency.HashRequest(z)
	if hx == hz {
		t.Errorf("array element order should be preserved (semantic), got equal: %s", hx)
	}
}

// TestHashRequest_NonJSONFallback — a request body that isn't valid JSON is
// hashed verbatim so the hash still compares deterministically. This is a
// safety net; valid JSON callers never hit the fallback.
func TestHashRequest_NonJSONFallback(t *testing.T) {
	b := []byte("not json")
	h1, err := idempotency.HashRequest(b)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h2, err := idempotency.HashRequest(b)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if h1 != h2 || h1 == "" {
		t.Errorf("non-JSON hashing not deterministic: %q %q", h1, h2)
	}
}

// TestValidateKey_ErrorContainsCode_NotMessage — the validator's error must
// surface the machine code, not a free-form string the handler might
// accidentally render as the user-facing message.
func TestValidateKey_ErrorContainsCode_NotMessage(t *testing.T) {
	err := idempotency.ValidateKey("nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "INVALID_IDEMPOTENCY_KEY") {
		t.Errorf("error string missing code: %v", err)
	}
}
