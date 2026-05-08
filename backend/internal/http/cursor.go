// cursor.go owns the opaque cursor used by every cursor-paginated list
// endpoint (members, check-ins). Keeping the codec in one place means the
// catalog of decoding errors maps to a single 400 INVALID_CURSOR everywhere
// and tests don't drift between handlers.
package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
)

// DefaultListLimit / MaxListLimit gate every list endpoint. Constants live
// here so members/check-ins/etc. share a single tunable surface.
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

// Cursor is the JSON payload carried inside the opaque base64 string. The
// (timestamp, id) tuple is keyset-paginated against `created_at DESC, id DESC`
// (or `checked_in_at` for check-ins) so a row produced after the cursor was
// minted never silently sneaks into a later page.
type Cursor struct {
	T  time.Time `json:"t"`
	ID int64     `json:"id"`
}

// EncodeCursor produces a compact, URL-safe opaque string. We always emit the
// timestamp in UTC RFC3339Nano so two callers minting the same cursor on
// different shells get identical strings.
func EncodeCursor(c Cursor) string {
	payload := map[string]any{
		"t":  c.T.UTC().Format(time.RFC3339Nano),
		"id": c.ID,
	}
	raw, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses an opaque cursor back into its components. Any failure
// path — malformed base64, malformed JSON, missing fields, wrong types —
// surfaces as 400 INVALID_CURSOR so handlers don't have to translate.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		// RawURLEncoding rejects '=' padding; accept padded base64 too so
		// older clients that stored the cursor with stdlib encoding still
		// work.
		raw, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return Cursor{}, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
		}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Cursor{}, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
	}
	tRaw, hasT := payload["t"]
	idRaw, hasID := payload["id"]
	if !hasT || !hasID {
		return Cursor{}, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
	}
	var ts string
	if err := json.Unmarshal(tRaw, &ts); err != nil {
		return Cursor{}, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Some clients may store RFC3339 without the nano portion; try both.
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return Cursor{}, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
		}
	}
	var id int64
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return Cursor{}, apperr.New(http.StatusBadRequest, "INVALID_CURSOR", "invalid cursor")
	}
	return Cursor{T: t.UTC(), ID: id}, nil
}

// ParseLimit normalises the ?limit= query argument. Empty string falls back
// to def; out-of-range / non-integer surfaces as 400 INVALID_LIMIT.
func ParseLimit(qs string, def, max int) (int, error) {
	if qs == "" {
		return def, nil
	}
	n, err := strconv.Atoi(qs)
	if err != nil {
		return 0, apperr.New(http.StatusBadRequest, "INVALID_LIMIT", "invalid limit")
	}
	if n <= 0 || n > max {
		return 0, apperr.New(http.StatusBadRequest, "INVALID_LIMIT", "invalid limit")
	}
	return n, nil
}
