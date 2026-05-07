package auth_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/auth"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := auth.HashPassword("Owner123Pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "" || hash == "Owner123Pass" {
		t.Fatalf("hash must be non-empty and not the plaintext")
	}
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") && !strings.HasPrefix(hash, "$2y$") {
		t.Fatalf("hash should be bcrypt: %q", hash)
	}
	if err := auth.VerifyPassword(hash, "Owner123Pass"); err != nil {
		t.Errorf("VerifyPassword should succeed: %v", err)
	}
	if err := auth.VerifyPassword(hash, "wrong"); err == nil {
		t.Errorf("VerifyPassword should fail on wrong password")
	}
}

func TestValidateStrength(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"valid-min", "a1234567", true},
		{"valid-mixed", "Password1", true},
		{"too-short", "a123456", false},
		{"only-letters", "aaaaaaaa", false},
		{"only-digits", "12345678", false},
		{"empty", "", false},
		{"unicode-letters-only", "한국어비번한", false}, // not ASCII letters, no digits
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := auth.ValidateStrength(c.pw)
			if c.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !c.ok {
				if !apperr.IsCode(err, "WEAK_PASSWORD") {
					t.Fatalf("expected WEAK_PASSWORD, got %v", err)
				}
			}
		})
	}
}

func TestGenerateTempPassword(t *testing.T) {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
	for i := 0; i < 10000; i++ {
		pw, err := auth.GenerateTempPassword(nil)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(pw) != 12 {
			t.Fatalf("iter %d: length=%d want 12", i, len(pw))
		}
		for _, r := range pw {
			if !strings.ContainsRune(charset, r) {
				t.Fatalf("iter %d: rune %q not in charset", i, r)
			}
		}
	}
}

func TestGenerateTempPasswordReaderError(t *testing.T) {
	// Empty reader → ErrUnexpectedEOF surfaces as a non-nil error.
	if _, err := auth.GenerateTempPassword(bytes.NewReader(nil)); err == nil {
		t.Fatal("expected error from empty reader")
	}
}
