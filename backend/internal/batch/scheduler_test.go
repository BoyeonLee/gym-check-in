package batch

import (
	"testing"
	"time"
)

func TestScheduler_RegisterAfterStart(t *testing.T) {
	s := NewScheduler(time.UTC)
	if err := s.Register("* * * * *", func() {}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	s.Start()
	defer s.Stop()
	if err := s.Register("* * * * *", func() {}); err == nil {
		t.Fatalf("Register after Start should fail")
	}
}

func TestScheduler_InvalidSpec(t *testing.T) {
	s := NewScheduler(time.UTC)
	if err := s.Register("nope", func() {}); err == nil {
		t.Fatalf("invalid spec should fail")
	}
}

func TestScheduler_StopWithoutStart(t *testing.T) {
	s := NewScheduler(time.UTC)
	// Should not panic and should mark stopped so Register fails afterwards.
	s.Stop()
	if err := s.Register("* * * * *", func() {}); err == nil {
		t.Fatalf("Register after Stop should fail")
	}
}

func TestScheduler_StartStopIdempotent(t *testing.T) {
	// Two Start calls must not panic; second Stop after a Stop must be a no-op.
	// We do not actually execute a job here because robfig/cron/v3's "@every"
	// granularity is not guaranteed to fire within a short test budget — that
	// path is exercised in production via the KST midnight schedule, and
	// regressions there would surface in the integration tests in batch_test.go.
	s := NewScheduler(time.UTC)
	if err := s.Register("1 0 * * *", func() {}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	s.Start()
	s.Start() // idempotent
	s.Stop()
	s.Stop() // idempotent
}

// Note: the "Stop waits for in-flight jobs" contract is a property of
// robfig/cron/v3's own Stop() implementation, which we re-export. Adding
// a Go-level integration test for it would require either spinning the
// wall clock past a scheduled tick (flaky) or driving cron's internals
// directly (fragile). We therefore rely on the upstream library's own
// tests for that guarantee and keep this file scoped to the wrapper's
// invariants (Register/Start ordering, idempotence, spec validation).

func TestScheduler_EntryCount(t *testing.T) {
	s := NewScheduler(time.UTC)
	if got := s.EntryCount(); got != 0 {
		t.Fatalf("empty scheduler EntryCount = %d, want 0", got)
	}
	if err := s.Register("* * * * *", func() {}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := s.EntryCount(); got != 1 {
		t.Fatalf("EntryCount after Register = %d, want 1", got)
	}
}

func TestParseSpec(t *testing.T) {
	if err := ParseSpec("1 0 * * *"); err != nil {
		t.Fatalf("ParseSpec(KST midnight+1m): %v", err)
	}
	if err := ParseSpec("garbage"); err == nil {
		t.Fatalf("ParseSpec(garbage) should fail")
	}
}
