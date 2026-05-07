// Package auth owns admin authentication primitives — JWT issuance and
// verification (HS256 only) plus password hashing and validation.
//
// JWT shape is intentionally minimal: an HS256 header, a JSON payload of the
// claim struct, and a base64url(HMAC) signature. This avoids a third-party
// JWT dependency (per ADR-001's "single-bin Go" stance) while keeping every
// rule from backend/CLAUDE.md enforceable: no alg=none, no RS256, claim
// integrity verified, expiry compared via injected Clock.
//
// All callers MUST go through Issuer methods — never sign or verify by hand.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// AccessTTL / RefreshTTL match backend/CLAUDE.md's session policy.
// Tests exercising expiry should drive the Clock rather than redefine these.
const (
	AccessTTL  = 30 * time.Minute
	RefreshTTL = 15 * time.Hour
)

// Fixed HS256 header. Marshalling once and embedding the bytes saves a
// JSON encode per token and prevents accidental drift in the canonical form.
const headerHS256JSON = `{"alg":"HS256","typ":"JWT"}`

// b64 is RFC 7515-compliant URL-safe base64 without padding.
var b64 = base64.RawURLEncoding

// AccessClaims carries everything the route guards need without re-querying
// the DB on each request. The DB is consulted only by the Auth middleware
// for revocation (deleted_at, password_updated_at) — see middleware/auth.go.
type AccessClaims struct {
	Sub                int64  `json:"sub"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	BranchID           *int64 `json:"branch_id,omitempty"`
	MustChangePassword bool   `json:"must_change_password"`
	Iat                int64  `json:"iat"`
	Exp                int64  `json:"exp"`
}

// RefreshClaims is deliberately small — refresh tokens never grant scope
// directly; they only authorize issuing a fresh access token whose claim
// values come from the live admin row.
type RefreshClaims struct {
	Sub int64  `json:"sub"`
	Jti string `json:"jti"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// Issuer mints and verifies tokens. Both secrets must be provided —
// production wiring reads them from JWT_ACCESS_SECRET / JWT_REFRESH_SECRET.
type Issuer struct {
	AccessSecret  []byte
	RefreshSecret []byte
	Clock         util.Clock
	UUIDGen       util.UUIDGen
}

func (i *Issuer) clock() util.Clock {
	if i.Clock != nil {
		return i.Clock
	}
	return util.SystemClock{}
}

func (i *Issuer) uuidGen() util.UUIDGen {
	if i.UUIDGen != nil {
		return i.UUIDGen
	}
	return util.SystemUUIDGen{}
}

// IssueAccess returns a signed access token. Iat/Exp are auto-populated from
// the injected Clock; callers may supply them only in tests that need
// hand-crafted timestamps.
func (i *Issuer) IssueAccess(c AccessClaims) (string, error) {
	now := i.clock().Now().Unix()
	if c.Iat == 0 {
		c.Iat = now
	}
	if c.Exp == 0 {
		c.Exp = c.Iat + int64(AccessTTL.Seconds())
	}
	return signHS256(i.AccessSecret, c)
}

// IssueRefresh returns (token, jti) — the jti is also the key callers use
// when persisting revocations to revoked_refresh_tokens.
func (i *Issuer) IssueRefresh(adminID int64) (string, string, error) {
	now := i.clock().Now().Unix()
	jti := i.uuidGen().NewV4()
	c := RefreshClaims{
		Sub: adminID,
		Jti: jti,
		Iat: now,
		Exp: now + int64(RefreshTTL.Seconds()),
	}
	tok, err := signHS256(i.RefreshSecret, c)
	if err != nil {
		return "", "", err
	}
	return tok, jti, nil
}

// ParseAccess verifies signature, decodes claims, enforces required fields,
// and rejects expired tokens. All failure modes collapse to a generic
// 401 UNAUTHORIZED — the failure reason stays in the wrapped cause for
// developer logs but never bleeds into the client response.
func (i *Issuer) ParseAccess(token string) (*AccessClaims, error) {
	payload, err := verifyHS256(token, i.AccessSecret)
	if err != nil {
		return nil, err
	}
	var c AccessClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, unauth(err)
	}
	if c.Sub == 0 || c.Role == "" || c.Iat == 0 || c.Exp == 0 {
		return nil, unauth(nil)
	}
	if c.Role != "global" && c.Role != "branch" {
		return nil, unauth(nil)
	}
	if i.clock().Now().Unix() >= c.Exp {
		return nil, unauth(nil)
	}
	return &c, nil
}

// ParseRefresh mirrors ParseAccess for refresh tokens. The result is used
// by /api/admin/refresh and /api/admin/logout.
func (i *Issuer) ParseRefresh(token string) (*RefreshClaims, error) {
	payload, err := verifyHS256(token, i.RefreshSecret)
	if err != nil {
		return nil, err
	}
	var c RefreshClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, unauth(err)
	}
	if c.Sub == 0 || c.Jti == "" || c.Iat == 0 || c.Exp == 0 {
		return nil, unauth(nil)
	}
	if i.clock().Now().Unix() >= c.Exp {
		return nil, unauth(nil)
	}
	return &c, nil
}

// signHS256 builds a compact JWS string for the given claim payload.
// We never sign with a non-HS256 algorithm and never accept a caller-supplied
// header — the format is fully owned by this package.
func signHS256(secret []byte, claims any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	headerSeg := b64.EncodeToString([]byte(headerHS256JSON))
	payloadSeg := b64.EncodeToString(payload)
	signing := headerSeg + "." + payloadSeg
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	sigSeg := b64.EncodeToString(mac.Sum(nil))
	return signing + "." + sigSeg, nil
}

// verifyHS256 enforces alg=HS256 and constant-time signature comparison,
// returning the raw payload bytes for the caller to JSON-decode.
func verifyHS256(token string, secret []byte) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, unauth(nil)
	}
	headerRaw, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, unauth(err)
	}
	var h struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerRaw, &h); err != nil {
		return nil, unauth(err)
	}
	// Defence against alg=none / RS256 substitution attacks: only HS256.
	if h.Alg != "HS256" {
		return nil, unauth(nil)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	got, err := b64.DecodeString(parts[2])
	if err != nil {
		return nil, unauth(err)
	}
	if !hmac.Equal(expected, got) {
		return nil, unauth(nil)
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, unauth(err)
	}
	return payload, nil
}

// unauth returns the canonical 401 error. The wrapped cause is preserved for
// debugging via errors.Is/As but the response message stays generic.
func unauth(cause error) *apperr.AppError {
	if cause == nil {
		return apperr.New(http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
	}
	return apperr.Wrap(http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", cause)
}
