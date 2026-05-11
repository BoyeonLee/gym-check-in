// Package util provides cross-cutting helpers — most notably the Clock and
// UUIDGen interfaces used to keep domain/handler code deterministic in tests.
//
// Production code MUST resolve time.Now / uuid.New through these interfaces
// (injected at composition root) so that FreezeTime / fake UUIDs in tests
// are honored.
package util

import (
	"time"

	"github.com/google/uuid"
)

// Clock returns the current time. Production: SystemClock{}. Tests: FakeClock.
type Clock interface {
	Now() time.Time
}

// UUIDGen returns a fresh UUID string. Production: SystemUUIDGen{}.
// Tests: FakeUUIDGen with a predetermined sequence.
type UUIDGen interface {
	NewV4() string
}

// SystemClock returns the wall-clock time.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// SystemUUIDGen wraps github.com/google/uuid.
type SystemUUIDGen struct{}

func (SystemUUIDGen) NewV4() string { return uuid.NewString() }

// FakeClock returns a fixed instant. Use FreezeTime in testutil to install one.
type FakeClock struct {
	Instant time.Time
}

func (f *FakeClock) Now() time.Time { return f.Instant }

// FakeUUIDGen returns predetermined values in order. After the slice is
// exhausted, the last value is returned repeatedly. Tests asserting on
// specific UUIDs should size the slice to match the call count.
type FakeUUIDGen struct {
	Values []string
	idx    int
}

func (f *FakeUUIDGen) NewV4() string {
	if len(f.Values) == 0 {
		return "00000000-0000-0000-0000-000000000000"
	}
	if f.idx >= len(f.Values) {
		return f.Values[len(f.Values)-1]
	}
	v := f.Values[f.idx]
	f.idx++
	return v
}

// KST is the Asia/Seoul location, loaded once. Handlers serializing
// timestamptz responses MUST convert to this location so the wire format
// reads "2026-04-27T18:23:00+09:00" rather than UTC "Z".
var KST = mustLoadKST()

func mustLoadKST() *time.Location {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		// Fall back to a fixed offset if the system tz database is absent.
		return time.FixedZone("KST", 9*60*60)
	}
	return loc
}
