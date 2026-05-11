package testutil

import (
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// Clock and UUIDGen aliases re-export the production interfaces so test
// files can depend on this single package.
type (
	Clock   = util.Clock
	UUIDGen = util.UUIDGen
)

// FreezeTime installs a process-global FakeClock pointing at `instant`.
// The returned restore function is also registered with t.Cleanup so the
// previous clock is restored automatically at test end. Callers wishing
// to advance the fake clock mid-test should hold a *util.FakeClock
// directly via NewFakeClock.
func FreezeTime(t *testing.T, instant time.Time) func() {
	t.Helper()

	prev := SystemClock
	SystemClock = &util.FakeClock{Instant: instant}

	restore := func() { SystemClock = prev }
	t.Cleanup(restore)
	return restore
}

// SystemClock is the package-level clock reference that production wiring
// can substitute. Tests should use FreezeTime instead of poking it directly.
var SystemClock util.Clock = util.SystemClock{}

// SystemUUIDGen mirrors SystemClock for UUID determinism.
var SystemUUIDGen util.UUIDGen = util.SystemUUIDGen{}
