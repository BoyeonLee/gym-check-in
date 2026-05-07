package auth

import (
	"crypto/rand"
	"errors"
	"io"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
)

// PasswordCost matches db/CLAUDE.md's "bcrypt cost 12" recommendation. The
// hashpw CLI used for seeding admin passwords uses the same constant so DB
// hashes and runtime verification always agree on cost.
const PasswordCost = 12

// TempPasswordCharset excludes visually ambiguous glyphs (0/O/I/l/1/o/i)
// so a coach reading a temporary password aloud isn't tripped up by font
// rendering on a tablet. Length 55 (24 + 23 + 8).
const TempPasswordCharset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

// TempPasswordLength is the number of charset symbols emitted by
// GenerateTempPassword. 12 yields ~71 bits of entropy on a 55-symbol alphabet
// — well above the brute-force threshold for a 24h-valid credential.
const TempPasswordLength = 12

// HashPassword applies bcrypt at our standard cost. The returned string is
// safe to store in admins.password_hash directly.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), PasswordCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword returns nil when plain matches hash. Mismatch returns a
// non-nil error (caller may translate to apperr 401 — the choice depends on
// the route, so we don't pre-wrap here).
func VerifyPassword(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}

// ValidateStrength enforces backend/CLAUDE.md's policy: min 8 chars, at least
// one ASCII letter, at least one ASCII digit. Special characters are NOT
// required (tablet/mobile entry burden — see CLAUDE.md).
func ValidateStrength(plain string) error {
	if len(plain) < 8 {
		return weakPassword()
	}
	hasLetter := false
	hasDigit := false
	for i := 0; i < len(plain); i++ {
		b := plain[i]
		switch {
		case (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z'):
			hasLetter = true
		case b >= '0' && b <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return weakPassword()
	}
	return nil
}

func weakPassword() *apperr.AppError {
	return apperr.New(http.StatusBadRequest, "WEAK_PASSWORD",
		"password must be at least 8 characters and contain a letter and a digit")
}

// GenerateTempPassword returns a fresh 12-character password drawn uniformly
// from TempPasswordCharset. The rng argument is exposed so tests can supply a
// deterministic byte stream; in production callers pass nil to fall back on
// crypto/rand.Reader.
//
// Rejection sampling discards bytes whose modulo would bias the alphabet.
// 256 / 55 = 4 with 36 left over, so we accept bytes < 220 (= 4*55) and
// reject the upper 36 — guaranteeing uniform distribution over 55 symbols.
func GenerateTempPassword(rng io.Reader) (string, error) {
	if rng == nil {
		rng = rand.Reader
	}
	out := make([]byte, TempPasswordLength)
	const accept = 220 // 4 * 55
	buf := make([]byte, 1)
	for i := 0; i < TempPasswordLength; i++ {
		for {
			n, err := io.ReadFull(rng, buf)
			if err != nil {
				return "", errors.Join(err)
			}
			if n != 1 {
				return "", io.ErrUnexpectedEOF
			}
			if buf[0] < accept {
				out[i] = TempPasswordCharset[int(buf[0])%len(TempPasswordCharset)]
				break
			}
		}
	}
	return string(out), nil
}
