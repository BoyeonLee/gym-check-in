// Package idempotency provides the request-side of the Idempotency-Key
// contract shared by every "risky" mutating endpoint (회원권 부여, 환불,
// bulk-extend). It owns three concerns:
//
//  1. Key shape validation — the header must be a UUIDv4 (rejects anything
//     else with apperr code INVALID_IDEMPOTENCY_KEY).
//  2. Request hashing — the body's canonical JSON form (keys sorted,
//     whitespace stripped) hashed with SHA-256 so a logically-equal body
//     produces the same hash regardless of whitespace or key ordering.
//  3. Lookup / Store — orchestrates the persisted row in idempotency_keys
//     by delegating SQL to internal/repo. Lookup returns the stored response
//     when a previous request finished successfully; mismatched body for the
//     same key surfaces 409 IDEMPOTENCY_KEY_CONFLICT.
//
// The package is the single source of truth for how the Idempotency-Key
// header is interpreted; handlers compose ValidateKey + HashRequest + Lookup
// + Store rather than rolling their own.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/apperr"
	"github.com/lboyeon1223/gym-check-in/backend/internal/repo"
)

// uuidV4Regex matches the canonical UUIDv4 form: 8-4-4-4-12 hex with version
// nibble == 4 and variant nibble in {8,9,a,b}. Mixed case is accepted so a
// caller that uppercases the header for readability still passes.
var uuidV4Regex = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`,
)

// ValidateKey enforces the UUIDv4 shape of the Idempotency-Key header. An
// empty string is treated as "missing" by the caller (which surfaces 400
// IDEMPOTENCY_KEY_REQUIRED) — this helper only catches malformed values, so
// a non-UUID returns INVALID_IDEMPOTENCY_KEY.
func ValidateKey(key string) error {
	if !uuidV4Regex.MatchString(key) {
		return apperr.New(http.StatusBadRequest,
			"INVALID_IDEMPOTENCY_KEY",
			"Idempotency-Key must be a UUIDv4")
	}
	return nil
}

// Result carries the outcome of a Lookup. Found=true means a matching
// (key, hash) row already exists and Status / Body should be replayed
// verbatim. Found=false means the caller must execute the actual work and
// then call Store.
type Result struct {
	Found  bool
	Status int
	Body   []byte
}

// Lookup checks idempotency_keys for a row matching key and (24h) freshness.
//
// Outcomes:
//   - row absent or older than 24h → Result{Found:false}, nil
//   - row present + body hash matches → Result{Found:true, Status, Body}, nil
//   - row present + body hash differs → 409 IDEMPOTENCY_KEY_CONFLICT
//
// The (admin_id, endpoint) tuple isn't used to demote a hit to a miss — it's
// validated as part of the conflict check below so a misuse (same key reused
// for a different endpoint or admin) surfaces as a conflict rather than
// silently re-running the work. This matches docs/API.md's contract that the
// key plus body together identify the operation.
func Lookup(ctx context.Context, q repo.Querier, key, endpoint string,
	adminID int64, requestHash string, now time.Time,
) (Result, error) {
	row, err := repo.FindIdempotencyKey(ctx, q, key, now)
	if err != nil {
		return Result{}, apperr.Wrap(http.StatusInternalServerError,
			apperr.CodeInternal, "internal server error", err)
	}
	if row == nil {
		return Result{Found: false}, nil
	}
	if row.AdminID != adminID || row.Endpoint != endpoint || row.RequestHash != requestHash {
		return Result{}, apperr.New(http.StatusConflict,
			"IDEMPOTENCY_KEY_CONFLICT",
			"Idempotency-Key reused with a different request body or scope")
	}
	return Result{Found: true, Status: row.ResponseStatus, Body: row.ResponseBody}, nil
}

// Store persists the final (status, body) tuple for the given key. Failure
// here is non-fatal to the response — the caller logs and returns the
// computed body anyway. The trade-off (a possible re-execution if the same
// key is replayed before storage succeeds) is acceptable for MVP scale.
func Store(ctx context.Context, q repo.Querier, key, endpoint string,
	adminID int64, requestHash string, status int, body []byte,
) error {
	return repo.InsertIdempotencyKey(ctx, q, repo.IdempotencyRow{
		Key:            key,
		AdminID:        adminID,
		Endpoint:       endpoint,
		RequestHash:    requestHash,
		ResponseStatus: status,
		ResponseBody:   body,
	})
}

// HashRequest returns a SHA-256 hex digest of the canonical form of body.
// Canonicalisation: parse as JSON, recursively sort object keys, re-emit
// without whitespace. Arrays preserve their order (semantic). Non-JSON input
// is hashed verbatim so the helper never fails on unexpected payloads.
func HashRequest(body []byte) (string, error) {
	canon, err := canonicaliseJSON(body)
	if err != nil {
		// Non-JSON or marshal failure → hash raw bytes so the helper still
		// produces a deterministic value.
		canon = body
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// canonicaliseJSON parses body, sorts every object's keys recursively, and
// re-emits the result with the standard library's compact JSON form (no
// whitespace).
func canonicaliseJSON(body []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	c := sortKeys(v)
	out, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// sortKeys walks a parsed JSON value and returns a structurally equivalent
// value with every map[string]any replaced by a key-sorted equivalent the
// stdlib encoder will emit deterministically.
func sortKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(orderedMap, 0, len(keys))
		for _, k := range keys {
			out = append(out, kv{K: k, V: sortKeys(x[k])})
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sortKeys(e)
		}
		return out
	default:
		return v
	}
}

// orderedMap is an []kv that marshals as a JSON object preserving insertion
// order. Combined with sorted keys above, the output is deterministic.
type orderedMap []kv

type kv struct {
	K string
	V any
}

// MarshalJSON emits an object whose keys appear in slice order.
func (o orderedMap) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, e := range o {
		if i > 0 {
			buf = append(buf, ',')
		}
		k, err := json.Marshal(e.K)
		if err != nil {
			return nil, err
		}
		buf = append(buf, k...)
		buf = append(buf, ':')
		v, err := json.Marshal(e.V)
		if err != nil {
			return nil, err
		}
		buf = append(buf, v...)
	}
	buf = append(buf, '}')
	return buf, nil
}
